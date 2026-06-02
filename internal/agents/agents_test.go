package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterJSONMcpServersFreshFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	action, err := registerJSONMcpServers(path, "/usr/local/bin/onboard")
	if err != nil {
		t.Fatal(err)
	}
	if action != "merged" {
		t.Errorf("action = %q, want merged", action)
	}
	root := readJSON(t, path)
	servers := root["mcpServers"].(map[string]any)
	ob := servers["onboard"].(map[string]any)
	if ob["command"] != "/usr/local/bin/onboard" {
		t.Errorf("command = %v", ob["command"])
	}
	args := ob["args"].([]any)
	if len(args) != 1 || args[0] != "serve" {
		t.Errorf("args = %v, want [serve]", args)
	}
}

func TestRegisterJSONMcpServersPreservesAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	writeJSON(t, path, map[string]any{
		"theme": "dark",
		"mcpServers": map[string]any{
			"other": map[string]any{"command": "x"},
		},
	})

	if _, err := registerJSONMcpServers(path, "/bin/onboard"); err != nil {
		t.Fatal(err)
	}
	root := readJSON(t, path)
	if root["theme"] != "dark" {
		t.Error("clobbered unrelated keys")
	}
	servers := root["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Error("dropped a sibling server")
	}

	action, err := registerJSONMcpServers(path, "/bin/onboard")
	if err != nil {
		t.Fatal(err)
	}
	if action != "already-present" {
		t.Errorf("second call action = %q, want already-present", action)
	}
}

func TestRegisterJSONBacksUpUnparseable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := registerJSONMcpServers(path, "/bin/onboard"); err != nil {
		t.Fatal(err)
	}
	if !exists(path + ".onboard-bak") {
		t.Error("expected a .onboard-bak backup of the unparseable file")
	}
	root := readJSON(t, path)
	if _, ok := root["mcpServers"]; !ok {
		t.Error("rewritten file missing mcpServers")
	}
}

