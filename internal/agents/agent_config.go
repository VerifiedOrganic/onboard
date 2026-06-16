package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// MCP-config writing: the per-Shape registration of onboard as an MCP server, plus the
// JSON/TOML read-merge-write machinery it needs. The installer's contract is that it never
// clobbers a config it does not understand and never duplicates an existing onboard entry —
// that safety lives here, separate from agent discovery/installation in agents.go.

type configResult struct {
	Action     string
	BackupPath string
}

func registerMCP(a Agent, binPath string) (configResult, error) {
	switch a.Shape {
	case ShapeJSONMcpServers:
		return registerJSONMcpServersDetailed(a.ConfigPath, binPath)
	case ShapeJSONMcpServersWithTools:
		return registerJSONMcpServersWithToolsDetailed(a.ConfigPath, binPath)
	case ShapeJSONOpencode:
		return registerJSONOpencodeDetailed(a.ConfigPath, binPath)
	case ShapeTOMLMcpServers:
		action, err := registerTOML(a.ConfigPath, binPath)
		return configResult{Action: action}, err
	}
	return configResult{Action: "skipped"}, nil
}

// loadJSONObjectDetailed reads a JSON config into a map. If the file exists but cannot be
// parsed, it is backed up to <path>.onboard-bak and an empty object is returned — we never
// silently clobber a config we don't understand.
func loadJSONObjectDetailed(path string) (map[string]any, string, error) {
	root := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return root, "", nil
		}
		return nil, "", err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return root, "", nil
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		bak, bakErr := uniqueBackup(path)
		if bakErr != nil {
			return nil, "", bakErr
		}
		if err := os.Rename(path, bak); err != nil {
			return nil, "", fmt.Errorf("back up unparseable JSON config %s: %w", path, err)
		}
		return map[string]any{}, bak, nil
	}
	root, ok := parsed.(map[string]any)
	if !ok || root == nil {
		return nil, "", fmt.Errorf("%s must contain a JSON object", path)
	}
	return root, "", nil
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
	res, err := registerJSONMcpServersDetailed(path, binPath)
	return res.Action, err
}

func registerJSONMcpServersDetailed(path, binPath string) (configResult, error) {
	return registerJSONMcpServersEntry(path, binPath, false)
}

// registerJSONMcpServersWithTools merges mcpServers.onboard = {type, command, args,
// tools} into JSON configs whose local server entries require an explicit tool allowlist
// (GitHub Copilot CLI).
func registerJSONMcpServersWithTools(path, binPath string) (string, error) {
	res, err := registerJSONMcpServersWithToolsDetailed(path, binPath)
	return res.Action, err
}

func registerJSONMcpServersWithToolsDetailed(path, binPath string) (configResult, error) {
	return registerJSONMcpServersEntry(path, binPath, true)
}

func registerJSONMcpServersEntry(path, binPath string, includeTools bool) (configResult, error) {
	root, backupPath, err := loadJSONObjectDetailed(path)
	if err != nil {
		return configResult{}, err
	}
	servers, err := objectField(root, "mcpServers")
	if err != nil {
		return configResult{}, err
	}
	if existing, ok := servers["onboard"]; ok {
		entry, ok := existing.(map[string]any)
		if !ok {
			return configResult{}, fmt.Errorf("mcpServers.onboard must be a JSON object")
		}
		changed := refreshStringField(entry, "command", binPath)
		changed = refreshStringListField(entry, "args", []string{"serve"}) || changed
		if includeTools {
			changed = refreshStringField(entry, "type", "local") || changed
			changed = refreshStringListField(entry, "tools", []string{"*"}) || changed
		}
		if !changed {
			return configResult{Action: "already-present", BackupPath: backupPath}, nil
		}
		if err := writeJSONObject(path, root); err != nil {
			return configResult{}, err
		}
		return configResult{Action: "refreshed", BackupPath: backupPath}, nil
	}
	entry := map[string]any{
		"command": binPath,
		"args":    []string{"serve"},
	}
	if includeTools {
		entry["type"] = "local"
		entry["tools"] = []string{"*"}
	}
	servers["onboard"] = entry
	root["mcpServers"] = servers
	if err := writeJSONObject(path, root); err != nil {
		return configResult{}, err
	}
	return configResult{Action: "merged", BackupPath: backupPath}, nil
}

// registerJSONOpencode merges mcp.onboard into opencode's config. opencode is the
// outlier: the root key is "mcp", the binary and its args go in a single "command"
// array, and the env field is "environment".
func registerJSONOpencode(path, binPath string) (string, error) {
	res, err := registerJSONOpencodeDetailed(path, binPath)
	return res.Action, err
}

