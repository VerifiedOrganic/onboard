package server

// stacks lists a repository's deployable infrastructure units — the IaC
// analogue of the routes tool. For an application repo the deploy surface is
// its HTTP endpoints; for a Terraform/Terragrunt/OpenTofu repo it is the set of
// Terragrunt units and Terraform root modules: what gets planned and applied,
// from where, against which state.
//
// Like routes and schema, this is a PATTERN reader, not a full HCL evaluator:
// it resolves literal paths, ${get_repo_root()} and find_in_parent_folders(),
// and leaves other interpolations symbolic. Input VALUES are never returned —
// only names — because Terragrunt inputs routinely carry secrets material
// (Vault paths, tokens) that has no business in a tool transcript.

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxStacks = 200

type stacksInput struct {
	Root string `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
}

type stackUnit struct {
	Path string `json:"path"` // unit directory, repo-relative ("." = repo root)
	File string `json:"file"` // the defining file (terragrunt.hcl or the .tf with the backend block)
	Kind string `json:"kind"` // terragrunt | terraform-root
	// Source is the unit's module source: a repo-relative directory when local,
	// the raw source string when external (git/registry), "" when absent.
	Source       string   `json:"source,omitempty"`
	SourceLocal  bool     `json:"source_local,omitempty"`
	Includes     []string `json:"includes,omitempty"`     // resolved include files (Terragrunt config layers)
	Dependencies []string `json:"dependencies,omitempty"` // resolved dependency unit directories
	Backend      string   `json:"backend,omitempty"`      // state backend type (s3, gcs, local, cloud, ...)
	StateKey     string   `json:"state_key,omitempty"`    // state key pattern; ${...} left symbolic
	Inputs       []string `json:"inputs,omitempty"`       // input NAMES only (values may hold secrets)
}

type stacksOutput struct {
	Stacks    []stackUnit `json:"stacks"`
	Total     int         `json:"total"`
	Truncated bool        `json:"truncated,omitempty"`
	Note      string      `json:"note,omitempty"`
}

var (
	tgIncludeBlockRe   = regexp.MustCompile(`(?m)^\s*include(?:\s+"([^"]*)")?\s*\{`)
	tgPathLiteralRe    = regexp.MustCompile(`path\s*=\s*"([^"]+)"`)
	tgFindInParentRe   = regexp.MustCompile(`find_in_parent_folders\(\s*(?:"([^"]*)")?\s*\)`)
	tgDependencyRe     = regexp.MustCompile(`(?s)dependency\s+"([^"]+)"\s*\{([^{}]*)\}`)
	tgConfigPathRe     = regexp.MustCompile(`config_path\s*=\s*"([^"]+)"`)
	tgRemoteStateRe    = regexp.MustCompile(`remote_state\s*\{`)
	tgBackendAttrRe    = regexp.MustCompile(`backend\s*=\s*"([^"]+)"`)
	tgStateKeyRe       = regexp.MustCompile(`key\s*=\s*"([^"]+)"`)
	tgInputsRe         = regexp.MustCompile(`(?m)^\s*inputs\s*=\s*\{`)
	tfBackendBlockRe   = regexp.MustCompile(`(?m)^\s*backend\s+"([^"]+)"\s*\{`)
	tfCloudBlockRe     = regexp.MustCompile(`(?m)^\s*cloud\s*\{`)
	tgTerraformBlockRe = regexp.MustCompile(`(?m)^\s*terraform\s*\{`)
)

func registerStacksTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "stacks",
		Description: "List a repo's deployable infrastructure units (Terraform/Terragrunt/OpenTofu): each Terragrunt unit and Terraform root module with its module source, include chain, inter-stack dependencies, state backend and key pattern, and input names. The IaC analogue of the routes tool — facts read from HCL, with unresolvable interpolations left symbolic. Input values are never returned (they can carry secrets).",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in stacksInput) (*mcp.CallToolResult, stacksOutput, error) {
		out, err := stacksExtract(in)
		return nil, out, err
	})
}

