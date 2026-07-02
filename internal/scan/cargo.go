package scan

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const cargoMetadataTimeout = 20 * time.Second

type cargoMetadataJSON struct {
	Packages []struct {
		Name         string `json:"name"`
		ManifestPath string `json:"manifest_path"`
		Dependencies []struct {
			Name     string `json:"name"`
			Req      string `json:"req"`
			Kind     string `json:"kind"`
			Target   string `json:"target"`
			Optional bool   `json:"optional"`
		} `json:"dependencies"`
		Targets []struct {
			Name       string   `json:"name"`
			Kind       []string `json:"kind"`
			CrateTypes []string `json:"crate_types"`
			SrcPath    string   `json:"src_path"`
			Edition    string   `json:"edition"`
		} `json:"targets"`
	} `json:"packages"`
}

type cargoTomlManifest struct {
	Package struct {
		Name    string `toml:"name"`
		Version string `toml:"version"`
	} `toml:"package"`
	Dependencies    map[string]any `toml:"dependencies"`
	DevDependencies map[string]any `toml:"dev-dependencies"`
	Workspace       struct {
		Dependencies map[string]any `toml:"dependencies"`
	} `toml:"workspace"`
	Target map[string]struct {
		Dependencies    map[string]any `toml:"dependencies"`
		DevDependencies map[string]any `toml:"dev-dependencies"`
	} `toml:"target"`
}

// LoadCargoMetadata runs `cargo metadata --no-deps` when cargo is available.
func LoadCargoMetadata(ctx context.Context, root string) (map[string]ManifestDeps, bool) {
	if _, err := exec.LookPath("cargo"); err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, cargoMetadataTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cargo", "metadata", "--format-version=1", "--no-deps")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	mds := ParseCargoMetadata(root, out)
	return mds, len(mds) > 0
}

// ParseCargoMetadata parses cargo metadata JSON into manifest deps keyed by manifest path.
func ParseCargoMetadata(root string, data []byte) map[string]ManifestDeps {
	var raw cargoMetadataJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	root = canonicalPath(root)
	out := map[string]ManifestDeps{}
	for _, pkg := range raw.Packages {
		manifestPath := canonicalPath(pkg.ManifestPath)
		rel, err := filepath.Rel(root, manifestPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		rel = filepath.ToSlash(rel)
		md := ManifestDeps{Manifest: rel, Ecosystem: "Rust", Module: pkg.Name}
		for _, dep := range pkg.Dependencies {
			kind := dep.Kind
			if kind == "" {
				kind = "normal"
			}
			md.Direct = append(md.Direct, Dependency{
				Name:     dep.Name,
				Version:  dep.Req,
				Kind:     kind,
				Target:   dep.Target,
				Optional: dep.Optional,
				Dev:      dep.Kind == "dev",
			})
		}
		sortDeps(md.Direct)
		for _, target := range pkg.Targets {
			srcPath := canonicalPath(target.SrcPath)
			srcRel, err := filepath.Rel(root, srcPath)
			if err != nil || strings.HasPrefix(srcRel, "..") {
				srcRel = target.SrcPath
			}
			md.Targets = append(md.Targets, RustTarget{
				Name:       target.Name,
				Kind:       target.Kind,
				CrateTypes: target.CrateTypes,
				SrcPath:    filepath.ToSlash(srcRel),
				Edition:    target.Edition,
			})
		}
		slices.SortFunc(md.Targets, func(a, b RustTarget) int {
			if c := cmp.Compare(a.SrcPath, b.SrcPath); c != 0 {
				return c
			}
			return cmp.Compare(a.Name, b.Name)
		})
		out[rel] = md
	}
	return out
}

func parseCargoToml(rel string, data []byte) (ManifestDeps, bool) {
	var raw cargoTomlManifest
	if err := toml.Unmarshal(data, &raw); err != nil {
		return ManifestDeps{}, false
	}

	md := ManifestDeps{Manifest: rel, Ecosystem: "Rust", Module: raw.Package.Name}
	appendCargoTomlDeps(&md, raw.Dependencies, "normal", false, "")
	appendCargoTomlDeps(&md, raw.DevDependencies, "dev", true, "")
	appendCargoTomlDeps(&md, raw.Workspace.Dependencies, "workspace", false, "")
	for target, deps := range raw.Target {
		appendCargoTomlDeps(&md, deps.Dependencies, "normal", false, target)
		appendCargoTomlDeps(&md, deps.DevDependencies, "dev", true, target)
	}
	sortDeps(md.Direct)
	return md, true
}

func appendCargoTomlDeps(md *ManifestDeps, deps map[string]any, kind string, dev bool, target string) {
	for name, value := range deps {
		md.Direct = append(md.Direct, Dependency{
			Name:    name,
			Version: cargoTomlDepVersion(value),
			Kind:    kind,
			Target:  target,
			Dev:     dev,
		})
	}
}

func cargoTomlDepVersion(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		if version, ok := v["version"].(string); ok {
			return version
		}
	}
	return ""
}

func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	if _, err := os.Stat(abs); err == nil {
		return abs
	}
	return filepath.Clean(abs)
}