func registerJSONOpencodeDetailed(path, binPath string) (configResult, error) {
	root, backupPath, err := loadJSONObjectDetailed(path)
	if err != nil {
		return configResult{}, err
	}
	servers, err := objectField(root, "mcp")
	if err != nil {
		return configResult{}, err
	}
	if existing, ok := servers["onboard"]; ok {
		entry, ok := existing.(map[string]any)
		if !ok {
			return configResult{}, fmt.Errorf("mcp.onboard must be a JSON object")
		}
		changed := refreshStringField(entry, "type", "local")
		changed = refreshStringListField(entry, "command", []string{binPath, "serve"}) || changed
		if enabled, ok := entry["enabled"].(bool); !ok || !enabled {
			entry["enabled"] = true
			changed = true
		}
		if _, ok := entry["environment"]; !ok {
			entry["environment"] = map[string]any{}
			changed = true
		}
		if !changed {
			return configResult{Action: "already-present", BackupPath: backupPath}, nil
		}
		if err := writeJSONObject(path, root); err != nil {
			return configResult{}, err
		}
		return configResult{Action: "refreshed", BackupPath: backupPath}, nil
	}
	servers["onboard"] = map[string]any{
		"type":        "local",
		"command":     []string{binPath, "serve"},
		"enabled":     true,
		"environment": map[string]any{},
	}
	root["mcp"] = servers
	if err := writeJSONObject(path, root); err != nil {
		return configResult{}, err
	}
	return configResult{Action: "merged", BackupPath: backupPath}, nil
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
	if loc := tomlOnboardTable.FindStringIndex(existing); loc != nil {
		refreshed, changed := refreshTOMLOnboardTable(existing, loc, binPath)
		if !changed {
			return "already-present", nil
		}
		if err := os.WriteFile(path, []byte(refreshed), configMode(path)); err != nil {
			return "", err
		}
		return "refreshed", nil
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

func planRegisterMCP(a Agent, binPath string) (configResult, error) {
	switch a.Shape {
	case ShapeJSONMcpServers:
		return planJSONMcpServersEntry(a.ConfigPath, binPath, false)
	case ShapeJSONMcpServersWithTools:
		return planJSONMcpServersEntry(a.ConfigPath, binPath, true)
	case ShapeJSONOpencode:
		return planJSONOpencode(a.ConfigPath, binPath)
	case ShapeTOMLMcpServers:
		action, err := planTOML(a.ConfigPath, binPath)
		return configResult{Action: action}, err
	}
	return configResult{Action: "skipped"}, nil
}

func planJSONMcpServersEntry(path, binPath string, includeTools bool) (configResult, error) {
	root, backupPath, unparseable, err := loadJSONObjectForPlan(path)
	if err != nil {
		return configResult{}, err
	}
	if unparseable {
		return configResult{Action: "merged", BackupPath: backupPath}, nil
	}
	servers, err := objectField(root, "mcpServers")
	if err != nil {
		return configResult{}, err
	}
	if existing, ok := servers["onboard"]; ok {
		entry, ok := existing.(map[string]any)
		if !ok {
			return configResult{}, fmt.Errorf("mcpServers.onboard must be a JSON object")
		}
		changed := refreshStringField(entry, "command", binPath)
		changed = refreshStringListField(entry, "args", []string{"serve"}) || changed
		if includeTools {
			changed = refreshStringField(entry, "type", "local") || changed
			changed = refreshStringListField(entry, "tools", []string{"*"}) || changed
		}
		if changed {
			return configResult{Action: "refreshed"}, nil
		}
		return configResult{Action: "already-present"}, nil
	}
	return configResult{Action: "merged"}, nil
}

func planJSONOpencode(path, binPath string) (configResult, error) {
	root, backupPath, unparseable, err := loadJSONObjectForPlan(path)
	if err != nil {
		return configResult{}, err
	}
	if unparseable {
		return configResult{Action: "merged", BackupPath: backupPath}, nil
	}
	servers, err := objectField(root, "mcp")
	if err != nil {
		return configResult{}, err
	}
	if existing, ok := servers["onboard"]; ok {
		entry, ok := existing.(map[string]any)
		if !ok {
			return configResult{}, fmt.Errorf("mcp.onboard must be a JSON object")
		}
		changed := refreshStringField(entry, "type", "local")
		changed = refreshStringListField(entry, "command", []string{binPath, "serve"}) || changed
		if enabled, ok := entry["enabled"].(bool); !ok || !enabled {
			changed = true
		}
		if _, ok := entry["environment"]; !ok {
			changed = true
		}
		if changed {
			return configResult{Action: "refreshed"}, nil
		}
		return configResult{Action: "already-present"}, nil
	}
	return configResult{Action: "merged"}, nil
}

func loadJSONObjectForPlan(path string) (map[string]any, string, bool, error) {
	root := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return root, "", false, nil
		}
		return nil, "", false, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return root, "", false, nil
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		bak, bakErr := uniqueBackup(path)
		if bakErr != nil {
			return nil, "", false, bakErr
		}
		return map[string]any{}, bak, true, nil
	}
	root, ok := parsed.(map[string]any)
	if !ok || root == nil {
		return nil, "", false, fmt.Errorf("%s must contain a JSON object", path)
	}
	return root, "", false, nil
}