func stacksExtract(in stacksInput) (stacksOutput, error) {
	out := stacksOutput{}
	root, err := resolveRoot(in.Root)
	if err != nil {
		return out, err
	}

	sawTF := false
	tfRootDirs := map[string]string{} // dir -> defining file (first backend/cloud block wins)

	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && shouldSkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)

		if name == "terragrunt.hcl" {
			if len(out.Stacks) >= maxStacks {
				out.Truncated = true
				return nil
			}
			if unit, ok := readTerragruntUnit(root, rel); ok {
				out.Stacks = append(out.Stacks, unit)
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".tf" || ext == ".tofu" {
			sawTF = true
			dir := path.Dir(rel)
			if _, seen := tfRootDirs[dir]; seen {
				return nil
			}
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			if tfBackendBlockRe.Match(data) || tfCloudBlockRe.Match(data) {
				tfRootDirs[dir] = rel
			}
		}
		return nil
	})

	for dir, file := range tfRootDirs {
		if len(out.Stacks) >= maxStacks {
			out.Truncated = true
			break
		}
		// A terragrunt.hcl in the same dir already owns this unit (generated
		// backend files are gitignored but belt-and-braces).
		if hasStackAt(out.Stacks, dir) {
			continue
		}
		unit := stackUnit{Path: dir, File: file, Kind: "terraform-root"}
		if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file))); err == nil {
			s := string(data)
			if m := tfBackendBlockRe.FindStringSubmatch(s); m != nil {
				unit.Backend = m[1]
				if loc := tfBackendBlockRe.FindStringIndex(s); loc != nil {
					body := hclBlockBody(s, strings.Index(s[loc[0]:], "{")+loc[0])
					if k := tgStateKeyRe.FindStringSubmatch(body); k != nil {
						unit.StateKey = k[1]
					}
				}
			} else if tfCloudBlockRe.MatchString(s) {
				unit.Backend = "cloud"
			}
		}
		out.Stacks = append(out.Stacks, unit)
	}

	sort.Slice(out.Stacks, func(i, j int) bool { return out.Stacks[i].Path < out.Stacks[j].Path })
	out.Total = len(out.Stacks)

	switch {
	case out.Total == 0 && sawTF:
		out.Note = "Terraform files exist but no Terragrunt units or backend/cloud blocks were found — this looks like a modules-only (library) repo, or state configuration is generated/injected elsewhere."
	case out.Total == 0:
		out.Note = "No Terraform/Terragrunt/OpenTofu units detected. For an application repo's deploy surface, use the routes tool instead."
	default:
		out.Note = "Facts read from HCL by pattern (not a full evaluator): literal paths, ${get_repo_root()} and find_in_parent_folders() are resolved; other interpolations are left symbolic. Input values are omitted by design."
	}
	return out, nil
}

func hasStackAt(stacks []stackUnit, dir string) bool {
	for _, s := range stacks {
		if s.Path == dir {
			return true
		}
	}
	return false
}

// readTerragruntUnit parses one terragrunt.hcl into a stackUnit. Backend and
// state key are searched in the unit file first, then in its resolved include
// chain (the conventional home of remote_state). Inputs are the union of the
// unit's and its includes' input names.
func readTerragruntUnit(root, rel string) (stackUnit, bool) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return stackUnit{}, false
	}
	s := string(data)
	unit := stackUnit{Path: path.Dir(rel), File: rel, Kind: "terragrunt"}

	// Module source.
	if loc := tgTerraformBlockRe.FindStringIndex(s); loc != nil {
		body := hclBlockBody(s, strings.Index(s[loc[0]:], "{")+loc[0])
		if m := tfSourceAttrRe.FindStringSubmatch(body); m != nil {
			unit.Source, unit.SourceLocal = resolveStackSource(rel, m[1])
		}
	}

	// Includes.
	for _, loc := range tgIncludeBlockRe.FindAllStringIndex(s, -1) {
		body := hclBlockBody(s, strings.Index(s[loc[0]:], "{")+loc[0])
		if target := resolveIncludeTarget(root, rel, body); target != "" {
			unit.Includes = append(unit.Includes, target)
		}
	}

	// Dependencies.
	for _, m := range tgDependencyRe.FindAllStringSubmatch(s, -1) {
		if cp := tgConfigPathRe.FindStringSubmatch(m[2]); cp != nil {
			dir := path.Clean(path.Join(path.Dir(rel), cp[1]))
			if !strings.HasPrefix(dir, "..") {
				unit.Dependencies = append(unit.Dependencies, dir)
			}
		}
	}

	// Backend + state key: unit file first, then include chain.
	unit.Backend, unit.StateKey = remoteStateOf(s)
	inputSet := map[string]bool{}
	for _, name := range inputNames(s) {
		inputSet[name] = true
	}
	for _, inc := range unit.Includes {
		incData, ierr := os.ReadFile(filepath.Join(root, filepath.FromSlash(inc)))
		if ierr != nil {
			continue
		}
		is := string(incData)
		if unit.Backend == "" {
			unit.Backend, unit.StateKey = remoteStateOf(is)
		}
		for _, name := range inputNames(is) {
			inputSet[name] = true
		}
	}
	for name := range inputSet {
		unit.Inputs = append(unit.Inputs, name)
	}
	sort.Strings(unit.Inputs)
	sort.Strings(unit.Includes)
	sort.Strings(unit.Dependencies)
	return unit, true
}

