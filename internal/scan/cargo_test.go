package scan

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/testenv"
)

func depByName(m ManifestDeps, name string) (Dependency, bool) {
	for _, d := range m.Direct {
		if d.Name == name {
			return d, true
		}
	}
	return Dependency{}, false
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

	got := ParseCargoMetadata(root, data)
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
		testenv.SkipUnlessTool(t, "cargo not installed")
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
	mds, ok := LoadCargoMetadata(context.Background(), root)
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
