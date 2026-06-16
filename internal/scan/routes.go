package scan

import (
	"cmp"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const (
	maxRouteFiles = 2000
	maxRoutes     = 1000
)

var (
	methodPathRe  = regexp.MustCompile("(?i)\\.(get|post|put|delete|patch|head|options)\\s*\\(\\s*[\"'`]([^\"'`]+)")
	routeRe       = regexp.MustCompile("(?i)\\.route\\(\\s*[\"'`]([^\"'`]+)[\"'`]([^)]*)\\)")
	methodsListRe = regexp.MustCompile(`(?i)methods\s*=\s*\[([^\]]*)\]`)
	handleRe      = regexp.MustCompile("(?i)\\b(?:HandleFunc|Handle)\\(\\s*[\"'`]([^\"'`]+)")
)

var routeExts = map[string]bool{
	".go": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true,
	".mjs": true, ".cjs": true, ".py": true, ".rb": true,
}

// Route is one extracted HTTP route.
type Route struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Source     string `json:"source"`
	Pattern    string `json:"pattern"`
	Confidence string `json:"confidence"`
}

type routeCandidate struct {
	Method     string
	Path       string
	File       string
	Line       int
	Source     string
	Pattern    string
	Confidence string
}

// RoutesResult is the output of route extraction.
type RoutesResult struct {
	Routes    []Route `json:"routes"`
	Total     int     `json:"total"`
	Truncated bool    `json:"truncated,omitempty"`
	Note      string  `json:"note,omitempty"`
}

// ExtractRoutes walks a repository and extracts HTTP routes from common framework patterns.
func ExtractRoutes(root string) RoutesResult {
	out := RoutesResult{}

	seen := map[string]bool{}
	files := 0
	sawIaC := false
	addCandidate := func(c routeCandidate) {
		c.Method = strings.ToUpper(c.Method)
		key := c.Method + " " + c.Path + " " + c.File
		if seen[key] {
			return
		}
		seen[key] = true
		out.Routes = append(out.Routes, Route(c))
	}
	add := func(method, path, file string, line int, source, pattern, confidence string) {
		addCandidate(routeCandidate{
			Method:     method,
			Path:       path,
			File:       file,
			Line:       line,
			Source:     source,
			Pattern:    pattern,
			Confidence: confidence,
		})
	}

	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && ShouldSkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		relSlash := filepath.ToSlash(rel)
		lowerRel := strings.ToLower(relSlash)

		switch strings.ToLower(filepath.Ext(p)) {
		case ".tf", ".tofu":
			sawIaC = true
		case ".hcl":
			if d.Name() == "terragrunt.hcl" {
				sawIaC = true
			}
		}

		if isRouteDirPath(lowerRel) && (strings.HasSuffix(lowerRel, "+page.svelte") || strings.HasSuffix(lowerRel, "+server.ts") || strings.HasSuffix(lowerRel, "+server.js")) {
			path := svelteKitRoutePath(relSlash)
			if strings.HasSuffix(lowerRel, "+page.svelte") {
				add("GET", path, relSlash, 1, "file-convention", "SvelteKit page", "high")
			} else {
				data, rerr := os.ReadFile(p)
				if rerr == nil {
					methods := scanServerMethods(string(data))
					for _, m := range methods {
						add(m, path, relSlash, 1, "file-convention", "SvelteKit server endpoint", "high")
					}
				}
			}
			return nil
		}

		if (strings.Contains(lowerRel, "/app/") || strings.HasPrefix(lowerRel, "app/")) &&
			(strings.HasSuffix(lowerRel, "/page.tsx") || strings.HasSuffix(lowerRel, "/page.ts") || strings.HasSuffix(lowerRel, "/page.jsx") || strings.HasSuffix(lowerRel, "/page.js") ||
				strings.HasSuffix(lowerRel, "/route.ts") || strings.HasSuffix(lowerRel, "/route.js")) {
			path := nextAppRoutePath(relSlash)
			if !strings.HasSuffix(lowerRel, "/route.ts") && !strings.HasSuffix(lowerRel, "/route.js") {
				add("GET", path, relSlash, 1, "file-convention", "Next.js App Router Page", "high")
			} else {
				data, rerr := os.ReadFile(p)
				if rerr == nil {
					methods := scanServerMethods(string(data))
					for _, m := range methods {
						add(m, path, relSlash, 1, "file-convention", "Next.js App Router API", "high")
					}
				}
			}
			return nil
		}

		if (strings.Contains(lowerRel, "/pages/") || strings.HasPrefix(lowerRel, "pages/")) &&
			!strings.Contains(lowerRel, "/_") &&
			(strings.HasSuffix(lowerRel, ".tsx") || strings.HasSuffix(lowerRel, ".ts") || strings.HasSuffix(lowerRel, ".jsx") || strings.HasSuffix(lowerRel, ".js")) {
			path := nextPagesRoutePath(relSlash)
			pattern := "Next.js Pages Router Page"
			method := "GET"
			if strings.Contains(lowerRel, "/pages/api/") {
				pattern = "Next.js Pages Router API"
				method = "ANY"
			}
			add(method, path, relSlash, 1, "file-convention", pattern, "high")
			return nil
		}

		if isRouteDirPath(lowerRel) &&
			(strings.HasSuffix(lowerRel, ".tsx") || strings.HasSuffix(lowerRel, ".jsx") || strings.HasSuffix(lowerRel, ".ts") || strings.HasSuffix(lowerRel, ".js")) {
			path := remixRoutePath(relSlash)
			data, rerr := os.ReadFile(p)
			if rerr == nil {
				content := string(data)
				hasLoader := strings.Contains(content, "export const loader") || strings.Contains(content, "export async function loader")
				hasAction := strings.Contains(content, "export const action") || strings.Contains(content, "export async function action")
				if hasLoader || (!hasLoader && !hasAction) {
					add("GET", path, relSlash, 1, "file-convention", "Remix route module", "high")
				}
				if hasAction {
					add("POST", path, relSlash, 1, "file-convention", "Remix route module", "high")
				}
			}
			return nil
		}

		if !routeExts[strings.ToLower(filepath.Ext(d.Name()))] || isRouteTestFile(p) {
			return nil
		}
		if files >= maxRouteFiles || len(out.Routes) >= maxRoutes {
			return fs.SkipDir
		}
		files++
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		scanRoutes(string(data), relSlash, add)
		return nil
	})

	if len(out.Routes) >= maxRoutes {
		out.Truncated = true
	}
	slices.SortFunc(out.Routes, func(a, b Route) int {
		if c := cmp.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Method, b.Method); c != 0 {
			return c
		}
		return cmp.Compare(a.File, b.File)
	})
	out.Total = len(out.Routes)

	if out.Total == 0 {
		out.Note = "No HTTP routes matched the known framework patterns (Go chi/gin/echo/gorilla/net-http, SvelteKit, Next.js, Remix, Angular, Express, Flask, FastAPI)."
		if sawIaC {
			out.Note += " This looks like an infrastructure (Terraform/Terragrunt/OpenTofu) repo — its deploy surface is stacks, not routes; use the stacks tool."
		}
		return out
	}
	out.Note = "Routes matched from file-conventions and registration patterns — a recall-oriented heuristic, not a parser: bespoke routing may be missed and dynamic paths may be approximate."
	return out
}

