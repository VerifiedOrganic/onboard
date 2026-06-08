package providers

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

const jsTagsQuery = `
(function_declaration (identifier) @name) @definition.function
(variable_declarator name: (identifier) @name value: (arrow_function)) @definition.function
(variable_declarator name: (identifier) @name value: (function_expression)) @definition.function
(class_declaration (identifier) @name) @definition.class
(method_definition (property_identifier) @name) @definition.method
(method_definition (identifier) @name) @definition.method
(pair key: (property_identifier) @name value: (arrow_function)) @definition.method
(pair key: (property_identifier) @name value: (function_expression)) @definition.method

(call_expression function: (identifier) @name) @reference.call
(call_expression function: (property_identifier) @name) @reference.call
(call_expression function: (member_expression property: (property_identifier) @name)) @reference.call
(new_expression constructor: (identifier) @name) @reference.call
(new_expression constructor: (member_expression property: (property_identifier) @name)) @reference.call

(jsx_self_closing_element name: (identifier) @name) @reference.call
(jsx_self_closing_element name: (member_expression property: (property_identifier) @name)) @reference.call
(jsx_opening_element name: (identifier) @name) @reference.call
(jsx_opening_element name: (member_expression property: (property_identifier) @name)) @reference.call
`

const tsTagsQuery = `
(function_declaration (identifier) @name) @definition.function
(variable_declarator name: (identifier) @name value: (arrow_function)) @definition.function
(variable_declarator name: (identifier) @name value: (function_expression)) @definition.function
(class_declaration (identifier) @name) @definition.class
(class_declaration (type_identifier) @name) @definition.class
(method_definition (property_identifier) @name) @definition.method
(method_definition (identifier) @name) @definition.method
(pair key: (property_identifier) @name value: (arrow_function)) @definition.method
(pair key: (property_identifier) @name value: (function_expression)) @definition.method

(interface_declaration (identifier) @name) @definition.interface
(interface_declaration (type_identifier) @name) @definition.interface
(type_alias_declaration (identifier) @name) @definition.type
(type_alias_declaration (type_identifier) @name) @definition.type
(enum_declaration (identifier) @name) @definition.type

(type_annotation (type_identifier) @name) @reference.call
(type_annotation (generic_type name: (type_identifier) @name)) @reference.call
(decorator (identifier) @name) @reference.call
(decorator (call_expression function: (identifier) @name)) @reference.call

(call_expression function: (identifier) @name) @reference.call
(call_expression function: (property_identifier) @name) @reference.call
(call_expression function: (member_expression property: (property_identifier) @name)) @reference.call
(new_expression constructor: (identifier) @name) @reference.call
(new_expression constructor: (member_expression property: (property_identifier) @name)) @reference.call
`

const tsxTagsQuery = tsTagsQuery + `
(jsx_self_closing_element name: (identifier) @name) @reference.call
(jsx_self_closing_element name: (member_expression property: (property_identifier) @name)) @reference.call
(jsx_opening_element name: (identifier) @name) @reference.call
(jsx_opening_element name: (member_expression property: (property_identifier) @name)) @reference.call
`

var esmImportRe = regexp.MustCompile(`(?m)import\s+(?:([\w$]+)\s*,\s*)?(?:(\*)\s+as\s+([\w$]+)|{([^}]+)})?\s*from\s*['"]([^'"]+)['"]`)
var requireImportRe = regexp.MustCompile(`(?m)\bconst\s+(?:([\w$]+)|{([^}]+)})\s*=\s*require\s*\(\s*['"]([^'"]+)['"]\s*\)`)

func jsRefHint(src []byte, tagStart, nameStart uint32) (recv string, allowBare bool) {
	allowBare = true
	if int(tagStart) > len(src) || int(nameStart) > len(src) || tagStart >= nameStart {
		return "", allowBare
	}
	prefix := strings.TrimRightFunc(string(src[tagStart:nameStart]), unicode.IsSpace)
	if strings.HasSuffix(prefix, ".") {
		expr := strings.TrimSpace(strings.TrimSuffix(prefix, "."))
		if idx := strings.LastIndexAny(expr, " \t\n\r()."); idx >= 0 {
			expr = expr[idx+1:]
		}
		if expr != "" && expr != "this" {
			return expr, false
		}
	}
	return "", true
}

