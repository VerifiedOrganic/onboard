package server

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/scan"
)

func writeFixtureFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationHardeningGoRoutes(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "go.mod", "module example.com/goservice\n\ngo 1.21\n")
	writeFixtureFile(t, root, "main.go", `package main
import (
	"net/http"
	"example.com/goservice/routes"
)
func main() {
	r := routes.SetupRouter()
	http.ListenAndServe(":8080", r)
}`)
	writeFixtureFile(t, root, "routes/routes.go", `package routes
import (
	"net/http"
	"github.com/go-chi/chi/v5"
)
func SetupRouter() http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/users", ListUsers)
		r.Post("/users", CreateUser)
	})
	return r
}
func ListUsers(w http.ResponseWriter, r *http.Request) {}
func CreateUser(w http.ResponseWriter, r *http.Request) {}`)

	cs, ctx := connect(t)
	var out routesOutput
	callStructured(ctx, t, cs, "routes", map[string]any{"root": root}, &out)

	if out.Total < 2 {
		t.Fatalf("expected at least 2 routes, got %d: %+v", out.Total, out.Routes)
	}

	foundGet := false
	foundPost := false
	for _, rt := range out.Routes {
		if rt.Path == "/api/v1/users" {
			if rt.Method == "GET" {
				foundGet = true
				if rt.Confidence != "high" || rt.Source != "regex-heuristic" {
					t.Errorf("GET route metadata mismatch: %+v", rt)
				}
			}
			if rt.Method == "POST" {
				foundPost = true
			}
		}
	}
	if !foundGet || !foundPost {
		t.Errorf("failed to extract nested Go routes: %+v", out.Routes)
	}
}

func TestIntegrationHardeningReactViteImpactAndTests(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{
		"name": "react-vite-app",
		"dependencies": {
			"react": "^18.2.0"
		},
		"devDependencies": {
			"vite": "^5.0.0"
		}
	}`)
	writeFixtureFile(t, root, "src/components/UserList.tsx", `
import React from 'react';
export function UserList() {
	return <div>Users</div>;
}`)
	writeFixtureFile(t, root, "src/components/UserList.test.tsx", `
import { UserList } from './UserList';
test('renders UserList', () => {
	UserList();
});`)
	writeFixtureFile(t, root, "src/App.tsx", `
import { UserList } from './components/UserList';
export default function App() {
	return (
		<div>
			<UserList />
		</div>
	);
}`)

	cs, ctx := connect(t)

	// Test deps parsing
	var depsOut depsOutput
	callStructured(ctx, t, cs, "deps", map[string]any{"root": root}, &depsOut)
	if len(depsOut.Manifests) == 0 {
		t.Fatal("expected manifest to be parsed")
	}
	manifest := depsOut.Manifests[0]
	if !slices.Contains(manifest.DetectedTools, "Vite") {
		t.Errorf("expected Vite to be detected in tools, got %v", manifest.DetectedTools)
	}

	// Test impact & test attribution
	var impactOut impactOutput
	callStructured(ctx, t, cs, "impact", map[string]any{"root": root, "symbol": "UserList"}, &impactOut)

	if len(impactOut.AtRiskTests) == 0 {
		t.Errorf("expected UserList.test.tsx to be an at-risk test, but got none. Transitive callers: %v", impactOut.TransitiveCallers)
	}

	foundTest := false
	for _, tc := range impactOut.AtRiskTests {
		if strings.Contains(tc, "UserList.test.tsx") {
			foundTest = true
		}
	}
	if !foundTest {
		t.Errorf("UserList.test.tsx not found in at risk tests: %v", impactOut.AtRiskTests)
	}
}

func TestIntegrationHardeningSvelteKit(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{
		"name": "sveltekit-app",
		"devDependencies": {
			"@sveltejs/kit": "^2.0.0",
			"svelte": "^4.0.0"
		}
	}`)
	writeFixtureFile(t, root, "src/routes/+page.svelte", `
<script>
	import UserList from './UserList.svelte';
</script>
<UserList />`)
	writeFixtureFile(t, root, "src/routes/UserList.svelte", `
<script>
	export let users = [];
</script>
<div>Users</div>`)
	writeFixtureFile(t, root, "src/routes/api/users/+server.ts", `
export async function GET() {
	return new Response("users");
}
export async function POST() {
	return new Response("create");
}`)

	cs, ctx := connect(t)

	// Test routes extraction
	var out routesOutput
	callStructured(ctx, t, cs, "routes", map[string]any{"root": root}, &out)

	foundPage := false
	foundGet := false
	foundPost := false
	for _, rt := range out.Routes {
		if rt.Path == "/" && rt.Method == "GET" && rt.Pattern == "SvelteKit page" {
			foundPage = true
		}
		if rt.Path == "/api/users" {
			if rt.Method == "GET" && rt.Pattern == "SvelteKit server endpoint" {
				foundGet = true
			}
			if rt.Method == "POST" && rt.Pattern == "SvelteKit server endpoint" {
				foundPost = true
			}
		}
	}

	if !foundPage || !foundGet || !foundPost {
		t.Errorf("SvelteKit routes not fully extracted: %+v", out.Routes)
	}

	// Test dead code - GET and POST inside +server.ts should NOT be reported as dead code
	var dcOut deadCodeOutput
	callStructured(ctx, t, cs, "dead_code", map[string]any{"root": root}, &dcOut)

	for _, o := range dcOut.Orphans {
		if strings.Contains(o.QName, "+server.ts") {
			t.Errorf("SvelteKit endpoint handler %q was falsely reported as dead code", o.QName)
		}
	}
}

