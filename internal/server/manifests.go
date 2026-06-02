package server

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
// deliberately small section reader rather than a full TOML dependency. The deps tool
// (tools_deps.go) owns the walk and rendering.

// parseManifest dispatches on the manifest filename. It returns ok=false for files it does
// not recognize or cannot parse, so an unreadable manifest is skipped, never fatal.
func parseManifest(root, path, name string) (manifestDeps, bool) {
	rel, _ := filepath.Rel(root, path)
	rel = filepath.ToSlash(rel)
	data, err := os.ReadFile(path)
	if err != nil {
		return manifestDeps{}, false
	}
	switch name {
	case "go.mod":
		return parseGoMod(rel, data)
	case "package.json":
		return parsePackageJSON(rel, data)
	case "requirements.txt":
		return parseRequirements(rel, data)
	case "Cargo.toml":
		return parseCargoToml(rel, data)
	}
	return manifestDeps{}, false
}

func parseGoMod(rel string, data []byte) (manifestDeps, bool) {
	f, err := modfile.Parse(rel, data, nil)
	if err != nil {
		return manifestDeps{}, false
	}
	md := manifestDeps{Manifest: rel, Ecosystem: "Go"}
	if f.Module != nil {
		md.Module = f.Module.Mod.Path
	}
	for _, r := range f.Require {
		if r.Indirect {
			md.Indirect++
			continue
		}
		md.Direct = append(md.Direct, dependency{Name: r.Mod.Path, Version: r.Mod.Version})
	}
	sortDeps(md.Direct)
	return md, true
}

func parsePackageJSON(rel string, data []byte) (manifestDeps, bool) {
	var pkg struct {
		Name            string            `json:"name"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return manifestDeps{}, false
	}
	md := manifestDeps{Manifest: rel, Ecosystem: "JavaScript/TypeScript (npm)", Module: pkg.Name}
	for name, ver := range pkg.Dependencies {
		md.Direct = append(md.Direct, dependency{Name: name, Version: ver})
	}
	for name, ver := range pkg.DevDependencies {
		md.Direct = append(md.Direct, dependency{Name: name, Version: ver, Dev: true})
	}
	sortDeps(md.Direct)
	return md, true
}

func parseRequirements(rel string, data []byte) (manifestDeps, bool) {
	md := manifestDeps{Manifest: rel, Ecosystem: "Python"}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue // blank, comment, or pip option (-r, -e, --hash)
		}
		line = stripRequirementComment(line)
		name, ver := splitRequirement(line)
		if name != "" {
			md.Direct = append(md.Direct, dependency{Name: name, Version: ver})
		}
	}
	sortDeps(md.Direct)
	return md, true
}

// splitRequirement separates a PEP 508 requirement into name and version constraint, e.g.
// "Django>=4.2" -> ("Django", ">=4.2"); "requests" -> ("requests", "").
func splitRequirement(line string) (string, string) {
	if i := strings.Index(line, ";"); i >= 0 { // drop environment markers
		line = strings.TrimSpace(line[:i])
	}
	if i := strings.Index(line, " @ "); i >= 0 { // direct URL/reference requirement
		return requirementName(line[:i]), strings.TrimSpace(line[i+1:])
	}
	for _, op := range []string{"==", ">=", "<=", "~=", "!=", ">", "<", "="} {
		if i := strings.Index(line, op); i >= 0 {
			return requirementName(line[:i]), strings.TrimSpace(line[i:])
		}
	}
	if i := strings.IndexByte(line, '['); i >= 0 { // extras: requests[security]
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

// parseCargoToml is a deliberately small TOML reader: it scans the package name,
// [dependencies], [dev-dependencies], and target-specific dependency tables (no general
// TOML parser dependency). It reads `name = "version"` and
// `name = { version = "..." }`; anything fancier degrades to a name with no version.
func parseCargoToml(rel string, data []byte) (manifestDeps, bool) {
	md := manifestDeps{Manifest: rel, Ecosystem: "Rust"}
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
			md.Direct = append(md.Direct, dependency{Name: name, Version: ver, Dev: dev})
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

func sortDeps(d []dependency) {
	sort.Slice(d, func(i, j int) bool {
		if d[i].Name != d[j].Name {
			return d[i].Name < d[j].Name
		}
		return !d[i].Dev && d[j].Dev
	})
}
