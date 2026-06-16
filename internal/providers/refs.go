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

// Lookup resolves a raw reference to a callee QName using lexical scoping heuristics.
func (r RawRef) Lookup(byFileName, byDirName, byName, byFileRecvName, byDirRecvName, byRecvName map[string][]string, fileImports map[string]map[string]ResolvedImport) string {
	if IsHCLFile(r.CallerFile) {
		return r.LookupHCL(byFileName, byDirName, fileImports)
	}

	if strings.HasSuffix(r.CallerFile, ".html") {
		assoc := strings.TrimSuffix(r.CallerFile, ".html") + ".ts"
		if cands := byFileName[assoc+"\x00"+r.CalleeName]; len(cands) == 1 {
			return cands[0]
		}
	}

	if imports, ok := fileImports[r.CallerFile]; ok {
		if imp, ok := imports[r.CalleeName]; ok {
			switch imp.TargetName {
			case "default":
				if cands := byFileName[imp.TargetFile+"\x00default"]; len(cands) == 1 {
					return cands[0]
				}
				if cands := byFileName[imp.TargetFile+"\x00"+r.CalleeName]; len(cands) == 1 {
					return cands[0]
				}
				baseName := strings.TrimSuffix(filepath.Base(imp.TargetFile), filepath.Ext(imp.TargetFile))
				if cands := byFileName[imp.TargetFile+"\x00"+baseName]; len(cands) == 1 {
					return cands[0]
				}
				if sole := soleDefinitionInFile(imp.TargetFile, byFileName); sole != "" {
					return sole
				}
			case "*":
				return ""
			default:
				if cands := byFileName[imp.TargetFile+"\x00"+imp.TargetName]; len(cands) == 1 {
					return cands[0]
				}
			}
		}
	}

	if r.CalleeRecv != "" {
		if imports, ok := fileImports[r.CallerFile]; ok {
			if imp, ok := imports[r.CalleeRecv]; ok {
				if cands := byFileName[imp.TargetFile+"\x00"+r.CalleeName]; len(cands) == 1 {
					return cands[0]
				}
			}
		}

		if q := lookupRecv(r.CallerFile, r.CalleeRecv, r.CalleeName, byFileRecvName, byDirRecvName, byRecvName); q != "" {
			return q
		}
		if left, _, ok := strings.Cut(r.CalleeRecv, " as "); ok && left != "" {
			if q := lookupRecv(r.CallerFile, left, r.CalleeName, byFileRecvName, byDirRecvName, byRecvName); q != "" {
				return q
			}
		}
	}

	if !r.AllowBare {
		return ""
	}
	if cands := byFileName[r.CallerFile+"\x00"+r.CalleeName]; len(cands) == 1 {
		return cands[0]
	}
	if cands := byDirName[DirOf(r.CallerFile)+"\x00"+r.CalleeName]; len(cands) == 1 {
		return cands[0]
	}
	if cands := byName[r.CalleeName]; len(cands) == 1 {
		return cands[0]
	}
	return ""
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

func lookupRecv(file, recv, name string, byFileRecvName, byDirRecvName, byRecvName map[string][]string) string {
	if cands := byFileRecvName[file+"\x00"+recv+"\x00"+name]; len(cands) == 1 {
		return cands[0]
	}
	if cands := byDirRecvName[DirOf(file)+"\x00"+recv+"\x00"+name]; len(cands) == 1 {
		return cands[0]
	}
	if cands := byRecvName[recv+"\x00"+name]; len(cands) == 1 {
		return cands[0]
	}

	if len(recv) > 0 && unicode.IsLower(rune(recv[0])) {
		capitalized := string(unicode.ToUpper(rune(recv[0]))) + recv[1:]
		if cands := byFileRecvName[file+"\x00"+capitalized+"\x00"+name]; len(cands) == 1 {
			return cands[0]
		}
		if cands := byDirRecvName[DirOf(file)+"\x00"+capitalized+"\x00"+name]; len(cands) == 1 {
			return cands[0]
		}
		if cands := byRecvName[capitalized+"\x00"+name]; len(cands) == 1 {
			return cands[0]
		}
	}

	return ""
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
