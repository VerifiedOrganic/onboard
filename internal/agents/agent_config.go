package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MCP-config writing: the per-Shape registration of onboard as an MCP server, plus the
// JSON/TOML read-merge-write machinery it needs. The installer's contract is that it never
// clobbers a config it does not understand and never duplicates an existing onboard entry —
// that safety lives here, separate from agent discovery/installation in agents.go.

func registerMCP(a Agent, binPath string) (string, error) {
	switch a.Shape {
	case ShapeJSONMcpServers:
		return registerJSONMcpServers(a.ConfigPath, binPath)
	case ShapeJSONOpencode:
		return registerJSONOpencode(a.ConfigPath, binPath)
	case ShapeTOMLMcpServers:
		return registerTOML(a.ConfigPath, binPath)
	}
	return "skipped", nil
}

// loadJSONObject reads a JSON config into a map. If the file exists but cannot be
// parsed, it is backed up to <path>.onboard-bak and an empty object is returned —
// we never silently clobber a config we don't understand.
func loadJSONObject(path string) (map[string]any, error) {
	root := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return root, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return root, nil
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		bak, bakErr := uniqueBackup(path)
		if bakErr != nil {
			return nil, bakErr
		}
		if err := os.Rename(path, bak); err != nil {
			return nil, fmt.Errorf("back up unparseable JSON config %s: %w", path, err)
		}
		return map[string]any{}, nil
	}
	root, ok := parsed.(map[string]any)
	if !ok || root == nil {
		return nil, fmt.Errorf("%s must contain a JSON object", path)
	}
	return root, nil
}

func writeJSONObject(path string, root map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, configMode(path))
}

// configMode preserves an existing config file's permissions (agent configs often
// hold tokens, e.g. ~/.claude.json at 0600) and defaults new files to 0600 rather
// than a world-readable 0644.
func configMode(path string) os.FileMode {
	if fi, err := os.Stat(path); err == nil {
		return fi.Mode().Perm()
	}
	return 0o600
}

// uniqueBackup returns a backup path that does not already exist, so a second
// unparseable-config run cannot overwrite the first (recoverable) backup.
func uniqueBackup(path string) (string, error) {
	bak := path + ".onboard-bak"
	for n := 1; ; n++ {
		if _, err := os.Stat(bak); err != nil {
			if os.IsNotExist(err) {
				return bak, nil
			}
			return "", err
		}
		bak = fmt.Sprintf("%s.onboard-bak.%d", path, n)
	}
}

func objectField(root map[string]any, key string) (map[string]any, error) {
	v, ok := root[key]
	if !ok || v == nil {
		return map[string]any{}, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%q must be a JSON object", key)
	}
	return m, nil
}

// registerJSONMcpServers merges mcpServers.onboard = {command, args} into a JSON
// config (Claude Code, Cursor, npm grok-cli), preserving other keys.
func registerJSONMcpServers(path, binPath string) (string, error) {
	root, err := loadJSONObject(path)
	if err != nil {
		return "", err
	}
	servers, err := objectField(root, "mcpServers")
	if err != nil {
		return "", err
	}
	if _, ok := servers["onboard"]; ok {
		return "already-present", nil
	}
	servers["onboard"] = map[string]any{
		"command": binPath,
		"args":    []string{"serve"},
	}
	root["mcpServers"] = servers
	if err := writeJSONObject(path, root); err != nil {
		return "", err
	}
	return "merged", nil
}

// registerJSONOpencode merges mcp.onboard into opencode's config. opencode is the
// outlier: the root key is "mcp", the binary and its args go in a single "command"
// array, and the env field is "environment".
func registerJSONOpencode(path, binPath string) (string, error) {
	root, err := loadJSONObject(path)
	if err != nil {
		return "", err
	}
	servers, err := objectField(root, "mcp")
	if err != nil {
		return "", err
	}
	if _, ok := servers["onboard"]; ok {
		return "already-present", nil
	}
	servers["onboard"] = map[string]any{
		"type":        "local",
		"command":     []string{binPath, "serve"},
		"enabled":     true,
		"environment": map[string]any{},
	}
	root["mcp"] = servers
	if err := writeJSONObject(path, root); err != nil {
		return "", err
	}
	return "merged", nil
}

// registerTOML appends an [mcp_servers.onboard] table if absent (Codex, xAI Grok).
// Appending (rather than re-marshaling) keeps the user's comments and ordering.
func registerTOML(path, binPath string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	var existing string
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	}
	// Match a real (non-commented) table header at line start, in either the bare
	// or quoted-key form. A naive substring check would false-match commented-out
	// tables and miss the [mcp_servers."onboard"] form.
	if tomlOnboardTable.MatchString(existing) {
		return "already-present", nil
	}
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	block := fmt.Sprintf("\n[mcp_servers.onboard]\ncommand = %q\nargs = [\"serve\"]\n", binPath)
	if err := os.WriteFile(path, []byte(existing+block), configMode(path)); err != nil {
		return "", err
	}
	return "appended", nil
}

var tomlOnboardTable = regexp.MustCompile(`(?m)^[ \t]*\[mcp_servers\.(?:onboard|"onboard")\]`)
