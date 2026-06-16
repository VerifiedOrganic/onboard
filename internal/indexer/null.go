package indexer

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/VerifiedOrganic/onboard/internal/providers"
)

// Null is the fallback provider: definitions via regex, no call edges.
type Null struct{}

// Name returns the provider identifier.
func (Null) Name() string { return "null" }

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
func (Null) Index(ctx context.Context, root string) (*providers.Graph, error) {
	root, err := providers.NormalizeRoot(root)
	if err != nil {
		return nil, err
	}
	g := &providers.Graph{
		Provider: "null",
		Defs:     map[string]*providers.Symbol{},
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
			if p != root && providers.SkipDir(d.Name()) {
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
				qn := providers.UniqueQName(g.Defs, rel, name, line)
				g.Defs[qn] = &providers.Symbol{QName: qn, Name: name, Kind: "symbol", File: rel, Line: line, Lang: ext}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	g.Langs = providers.SortedKeys(langSet)
	return g, nil
}