// remoteStateOf extracts the backend type and key pattern from a remote_state
// block, if present.
func remoteStateOf(s string) (backend, key string) {
	loc := tgRemoteStateRe.FindStringIndex(s)
	if loc == nil {
		return "", ""
	}
	body := hclBlockBody(s, strings.Index(s[loc[0]:], "{")+loc[0])
	if m := tgBackendAttrRe.FindStringSubmatch(body); m != nil {
		backend = m[1]
	}
	if m := tgStateKeyRe.FindStringSubmatch(body); m != nil {
		key = m[1]
	}
	return backend, key
}

// inputNames returns the top-level keys of an inputs = { ... } attribute.
func inputNames(s string) []string {
	loc := tgInputsRe.FindStringIndex(s)
	if loc == nil {
		return nil
	}
	body := hclBlockBody(s, strings.Index(s[loc[0]:], "{")+loc[0])
	var names []string
	depth := 0
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if depth == 0 {
			if eq := strings.Index(trimmed, "="); eq > 0 {
				name := strings.TrimSpace(trimmed[:eq])
				if isHCLIdent(name) {
					names = append(names, name)
				}
			}
		}
		depth += strings.Count(line, "{") + strings.Count(line, "[")
		depth -= strings.Count(line, "}") + strings.Count(line, "]")
		if depth < 0 {
			depth = 0
		}
	}
	return names
}

func isHCLIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9', r == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// resolveStackSource resolves a Terragrunt module source to a repo-relative
// directory when local; external sources are returned raw with local=false.
func resolveStackSource(fromRel, src string) (string, bool) {
	s := strings.TrimSpace(src)
	if strings.Contains(s, "${get_repo_root()}") {
		s = strings.ReplaceAll(s, "${get_repo_root()}", "")
		s = strings.ReplaceAll(s, "//", "/")
		s = strings.Trim(s, "/")
		s = path.Clean(s)
		if s == "." {
			s = ""
		}
		return s, true
	}
	if strings.Contains(s, "${") {
		return src, false
	}
	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || s == "." || s == ".." {
		s = strings.ReplaceAll(s, "//", "/")
		joined := path.Clean(path.Join(path.Dir(fromRel), s))
		if strings.HasPrefix(joined, "..") {
			return src, false
		}
		return joined, true
	}
	return src, false
}

// resolveIncludeTarget resolves an include block body's path expression to a
// repo-relative file.
func resolveIncludeTarget(root, fromRel, body string) string {
	if m := tgFindInParentRe.FindStringSubmatch(body); m != nil {
		name := m[1]
		if name == "" {
			if t := stacksFindInParents(root, fromRel, "root.hcl"); t != "" {
				return t
			}
			return stacksFindInParents(root, fromRel, "terragrunt.hcl")
		}
		return stacksFindInParents(root, fromRel, name)
	}
	if m := tgPathLiteralRe.FindStringSubmatch(body); m != nil && !strings.Contains(m[1], "${") {
		joined := path.Clean(path.Join(path.Dir(fromRel), m[1]))
		if !strings.HasPrefix(joined, "..") {
			return joined
		}
	}
	return ""
}

// stacksFindInParents mirrors Terragrunt's find_in_parent_folders: search the
// file's parent directories (not its own), stopping at the repo root.
func stacksFindInParents(root, fromRel, name string) string {
	dir := path.Dir(fromRel)
	for dir != "." && dir != "/" {
		dir = path.Dir(dir)
		cand := name
		if dir != "." {
			cand = path.Join(dir, name)
		}
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(cand))); err == nil && !info.IsDir() {
			return cand
		}
		if dir == "." {
			break
		}
	}
	return ""
}