func planTOML(path, binPath string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "appended", nil
		}
		return "", err
	}
	existing := string(data)
	if loc := tomlOnboardTable.FindStringIndex(existing); loc != nil {
		_, changed := refreshTOMLOnboardTable(existing, loc, binPath)
		if changed {
			return "refreshed", nil
		}
		return "already-present", nil
	}
	return "appended", nil
}

func unregisterMCP(a Agent) (configResult, error) {
	switch a.Shape {
	case ShapeJSONMcpServers, ShapeJSONMcpServersWithTools:
		action, err := unregisterJSONServer(a.ConfigPath, "mcpServers")
		return configResult{Action: action}, err
	case ShapeJSONOpencode:
		action, err := unregisterJSONServer(a.ConfigPath, "mcp")
		return configResult{Action: action}, err
	case ShapeTOMLMcpServers:
		action, err := unregisterTOML(a.ConfigPath)
		return configResult{Action: action}, err
	}
	return configResult{Action: "skipped"}, nil
}

func planUnregisterMCP(a Agent) (configResult, error) {
	switch a.Shape {
	case ShapeJSONMcpServers, ShapeJSONMcpServersWithTools:
		action, err := planUnregisterJSONServer(a.ConfigPath, "mcpServers")
		return configResult{Action: action}, err
	case ShapeJSONOpencode:
		action, err := planUnregisterJSONServer(a.ConfigPath, "mcp")
		return configResult{Action: action}, err
	case ShapeTOMLMcpServers:
		action, err := planUnregisterTOML(a.ConfigPath)
		return configResult{Action: action}, err
	}
	return configResult{Action: "skipped"}, nil
}

func unregisterJSONServer(path, rootKey string) (string, error) {
	root, exists, err := readJSONObjectStrict(path)
	if err != nil {
		return "", err
	}
	if !exists {
		return "already-absent", nil
	}
	servers, present, err := jsonServerObject(root, rootKey)
	if err != nil {
		return "", err
	}
	if !present {
		return "already-absent", nil
	}
	if _, ok := servers["onboard"]; !ok {
		return "already-absent", nil
	}
	delete(servers, "onboard")
	root[rootKey] = servers
	if err := writeJSONObject(path, root); err != nil {
		return "", err
	}
	return "removed", nil
}

func planUnregisterJSONServer(path, rootKey string) (string, error) {
	root, exists, err := readJSONObjectStrict(path)
	if err != nil {
		return "", err
	}
	if !exists {
		return "already-absent", nil
	}
	servers, present, err := jsonServerObject(root, rootKey)
	if err != nil {
		return "", err
	}
	if !present {
		return "already-absent", nil
	}
	if _, ok := servers["onboard"]; !ok {
		return "already-absent", nil
	}
	return "removed", nil
}

func readJSONObjectStrict(path string) (map[string]any, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]any{}, true, nil
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, true, fmt.Errorf("parse JSON config %s: %w", path, err)
	}
	root, ok := parsed.(map[string]any)
	if !ok || root == nil {
		return nil, true, fmt.Errorf("%s must contain a JSON object", path)
	}
	return root, true, nil
}

func jsonServerObject(root map[string]any, key string) (map[string]any, bool, error) {
	v, ok := root[key]
	if !ok || v == nil {
		return nil, false, nil
	}
	servers, ok := v.(map[string]any)
	if !ok {
		return nil, true, fmt.Errorf("%q must be a JSON object", key)
	}
	return servers, true, nil
}