func getOrBuildTagger(name string, taggers map[string]*ts.Tagger) *ts.Tagger {
	if t, ok := taggers[name]; ok {
		return t
	}
	entry := grammars.DetectLanguage("file." + getExtForLang(name))
	if entry == nil {
		return nil
	}
	tagger := buildTagger(entry)
	taggers[name] = tagger
	return tagger
}

func getExtForLang(name string) string {
	switch name {
	case "javascript":
		return "js"
	case "typescript":
		return "ts"
	case "tsx":
		return "tsx"
	}
	return "js"
}

func parseJSImports(root, rel string, src []byte) map[string]resolvedImport {
	imports := make(map[string]resolvedImport)
	srcStr := string(src)

	// 1. ESM Imports
	for _, m := range esmImportRe.FindAllStringSubmatch(srcStr, -1) {
		defaultImport := m[1]
		namespaceImport := m[3]
		namedImports := m[4]
		importPath := m[5]

		targetFile := resolveImportPath(root, rel, importPath)
		if targetFile == "" {
			continue
		}

		if defaultImport != "" {
			imports[defaultImport] = resolvedImport{targetFile: targetFile, targetName: "default"}
		}
		if namespaceImport != "" {
			imports[namespaceImport] = resolvedImport{targetFile: targetFile, targetName: "*"}
		}
		if namedImports != "" {
			for _, part := range strings.Split(namedImports, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				if strings.Contains(part, " as ") {
					parts := strings.Split(part, " as ")
					orig := strings.TrimSpace(parts[0])
					alias := strings.TrimSpace(parts[1])
					imports[alias] = resolvedImport{targetFile: targetFile, targetName: orig}
				} else {
					imports[part] = resolvedImport{targetFile: targetFile, targetName: part}
				}
			}
		}
	}

	// 2. require imports
	for _, m := range requireImportRe.FindAllStringSubmatch(srcStr, -1) {
		defaultImport := m[1]
		namedImports := m[2]
		importPath := m[3]

		targetFile := resolveImportPath(root, rel, importPath)
		if targetFile == "" {
			continue
		}

		if defaultImport != "" {
			imports[defaultImport] = resolvedImport{targetFile: targetFile, targetName: "default"}
		}
		if namedImports != "" {
			for _, part := range strings.Split(namedImports, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				if strings.Contains(part, ":") {
					parts := strings.Split(part, ":")
					orig := strings.TrimSpace(parts[0])
					alias := strings.TrimSpace(parts[1])
					imports[alias] = resolvedImport{targetFile: targetFile, targetName: orig}
				} else {
					imports[part] = resolvedImport{targetFile: targetFile, targetName: part}
				}
			}
		}
	}

	return imports
}

