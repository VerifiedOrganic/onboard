package providers

import (
	"os"
	"path/filepath"
	"testing"
)

func writeJSFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseJSImportsESM(t *testing.T) {
	root := t.TempDir()
	writeJSFile(t, root, "src/main.js", `import def, * as ns from './util';
import { foo as bar } from '../lib/helper';
`)
	writeJSFile(t, root, "src/util.js", "export default 1;\n")
	writeJSFile(t, root, "lib/helper.js", "export const foo = 2;\n")

	imports := parseJSImports(root, "src/main.js", readJSFile(t, filepath.Join(root, "src/main.js")))
	if got := imports["def"].targetFile; got != "src/util.js" {
		t.Fatalf("def -> %q, want src/util.js", got)
	}
	if got := imports["ns"].targetName; got != "*" {
		t.Fatalf("ns targetName = %q, want *", got)
	}
	if got := imports["bar"].targetFile; got != "lib/helper.js" {
		t.Fatalf("bar -> %q, want lib/helper.js", got)
	}
	if got := imports["bar"].targetName; got != "foo" {
		t.Fatalf("bar targetName = %q, want foo", got)
	}
}

func TestResolveImportPathAlias(t *testing.T) {
	root := t.TempDir()
	writeJSFile(t, root, "src/components/Button.tsx", "export {};\n")
	writeJSFile(t, root, "tsconfig.json", `{
  "compilerOptions": {
    "paths": { "@/*": ["src/*"] }
  }
}`)
	got := resolveImportPath(root, "src/App.tsx", "@/components/Button")
	if got != "src/components/Button.tsx" {
		t.Fatalf("resolveImportPath = %q, want src/components/Button.tsx", got)
	}
}

func TestResolveImportPathRelative(t *testing.T) {
	root := t.TempDir()
	writeJSFile(t, root, "pkg/a.ts", "export {};\n")
	got := resolveImportPath(root, "pkg/b.ts", "./a")
	if got != "pkg/a.ts" {
		t.Fatalf("resolveImportPath = %q, want pkg/a.ts", got)
	}
}

func TestScanTemplateRefsSkipsKeywords(t *testing.T) {
	src := []byte(`<MyComponent on:click={handler} />
{#each items as item}
  <Child />
{/each}`)
	refs := scanTemplateRefs(src, false)
	if len(refs) < 2 {
		t.Fatalf("refs = %v, want component names", refs)
	}
	for _, r := range refs {
		if r == "each" || r == "as" {
			t.Fatalf("keyword leaked into refs: %v", refs)
		}
	}
}

func TestTagHTMLFileCollectsAngularRefs(t *testing.T) {
	rel := "app.component.html"
	src := []byte(`<button (click)="saveRecord()">{{ title }}</button>`)
	_, refs := tagHTMLFile(rel, src)
	if len(refs) == 0 {
		t.Fatal("expected template refs from Angular HTML")
	}
}

func readJSFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
