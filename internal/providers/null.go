package providers

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Null is the fallback provider: it lists definitions via regex but produces no
// call edges. It exists so trace/impact degrade gracefully if the Builtin engine
// cannot index a tree at all. Results are intentionally coarse.
type Null struct{}

// Name returns the provider identifier.
func (Null) Name() string { return "null" }

// defPatterns maps a file extension to regexes whose first submatch is a symbol name.
var defPatterns = map[string][]*regexp.Regexp{
	".go": {regexp.MustCompile(`(?m)^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*[\(\[]`)},
	".py": {
		regexp.MustCompile(`(?m)^\s*def\s+([A-Za-z_]\w*)\s*\(`),
		regexp.MustCompile(`(?m)^\s*class\s+([A-Za-z_]\w*)`),
	},
	".js":   {regexp.MustCompile(`(?m)\bfunction\s+([A-Za-z_]\w*)\s*\(`)},
	".ts":   {regexp.MustCompile(`(?m)\bfunction\s+([A-Za-z_]\w*)\s*\(`)},
	".rs":   {regexp.MustCompile(`(?m)\bfn\s+([A-Za-z_]\w*)\s*[\(<]`)},
	".rb":   {regexp.MustCompile(`(?m)^\s*def\s+([A-Za-z_]\w*)`)},
	".java": {regexp.MustCompile(`(?m)\b(?:public|private|protected|static|\s)+[\w<>\[\]]+\s+([A-Za-z_]\w*)\s*\(`)},
}

// Index walks root and extracts coarse definitions with regular expressions.
func (Null) Index(ctx context.Context, root string) (*Graph, error) {
	root, err := normalizeRoot(root)
	if err != nil {
		return nil, err
	}
	g := &Graph{
		Provider: "null",
		Defs:     map[string]*Symbol{},
		Forward:  map[string][]string{},
		Reverse:  map[string][]string{},
		Note:     "Fallback provider: definitions only, no call graph. trace_flow/impact are unavailable.",
	}
	langSet := map[string]bool{}

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != root && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		pats := defPatterns[ext]
		if pats == nil {
			return nil
		}
		src, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		langSet[ext] = true
		g.Files++
		for _, re := range pats {
			for _, m := range re.FindAllSubmatchIndex(src, -1) {
				if len(m) < 4 || m[2] < 0 || m[3] < 0 {
					continue
				}
				name := string(src[m[2]:m[3]])
				line := 1 + bytes.Count(src[:m[0]], []byte("\n"))
				qn := uniqueQName(g.Defs, rel, name, line)
				g.Defs[qn] = &Symbol{QName: qn, Name: name, Kind: "symbol", File: rel, Line: line, Lang: ext}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	g.Langs = sortedKeys(langSet)
	return g, nil
}