func unregisterTOML(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "already-absent", nil
		}
		return "", err
	}
	existing := string(data)
	loc := tomlOnboardTable.FindStringIndex(existing)
	if loc == nil {
		return "already-absent", nil
	}
	updated := removeTOMLOnboardTable(existing, loc)
	if err := os.WriteFile(path, []byte(updated), configMode(path)); err != nil {
		return "", err
	}
	return "removed", nil
}

func planUnregisterTOML(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "already-absent", nil
		}
		return "", err
	}
	if tomlOnboardTable.FindStringIndex(string(data)) == nil {
		return "already-absent", nil
	}
	return "removed", nil
}

func removeTOMLOnboardTable(text string, loc []int) string {
	bodyStart := lineEndAt(text, loc[1])
	sectionEnd := len(text)
	if next := tomlNextTableHeader.FindStringIndex(text[bodyStart:]); next != nil {
		sectionEnd = bodyStart + next[0]
	}
	updated := text[:loc[0]] + text[sectionEnd:]
	for strings.Contains(updated, "\n\n\n") {
		updated = strings.ReplaceAll(updated, "\n\n\n", "\n\n")
	}
	return updated
}

func refreshStringField(entry map[string]any, key, want string) bool {
	if got, ok := entry[key].(string); ok && got == want {
		return false
	}
	entry[key] = want
	return true
}

func refreshStringListField(entry map[string]any, key string, want []string) bool {
	if stringListEqual(entry[key], want) {
		return false
	}
	entry[key] = want
	return true
}

func stringListEqual(v any, want []string) bool {
	switch got := v.(type) {
	case []string:
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	case []any:
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			s, ok := got[i].(string)
			if !ok || s != want[i] {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func refreshTOMLOnboardTable(text string, loc []int, binPath string) (string, bool) {
	bodyStart := lineEndAt(text, loc[1])
	sectionEnd := len(text)
	if next := tomlNextTableHeader.FindStringIndex(text[bodyStart:]); next != nil {
		sectionEnd = bodyStart + next[0]
	}

	section := text[loc[0]:sectionEnd]
	changed := false
	commandLine := fmt.Sprintf("command = %q", binPath)
	if current, ok := tomlCommandFromSection(section); !ok || current != binPath {
		section = replaceOrInsertTOMLLine(section, tomlCommandLine, commandLine, tableHeaderEnd(section))
		changed = true
	}

	const argsLine = `args = ["serve"]`
	if argsLoc := tomlArgsLine.FindStringIndex(section); argsLoc != nil {
		if strings.TrimSpace(section[argsLoc[0]:argsLoc[1]]) != argsLine {
			section = section[:argsLoc[0]] + argsLine + section[argsLoc[1]:]
			changed = true
		}
	} else {
		insertAt := tableHeaderEnd(section)
		if cmdLoc := tomlCommandLine.FindStringIndex(section); cmdLoc != nil {
			insertAt = lineEndAt(section, cmdLoc[1])
		}
		section = insertTOMLLine(section, insertAt, argsLine)
		changed = true
	}

	if !changed {
		return text, false
	}
	return text[:loc[0]] + section + text[sectionEnd:], true
}

func tomlCommandFromSection(section string) (string, bool) {
	m := tomlCommandValue.FindStringSubmatch(section)
	if m == nil {
		return "", false
	}
	unq, err := strconv.Unquote(m[1])
	if err != nil {
		return "", false
	}
	return unq, true
}

func replaceOrInsertTOMLLine(section string, lineRE *regexp.Regexp, line string, insertAt int) string {
	if loc := lineRE.FindStringIndex(section); loc != nil {
		return section[:loc[0]] + line + section[loc[1]:]
	}
	return insertTOMLLine(section, insertAt, line)
}

func insertTOMLLine(section string, insertAt int, line string) string {
	insert := line + "\n"
	if insertAt > 0 && insertAt <= len(section) && section[insertAt-1] != '\n' {
		insert = "\n" + insert
	}
	return section[:insertAt] + insert + section[insertAt:]
}

func tableHeaderEnd(section string) int {
	return lineEndAt(section, 0)
}

func lineEndAt(s string, pos int) int {
	if pos > len(s) {
		return len(s)
	}
	if next := strings.IndexByte(s[pos:], '\n'); next >= 0 {
		return pos + next + 1
	}
	return len(s)
}

var (
	tomlOnboardTable = regexp.MustCompile(`(?m)^[ \t]*\[mcp_servers\.(?:onboard|"onboard")\]`)
	tomlCommandLine  = regexp.MustCompile(`(?m)^[ \t]*command[ \t]*=.*$`)
	tomlArgsLine     = regexp.MustCompile(`(?m)^[ \t]*args[ \t]*=.*$`)
)
