package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func depsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/proj\n\ngo 1.21\n\n" +
			"require (\n\tgithub.com/foo/bar v1.2.3\n\tgithub.com/baz/qux v0.1.0 // indirect\n)\n\n" +
			"require golang.org/x/text v0.3.0\n",
		"web/package.json":    `{"name":"app","dependencies":{"react":"^18.0.0"},"devDependencies":{"jest":"^29.0.0"}}`,
		"py/requirements.txt": "Django>=4.2\nrequests  # http client\npkg @ https://example.invalid/pkg.whl#sha256=abc\n-r other.txt\n\n# a comment\n",
		"rs/Cargo.toml": "[package]\nname = \"mycrate\"\n\n[dependencies]\nserde = \"1.0\" # serde runtime\n" +
			"tokio = { version = \"1.35\", features = [\"full\"] }\n\n[target.'cfg(unix)'.dependencies]\nnix = \"0.27\"\n\n[dev-dependencies]\ncriterion = \"0.5\"\n",
	}
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func manifestByEco(out depsOutput, eco string) (manifestDeps, bool) {
	for _, m := range out.Manifests {
		if m.Ecosystem == eco {
			return m, true
		}
	}
	return manifestDeps{}, false
}

func depByName(m manifestDeps, name string) (dependency, bool) {
	for _, d := range m.Direct {
		if d.Name == name {
			return d, true
		}
	}
	return dependency{}, false
}

func TestDepsExtractsAllEcosystems(t *testing.T) {
	root := depsFixture(t)
	out, err := depsExtract(context.Background(), depsInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Manifests) != 4 {
		t.Fatalf("expected 4 manifests, got %d: %+v", len(out.Manifests), out.Manifests)
	}

	// Go: two direct, one indirect (counted, not listed).
	gomod, ok := manifestByEco(out, "Go")
	if !ok {
		t.Fatal("no Go manifest")
	}
	if gomod.Module != "example.com/proj" {
		t.Errorf("go module = %q", gomod.Module)
	}
	if gomod.Indirect != 1 {
		t.Errorf("expected 1 indirect dep, got %d", gomod.Indirect)
	}
	if d, ok := depByName(gomod, "github.com/foo/bar"); !ok || d.Version != "v1.2.3" {
		t.Errorf("foo/bar = %+v ok=%v", d, ok)
	}
	if _, ok := depByName(gomod, "github.com/baz/qux"); ok {
		t.Error("indirect dep baz/qux should not appear in Direct")
	}

	// npm: dev flag distinguishes jest from react.
	npm, ok := manifestByEco(out, "JavaScript/TypeScript (npm)")
	if !ok {
		t.Fatal("no npm manifest")
	}
	if d, ok := depByName(npm, "react"); !ok || d.Dev {
		t.Errorf("react should be a non-dev dep, got %+v", d)
	}
	if d, ok := depByName(npm, "jest"); !ok || !d.Dev {
		t.Errorf("jest should be a dev dep, got %+v", d)
	}

	// Python: version constraint kept; bare requests has none; -r line skipped.
	py, ok := manifestByEco(out, "Python")
	if !ok {
		t.Fatal("no Python manifest")
	}
	if d, ok := depByName(py, "Django"); !ok || d.Version != ">=4.2" {
		t.Errorf("Django = %+v", d)
	}
	if d, ok := depByName(py, "requests"); !ok || d.Version != "" {
		t.Errorf("requests = %+v", d)
	}
	if d, ok := depByName(py, "pkg"); !ok || d.Version != "@ https://example.invalid/pkg.whl#sha256=abc" {
		t.Errorf("direct URL pkg = %+v", d)
	}
	if len(py.Direct) != 3 {
		t.Errorf("expected 3 python deps (the -r and comment lines dropped), got %d: %+v", len(py.Direct), py.Direct)
	}

	// Rust: inline-table version parsed; dev-dependency flagged.
	rs, ok := manifestByEco(out, "Rust")
	if !ok {
		t.Fatal("no Rust manifest")
	}
	if rs.Module != "mycrate" {
		t.Errorf("rust module = %q, want mycrate", rs.Module)
	}
	if d, ok := depByName(rs, "serde"); !ok || d.Version != "1.0" {
		t.Errorf("serde inline comment not stripped: %+v", d)
	}
	if d, ok := depByName(rs, "tokio"); !ok || d.Version != "1.35" {
		t.Errorf("tokio inline-table version not parsed: %+v", d)
	}
	if d, ok := depByName(rs, "nix"); !ok || d.Dev || d.Version != "0.27" {
		t.Errorf("target-specific nix dep not parsed as a non-dev dependency: %+v", d)
	}
	if d, ok := depByName(rs, "criterion"); !ok || !d.Dev {
		t.Errorf("criterion should be a dev dep, got %+v", d)
	}
}

