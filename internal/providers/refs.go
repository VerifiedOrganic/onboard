package providers

import (
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

// RawRef is an unresolved call reference extracted during indexing.
type RawRef struct {
	CallerQName string
	CallerFile  string
	CalleeName  string
	CalleeRecv  string
	AllowBare   bool
}

// RefLookupIndex groups definition and import indexes used to resolve raw references.
type RefLookupIndex struct {
	ByFileName     map[string][]string
	ByDirName      map[string][]string
	ByName         map[string][]string
	ByFileRecvName map[string][]string
	ByDirRecvName  map[string][]string
	ByRecvName     map[string][]string
	FileImports    map[string]map[string]ResolvedImport
}

// Lookup resolves a raw reference to a callee QName using lexical scoping heuristics.
func (r RawRef) Lookup(index RefLookupIndex) string {
	if IsHCLFile(r.CallerFile) {
		return r.LookupHCL(index.ByFileName, index.ByDirName, index.FileImports)
	}

	if strings.HasSuffix(r.CallerFile, ".html") {
		assoc := strings.TrimSuffix(r.CallerFile, ".html") + ".ts"
		if cands := index.ByFileName[nameKey(assoc, r.CalleeName)]; len(cands) == 1 {
			return cands[0]
		}
	}

	if imports, ok := index.FileImports[r.CallerFile]; ok {
		if imp, ok := imports[r.CalleeName]; ok {
			switch imp.TargetName {
			case "default":
				if cands := index.ByFileName[nameKey(imp.TargetFile, "default")]; len(cands) == 1 {
					return cands[0]
				}
				if cands := index.ByFileName[nameKey(imp.TargetFile, r.CalleeName)]; len(cands) == 1 {
					return cands[0]
				}
				baseName := strings.TrimSuffix(filepath.Base(imp.TargetFile), filepath.Ext(imp.TargetFile))
				if cands := index.ByFileName[nameKey(imp.TargetFile, baseName)]; len(cands) == 1 {
					return cands[0]
				}
				if sole := soleDefinitionInFile(imp.TargetFile, index.ByFileName); sole != "" {
					return sole
				}
			case "*":
				return ""
			default:
				if cands := index.ByFileName[nameKey(imp.TargetFile, imp.TargetName)]; len(cands) == 1 {
					return cands[0]
				}
			}
		}
	}

	if r.CalleeRecv != "" {
		if imports, ok := index.FileImports[r.CallerFile]; ok {
			if imp, ok := imports[r.CalleeRecv]; ok {
				if cands := index.ByFileName[nameKey(imp.TargetFile, r.CalleeName)]; len(cands) == 1 {
					return cands[0]
				}
			}
		}

		if q := lookupRecv(index, r.CallerFile, r.CalleeRecv, r.CalleeName); q != "" {
			return q
		}
		if left, _, ok := strings.Cut(r.CalleeRecv, " as "); ok && left != "" {
			if q := lookupRecv(index, r.CallerFile, left, r.CalleeName); q != "" {
				return q
			}
		}
	}

	if !r.AllowBare {
		return ""
	}
	if cands := index.ByFileName[nameKey(r.CallerFile, r.CalleeName)]; len(cands) == 1 {
		return cands[0]
	}
	if cands := index.ByDirName[nameKey(DirOf(r.CallerFile), r.CalleeName)]; len(cands) == 1 {
		return cands[0]
	}
	if cands := index.ByName[r.CalleeName]; len(cands) == 1 {
		return cands[0]
	}
	return ""
}

func nameKey(scope, name string) string {
	return scope + "\x00" + name
}

func soleDefinitionInFile(file string, byFileName map[string][]string) string {
	prefix := file + "\x00"
	var sole string
	count := 0
	for k, cands := range byFileName {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		for _, q := range cands {
			count++
			if count > 1 {
				return ""
			}
			sole = q
		}
	}
	return sole
}

func lookupRecv(index RefLookupIndex, file, recv, name string) string {
	if cands := index.ByFileRecvName[recvKey(file, recv, name)]; len(cands) == 1 {
		return cands[0]
	}
	if cands := index.ByDirRecvName[recvKey(DirOf(file), recv, name)]; len(cands) == 1 {
		return cands[0]
	}
	if cands := index.ByRecvName[nameKey(recv, name)]; len(cands) == 1 {
		return cands[0]
	}

	if len(recv) > 0 && unicode.IsLower(rune(recv[0])) {
		capitalized := string(unicode.ToUpper(rune(recv[0]))) + recv[1:]
		if cands := index.ByFileRecvName[recvKey(file, capitalized, name)]; len(cands) == 1 {
			return cands[0]
		}
		if cands := index.ByDirRecvName[recvKey(DirOf(file), capitalized, name)]; len(cands) == 1 {
			return cands[0]
		}
		if cands := index.ByRecvName[nameKey(capitalized, name)]; len(cands) == 1 {
			return cands[0]
		}
	}

	return ""
}

func recvKey(scope, recv, name string) string {
	return scope + "\x00" + recv + "\x00" + name
}

// defSpan records a definition's byte span for caller attribution during tagging.
type defSpan struct {
	qname      string
	start, end uint32
}

// DirOf returns the slash directory of a repo-relative file path, or "" for a top-level file.
func DirOf(file string) string {
	if d := path.Dir(file); d != "." {
		return d
	}
	return ""
}
