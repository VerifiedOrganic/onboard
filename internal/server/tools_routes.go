package server

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// routes extracts an HTTP API surface from common router-registration patterns across
// frameworks (Go chi/gin/echo/gorilla/net-http, Express, Flask, FastAPI). Unlike deps and
// schema, route registration is not a single grammar, so this is a PATTERN matcher, not a
// parser: it favors recall and is honest that it can miss bespoke routing and occasionally
// over-match. The result is a method+path+location list — the endpoint map a newcomer wants.

const (
	maxRouteFiles = 2000
	maxRoutes     = 1000
)

var (
	// method calls such as .get / .GET / .post taking a quoted path — chi, gin, echo, Express, FastAPI.
	methodPathRe = regexp.MustCompile("(?i)\\.(get|post|put|delete|patch|head|options)\\s*\\(\\s*[\"'`]([^\"'`]+)")
	// a .route call taking a quoted path plus an optional methods=[...] list — Flask / Django.
	routeRe       = regexp.MustCompile("(?i)\\.route\\(\\s*[\"'`]([^\"'`]+)[\"'`]([^)]*)\\)")
	methodsListRe = regexp.MustCompile(`(?i)methods\s*=\s*\[([^\]]*)\]`)
	// net/http and gorilla HandleFunc / Handle taking a quoted path — the method is unknown.
	handleRe = regexp.MustCompile("(?i)\\b(?:HandleFunc|Handle)\\(\\s*[\"'`]([^\"'`]+)")
)

var routeExts = map[string]bool{
	".go": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true,
	".mjs": true, ".cjs": true, ".py": true, ".rb": true,
}

type route struct {
	Method     string `json:"method"` // GET/POST/...; ANY when the pattern does not pin one
	Path       string `json:"path"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Source     string `json:"source"`
	Pattern    string `json:"pattern"`
	Confidence string `json:"confidence"`
}

type routesInput struct {
	Root string `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
}

type routesOutput struct {
	Routes    []route `json:"routes"`
	Total     int     `json:"total"`
	Truncated bool    `json:"truncated,omitempty"`
	Note      string  `json:"note,omitempty"`
}

func routesExtract(_ context.Context, in routesInput) (routesOutput, error) {
	out := routesOutput{}
	root, err := resolveRoot(in.Root)
	if err != nil {
		return out, err
	}

	seen := map[string]bool{}
	files := 0
	add := func(method, path, file string, line int, source, pattern, confidence string) {
		method = strings.ToUpper(method)
		key := method + " " + path + " " + file
		if seen[key] {
			return
		}
		seen[key] = true
		out.Routes = append(out.Routes, route{
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
			if p != root && shouldSkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		relSlash := filepath.ToSlash(rel)
		lowerRel := strings.ToLower(relSlash)

		// 1. SvelteKit file-convention
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

		// 2. Next.js App Router file-convention
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

		// 3. Next.js Pages Router file-convention
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

		// 4. Remix flat routes
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

		if !routeExts[strings.ToLower(filepath.Ext(d.Name()))] || isTestFile(p) {
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
	sort.Slice(out.Routes, func(i, j int) bool {
		if out.Routes[i].Path != out.Routes[j].Path {
			return out.Routes[i].Path < out.Routes[j].Path
		}
		if out.Routes[i].Method != out.Routes[j].Method {
			return out.Routes[i].Method < out.Routes[j].Method
		}
		return out.Routes[i].File < out.Routes[j].File
	})
	out.Total = len(out.Routes)

	if out.Total == 0 {
		out.Note = "No HTTP routes matched the known framework patterns (Go chi/gin/echo/gorilla/net-http, SvelteKit, Next.js, Remix, Angular, Express, Flask, FastAPI)."
		return out, nil
	}
	out.Note = "Routes matched from file-conventions and registration patterns — a recall-oriented heuristic, not a parser: bespoke routing may be missed and dynamic paths may be approximate."
	return out, nil
}

func scanRoutes(content, file string, add func(method, path, file string, line int, source, pattern, confidence string)) {
	ext := strings.ToLower(filepath.Ext(file))
	if ext == ".go" {
		scanGoRoutes(content, file, add)
		return
	}

	scanAngularRoutes(content, file, add)

	// React Router
	reactRouterRe := regexp.MustCompile(`\bpath\s*(?::|=)\s*['"]([^'"]+)['"]`)
	for _, m := range reactRouterRe.FindAllStringSubmatchIndex(content, -1) {
		path := content[m[2]:m[3]]
		end := m[0] + 30
		if end > len(content) {
			end = len(content)
		}
		if (looksLikePath(path) || looksLikeReactRouterPath(path)) && !strings.Contains(content[m[0]:end], "component") {
			add("ANY", "/"+strings.TrimPrefix(path, "/"), file, lineAt(content, m[0]), "regex-heuristic", "React Router", "medium")
		}
	}

	// Express/FastAPI-style
	for _, m := range methodPathRe.FindAllStringSubmatchIndex(content, -1) {
		method := content[m[2]:m[3]]
		path := content[m[4]:m[5]]
		if looksLikePath(path) {
			add(method, path, file, lineAt(content, m[0]), "regex-heuristic", "Express-style routing", "medium")
		}
	}

	// Flask/Django
	for _, m := range routeRe.FindAllStringSubmatchIndex(content, -1) {
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

	// HandleFunc / Handle
	for _, m := range handleRe.FindAllStringSubmatchIndex(content, -1) {
		path := content[m[2]:m[3]]
		if looksLikePath(path) {
			add("ANY", path, file, lineAt(content, m[0]), "regex-heuristic", "Go standard HTTP multiplexer", "medium")
		}
	}
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

	for i, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		assignedGroup := false
		if m := groupRe.FindStringSubmatch(line); m != nil {
			child := m[1]
			parent := m[2]
			pathArg := m[3]
			parentPrefix := prefixes[parent]
			fullPrefix := parentPrefix + "/" + strings.TrimPrefix(pathArg, "/")
			fullPrefix = strings.ReplaceAll(fullPrefix, "//", "/")
			prefixes[child] = fullPrefix
			assignedGroup = true
		}
		if !assignedGroup {
			if m := nonAssignGroupRe.FindStringSubmatch(line); m != nil {
				router := m[1]
				pathArg := m[2]
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
		if m := methodRe.FindStringSubmatch(line); m != nil {
			router := m[1]
			method := m[2]
			pathArg := m[3]
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
	}
}

func scanAngularRoutes(content, file string, add func(method, path, file string, line int, source, pattern, confidence string)) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?s)\{[^{}]*\bpath\s*:\s*['"]([^'"]*)['"][^{}]*(?:\bcomponent\b|\bloadChildren\b|\bloadComponent\b)\s*:[^{}]*\}`),
		regexp.MustCompile(`(?s)\{[^{}]*(?:\bcomponent\b|\bloadChildren\b|\bloadComponent\b)\s*:[^{}]*\bpath\s*:\s*['"]([^'"]*)['"][^{}]*\}`),
	}
	for _, re := range patterns {
		for _, m := range re.FindAllStringSubmatchIndex(content, -1) {
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

func registerRoutesTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "routes",
		Description: "Extract the HTTP API surface — method, path, and source location for each route — from common framework registration patterns (Go chi/gin/echo/gorilla/net-http, Express, Flask, FastAPI). A recall-oriented heuristic across frameworks, not a parser. Use to map a service's endpoints.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in routesInput) (*mcp.CallToolResult, routesOutput, error) {
		out, err := routesExtract(ctx, in)
		return nil, out, err
	})
}
