package scan

import (
	"regexp"
	"strings"
)

var (
	tfRequiredVersionRe   = regexp.MustCompile(`required_version\s*=\s*"([^"]+)"`)
	tfRequiredProvidersRe = regexp.MustCompile(`required_providers\s*\{`)
	tfProviderEntryRe     = regexp.MustCompile(`(?s)([A-Za-z_][\w-]*)\s*=\s*\{([^{}]*)\}`)
	tfSourceAttrRe        = regexp.MustCompile(`source\s*=\s*"([^"]+)"`)
	tfVersionAttrRe       = regexp.MustCompile(`version\s*=\s*"([^"]+)"`)
	tfModuleBlockRe       = regexp.MustCompile(`(?m)^\s*module\s+"[^"]+"\s*\{`)
	tfLockProviderRe      = regexp.MustCompile(`(?s)provider\s+"([^"]+)"\s*\{([^{}]*)\}`)
)

// hclBlockBody returns the content between the brace at openIdx and its
// matching close brace (naive — braces inside strings are not special-cased;
// good enough for manifest-shaped HCL).
func hclBlockBody(s string, openIdx int) string {
	depth := 0
	for i := openIdx; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[openIdx+1 : i]
			}
		}
	}
	return s[openIdx+1:]
}

// tfSourceIsLocal reports whether a module source stays inside the repo.
func tfSourceIsLocal(src string) bool {
	return strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") ||
		src == "." || src == ".." || strings.Contains(src, "${get_repo_root()}")
}