func resolveImportPath(root, currentFile, importPath string) string {
	if importPath == "" {
		return ""
	}
	if strings.HasPrefix(importPath, "@/") {
		p := filepath.Join(root, "src", importPath[2:])
		if fileExists(p) {
			rel, _ := filepath.Rel(root, p)
			return filepath.ToSlash(rel)
		}
		p = filepath.Join(root, importPath[2:])
		if fileExists(p) {
			rel, _ := filepath.Rel(root, p)
			return filepath.ToSlash(rel)
		}
		if tsPath := resolveTsconfigPath(root, importPath); tsPath != "" {
			return tsPath
		}
	} else if strings.HasPrefix(importPath, "~/") {
		p := filepath.Join(root, "src", importPath[2:])
		if fileExists(p) {
			rel, _ := filepath.Rel(root, p)
			return filepath.ToSlash(rel)
		}
		p = filepath.Join(root, importPath[2:])
		if fileExists(p) {
			rel, _ := filepath.Rel(root, p)
			return filepath.ToSlash(rel)
		}
	}

	if strings.HasPrefix(importPath, ".") {
		dir := filepath.Dir(filepath.Join(root, currentFile))
		p := filepath.Join(dir, importPath)
		target := findFileWithExt(p)
		if target != "" {
			rel, _ := filepath.Rel(root, target)
			return filepath.ToSlash(rel)
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func findFileWithExt(basePath string) string {
	exts := []string{".tsx", ".ts", ".jsx", ".js", ".svelte", ""}
	for _, ext := range exts {
		p := basePath + ext
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	for _, ext := range exts {
		if ext == "" {
			continue
		}
		p := filepath.Join(basePath, "index"+ext)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func resolveTsconfigPath(root, importPath string) string {
	data, err := os.ReadFile(filepath.Join(root, "tsconfig.json"))
	if err != nil {
		data, err = os.ReadFile(filepath.Join(root, "jsconfig.json"))
	}
	if err != nil {
		return ""
	}
	pathsRe := regexp.MustCompile(`"([^"]+)"\s*:\s*\[\s*"([^"]+)"\s*\]`)
	matches := pathsRe.FindAllStringSubmatch(string(data), -1)
	for _, m := range matches {
		key := m[1]
		val := m[2]
		if strings.HasSuffix(key, "/*") && strings.HasSuffix(val, "/*") {
			prefix := key[:len(key)-2]
			targetPrefix := val[:len(val)-2]
			if strings.HasPrefix(importPath, prefix) {
				subPath := importPath[len(prefix):]
				targetPrefix = strings.TrimPrefix(targetPrefix, "./")
				p := filepath.Join(root, targetPrefix, subPath)
				if target := findFileWithExt(p); target != "" {
					rel, _ := filepath.Rel(root, target)
					return filepath.ToSlash(rel)
				}
			}
		}
	}
	return ""
}

func tagSvelteFile(rel string, src []byte, taggers map[string]*ts.Tagger) ([]*Symbol, []rawRef) {
	var defs []*Symbol
	var refs []rawRef

	// Svelte implicit component symbols
	compName := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	defs = append(defs, &Symbol{
		QName:  rel + "::default",
		Name:   "default",
		Kind:   "component",
		File:   rel,
		Line:   1,
		Lang:   "svelte",
		Public: true,
	})
	defs = append(defs, &Symbol{
		QName:  rel + "::" + compName,
		Name:   compName,
		Kind:   "component",
		File:   rel,
		Line:   1,
		Lang:   "svelte",
		Public: true,
	})

	srcStr := string(src)
	scriptRe := regexp.MustCompile(`(?i)<script([^>]*)>([\s\S]*?)</script>`)
	matches := scriptRe.FindAllStringSubmatchIndex(srcStr, -1)

	maskedSrc := make([]byte, len(src))
	copy(maskedSrc, src)

	for _, m := range matches {
		attrStart, attrEnd := m[2], m[3]
		contentStart, contentEnd := m[4], m[5]

		replaceContentWithSpaces(maskedSrc, m[0], m[1])

		attrs := srcStr[attrStart:attrEnd]
		scriptContent := srcStr[contentStart:contentEnd]

		lang := "javascript"
		if strings.Contains(attrs, `lang="ts"`) || strings.Contains(attrs, `lang="typescript"`) {
			lang = "typescript"
		}

		tagger := getOrBuildTagger(lang, taggers)
		if tagger == nil {
			continue
		}

		scriptSrcBytes := []byte(scriptContent)
		tags := safeTag(tagger, scriptSrcBytes)
		if len(tags) == 0 {
			continue
		}

		scriptDefs, scriptRefs := tagFile(rel, lang, scriptSrcBytes, tags)
		startLine := lineAt(srcStr, contentStart)

		for _, sym := range scriptDefs {
			sym.Line = startLine + sym.Line - 1
			sym.QName = uniqueQNameForSvelte(defs, rel, sym.Name, sym.Line)
			defs = append(defs, sym)
		}

		for _, ref := range scriptRefs {
			ref.callerFile = rel
			refs = append(refs, ref)
		}
	}

	styleRe := regexp.MustCompile(`(?i)<style([^>]*)>([\s\S]*?)</style>`)
	for _, m := range styleRe.FindAllStringSubmatchIndex(srcStr, -1) {
		replaceContentWithSpaces(maskedSrc, m[0], m[1])
	}

	templateRefs := scanTemplateRefs(maskedSrc, false)
	caller := rel + "::(top-level)"
	for _, r := range templateRefs {
		refs = append(refs, rawRef{
			callerQName: caller,
			callerFile:  rel,
			calleeName:  r,
			allowBare:   true,
		})
	}

	return defs, refs
}

func uniqueQNameForSvelte(defs []*Symbol, rel, name string, line int) string {
	qn := rel + "::" + name
	exists := false
	for _, sym := range defs {
		if sym.QName == qn {
			exists = true
			break
		}
	}
	if !exists {
		return qn
	}
	base := fmt.Sprintf("%s::%s#%d", rel, name, line)
	qn = base
	for n := 2; ; n++ {
		exists = false
		for _, sym := range defs {
			if sym.QName == qn {
				exists = true
				break
			}
		}
		if !exists {
			return qn
		}
		qn = fmt.Sprintf("%s.%d", base, n)
	}
}

func tagHTMLFile(rel string, src []byte) ([]*Symbol, []rawRef) {
	var refs []rawRef
	templateRefs := scanTemplateRefs(src, true)
	caller := rel + "::(top-level)"
	for _, r := range templateRefs {
		refs = append(refs, rawRef{
			callerQName: caller,
			callerFile:  rel,
			calleeName:  r,
			allowBare:   true,
		})
	}
	return nil, refs
}

var jsKeywords = map[string]bool{
	"let": true, "const": true, "var": true, "function": true, "class": true,
	"import": true, "export": true, "from": true, "default": true, "true": true,
	"false": true, "null": true, "undefined": true, "if": true, "else": true,
	"for": true, "each": true, "as": true, "await": true, "then": true,
	"catch": true, "promise": true, "key": true, "this": true, "of": true,
}

func scanTemplateRefs(src []byte, isAngular bool) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || jsKeywords[s] {
			return
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}

	srcStr := string(src)

	if isAngular {
		eventRe := regexp.MustCompile(`\([\w-]+\)\s*=\s*["']([^"']+)["']`)
		for _, m := range eventRe.FindAllStringSubmatch(srcStr, -1) {
			extractIdentifiers(m[1], add)
		}
		interpRe := regexp.MustCompile(`\{\{([^}]+)\}\}`)
		for _, m := range interpRe.FindAllStringSubmatch(srcStr, -1) {
			extractIdentifiers(m[1], add)
		}
		propRe := regexp.MustCompile(`\[[\w-]+\]\s*=\s*["']([^"']+)["']`)
		for _, m := range propRe.FindAllStringSubmatch(srcStr, -1) {
			extractIdentifiers(m[1], add)
		}
		structRe := regexp.MustCompile(`\*[\w-]+\s*=\s*["']([^"']+)["']`)
		for _, m := range structRe.FindAllStringSubmatch(srcStr, -1) {
			extractIdentifiers(m[1], add)
		}
	} else {
		tagRe := regexp.MustCompile(`<([A-Z][a-zA-Z0-9_-]*(?:\.[A-Z][a-zA-Z0-9_-]*)*)`)
		for _, m := range tagRe.FindAllStringSubmatch(srcStr, -1) {
			name := m[1]
			if i := strings.IndexByte(name, '.'); i >= 0 {
				name = name[:i]
			}
			add(name)
		}
		depth := 0
		start := -1
		for i := 0; i < len(src); i++ {
			switch src[i] {
			case '{':
				if depth == 0 {
					start = i + 1
				}
				depth++
			case '}':
				depth--
				if depth == 0 && start >= 0 {
					expr := string(src[start:i])
					extractIdentifiers(expr, add)
					start = -1
				}
			}
		}
	}

	return out
}

func extractIdentifiers(expr string, add func(string)) {
	identRe := regexp.MustCompile(`[a-zA-Z_$][\w$]*`)
	for _, match := range identRe.FindAllString(expr, -1) {
		add(match)
	}
}

func replaceContentWithSpaces(src []byte, start, end int) {
	for i := start; i < end; i++ {
		if src[i] != '\n' && src[i] != '\r' {
			src[i] = ' '
		}
	}
}

func lineAt(content string, idx int) int {
	return 1 + strings.Count(content[:idx], "\n")
}
