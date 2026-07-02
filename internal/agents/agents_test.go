package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

func TestRegisterJSONMcpServersRefreshesStalePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	writeJSON(t, path, map[string]any{
		"theme": "dark",
		"mcpServers": map[string]any{
			"other":   map[string]any{"command": "x"},
			"onboard": map[string]any{"command": "/old/onboard", "args": []string{"serve"}, "note": "keep"},
		},
	})

	action, err := registerJSONMcpServers(path, "/new/onboard")
	if err != nil {
		t.Fatal(err)
	}
	if action != "refreshed" {
		t.Errorf("action = %q, want refreshed", action)
	}
	root := readJSON(t, path)
	if root["theme"] != "dark" {
		t.Error("clobbered unrelated root key")
	}
	servers := root["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Error("dropped a sibling server")
	}
	ob := servers["onboard"].(map[string]any)
	if ob["command"] != "/new/onboard" {
		t.Errorf("command = %v, want refreshed path", ob["command"])
	}
	if ob["note"] != "keep" {
		t.Errorf("dropped unknown onboard field: %v", ob)
	}
}

func TestRegisterJSONMcpServersWithTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-config.json")
	action, err := registerJSONMcpServersWithTools(path, "/usr/local/bin/onboard")
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
	if ob["type"] != "local" {
		t.Errorf("type = %v, want local", ob["type"])
	}
	args := ob["args"].([]any)
	if len(args) != 1 || args[0] != "serve" {
		t.Errorf("args = %v, want [serve]", args)
	}
	tools := ob["tools"].([]any)
	if len(tools) != 1 || tools[0] != "*" {
		t.Errorf("tools = %v, want [*]", tools)
	}
}

func TestRegisterJSONMcpServersWithToolsRefreshesToolFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-config.json")
	writeJSON(t, path, map[string]any{
		"mcpServers": map[string]any{
			"onboard": map[string]any{
				"command": "/old/onboard",
				"args":    []string{"serve"},
				"type":    "stdio",
				"tools":   []string{"repo_map"},
				"note":    "keep",
			},
		},
	})

	action, err := registerJSONMcpServersWithTools(path, "/new/onboard")
	if err != nil {
		t.Fatal(err)
	}
	if action != "refreshed" {
		t.Errorf("action = %q, want refreshed", action)
	}
	root := readJSON(t, path)
	ob := root["mcpServers"].(map[string]any)["onboard"].(map[string]any)
	if ob["command"] != "/new/onboard" {
		t.Errorf("command = %v", ob["command"])
	}
	if ob["type"] != "local" {
		t.Errorf("type = %v, want local", ob["type"])
	}
	tools := ob["tools"].([]any)
	if len(tools) != 1 || tools[0] != "*" {
		t.Errorf("tools = %v, want [*]", tools)
	}
	if ob["note"] != "keep" {
		t.Errorf("dropped unknown onboard field: %v", ob)
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

func TestRegisterJSONReportsBackupPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := registerJSONMcpServersDetailed(path, "/bin/onboard")
	if err != nil {
		t.Fatal(err)
	}
	if res.BackupPath != path+".onboard-bak" {
		t.Fatalf("BackupPath = %q, want %q", res.BackupPath, path+".onboard-bak")
	}
	if !exists(res.BackupPath) {
		t.Fatal("reported backup path does not exist")
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

func TestRegisterJSONOpencodeRefreshesStalePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	writeJSON(t, path, map[string]any{
		"mcp": map[string]any{
			"other": map[string]any{"command": []string{"x"}},
			"onboard": map[string]any{
				"type":        "local",
				"command":     []string{"/old/onboard", "serve"},
				"enabled":     true,
				"environment": map[string]any{"KEEP": "1"},
				"note":        "keep",
			},
		},
	})

	action, err := registerJSONOpencode(path, "/new/onboard")
	if err != nil {
		t.Fatal(err)
	}
	if action != "refreshed" {
		t.Errorf("action = %q, want refreshed", action)
	}
	root := readJSON(t, path)
	mcp := root["mcp"].(map[string]any)
	if _, ok := mcp["other"]; !ok {
		t.Error("dropped a sibling server")
	}
	ob := mcp["onboard"].(map[string]any)
	cmd := ob["command"].([]any)
	if len(cmd) != 2 || cmd[0] != "/new/onboard" || cmd[1] != "serve" {
		t.Errorf("command = %v, want [/new/onboard serve]", cmd)
	}
	env := ob["environment"].(map[string]any)
	if env["KEEP"] != "1" {
		t.Errorf("environment was not preserved: %v", env)
	}
	if ob["note"] != "keep" {
		t.Errorf("dropped unknown onboard field: %v", ob)
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

func TestRegisterTOMLRefreshesStalePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	existing := "model = \"gpt-5\"\n\n[mcp_servers.onboard]\n# keep this comment\ncommand = \"/old/onboard\"\nargs = [\"serve\"]\n\n[mcp_servers.other]\ncommand = \"x\"\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	action, err := registerTOML(path, "/new/onboard")
	if err != nil {
		t.Fatal(err)
	}
	if action != "refreshed" {
		t.Errorf("action = %q, want refreshed", action)
	}
	s := readFile(t, path)
	if !strings.Contains(s, "model = \"gpt-5\"") || !strings.Contains(s, "# keep this comment") {
		t.Errorf("dropped existing TOML content:\n%s", s)
	}
	if !strings.Contains(s, `command = "/new/onboard"`) {
		t.Errorf("did not refresh onboard command:\n%s", s)
	}
	if !strings.Contains(s, "[mcp_servers.other]\ncommand = \"x\"") {
		t.Errorf("modified another TOML table:\n%s", s)
	}
}

func TestInstallSkills(t *testing.T) {
	dir := t.TempDir()
	n, removed, err := installSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("installSkills wrote nothing")
	}
	if removed != 0 {
		t.Fatalf("fresh install removed %d legacy dirs, want 0", removed)
	}
	if !exists(filepath.Join(dir, "onboard-codebase-walkthrough", "SKILL.md")) {
		t.Error("expected SKILL.md in installed skill dir")
	}
}

func TestInstallSkillsCleansLegacyOnboardDirs(t *testing.T) {
	dir := t.TempDir()
	legacyDir := filepath.Join(dir, "codebase-walkthrough")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "SKILL.md"), []byte("---\nname: codebase-walkthrough\n---\n# Codebase Walkthrough\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, removed, err := installSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if exists(legacyDir) {
		t.Error("legacy codebase-walkthrough dir still exists")
	}
	if !exists(filepath.Join(dir, "onboard-codebase-walkthrough", "SKILL.md")) {
		t.Error("new onboard-codebase-walkthrough dir missing")
	}
}

