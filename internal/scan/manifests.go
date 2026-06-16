package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

// Per-ecosystem manifest parsers for the deps tool. Each reads direct dependencies (and, for
// Go, counts indirect ones) from one manifest format. go.mod uses the real x/mod parser;
// package.json and requirements.txt use encoding/json and line parsing; Cargo.toml uses a
// deliberately small section reader rather than a full TOML dependency.

// Dependency is one direct dependency entry from a manifest.
type Dependency struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Kind      string `json:"kind,omitempty"`   // normal | dev | build | peer | optional
	Target    string `json:"target,omitempty"` // target cfg for platform-specific deps
	Optional  bool   `json:"optional,omitempty"`
	Dev       bool   `json:"dev,omitempty"`       // development-only dependency
	Workspace bool   `json:"workspace,omitempty"` // true if it is a local workspace package
}

// RustTarget describes one Cargo build target from metadata.
type RustTarget struct {
	Name       string   `json:"name"`
	Kind       []string `json:"kind,omitempty"`
	CrateTypes []string `json:"crate_types,omitempty"`
	SrcPath    string   `json:"src_path,omitempty"`
	Edition    string   `json:"edition,omitempty"`
}

// ManifestDeps holds parsed dependency facts from one manifest file.
type ManifestDeps struct {
	Manifest              string            `json:"manifest"`  // repo-relative path
	Ecosystem             string            `json:"ecosystem"` // Go, JavaScript/TypeScript (npm), ...
	Module                string            `json:"module,omitempty"`
	Direct                []Dependency      `json:"direct"`
	Indirect              int               `json:"indirect,omitempty"` // count of indirect deps (go.mod)
	Targets               []RustTarget      `json:"targets,omitempty"`  // Cargo targets, when Cargo metadata is available
	WorkspaceDependencies []string          `json:"workspace_dependencies,omitempty"`
	PackageManager        string            `json:"package_manager,omitempty"`
	Workspaces            []string          `json:"workspaces,omitempty"`
	Scripts               map[string]string `json:"scripts,omitempty"`
	Engines               map[string]string `json:"engines,omitempty"`
	DetectedTools         []string          `json:"detected_tools,omitempty"`
}

// ParseManifest dispatches on the manifest filename. It returns ok=false for files it does
// not recognize or cannot parse, so an unreadable manifest is skipped, never fatal.
func ParseManifest(root, path, name string) (ManifestDeps, bool) {
	rel, _ := filepath.Rel(root, path)
	rel = filepath.ToSlash(rel)
	data, err := os.ReadFile(path)
	if err != nil {
		return ManifestDeps{}, false
	}
	switch name {
	case "go.mod":
		return parseGoMod(rel, data)
	case "package.json":
		return parsePackageJSON(root, rel, data)
	case "requirements.txt":
		return parseRequirements(rel, data)
	case "Cargo.toml":
		return parseCargoToml(rel, data)
	case ".terraform.lock.hcl":
		return parseTFLock(rel, data, "Terraform (lock)")
	case ".opentofu.lock.hcl":
		return parseTFLock(rel, data, "OpenTofu (lock)")
	case "terragrunt.hcl":
		return parseTerragruntManifest(rel, data)
	}
	if strings.HasSuffix(name, ".tf") || strings.HasSuffix(name, ".tofu") {
		return parseTerraformFile(rel, data)
	}
	return ManifestDeps{}, false
}

func parseGoMod(rel string, data []byte) (ManifestDeps, bool) {
	f, err := modfile.Parse(rel, data, nil)
	if err != nil {
		return ManifestDeps{}, false
	}
	md := ManifestDeps{Manifest: rel, Ecosystem: "Go"}
	if f.Module != nil {
		md.Module = f.Module.Mod.Path
	}
	for _, r := range f.Require {
		if r.Indirect {
			md.Indirect++
			continue
		}
		md.Direct = append(md.Direct, Dependency{Name: r.Mod.Path, Version: r.Mod.Version})
	}
	sortDeps(md.Direct)
	return md, true
}