func TestIntegrationHardeningAngularDIAndTemplates(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{
		"name": "angular-app",
		"dependencies": {
			"@angular/core": "^17.0.0"
		}
	}`)
	writeFixtureFile(t, root, "src/app/user.service.ts", `
import { Injectable } from '@angular/core';
@Injectable({
	providedIn: 'root'
})
export class UserService {
	getUsers() { return [1, 2]; }
}`)
	writeFixtureFile(t, root, "src/app/user-list.component.ts", `
import { Component } from '@angular/core';
import { UserService } from './user.service';
@Component({
	selector: 'app-user-list',
	templateUrl: './user-list.component.html'
})
export class UserListComponent {
	constructor(private userService: UserService) {}
	load() {
		this.userService.getUsers();
	}
}`)
	writeFixtureFile(t, root, "src/app/user-list.component.html", `
<button (click)="load()">Load</button>`)

	cs, ctx := connect(t)

	// Test trace flow or impact to verify template-component-service connections
	var impactOut impactOutput
	callStructured(ctx, t, cs, "impact", map[string]any{"root": root, "symbol": "getUsers"}, &impactOut)

	foundComponent := false
	foundTemplate := false
	for _, c := range impactOut.TransitiveCallers {
		if strings.Contains(c, "user-list.component.ts::load") {
			foundComponent = true
		}
		if strings.Contains(c, "user-list.component.html") {
			foundTemplate = true
		}
	}

	if !foundComponent {
		t.Errorf("UserService.getUsers should be called by UserListComponent.load: callers were %v", impactOut.TransitiveCallers)
	}
	if !foundTemplate {
		t.Errorf("UserService.getUsers should transitively list the HTML template as caller: callers were %v", impactOut.TransitiveCallers)
	}
}

func TestIntegrationHardeningMonorepoWorkspaces(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "package.json", `{
		"name": "monorepo-root",
		"workspaces": ["packages/*"],
		"devDependencies": {
			"turbo": "^1.10.0"
		}
	}`)
	writeFixtureFile(t, root, "packages/common/package.json", `{
		"name": "@mono/common",
		"version": "1.0.0"
	}`)
	writeFixtureFile(t, root, "packages/web/package.json", `{
		"name": "@mono/web",
		"version": "1.0.0",
		"dependencies": {
			"@mono/common": "workspace:*"
		}
	}`)

	cs, ctx := connect(t)

	var depsOut depsOutput
	callStructured(ctx, t, cs, "deps", map[string]any{"root": root}, &depsOut)

	var webManifest *scan.ManifestDeps
	for i := range depsOut.Manifests {
		if depsOut.Manifests[i].Module == "@mono/web" {
			webManifest = &depsOut.Manifests[i]
		}
	}

	if webManifest == nil {
		t.Fatal("could not find @mono/web manifest")
	}

	if len(webManifest.WorkspaceDependencies) != 1 || webManifest.WorkspaceDependencies[0] != "packages/common/package.json" {
		t.Errorf("web workspace dependency mapping failed: got %+v", webManifest.WorkspaceDependencies)
	}

	foundWorkspaceDep := false
	for _, dep := range webManifest.Direct {
		if dep.Name == "@mono/common" {
			if dep.Workspace {
				foundWorkspaceDep = true
			}
		}
	}
	if !foundWorkspaceDep {
		t.Error("@mono/common should be marked as Workspace dependency")
	}
}
