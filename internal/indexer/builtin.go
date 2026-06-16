// Package indexer implements syntactic repository indexing (Builtin and Null providers).
package indexer

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/VerifiedOrganic/onboard/internal/providers"
)

// Builtin is the pure-Go tree-sitter code-graph engine.
type Builtin struct{}

const maxIndexedFileBytes = 4 << 20

// Name returns the provider identifier.
func (Builtin) Name() string { return "builtin" }

// Index walks root and builds a syntactic call graph via tree-sitter tags.
func (Builtin) Index(ctx context.Context, root string) (*providers.Graph, error) {
	return indexBuiltin(ctx, root, "")
}

// IndexWithCache is Index plus a persistent, content-hashed per-file cache at cachePath.
func (Builtin) IndexWithCache(ctx context.Context, root, cachePath string) (*providers.Graph, error) {
	return indexBuiltin(ctx, root, cachePath)
}

func indexBuiltin(ctx context.Context, root, cachePath string) (*providers.Graph, error) {
	root, err := providers.NormalizeRoot(root)
	if err != nil {
		return nil, err
	}

	var prev *providers.DiskIndex
	if cachePath != "" {
		prev = providers.LoadDiskIndex(cachePath)
	}
	fresh := &providers.DiskIndex{Version: providers.CacheVersion, Files: map[string]providers.DiskFile{}}
	perFile := map[string]providers.FileData{}

	taggers := map[string]*ts.Tagger{}
	var hclEng *providers.HCLEngine
	hclEngKnown := false
	var reused, retagged int
	var skippedLarge []string

	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
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

		entry := grammars.DetectLanguage(p)
		if entry == nil {
			if ext := strings.ToLower(filepath.Ext(p)); ext == ".tofu" || ext == ".tofuvars" {
				entry = grammars.DetectLanguageByName("hcl")
			}
		}
		if entry == nil {
			return nil
		}
		info, statErr := d.Info()
		if statErr == nil && info.Size() > maxIndexedFileBytes {
			rel, _ := filepath.Rel(root, p)
			skippedLarge = append(skippedLarge, filepath.ToSlash(rel))
			return nil
		}
		src, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		h := providers.HashBytes(src)

		if prev != nil {
			if pf, ok := prev.Files[rel]; ok && pf.Hash == h {
				perFile[rel] = providers.FileData{
					Lang:    pf.Lang,
					Defs:    pf.Defs,
					Refs:    providers.FromDiskRefs(pf.Refs),
					Imports: providers.FromDiskImports(pf.Imports),
				}
				fresh.Files[rel] = pf
				reused++
				return nil
			}
		}

		if entry.Name == "svelte" {
			defs, refs := providers.TagSvelteFile(rel, src, taggers)
			imports := providers.ParseJSImports(root, rel, src)
			perFile[rel] = providers.FileData{Lang: entry.Name, Defs: defs, Refs: refs, Imports: imports}
			fresh.Files[rel] = providers.DiskFile{
				Hash:    h,
				Lang:    entry.Name,
				Defs:    defs,
				Refs:    providers.ToDiskRefs(refs),
				Imports: providers.ToDiskImports(imports),
			}
			retagged++
			return nil
		}

		if entry.Name == "hcl" {
			if base := filepath.Base(p); base == ".terraform.lock.hcl" || base == ".opentofu.lock.hcl" {
				return nil
			}
			if !hclEngKnown {
				hclEng = providers.NewHCLEngine()
				hclEngKnown = true
			}
			if hclEng == nil {
				return nil
			}
			defs, refs, imports := providers.TagHCLFile(root, rel, src, hclEng)
			if len(defs) == 0 && len(refs) == 0 {
				return nil
			}
			perFile[rel] = providers.FileData{Lang: "hcl", Defs: defs, Refs: refs, Imports: imports}
			fresh.Files[rel] = providers.DiskFile{
				Hash:    h,
				Lang:    "hcl",
				Defs:    defs,
				Refs:    providers.ToDiskRefs(refs),
				Imports: providers.ToDiskImports(imports),
			}
			retagged++
			return nil
		}

		if entry.Name == "html" {
			defs, refs := providers.TagHTMLFile(rel, src)
			perFile[rel] = providers.FileData{Lang: entry.Name, Defs: defs, Refs: refs}
			fresh.Files[rel] = providers.DiskFile{
				Hash: h,
				Lang: entry.Name,
				Defs: defs,
				Refs: providers.ToDiskRefs(refs),
			}
			retagged++
			return nil
		}

		tagger, known := taggers[entry.Name]
		if !known {
			tagger = providers.BuildTagger(entry)
			taggers[entry.Name] = tagger
		}
		if tagger == nil {
			return nil
		}
		tags := providers.SafeTag(tagger, src)
		if len(tags) == 0 {
			return nil
		}
		defs, refs := providers.TagFile(rel, entry.Name, src, tags)
		var imports map[string]providers.ResolvedImport
		if entry.Name == "javascript" || entry.Name == "typescript" || entry.Name == "tsx" {
			imports = providers.ParseJSImports(root, rel, src)
		}
		perFile[rel] = providers.FileData{Lang: entry.Name, Defs: defs, Refs: refs, Imports: imports}
		fresh.Files[rel] = providers.DiskFile{
			Hash:    h,
			Lang:    entry.Name,
			Defs:    defs,
			Refs:    providers.ToDiskRefs(refs),
			Imports: providers.ToDiskImports(imports),
		}
		retagged++
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	g := assembleGraph(perFile)
	g.Reused, g.Retagged = reused, retagged
	if len(skippedLarge) > 0 {
		g.Note = fmt.Sprintf("Skipped %d file(s) larger than %d MiB during indexing: %s", len(skippedLarge), maxIndexedFileBytes>>20, strings.Join(skippedLarge, ", "))
	}
	if cachePath != "" {
		providers.SaveDiskIndex(cachePath, fresh)
	}
	return g, nil
}