func TestInstallSkillsDoesNotCleanCustomLegacyNamedDirs(t *testing.T) {
	dir := t.TempDir()
	customDir := filepath.Join(dir, "codebase-walkthrough")
	if err := os.MkdirAll(customDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(customDir, "SKILL.md"), []byte("---\nname: codebase-walkthrough\n---\n# My Custom Skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, removed, err := installSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	if !exists(customDir) {
		t.Error("custom legacy-named dir was removed")
	}
}

func TestInspectFlagsLegacySkillDirs(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	canonicalDir := filepath.Join(skillsDir, "onboard-codebase-walkthrough")
	legacyDir := filepath.Join(skillsDir, "codebase-walkthrough")
	for _, path := range []string{canonicalDir, legacyDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(canonicalDir, "SKILL.md"), []byte("---\nname: onboard-codebase-walkthrough\n---\n# Codebase Walkthrough\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "SKILL.md"), []byte("---\nname: codebase-walkthrough\n---\n# Codebase Walkthrough\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	agent := Agent{Name: "claude", SkillsDir: skillsDir, ConfigPath: filepath.Join(dir, "config.json"), Shape: ShapeJSONMCPServers}
	h := Inspect(agent)
	if !slices.Contains(h.LegacySkillDirs, "codebase-walkthrough") {
		t.Fatalf("LegacySkillDirs = %v, want codebase-walkthrough", h.LegacySkillDirs)
	}

	cleanDir := filepath.Join(dir, "clean-skills")
	cleanCanonical := filepath.Join(cleanDir, "onboard-codebase-walkthrough")
	if err := os.MkdirAll(cleanCanonical, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cleanCanonical, "SKILL.md"), []byte("---\nname: onboard-codebase-walkthrough\n---\n# Codebase Walkthrough\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanAgent := Agent{Name: "claude", SkillsDir: cleanDir, ConfigPath: filepath.Join(dir, "clean.json"), Shape: ShapeJSONMCPServers}
	clean := Inspect(cleanAgent)
	if len(clean.LegacySkillDirs) != 0 {
		t.Fatalf("clean LegacySkillDirs = %v, want none", clean.LegacySkillDirs)
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

func TestDetectedDoesNotTreatHomeAsAgentInstall(t *testing.T) {
	home := t.TempDir()
	claude := Agent{
		Name:       "claude",
		SkillsDir:  filepath.Join(home, ".claude", "skills"),
		ConfigPath: filepath.Join(home, ".claude.json"),
		Shape:      ShapeJSONMCPServers,
	}
	if Detected(claude) {
		t.Fatal("Detected should be false when only the home directory exists")
	}
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !Detected(claude) {
		t.Fatal("Detected should be true when the agent-specific directory exists")
	}
}

func TestRegistryShapes(t *testing.T) {
	all, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 8 {
		t.Errorf("expected >=8 agents, got %d", len(all))
	}
	byName := map[string]Agent{}
	for _, a := range all {
		if a.Name == "" || a.ConfigPath == "" || a.SkillsDir == "" {
			t.Errorf("agent %+v has an empty required field", a)
		}
		byName[a.Name] = a
	}
	if byName["codex"].Shape != ShapeTOMLMCPServers {
		t.Error("codex should be TOML mcp_servers")
	}
	if byName["kimi"].Shape != ShapeJSONMCPServers {
		t.Error("kimi should be JSON mcpServers")
	}
	if byName["opencode"].Shape != ShapeJSONOpencode {
		t.Error("opencode should be the JSON opencode outlier shape")
	}
	if byName["claude"].Shape != ShapeJSONMCPServers {
		t.Error("claude should be JSON mcpServers")
	}
	if byName["copilot"].Shape != ShapeJSONMCPServersWithTools {
		t.Error("copilot should be JSON mcpServers with tools")
	}
	if byName["junie"].Shape != ShapeJSONMCPServers {
		t.Error("junie should be JSON mcpServers")
	}
	if !strings.HasSuffix(byName["copilot"].ConfigPath, filepath.Join(".copilot", "mcp-config.json")) {
		t.Errorf("copilot config path = %q", byName["copilot"].ConfigPath)
	}
	if !strings.HasSuffix(byName["copilot"].SkillsDir, filepath.Join(".copilot", "skills")) {
		t.Errorf("copilot skills path = %q", byName["copilot"].SkillsDir)
	}
	if !strings.HasSuffix(byName["junie"].ConfigPath, filepath.Join(".junie", "mcp", "mcp.json")) {
		t.Errorf("junie config path = %q", byName["junie"].ConfigPath)
	}
	if !strings.HasSuffix(byName["junie"].SkillsDir, filepath.Join(".junie", "skills")) {
		t.Errorf("junie skills path = %q", byName["junie"].SkillsDir)
	}
	if !strings.HasSuffix(byName["kimi"].ConfigPath, filepath.Join(".kimi-code", "mcp.json")) {
		t.Errorf("kimi config path = %q", byName["kimi"].ConfigPath)
	}
	if !strings.HasSuffix(byName["kimi"].SkillsDir, filepath.Join(".kimi-code", "skills")) {
		t.Errorf("kimi skills path = %q", byName["kimi"].SkillsDir)
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

func TestRegistryHonorsCopilotHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("COPILOT_HOME", dir)

	all, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	var copilot Agent
	for _, a := range all {
		if a.Name == "copilot" {
			copilot = a
			break
		}
	}
	if copilot.ConfigPath != filepath.Join(dir, "mcp-config.json") {
		t.Errorf("copilot config path = %q, want COPILOT_HOME config", copilot.ConfigPath)
	}
	if copilot.SkillsDir != filepath.Join(dir, "skills") {
		t.Errorf("copilot skills dir = %q, want COPILOT_HOME skills", copilot.SkillsDir)
	}
}

func TestRegistryHonorsKimiCodeHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", dir)

	all, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	var kimi Agent
	for _, a := range all {
		if a.Name == "kimi" {
			kimi = a
			break
		}
	}
	if kimi.ConfigPath != filepath.Join(dir, "mcp.json") {
		t.Errorf("kimi config path = %q, want KIMI_CODE_HOME config", kimi.ConfigPath)
	}
	if kimi.SkillsDir != filepath.Join(dir, "skills") {
		t.Errorf("kimi skills dir = %q, want KIMI_CODE_HOME skills", kimi.SkillsDir)
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

func TestInstallRefreshesStaleBinarySoInspectIsHealthy(t *testing.T) {
	dir := t.TempDir()
	oldBin := filepath.Join(dir, "old", "onboard")
	newBin := filepath.Join(dir, "new", "onboard")
	if err := os.MkdirAll(filepath.Dir(oldBin), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(newBin), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldBin, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newBin, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}

	a := Agent{
		Name:       "test",
		SkillsDir:  filepath.Join(dir, "skills"),
		ConfigPath: filepath.Join(dir, "config.toml"),
		Shape:      ShapeTOMLMCPServers,
	}
	if _, err := Install(a, oldBin); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(oldBin); err != nil {
		t.Fatal(err)
	}
	res, err := Install(a, newBin)
	if err != nil {
		t.Fatal(err)
	}
	if res.ConfigAction != "refreshed" {
		t.Fatalf("ConfigAction = %q, want refreshed", res.ConfigAction)
	}
	h := Inspect(a)
	if !h.OK() {
		t.Fatalf("Inspect() not healthy after refresh: %+v", h)
	}
	if h.ConfiguredBin != newBin {
		t.Errorf("ConfiguredBin = %q, want %q", h.ConfiguredBin, newBin)
	}
}

func TestPreviewInstallDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	a := Agent{
		Name:       "test",
		SkillsDir:  filepath.Join(dir, "skills"),
		ConfigPath: filepath.Join(dir, "config.toml"),
		Shape:      ShapeTOMLMCPServers,
	}
	res, err := PreviewInstall(a, "/bin/onboard")
	if err != nil {
		t.Fatal(err)
	}
	if res.ConfigAction != "appended" {
		t.Fatalf("ConfigAction = %q, want appended", res.ConfigAction)
	}
	if res.SkillFiles == 0 {
		t.Fatal("preview should report embedded skill file count")
	}
	if exists(a.ConfigPath) {
		t.Fatal("preview created config file")
	}
	if exists(a.SkillsDir) {
		t.Fatal("preview created skills dir")
	}
}

func TestPreviewUninstallDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	a := Agent{
		Name:       "test",
		SkillsDir:  filepath.Join(dir, "skills"),
		ConfigPath: filepath.Join(dir, "config.toml"),
		Shape:      ShapeTOMLMCPServers,
	}
	if _, err := Install(a, "/bin/onboard"); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, a.ConfigPath)
	res, err := PreviewUninstall(a)
	if err != nil {
		t.Fatal(err)
	}
	if res.ConfigAction != "removed" {
		t.Fatalf("ConfigAction = %q, want removed", res.ConfigAction)
	}
	if res.SkillDirsRemoved == 0 {
		t.Fatal("preview should report skill dirs to remove")
	}
	if readFile(t, a.ConfigPath) != before {
		t.Fatal("preview modified config file")
	}
	if !exists(filepath.Join(a.SkillsDir, "onboard-codebase-walkthrough")) {
		t.Fatal("preview removed skill dir early")
	}
}

func TestUninstallRemovesOnboardConfigAndSkills(t *testing.T) {
	dir := t.TempDir()
	a := Agent{
		Name:       "test",
		SkillsDir:  filepath.Join(dir, "skills"),
		ConfigPath: filepath.Join(dir, "config.toml"),
		Shape:      ShapeTOMLMCPServers,
	}
	if _, err := Install(a, "/bin/onboard"); err != nil {
		t.Fatal(err)
	}
	res, err := Uninstall(a)
	if err != nil {
		t.Fatal(err)
	}
	if res.ConfigAction != "removed" {
		t.Fatalf("ConfigAction = %q, want removed", res.ConfigAction)
	}
	if res.SkillDirsRemoved == 0 {
		t.Fatal("expected uninstall to remove onboard skill dirs")
	}
	if strings.Contains(readFile(t, a.ConfigPath), "[mcp_servers.onboard]") {
		t.Fatal("uninstall left onboard TOML table behind")
	}
	if exists(filepath.Join(a.SkillsDir, "onboard-codebase-walkthrough")) {
		t.Fatal("uninstall left onboard skill dir behind")
	}
}

func TestUninstallJSONPreservesSiblingServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	writeJSON(t, path, map[string]any{
		"mcpServers": map[string]any{
			"other":   map[string]any{"command": "x"},
			"onboard": map[string]any{"command": "/bin/onboard", "args": []string{"serve"}},
		},
	})
	a := Agent{Name: "test", ConfigPath: path, Shape: ShapeJSONMCPServers}
	res, err := Uninstall(a)
	if err != nil {
		t.Fatal(err)
	}
	if res.ConfigAction != "removed" {
		t.Fatalf("ConfigAction = %q, want removed", res.ConfigAction)
	}
	servers := readJSON(t, path)["mcpServers"].(map[string]any)
	if _, ok := servers["onboard"]; ok {
		t.Fatal("onboard server was not removed")
	}
	if _, ok := servers["other"]; !ok {
		t.Fatal("sibling server was removed")
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
	if err := os.WriteFile(path, []byte("[mcp_servers.\"onboard\"]\ncommand = \"/bin/onboard\"\nargs = [\"serve\"]\n"), 0o644); err != nil {
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