type rawPackageJSON struct {
	Name                 string            `json:"name"`
	PackageManager       string            `json:"packageManager"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	Workspaces           json.RawMessage   `json:"workspaces"`
	Scripts              map[string]string `json:"scripts"`
	Engines              map[string]string `json:"engines"`
}

func parsePackageJSON(root, rel string, data []byte) (ManifestDeps, bool) {
	var raw rawPackageJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return ManifestDeps{}, false
	}
	md := ManifestDeps{
		Manifest:       rel,
		Ecosystem:      "JavaScript/TypeScript (npm)",
		Module:         raw.Name,
		PackageManager: raw.PackageManager,
		Scripts:        raw.Scripts,
		Engines:        raw.Engines,
	}

	var workspaces []string
	if len(raw.Workspaces) > 0 {
		var arr []string
		if err := json.Unmarshal(raw.Workspaces, &arr); err == nil {
			workspaces = arr
		} else {
			var obj struct {
				Packages []string `json:"packages"`
			}
			if err := json.Unmarshal(raw.Workspaces, &obj); err == nil {
				workspaces = obj.Packages
			}
		}
	}
	md.Workspaces = workspaces

	md.DetectedTools = detectWorkspacesAndTools(root, rel, raw, workspaces)

	for name, ver := range raw.Dependencies {
		md.Direct = append(md.Direct, Dependency{Name: name, Version: ver, Kind: "normal"})
	}
	for name, ver := range raw.DevDependencies {
		md.Direct = append(md.Direct, Dependency{Name: name, Version: ver, Dev: true, Kind: "dev"})
	}
	for name, ver := range raw.PeerDependencies {
		md.Direct = append(md.Direct, Dependency{Name: name, Version: ver, Kind: "peer"})
	}
	for name, ver := range raw.OptionalDependencies {
		md.Direct = append(md.Direct, Dependency{Name: name, Version: ver, Optional: true, Kind: "optional"})
	}
	sortDeps(md.Direct)
	return md, true
}

func detectWorkspacesAndTools(root, rel string, raw rawPackageJSON, workspaces []string) []string {
	var tools []string
	dir := filepath.Dir(filepath.Join(root, rel))

	hasFile := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}

	isRoot := rel == "package.json"
	if isRoot {
		if hasFile("pnpm-workspace.yaml") || strings.HasPrefix(raw.PackageManager, "pnpm") {
			tools = append(tools, "pnpm workspaces")
		} else if len(workspaces) > 0 {
			if strings.HasPrefix(raw.PackageManager, "yarn") {
				tools = append(tools, "yarn workspaces")
			} else {
				tools = append(tools, "npm workspaces")
			}
		}
	}

	checkDep := func(dep string) bool {
		if _, ok := raw.Dependencies[dep]; ok {
			return true
		}
		if _, ok := raw.DevDependencies[dep]; ok {
			return true
		}
		if _, ok := raw.PeerDependencies[dep]; ok {
			return true
		}
		return false
	}

	if checkDep("nx") || hasFile("nx.json") {
		tools = append(tools, "Nx")
	}
	if checkDep("turbo") || hasFile("turbo.json") {
		tools = append(tools, "Turbo")
	}
	if checkDep("vite") || hasFile("vite.config.ts") || hasFile("vite.config.js") || hasFile("vite.config.mts") {
		tools = append(tools, "Vite")
	}
	if checkDep("next") || hasFile("next.config.js") || hasFile("next.config.mjs") {
		tools = append(tools, "Next")
	}
	if checkDep("@sveltejs/kit") || hasFile("svelte.config.js") {
		tools = append(tools, "SvelteKit")
	}
	if checkDep("@angular/core") || hasFile("angular.json") {
		tools = append(tools, "Angular")
	}
	if checkDep("@angular/cli") {
		tools = append(tools, "Angular CLI")
	}
	if checkDep("vitest") || hasFile("vitest.config.ts") || hasFile("vitest.config.js") {
		tools = append(tools, "Vitest")
	}
	if checkDep("jest") || hasFile("jest.config.ts") || hasFile("jest.config.js") {
		tools = append(tools, "Jest")
	}
	if checkDep("@playwright/test") || hasFile("playwright.config.ts") || hasFile("playwright.config.js") {
		tools = append(tools, "Playwright")
	}
	if checkDep("cypress") || hasFile("cypress.config.ts") || hasFile("cypress.config.js") {
		tools = append(tools, "Cypress")
	}

	hasStorybook := false
	for dep := range raw.Dependencies {
		if strings.HasPrefix(dep, "@storybook/") {
			hasStorybook = true
		}
	}
	for dep := range raw.DevDependencies {
		if strings.HasPrefix(dep, "@storybook/") {
			hasStorybook = true
		}
	}
	if hasStorybook || hasFile(".storybook") {
		tools = append(tools, "Storybook")
	}

	sort.Strings(tools)
	var out []string
	for i, t := range tools {
		if i == 0 || t != tools[i-1] {
			out = append(out, t)
		}
	}
	return out
}

func parseRequirements(rel string, data []byte) (ManifestDeps, bool) {
	md := ManifestDeps{Manifest: rel, Ecosystem: "Python"}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		line = stripRequirementComment(line)
		name, ver := SplitRequirement(line)
		if name != "" {
			md.Direct = append(md.Direct, Dependency{Name: name, Version: ver})
		}
	}
	sortDeps(md.Direct)
	return md, true
}

// SplitRequirement separates a PEP 508 requirement into name and version constraint, e.g.
// "Django>=4.2" -> ("Django", ">=4.2"); "requests" -> ("requests", "").
func SplitRequirement(line string) (string, string) {
	if i := strings.Index(line, ";"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	if i := strings.Index(line, " @ "); i >= 0 {
		return requirementName(line[:i]), strings.TrimSpace(line[i+1:])
	}
	for _, op := range []string{"==", ">=", "<=", "~=", "!=", ">", "<", "="} {
		if i := strings.Index(line, op); i >= 0 {
			return requirementName(line[:i]), strings.TrimSpace(line[i:])
		}
	}
	if i := strings.IndexByte(line, '['); i >= 0 {
		return requirementName(line[:i]), ""
	}
	return requirementName(line), ""
}

func requirementName(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '['); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func stripRequirementComment(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return strings.TrimSpace(line[:i])
		}
	}
	return line
}

func parseCargoToml(rel string, data []byte) (ManifestDeps, bool) {
	md := ManifestDeps{Manifest: rel, Ecosystem: "Rust"}
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(stripTomlComment(line))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[]")
			continue
		}
		if section == "package" {
			if name, val := parseTomlAssignment(line); name == "name" {
				md.Module = tomlString(val)
			}
			continue
		}
		dev, ok := cargoDependencySection(section)
		if !ok {
			continue
		}
		name, ver := parseCargoDep(line)
		if name != "" {
			md.Direct = append(md.Direct, Dependency{Name: name, Version: ver, Dev: dev})
		}
	}
	sortDeps(md.Direct)
	return md, true
}

func cargoDependencySection(section string) (dev, ok bool) {
	switch section {
	case "dependencies":
		return false, true
	case "dev-dependencies":
		return true, true
	}
	if strings.HasPrefix(section, "target.") {
		switch {
		case strings.HasSuffix(section, ".dependencies"):
			return false, true
		case strings.HasSuffix(section, ".dev-dependencies"):
			return true, true
		}
	}
	return false, false
}

func parseCargoDep(line string) (string, string) {
	name, rhs := parseTomlAssignment(line)
	if name == "" {
		return "", ""
	}
	if strings.HasPrefix(rhs, "{") {
		return name, tomlInlineStringField(rhs, "version")
	}
	return name, tomlString(rhs)
}

func parseTomlAssignment(line string) (string, string) {
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return "", ""
	}
	name := strings.TrimSpace(line[:eq])
	rhs := strings.TrimSpace(line[eq+1:])
	return strings.Trim(name, "\"'"), rhs
}

func tomlInlineStringField(table, field string) string {
	for _, part := range strings.Split(strings.Trim(table, "{}"), ",") {
		name, rhs := parseTomlAssignment(part)
		if name == field {
			return tomlString(rhs)
		}
	}
	return ""
}

func tomlString(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "\"") {
		if v, err := strconv.Unquote(s); err == nil {
			return v
		}
	}
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") {
		return strings.Trim(s, "'")
	}
	return strings.Trim(s, "\"'")
}

func stripTomlComment(line string) string {
	inSingle, inDouble, escaped := false, false, false
	for i, r := range line {
		switch {
		case escaped:
			escaped = false
		case inDouble && r == '\\':
			escaped = true
		case !inDouble && r == '\'':
			inSingle = !inSingle
		case !inSingle && r == '"':
			inDouble = !inDouble
		case !inSingle && !inDouble && r == '#':
			return strings.TrimSpace(line[:i])
		}
	}
	return line
}

func sortDeps(d []Dependency) {
	sort.Slice(d, func(i, j int) bool {
		if d[i].Name != d[j].Name {
			return d[i].Name < d[j].Name
		}
		return !d[i].Dev && d[j].Dev
	})
}

func parseTerraformFile(rel string, data []byte) (ManifestDeps, bool) {
	s := string(data)
	md := ManifestDeps{Manifest: rel, Ecosystem: "Terraform/OpenTofu (HCL)"}

	if m := tfRequiredVersionRe.FindStringSubmatch(s); m != nil {
		md.Engines = map[string]string{"terraform": m[1]}
	}
	if loc := tfRequiredProvidersRe.FindStringIndex(s); loc != nil {
		body := hclBlockBody(s, loc[1]-1)
		for _, entry := range tfProviderEntryRe.FindAllStringSubmatch(body, -1) {
			dep := Dependency{Name: entry[1], Kind: "provider"}
			if src := tfSourceAttrRe.FindStringSubmatch(entry[2]); src != nil {
				dep.Name = src[1]
			}
			if v := tfVersionAttrRe.FindStringSubmatch(entry[2]); v != nil {
				dep.Version = v[1]
			}
			md.Direct = append(md.Direct, dep)
		}
	}
	for _, loc := range tfModuleBlockRe.FindAllStringIndex(s, -1) {
		body := hclBlockBody(s, strings.Index(s[loc[0]:], "{")+loc[0])
		src := tfSourceAttrRe.FindStringSubmatch(body)
		if src == nil || tfSourceIsLocal(src[1]) {
			continue
		}
		dep := Dependency{Name: src[1], Kind: "module"}
		if v := tfVersionAttrRe.FindStringSubmatch(body); v != nil {
			dep.Version = v[1]
		}
		md.Direct = append(md.Direct, dep)
	}

	if len(md.Direct) == 0 && md.Engines == nil {
		return ManifestDeps{}, false
	}
	sortDeps(md.Direct)
	return md, true
}

func parseTFLock(rel string, data []byte, ecosystem string) (ManifestDeps, bool) {
	md := ManifestDeps{Manifest: rel, Ecosystem: ecosystem}
	for _, m := range tfLockProviderRe.FindAllStringSubmatch(string(data), -1) {
		dep := Dependency{Name: m[1], Kind: "locked"}
		if v := tfVersionAttrRe.FindStringSubmatch(m[2]); v != nil {
			dep.Version = v[1]
		}
		md.Direct = append(md.Direct, dep)
	}
	if len(md.Direct) == 0 {
		return ManifestDeps{}, false
	}
	sortDeps(md.Direct)
	return md, true
}

func parseTerragruntManifest(rel string, data []byte) (ManifestDeps, bool) {
	src := tfSourceAttrRe.FindStringSubmatch(string(data))
	if src == nil || tfSourceIsLocal(src[1]) {
		return ManifestDeps{}, false
	}
	md := ManifestDeps{
		Manifest:  rel,
		Ecosystem: "Terragrunt",
		Direct:    []Dependency{{Name: src[1], Kind: "module"}},
	}
	return md, true
}
