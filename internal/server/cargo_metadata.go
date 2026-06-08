package server

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
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

func loadCargoMetadata(ctx context.Context, root string) (map[string]manifestDeps, bool) {
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
	mds := parseCargoMetadata(root, out)
	return mds, len(mds) > 0
}

func parseCargoMetadata(root string, data []byte) map[string]manifestDeps {
	var raw cargoMetadataJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	root = canonicalPath(root)
	out := map[string]manifestDeps{}
	for _, pkg := range raw.Packages {
		manifestPath := canonicalPath(pkg.ManifestPath)
		rel, err := filepath.Rel(root, manifestPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		rel = filepath.ToSlash(rel)
		md := manifestDeps{Manifest: rel, Ecosystem: "Rust", Module: pkg.Name}
		for _, dep := range pkg.Dependencies {
			kind := dep.Kind
			if kind == "" {
				kind = "normal"
			}
			md.Direct = append(md.Direct, dependency{
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
			md.Targets = append(md.Targets, rustTarget{
				Name:       target.Name,
				Kind:       target.Kind,
				CrateTypes: target.CrateTypes,
				SrcPath:    filepath.ToSlash(srcRel),
				Edition:    target.Edition,
			})
		}
		sort.Slice(md.Targets, func(i, j int) bool {
			if md.Targets[i].SrcPath != md.Targets[j].SrcPath {
				return md.Targets[i].SrcPath < md.Targets[j].SrcPath
			}
			return md.Targets[i].Name < md.Targets[j].Name
		})
		out[rel] = md
	}
	return out
}

func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	// filepath.EvalSymlinks fails for files that do not exist yet. Keep the cleaned
	// absolute path rather than dropping the package.
	if _, err := os.Stat(abs); err == nil {
		return abs
	}
	return filepath.Clean(abs)
}

func cargoTargetSummaries(metadata map[string]manifestDeps) []string {
	var out []string
	for _, md := range metadata {
		for _, target := range md.Targets {
			kind := strings.Join(target.Kind, ",")
			if kind == "" {
				kind = "target"
			}
			out = append(out, md.Module+":"+target.Name+" ("+kind+") "+target.SrcPath)
		}
	}
	sort.Strings(out)
	return out
}