func TestDepsMermaid(t *testing.T) {
	root := depsFixture(t)
	out, err := depsExtract(context.Background(), depsInput{Root: root, Format: "mermaid"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Mermaid, "flowchart") {
		t.Errorf("expected a flowchart, got:\n%s", out.Mermaid)
	}
	if !strings.Contains(out.Mermaid, "react") || !strings.Contains(out.Mermaid, "serde") {
		t.Errorf("mermaid should reference dependencies:\n%s", out.Mermaid)
	}
}

func TestParseCargoMetadataAddsTargetsAndKinds(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "Cargo.toml")
	lib := filepath.Join(root, "src", "lib.rs")
	bin := filepath.Join(root, "src", "main.rs")
	if err := os.MkdirAll(filepath.Dir(lib), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{manifest, lib, bin} {
		if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data := []byte(fmt.Sprintf(`{
  "packages": [{
    "name": "mycrate",
    "manifest_path": %q,
    "dependencies": [
      {"name": "serde", "req": "1.0", "kind": null, "target": null, "optional": false},
      {"name": "criterion", "req": "0.5", "kind": "dev", "target": null, "optional": true},
      {"name": "cc", "req": "1", "kind": "build", "target": "cfg(unix)", "optional": false}
    ],
    "targets": [
      {"name": "mycrate", "kind": ["lib"], "crate_types": ["lib"], "src_path": %q, "edition": "2021"},
      {"name": "cli", "kind": ["bin"], "crate_types": ["bin"], "src_path": %q, "edition": "2021"}
    ]
  }]
}`, manifest, lib, bin))

	got := parseCargoMetadata(root, data)
	md, ok := got["Cargo.toml"]
	if !ok {
		t.Fatalf("metadata missing Cargo.toml: %+v", got)
	}
	if md.Module != "mycrate" || md.Ecosystem != "Rust" {
		t.Errorf("module/ecosystem = %q/%q", md.Module, md.Ecosystem)
	}
	if d, ok := depByName(md, "criterion"); !ok || !d.Dev || !d.Optional || d.Kind != "dev" {
		t.Errorf("criterion metadata not preserved: %+v ok=%v", d, ok)
	}
	if d, ok := depByName(md, "cc"); !ok || d.Kind != "build" || d.Target != "cfg(unix)" {
		t.Errorf("build target dep not preserved: %+v ok=%v", d, ok)
	}
	if len(md.Targets) != 2 {
		t.Fatalf("expected two Cargo targets, got %+v", md.Targets)
	}
	if md.Targets[0].SrcPath != "src/lib.rs" || md.Targets[1].SrcPath != "src/main.rs" {
		t.Errorf("target src paths not normalized: %+v", md.Targets)
	}
}

func TestLoadCargoMetadataWhenCargoAvailable(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo not installed")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname = \"live\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "lib.rs"), []byte("pub fn live() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mds, ok := loadCargoMetadata(context.Background(), root)
	if !ok {
		t.Fatal("cargo metadata did not return package metadata")
	}
	md, ok := mds["Cargo.toml"]
	if !ok {
		t.Fatalf("metadata missing Cargo.toml: %+v", mds)
	}
	if md.Module != "live" || len(md.Targets) == 0 {
		t.Fatalf("cargo metadata missing module/targets: %+v", md)
	}
}

func TestDepsEmptyRepo(t *testing.T) {
	out, err := depsExtract(context.Background(), depsInput{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Manifests) != 0 || out.Note == "" {
		t.Errorf("empty repo should yield no manifests with a note; got %d manifests, note=%q", len(out.Manifests), out.Note)
	}
}

func TestSplitRequirement(t *testing.T) {
	cases := map[string][2]string{
		"Django>=4.2":                    {"Django", ">=4.2"},
		"requests":                       {"requests", ""},
		"numpy==1.26.0":                  {"numpy", "==1.26.0"},
		"requests[security]":             {"requests", ""},
		"requests[security]>=2":          {"requests", ">=2"},
		"pkg>=1.0; python_version<'3.9'": {"pkg", ">=1.0"},
		"pkg[extra] @ https://example.invalid/pkg.whl#sha256=abc": {"pkg", "@ https://example.invalid/pkg.whl#sha256=abc"},
	}
	for in, want := range cases {
		gotName, gotVer := splitRequirement(in)
		if gotName != want[0] || gotVer != want[1] {
			t.Errorf("splitRequirement(%q) = (%q,%q), want (%q,%q)", in, gotName, gotVer, want[0], want[1])
		}
	}
}