func scanRoutes(content, file string, add func(method, path, file string, line int, source, pattern, confidence string)) {
	content = stripRouteComments(content)
	ext := strings.ToLower(filepath.Ext(file))
	if ext == ".go" {
		scanGoRoutes(content, file, add)
		return
	}

	scanAngularRoutes(content, file, add)

	reactRouterRe := regexp.MustCompile(`\bpath\s*(?::|=)\s*['"]([^'"]+)['"]`)
	for _, m := range reactRouterRe.FindAllStringSubmatchIndex(content, -1) {
		if insideRouteStringLiteral(content, m[0]) {
			continue
		}
		path := content[m[2]:m[3]]
		end := m[0] + 30
		if end > len(content) {
			end = len(content)
		}
		if (looksLikePath(path) || looksLikeReactRouterPath(path)) && !strings.Contains(content[m[0]:end], "component") {
			add("ANY", "/"+strings.TrimPrefix(path, "/"), file, lineAt(content, m[0]), "regex-heuristic", "React Router", "medium")
		}
	}

	for _, m := range methodPathRe.FindAllStringSubmatchIndex(content, -1) {
		if insideRouteStringLiteral(content, m[0]) {
			continue
		}
		method := content[m[2]:m[3]]
		path := content[m[4]:m[5]]
		if looksLikePath(path) {
			add(method, path, file, lineAt(content, m[0]), "regex-heuristic", "Express-style routing", "medium")
		}
	}

	for _, m := range routeRe.FindAllStringSubmatchIndex(content, -1) {
		if insideRouteStringLiteral(content, m[0]) {
			continue
		}
		path := content[m[2]:m[3]]
		if !looksLikePath(path) {
			continue
		}
		line := lineAt(content, m[0])
		rest := content[m[4]:m[5]]
		if ml := methodsListRe.FindStringSubmatch(rest); ml != nil {
			for _, meth := range splitMethods(ml[1]) {
				add(meth, path, file, line, "regex-heuristic", "Flask/Python routing", "medium")
			}
		} else {
			add("GET", path, file, line, "regex-heuristic", "Flask/Python routing", "medium")
		}
	}

	for _, m := range handleRe.FindAllStringSubmatchIndex(content, -1) {
		if insideRouteStringLiteral(content, m[0]) {
			continue
		}
		path := content[m[2]:m[3]]
		if looksLikePath(path) {
			add("ANY", path, file, lineAt(content, m[0]), "regex-heuristic", "Go standard HTTP multiplexer", "medium")
		}
	}
}