func assembleGraph(perFile map[string]providers.FileData) *providers.Graph {
	g := &providers.Graph{
		Provider: "builtin",
		Defs:     map[string]*providers.Symbol{},
		Forward:  map[string][]string{},
		Reverse:  map[string][]string{},
	}
	defsByName := map[string][]string{}
	defsByFileName := map[string][]string{}
	defsByDirName := map[string][]string{}
	defsByRecvName := map[string][]string{}
	defsByFileRecvName := map[string][]string{}
	defsByDirRecvName := map[string][]string{}
	langSet := map[string]bool{}
	var refs []providers.RawRef
	fileImports := make(map[string]map[string]providers.ResolvedImport)

	for file, fd := range perFile {
		langSet[fd.Lang] = true
		g.Files++
		if len(fd.Imports) > 0 {
			fileImports[file] = fd.Imports
		}
		for _, sym := range fd.Defs {
			if sym == nil {
				continue
			}
			g.Defs[sym.QName] = sym
			defsByName[sym.Name] = append(defsByName[sym.Name], sym.QName)
			defsByFileName[sym.File+"\x00"+sym.Name] = append(defsByFileName[sym.File+"\x00"+sym.Name], sym.QName)
			defsByDirName[providers.DirOf(sym.File)+"\x00"+sym.Name] = append(defsByDirName[providers.DirOf(sym.File)+"\x00"+sym.Name], sym.QName)
			if sym.Recv != "" {
				defsByRecvName[sym.Recv+"\x00"+sym.Name] = append(defsByRecvName[sym.Recv+"\x00"+sym.Name], sym.QName)
				defsByFileRecvName[sym.File+"\x00"+sym.Recv+"\x00"+sym.Name] = append(defsByFileRecvName[sym.File+"\x00"+sym.Recv+"\x00"+sym.Name], sym.QName)
				defsByDirRecvName[providers.DirOf(sym.File)+"\x00"+sym.Recv+"\x00"+sym.Name] = append(defsByDirRecvName[providers.DirOf(sym.File)+"\x00"+sym.Recv+"\x00"+sym.Name], sym.QName)
			}
		}
		refs = append(refs, fd.Refs...)
	}

	edges := providers.NewGraphEdgeSet()
	for _, r := range refs {
		callee := r.Lookup(defsByFileName, defsByDirName, defsByName, defsByFileRecvName, defsByDirRecvName, defsByRecvName, fileImports)
		if callee == "" || callee == r.CallerQName {
			if callee == "" {
				g.Unresolved++
			}
			continue
		}
		edges.Add(g, r.CallerQName, callee)
	}

	g.Langs = providers.SortedKeys(langSet)
	if g.Files == 0 {
		g.Note = "No files matched a supported grammar with a tags query."
	} else {
		g.Note = "Call edges are syntactic (name + lexical scope), not type-checked; treat as likely, not proven."
	}
	return g
}
