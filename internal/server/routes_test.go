package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeRouteFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasRoute(out routesOutput, method, path string) bool {
	for _, r := range out.Routes {
		if r.Method == method && r.Path == path {
			return true
		}
	}
	return false
}

func TestRoutesAcrossFrameworks(t *testing.T) {
	root := t.TempDir()
	// Go (gin/chi style + net/http)
	writeRouteFile(t, root, "api/server.go", `package api
func setup(r *gin.Engine) {
	r.GET("/users", listUsers)
	r.POST("/users", createUser)
	mux.HandleFunc("/health", health)
	cache.Get("timeout") // not a route — no leading slash
}`)
	// Express
	writeRouteFile(t, root, "web/routes.js", `
app.get('/products', listProducts);
router.delete('/products/:id', removeProduct);
`)
	// Flask
	writeRouteFile(t, root, "svc/app.py", `
@app.route("/orders", methods=["GET", "POST"])
def orders(): ...

@app.get("/orders/<id>")
def get_order(id): ...
`)
	out, err := routesExtract(context.Background(), routesInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}

	want := []struct{ method, path string }{
		{"GET", "/users"}, {"POST", "/users"}, {"ANY", "/health"},
		{"GET", "/products"}, {"DELETE", "/products/:id"},
		{"GET", "/orders"}, {"POST", "/orders"}, {"GET", "/orders/<id>"},
	}
	for _, w := range want {
		if !hasRoute(out, w.method, w.path) {
			t.Errorf("missing route %s %s; got %+v", w.method, w.path, out.Routes)
		}
	}
	// The cache.Get("timeout") call must NOT be picked up as a route.
	for _, r := range out.Routes {
		if r.Path == "timeout" {
			t.Errorf("non-path string 'timeout' was wrongly extracted as a route")
		}
	}
}

func TestRoutesLineNumbers(t *testing.T) {
	root := t.TempDir()
	writeRouteFile(t, root, "r.go", "package r\n\nfunc f() {\n\tr.GET(\"/a\", h)\n}\n")
	out, err := routesExtract(context.Background(), routesInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Routes) != 1 || out.Routes[0].Line != 4 {
		t.Errorf("expected one route on line 4, got %+v", out.Routes)
	}
}

func TestGoRouteGroupPrefixDoesNotLeakPastClosure(t *testing.T) {
	root := t.TempDir()
	writeRouteFile(t, root, "routes.go", `package routes
func setup(r chi.Router) {
	r.Route("/api", func(r chi.Router) {
		r.Get("/users", users)
	})
	r.Get("/health", health)
}`)
	out, err := routesExtract(context.Background(), routesInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRoute(out, "GET", "/api/users") {
		t.Fatalf("missing grouped route; got %+v", out.Routes)
	}
	if !hasRoute(out, "GET", "/health") {
		t.Fatalf("route after group should not keep /api prefix; got %+v", out.Routes)
	}
	if hasRoute(out, "GET", "/api/health") {
		t.Fatalf("group prefix leaked past closure; got %+v", out.Routes)
	}
}

func TestRoutesFrontendConventions(t *testing.T) {
	root := t.TempDir()
	writeRouteFile(t, root, "src/routes/[userId]/+server.ts", `
export async function GET() {}
export const POST = async () => {};
`)
	writeRouteFile(t, root, "app/blog/[slug]/page.tsx", `export default function Page() { return null }`)
	writeRouteFile(t, root, "pages/users/[id].tsx", `export default function User() { return null }`)
	writeRouteFile(t, root, "src/App.tsx", `
const routes = [
	{ path: 'settings', element: <Settings /> },
	{ path: 'profile/:id', element: <Profile /> },
];
`)
	writeRouteFile(t, root, "app/app.routes.ts", `
const routes = [
	{ component: UserComponent, path: 'users' },
	{ path: 'admin', loadChildren: () => import('./admin') },
];
`)

	out, err := routesExtract(context.Background(), routesInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ method, path string }{
		{"GET", "/:userId"},
		{"POST", "/:userId"},
		{"GET", "/blog/:slug"},
		{"GET", "/users/:id"},
		{"ANY", "/settings"},
		{"ANY", "/profile/:id"},
		{"ANY", "/users"},
		{"ANY", "/admin"},
	}
	for _, w := range want {
		if !hasRoute(out, w.method, w.path) {
			t.Errorf("missing route %s %s; got %+v", w.method, w.path, out.Routes)
		}
	}
}

func TestRoutesEmptyRepo(t *testing.T) {
	out, err := routesExtract(context.Background(), routesInput{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 0 || out.Note == "" {
		t.Errorf("no routes should yield total 0 with a note; got %d", out.Total)
	}
}