func TestRegisterJSONRejectsNonObjectServerKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	original := `{"mcpServers":[]}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := registerJSONMcpServers(path, "/bin/onboard"); err == nil {
		t.Fatal("expected an error when mcpServers is not an object")
	}
	if got := readFile(t, path); got != original {
		t.Errorf("config was modified after a type error: %q", got)
	}
}

func TestRegisterJSONRejectsNonObjectRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	original := `[]`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := registerJSONMcpServers(path, "/bin/onboard"); err == nil {
		t.Fatal("expected an error when the JSON root is not an object")
	}
	if got := readFile(t, path); got != original {
		t.Errorf("config was modified after a type error: %q", got)
	}
	if exists(path + ".onboard-bak") {
		t.Error("valid but unsupported JSON must not be renamed as an unparseable backup")
	}
}

func TestRegisterJSONOpencodeShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if _, err := registerJSONOpencode(path, "/bin/onboard"); err != nil {
		t.Fatal(err)
	}
	root := readJSON(t, path)
	mcp, ok := root["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("opencode must use root key 'mcp', got keys %v", keysOf(root))
	}
	ob := mcp["onboard"].(map[string]any)
	if ob["type"] != "local" {
		t.Errorf("type = %v, want local", ob["type"])
	}
	if ob["enabled"] != true {
		t.Errorf("enabled = %v, want true", ob["enabled"])
	}
	cmd := ob["command"].([]any)
	if len(cmd) != 2 || cmd[0] != "/bin/onboard" || cmd[1] != "serve" {
		t.Errorf("command = %v, want [/bin/onboard serve] (one array, opencode outlier)", cmd)
	}
	if _, ok := ob["environment"]; !ok {
		t.Error("opencode entry should use 'environment' key")
	}
}

func TestRegisterTOMLAppendsAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	existing := "# my codex config\nmodel = \"gpt-5\"\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	action, err := registerTOML(path, "/bin/onboard")
	if err != nil {
		t.Fatal(err)
	}
	if action != "appended" {
		t.Errorf("action = %q, want appended", action)
	}
	s := readFile(t, path)
	if !strings.Contains(s, "model = \"gpt-5\"") {
		t.Error("dropped existing content")
	}
	if !strings.Contains(s, "[mcp_servers.onboard]") {
		t.Error("did not add the table (note: mcp_servers, not mcpServers)")
	}
	if !strings.Contains(s, `command = "/bin/onboard"`) {
		t.Error("did not write command")
	}

	action, err = registerTOML(path, "/bin/onboard")
	if err != nil {
		t.Fatal(err)
	}
	if action != "already-present" {
		t.Errorf("second call action = %q, want already-present", action)
	}
}

func TestInstallSkills(t *testing.T) {
	dir := t.TempDir()
	n, err := installSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("installSkills wrote nothing")
	}
	if !exists(filepath.Join(dir, "codebase-walkthrough", "SKILL.md")) {
		t.Error("expected SKILL.md in installed skill dir")
	}
}

func TestDetected(t *testing.T) {
	dir := t.TempDir()
	present := Agent{Name: "x", ConfigPath: filepath.Join(dir, "cfg.json")}
	if !Detected(present) {
		t.Error("Detected should be true when config parent dir exists")
	}
	absent := Agent{Name: "y", ConfigPath: filepath.Join(dir, "nope", "cfg.json")}
	if Detected(absent) {
		t.Error("Detected should be false when parent dir is absent")
	}
}

func TestRegistryShapes(t *testing.T) {
	all, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 5 {
		t.Errorf("expected >=5 agents, got %d", len(all))
	}
	byName := map[string]Agent{}
	for _, a := range all {
		if a.Name == "" || a.ConfigPath == "" || a.SkillsDir == "" {
			t.Errorf("agent %+v has an empty required field", a)
		}
		byName[a.Name] = a
	}
	if byName["codex"].Shape != ShapeTOMLMcpServers {
		t.Error("codex should be TOML mcp_servers")
	}
	if byName["opencode"].Shape != ShapeJSONOpencode {
		t.Error("opencode should be the JSON opencode outlier shape")
	}
	if byName["claude"].Shape != ShapeJSONMcpServers {
		t.Error("claude should be JSON mcpServers")
	}
}

func TestRegistryHonorsCodexHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)

	all, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	var codex Agent
	for _, a := range all {
		if a.Name == "codex" {
			codex = a
			break
		}
	}
	if codex.ConfigPath != filepath.Join(dir, "config.toml") {
		t.Errorf("codex config path = %q, want CODEX_HOME config", codex.ConfigPath)
	}
	if codex.SkillsDir != filepath.Join(dir, "skills") {
		t.Errorf("codex skills dir = %q, want CODEX_HOME skills", codex.SkillsDir)
	}
}

func TestFindNormalizesName(t *testing.T) {
	a, err := Find(" CoDeX ")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "codex" {
		t.Errorf("Find returned %q, want codex", a.Name)
	}
}

func TestRegisterJSONOpencodeIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if _, err := registerJSONOpencode(path, "/bin/onboard"); err != nil {
		t.Fatal(err)
	}
	action, err := registerJSONOpencode(path, "/bin/onboard")
	if err != nil {
		t.Fatal(err)
	}
	if action != "already-present" {
		t.Errorf("second opencode install = %q, want already-present", action)
	}
}

func TestUnparseableRewriteIsNotWorldReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registerJSONMcpServers(path, "/bin/onboard"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("rewritten config is group/other-accessible (%o); a 0600 config must not be downgraded", fi.Mode().Perm())
	}
}

func TestUnparseableBackupDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")

	if err := os.WriteFile(path, []byte("FIRST bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := registerJSONMcpServers(path, "/bin/onboard"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("SECOND bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := registerJSONMcpServers(path, "/bin/onboard"); err != nil {
		t.Fatal(err)
	}

	b1 := path + ".onboard-bak"
	b2 := path + ".onboard-bak.1"
	if !exists(b1) || !exists(b2) {
		t.Fatalf("expected two distinct backups (%v, %v)", exists(b1), exists(b2))
	}
	if got := readFile(t, b1); got != "FIRST bad" {
		t.Errorf("first backup was overwritten: %q", got)
	}
}

func TestRegisterTOMLCommentedTableNotDetected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("# [mcp_servers.onboard]\n# command = \"old\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	action, err := registerTOML(path, "/bin/onboard")
	if err != nil {
		t.Fatal(err)
	}
	if action != "appended" {
		t.Errorf("a commented-out table must not count as present; got %q", action)
	}
}

func TestRegisterTOMLQuotedKeyFormDetected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[mcp_servers.\"onboard\"]\ncommand = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	action, err := registerTOML(path, "/bin/onboard")
	if err != nil {
		t.Fatal(err)
	}
	if action != "already-present" {
		t.Errorf("the quoted-key table form must be detected; got %q", action)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return m
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
