package scan

import (
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

func hasRoute(out RoutesResult, method, path string) bool {
	for _, r := range out.Routes {
		if r.Method == method && r.Path == path {
			return true
		}
	}
	return false
}

func TestExtractRoutesAcrossFrameworks(t *testing.T) {
	root := t.TempDir()
	writeRouteFile(t, root, "api/server.go", `package api
func setup(r *gin.Engine) {
	r.GET("/users", listUsers)
	r.POST("/users", createUser)
	mux.HandleFunc("/health", health)
	cache.Get("timeout")
}`)
	writeRouteFile(t, root, "web/routes.js", `
app.get('/products', listProducts);
router.delete('/products/:id', removeProduct);
`)
	writeRouteFile(t, root, "svc/app.py", `
@app.route("/orders", methods=["GET", "POST"])
def orders(): ...

@app.get("/orders/<id>")
def get_order(id): ...
`)
	out := ExtractRoutes(root)

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
	for _, r := range out.Routes {
		if r.Path == "timeout" {
			t.Errorf("non-path string 'timeout' was wrongly extracted as a route")
		}
	}
}