func stripRouteComments(content string) string {
	var b strings.Builder
	b.Grow(len(content))
	inSingle, inDouble, inBacktick, escaped, inBlock := false, false, false, false, false
	for i := 0; i < len(content); i++ {
		c := content[i]
		if inBlock {
			if c == '*' && i+1 < len(content) && content[i+1] == '/' {
				b.WriteString("  ")
				i++
				inBlock = false
				continue
			}
			if c == '\n' {
				b.WriteByte('\n')
			} else {
				b.WriteByte(' ')
			}
			continue
		}
		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}
		if inSingle || inDouble || inBacktick {
			b.WriteByte(c)
			if c == '\\' && !inBacktick {
				escaped = true
				continue
			}
			switch c {
			case '\'':
				if inSingle {
					inSingle = false
				}
			case '"':
				if inDouble {
					inDouble = false
				}
			case '`':
				if inBacktick {
					inBacktick = false
				}
			}
			continue
		}
		switch {
		case c == '/' && i+1 < len(content) && content[i+1] == '/':
			for i < len(content) && content[i] != '\n' {
				b.WriteByte(' ')
				i++
			}
			if i < len(content) {
				b.WriteByte('\n')
			}
		case c == '/' && i+1 < len(content) && content[i+1] == '*':
			b.WriteString("  ")
			i++
			inBlock = true
		case c == '#':
			for i < len(content) && content[i] != '\n' {
				b.WriteByte(' ')
				i++
			}
			if i < len(content) {
				b.WriteByte('\n')
			}
		case c == '\'':
			inSingle = true
			b.WriteByte(c)
		case c == '"':
			inDouble = true
			b.WriteByte(c)
		case c == '`':
			inBacktick = true
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func insideRouteStringLiteral(content string, idx int) bool {
	inSingle, inDouble, inBacktick, escaped := false, false, false, false
	for i := 0; i < len(content) && i < idx; i++ {
		c := content[i]
		if escaped {
			escaped = false
			continue
		}
		if inSingle || inDouble || inBacktick {
			if c == '\\' && !inBacktick {
				escaped = true
				continue
			}
			switch c {
			case '\'':
				if inSingle {
					inSingle = false
				}
			case '"':
				if inDouble {
					inDouble = false
				}
			case '`':
				if inBacktick {
					inBacktick = false
				}
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '`':
			inBacktick = true
		}
	}
	return inSingle || inDouble || inBacktick
}

func scanGoRoutes(content, file string, add func(method, path, file string, line int, source, pattern, confidence string)) {
	lines := strings.Split(content, "\n")
	prefixes := make(map[string]string)
	type prefixScope struct {
		router string
		prev   string
		depth  int
	}
	var scopes []prefixScope
	braceDepth := 0
	groupRe := regexp.MustCompile(`([\w$]+)\s*(?::?=|=)\s*([\w$]+)\.(?:Group|Route|PathPrefix|Subrouter)\(\s*["']([^"']+)`)
	nonAssignGroupRe := regexp.MustCompile(`([\w$]+)\.(?:Group|Route|PathPrefix|Subrouter)\(\s*["']([^"']+)`)
	methodRe := regexp.MustCompile(`(?i)([\w$]+)\.(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|HandleFunc|Handle)\(\s*["']([^"']+)`)

	offset := 0
	for i, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "//") {
			offset += len(rawLine) + 1
			continue
		}
		lineOffset := offset + len(rawLine) - len(strings.TrimLeft(rawLine, " \t"))
		assignedGroup := false
		if m := groupRe.FindStringSubmatchIndex(line); m != nil && !insideRouteStringLiteral(content, lineOffset+m[0]) {
			child := line[m[2]:m[3]]
			parent := line[m[4]:m[5]]
			pathArg := line[m[6]:m[7]]
			parentPrefix := prefixes[parent]
			fullPrefix := parentPrefix + "/" + strings.TrimPrefix(pathArg, "/")
			fullPrefix = strings.ReplaceAll(fullPrefix, "//", "/")
			prefixes[child] = fullPrefix
			assignedGroup = true
		}
		if !assignedGroup {
			if m := nonAssignGroupRe.FindStringSubmatchIndex(line); m != nil && !insideRouteStringLiteral(content, lineOffset+m[0]) {
				router := line[m[2]:m[3]]
				pathArg := line[m[4]:m[5]]
				prefix := prefixes[router]
				fullPrefix := prefix + "/" + strings.TrimPrefix(pathArg, "/")
				fullPrefix = strings.ReplaceAll(fullPrefix, "//", "/")
				scopeDepth := braceDepth + strings.Count(line, "{") - strings.Count(line, "}")
				if scopeDepth <= braceDepth {
					scopeDepth = braceDepth + 1
				}
				scopes = append(scopes, prefixScope{router: router, prev: prefixes[router], depth: scopeDepth})
				prefixes[router] = fullPrefix
			}
		}
		if m := methodRe.FindStringSubmatchIndex(line); m != nil && !insideRouteStringLiteral(content, lineOffset+m[0]) {
			router := line[m[2]:m[3]]
			method := line[m[4]:m[5]]
			pathArg := line[m[6]:m[7]]
			prefix := prefixes[router]
			fullPrefix := prefix + "/" + strings.TrimPrefix(pathArg, "/")
			fullPrefix = strings.ReplaceAll(fullPrefix, "//", "/")
			lineNum := i + 1
			meth := strings.ToUpper(method)
			if meth == "HANDLEFUNC" || meth == "HANDLE" {
				meth = "ANY"
			}
			add(meth, fullPrefix, file, lineNum, "regex-heuristic", "Go nested router group", "high")
		}

		braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
		if braceDepth < 0 {
			braceDepth = 0
		}
		for len(scopes) > 0 && braceDepth < scopes[len(scopes)-1].depth {
			scope := scopes[len(scopes)-1]
			scopes = scopes[:len(scopes)-1]
			if scope.prev == "" {
				delete(prefixes, scope.router)
			} else {
				prefixes[scope.router] = scope.prev
			}
		}
		offset += len(rawLine) + 1
	}
}

func scanAngularRoutes(content, file string, add func(method, path, file string, line int, source, pattern, confidence string)) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?s)\{[^{}]*\bpath\s*:\s*['"]([^'"]*)['"][^{}]*(?:\bcomponent\b|\bloadChildren\b|\bloadComponent\b)\s*:[^{}]*\}`),
		regexp.MustCompile(`(?s)\{[^{}]*(?:\bcomponent\b|\bloadChildren\b|\bloadComponent\b)\s*:[^{}]*\bpath\s*:\s*['"]([^'"]*)['"][^{}]*\}`),
	}
	for _, re := range patterns {
		for _, m := range re.FindAllStringSubmatchIndex(content, -1) {
			if insideRouteStringLiteral(content, m[0]) {
				continue
			}
			path := content[m[2]:m[3]]
			add("ANY", "/"+strings.TrimPrefix(path, "/"), file, lineAt(content, m[0]), "regex-heuristic", "Angular Router", "high")
		}
	}
}

func svelteKitRoutePath(relPath string) string {
	idx := strings.Index(relPath, "routes/")
	if idx < 0 {
		return "/"
	}
	sub := relPath[idx+len("routes/"):]
	sub = filepath.Dir(sub)
	if sub == "." || sub == "" {
		return "/"
	}
	parts := strings.Split(filepath.ToSlash(sub), "/")
	var routeParts []string
	for _, p := range parts {
		if seg, ok := normalizeFileRouteSegment(p); ok {
			routeParts = append(routeParts, seg)
		}
	}
	return "/" + strings.Join(routeParts, "/")
}

func isRouteDirPath(relPath string) bool {
	return strings.HasPrefix(relPath, "routes/") || strings.Contains(relPath, "/routes/")
}

func normalizeFileRouteSegment(seg string) (string, bool) {
	if seg == "" {
		return "", false
	}
	if strings.HasPrefix(seg, "(") && strings.HasSuffix(seg, ")") {
		return "", false
	}
	if strings.HasPrefix(seg, "@") || strings.HasPrefix(seg, "_") {
		return "", false
	}
	if strings.HasPrefix(seg, "[") && strings.HasSuffix(seg, "]") {
		param := strings.Trim(seg, "[]")
		param = strings.TrimPrefix(param, "...")
		if before, _, ok := strings.Cut(param, "="); ok {
			param = before
		}
		if param == "" {
			return "", false
		}
		if strings.Contains(seg, "...") {
			return ":" + param + "*", true
		}
		return ":" + param, true
	}
	return seg, true
}

func nextAppRoutePath(relPath string) string {
	idx := strings.Index(relPath, "app/")
	if idx < 0 {
		return "/"
	}
	sub := relPath[idx+len("app/"):]
	sub = filepath.Dir(sub)
	if sub == "." || sub == "" {
		return "/"
	}
	parts := strings.Split(filepath.ToSlash(sub), "/")
	var routeParts []string
	for _, p := range parts {
		if seg, ok := normalizeFileRouteSegment(p); ok {
			routeParts = append(routeParts, seg)
		}
	}
	return "/" + strings.Join(routeParts, "/")
}

func nextPagesRoutePath(relPath string) string {
	idx := strings.Index(relPath, "pages/")
	if idx < 0 {
		return "/"
	}
	sub := relPath[idx+len("pages/"):]
	ext := filepath.Ext(sub)
	sub = sub[:len(sub)-len(ext)]
	sub = strings.TrimSuffix(sub, "/index")
	if sub == "index" || sub == "" {
		return "/"
	}
	parts := strings.Split(filepath.ToSlash(sub), "/")
	var routeParts []string
	for _, p := range parts {
		if seg, ok := normalizeFileRouteSegment(p); ok {
			routeParts = append(routeParts, seg)
		}
	}
	if len(routeParts) == 0 {
		return "/"
	}
	return "/" + strings.Join(routeParts, "/")
}

func remixRoutePath(relPath string) string {
	idx := strings.Index(relPath, "routes/")
	if idx < 0 {
		return "/"
	}
	sub := relPath[idx+len("routes/"):]
	ext := filepath.Ext(sub)
	sub = sub[:len(sub)-len(ext)]
	sub = strings.ReplaceAll(sub, ".", "/")
	sub = strings.ReplaceAll(sub, "$", ":")
	sub = strings.TrimSuffix(sub, "/_index")
	if sub == "_index" || sub == "" {
		return "/"
	}
	return "/" + filepath.ToSlash(sub)
}

func scanServerMethods(content string) []string {
	content = stripRouteComments(content)
	var methods []string
	known := []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD"}
	for _, m := range known {
		if strings.Contains(content, "export const "+m) ||
			strings.Contains(content, "export function "+m) ||
			strings.Contains(content, "export async function "+m) {
			methods = append(methods, m)
		}
	}
	if len(methods) == 0 {
		return []string{"ANY"}
	}
	return methods
}

func looksLikePath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, ":")
}

func looksLikeReactRouterPath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.ContainsAny(s, " \t\n\r{}()[]<>\"'") {
		return false
	}
	if idx := strings.LastIndex(s, "."); idx >= 0 {
		ext := strings.ToLower(s[idx:])
		if ext == ".js" || ext == ".ts" || ext == ".jsx" || ext == ".tsx" || ext == ".json" || ext == ".css" || ext == ".html" || ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".svg" {
			return false
		}
	}
	return true
}

func splitMethods(s string) []string {
	var out []string
	for _, m := range strings.Split(s, ",") {
		m = strings.Trim(strings.TrimSpace(m), "\"'`")
		if m != "" {
			out = append(out, m)
		}
	}
	return out
}

func lineAt(content string, idx int) int {
	return 1 + strings.Count(content[:idx], "\n")
}

func isRouteTestFile(path string) bool {
	base := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(path))
	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	baseLower := strings.ToLower(base)
	if strings.Contains(baseLower, ".test.") ||
		strings.Contains(baseLower, ".spec.") ||
		strings.Contains(baseLower, ".cy.") ||
		strings.HasPrefix(baseLower, "test_") ||
		strings.HasSuffix(baseLower, "_test") ||
		strings.HasSuffix(baseLower, ".test"+ext) ||
		strings.HasSuffix(baseLower, ".spec"+ext) ||
		strings.HasSuffix(baseLower, ".cy"+ext) ||
		strings.HasSuffix(baseLower, ".tftest.hcl") ||
		strings.HasSuffix(baseLower, ".tofutest.hcl") {
		return true
	}
	slashed := "/" + filepath.ToSlash(strings.ToLower(path)) + "/"
	if strings.Contains(slashed, "/tests/") ||
		strings.Contains(slashed, "/__tests__/") ||
		strings.Contains(slashed, "/e2e/") ||
		strings.Contains(slashed, "/cypress/") ||
		strings.Contains(slashed, "/playwright/") {
		return true
	}
	return false
}
