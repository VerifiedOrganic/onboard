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
	Method string `json:"method"` // GET/POST/...; ANY when the pattern does not pin one
	Path   string `json:"path"`
	File   string `json:"file"`
	Line   int    `json:"line"`
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
	add := func(method, path, file string, line int) {
		method = strings.ToUpper(method)
		key := method + " " + path + " " + file
		if seen[key] {
			return
		}
		seen[key] = true
		out.Routes = append(out.Routes, route{Method: method, Path: path, File: file, Line: line})
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
		rel, _ := filepath.Rel(root, p)
		scanRoutes(string(data), filepath.ToSlash(rel), add)
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
		out.Note = "No HTTP routes matched the known framework patterns (chi/gin/echo/gorilla/net-http, Express, Flask, FastAPI)."
		return out, nil
	}
	out.Note = "Routes matched from framework registration patterns — a recall-oriented heuristic, not a parser: bespoke routing may be missed and dynamic paths may be approximate. ANY = the pattern (e.g. net/http HandleFunc) does not pin a method."
	return out, nil
}

// scanRoutes runs every route pattern over one file's content, reporting each match's path,
// method, and 1-based line via add.
func scanRoutes(content, file string, add func(method, path, file string, line int)) {
	for _, m := range methodPathRe.FindAllStringSubmatchIndex(content, -1) {
		method := content[m[2]:m[3]]
		path := content[m[4]:m[5]]
		if looksLikePath(path) {
			add(method, path, file, lineAt(content, m[0]))
		}
	}
	for _, m := range routeRe.FindAllStringSubmatchIndex(content, -1) {
		path := content[m[2]:m[3]]
		if !looksLikePath(path) {
			continue
		}
		line := lineAt(content, m[0])
		rest := content[m[4]:m[5]]
		if ml := methodsListRe.FindStringSubmatch(rest); ml != nil {
			for _, meth := range splitMethods(ml[1]) {
				add(meth, path, file, line)
			}
		} else {
			add("GET", path, file, line) // Flask .route default
		}
	}
	for _, m := range handleRe.FindAllStringSubmatchIndex(content, -1) {
		path := content[m[2]:m[3]]
		if looksLikePath(path) {
			add("ANY", path, file, lineAt(content, m[0]))
		}
	}
}

// looksLikePath filters out string literals that matched the call pattern but are clearly not
// route paths (e.g. a `.get("timeout")` config key). A route path starts with "/" or ":".
func looksLikePath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, ":")
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

// lineAt returns the 1-based line number of the byte offset idx in content.
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
