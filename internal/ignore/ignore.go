// Package ignore is the single source of truth for directories that code-walking tools skip:
// dependency, build-output, and cache directories that never hold a project's own source.
// Centralizing the list keeps the indexer (internal/providers) and the recon scanner
// (internal/server) from drifting apart, as they had — one listed .venv but not bin/obj, the
// other the reverse. Dot-directory policy is deliberately NOT decided here (the indexer skips
// all dotdirs including .git; recon keeps .github), so this covers only the non-dot set.
package ignore

var dirs = map[string]bool{
	"node_modules": true, // JS/TS dependencies
	"vendor":       true, // Go / PHP vendored deps
	"dist":         true, // build output
	"build":        true, // build output
	"target":       true, // Rust / Java (Maven) build output
	"__pycache__":  true, // Python bytecode cache
	"venv":         true, // Python virtualenv (dot form .venv is caught by dotdir policy)
	"coverage":     true, // coverage reports
	"bin":          true, // build output
	"obj":          true, // .NET build output
}

// Dir reports whether a directory name is a dependency/build/cache directory that source
// tools should not descend into.
func Dir(name string) bool { return dirs[name] }
