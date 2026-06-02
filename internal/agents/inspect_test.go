package agents

import (
	"os"
	"path/filepath"
	"testing"
)

// A fresh install must inspect as fully healthy, and the configured binary path must be
// read back from the JSON config.
func TestInspectHealthyAfterInstall(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "onboard")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := Agent{
		Name:       "claude",
		SkillsDir:  filepath.Join(home, ".claude", "skills"),
		ConfigPath: filepath.Join(home, ".claude.json"),
		Shape:      ShapeJSONMcpServers,
	}
	if _, err := Install(a, bin); err != nil {
		t.Fatal(err)
	}

	h := Inspect(a)
	if !h.Registered || !h.BinExists {
		t.Errorf("after install: registered=%v binExists=%v, want both true", h.Registered, h.BinExists)
	}
	if h.ConfiguredBin != bin {
		t.Errorf("configured bin = %q, want %q", h.ConfiguredBin, bin)
	}
	if h.SkillsExpected == 0 || h.SkillsPresent != h.SkillsExpected {
		t.Errorf("skills %d/%d, want all present", h.SkillsPresent, h.SkillsExpected)
	}
	if !h.OK() || len(h.Issues) != 0 {
		t.Errorf("expected healthy, got OK=%v issues=%v", h.OK(), h.Issues)
	}
}

// A config that exists but never registered onboard must be flagged, not reported OK.
func TestInspectFlagsMissingRegistration(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(cfg, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := Agent{Name: "claude", SkillsDir: filepath.Join(home, ".claude", "skills"), ConfigPath: cfg, Shape: ShapeJSONMcpServers}

	h := Inspect(a)
	if h.Registered {
		t.Error("should not be registered")
	}
	if h.OK() || len(h.Issues) == 0 {
		t.Errorf("missing registration must be flagged: OK=%v issues=%v", h.OK(), h.Issues)
	}
}

// A TOML install whose binary later disappears must report registered=true, bin missing —
// this also exercises the TOML command parser.
func TestInspectFlagsMissingBinaryTOML(t *testing.T) {
	home := t.TempDir()
	a := Agent{
		Name:       "codex",
		SkillsDir:  filepath.Join(home, ".codex", "skills"),
		ConfigPath: filepath.Join(home, ".codex", "config.toml"),
		Shape:      ShapeTOMLMcpServers,
	}
	bin := filepath.Join(home, "ghost")
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(a, bin); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}

	h := Inspect(a)
	if !h.Registered {
		t.Fatal("should be registered (TOML table present)")
	}
	if h.ConfiguredBin != bin {
		t.Errorf("TOML command parse = %q, want %q", h.ConfiguredBin, bin)
	}
	if h.BinExists || h.OK() {
		t.Errorf("missing binary must not be OK: binExists=%v OK=%v", h.BinExists, h.OK())
	}
}
