package agents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/VerifiedOrganic/onboard/internal/skills"
)

// Installation health-check (read-only). Where agents.go/agent_config.go *write* the
// install, this inspects it: is onboard registered, does the configured binary still
// exist, and did the skill files land? It never modifies a config — in particular it must
// not trigger loadJSONObject's unparseable-config backup, so it uses its own RO loader.

// Health is the result of inspecting one agent's onboard installation.
type Health struct {
	Agent          string   `json:"agent"`
	Detected       bool     `json:"detected"`
	ConfigPath     string   `json:"config_path"`
	ConfigPresent  bool     `json:"config_present"`
	Registered     bool     `json:"registered"`
	ConfiguredBin  string   `json:"configured_bin,omitempty"`
	BinExists      bool     `json:"bin_exists"`
	SkillsDir      string   `json:"skills_dir"`
	SkillsPresent  int      `json:"skills_present"`
	SkillsExpected int      `json:"skills_expected"`
	Issues         []string `json:"issues,omitempty"`
}

// OK reports whether the install is healthy enough to use: onboard is registered, the
// configured binary exists, every skill landed, and nothing flagged an issue. An
// undetected agent is not OK but is not a problem either — callers tell the two apart via
// Detected.
func (h Health) OK() bool {
	return h.Registered && h.BinExists && h.SkillsPresent >= h.SkillsExpected && len(h.Issues) == 0
}

// Inspect checks one agent's onboard installation without modifying anything.
func Inspect(a Agent) Health {
	h := Health{Agent: a.Name, ConfigPath: a.ConfigPath, SkillsDir: a.SkillsDir, Detected: Detected(a)}
	h.ConfigPresent = exists(a.ConfigPath)

	bin, registered := configuredBin(a)
	h.Registered = registered
	h.ConfiguredBin = bin
	if registered && bin != "" {
		h.BinExists = exists(bin)
	}

	if all, err := skills.List(); err == nil {
		h.SkillsExpected = len(all)
		if a.SkillsDir != "" {
			for _, sk := range all {
				if exists(filepath.Join(a.SkillsDir, sk.Name, "SKILL.md")) {
					h.SkillsPresent++
				}
			}
		}
	}

	switch {
	case !h.Detected:
		// not installed — absent, not broken
	case !h.ConfigPresent:
		h.Issues = append(h.Issues, "agent detected but its config file is missing: "+a.ConfigPath)
	case !h.Registered:
		h.Issues = append(h.Issues, "onboard is not registered in the config (run: onboard install --agent "+a.Name+")")
	}
	if h.Registered && bin != "" && !h.BinExists {
		h.Issues = append(h.Issues, "configured binary not found: "+bin+" (re-run install to refresh the path)")
	}
	if h.Detected && a.SkillsDir != "" && h.SkillsExpected > 0 && h.SkillsPresent < h.SkillsExpected {
		h.Issues = append(h.Issues, fmt.Sprintf("skills incomplete: %d of %d present in %s (run install)", h.SkillsPresent, h.SkillsExpected, a.SkillsDir))
	}
	return h
}

// configuredBin returns the binary path onboard is registered with in the agent's config,
// and whether an onboard entry is present at all. Best-effort: returns ("", false) when the
// config is absent, unparseable, or has no onboard entry.
func configuredBin(a Agent) (string, bool) {
	switch a.Shape {
	case ShapeJSONMcpServers, ShapeJSONMcpServersWithTools:
		return jsonServerCommand(a.ConfigPath, "mcpServers")
	case ShapeJSONOpencode:
		return jsonServerCommand(a.ConfigPath, "mcp")
	case ShapeTOMLMcpServers:
		return tomlOnboardCommand(a.ConfigPath)
	}
	return "", false
}

// jsonServerCommand reads <key>.onboard.command. The command is a string for the
// mcpServers shape and a [bin, args...] array for opencode's mcp shape.
func jsonServerCommand(path, key string) (string, bool) {
	root, ok := readJSONObjectRO(path)
	if !ok {
		return "", false
	}
	servers, _ := root[key].(map[string]any)
	entry, ok := servers["onboard"].(map[string]any)
	if !ok {
		return "", false
	}
	switch cmd := entry["command"].(type) {
	case string:
		return cmd, true
	case []any:
		if len(cmd) > 0 {
			if s, ok := cmd[0].(string); ok {
				return s, true
			}
		}
	}
	return "", true // registered, but the command field has an unexpected shape
}

// readJSONObjectRO parses a JSON config without side effects — unlike loadJSONObject it
// never backs up or renames an unparseable file. Returns (nil, false) on any problem.
func readJSONObjectRO(path string) (map[string]any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var parsed any
	if json.Unmarshal(data, &parsed) != nil {
		return nil, false
	}
	m, ok := parsed.(map[string]any)
	return m, ok
}

// tomlOnboardCommand extracts the command from the [mcp_servers.onboard] table, scoping the
// search to that table (up to the next table header) so it can't read another server's
// command. Returns (bin, true) when the table exists; bin may be "" if command isn't parsed.
func tomlOnboardCommand(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	text := string(data)
	loc := tomlOnboardTable.FindStringIndex(text)
	if loc == nil {
		return "", false
	}
	rest := text[loc[1]:]
	if next := tomlNextTableHeader.FindStringIndex(rest); next != nil {
		rest = rest[:next[0]]
	}
	if m := tomlCommandValue.FindStringSubmatch(rest); m != nil {
		if unq, err := strconv.Unquote(m[1]); err == nil {
			return unq, true
		}
	}
	return "", true
}

var (
	tomlNextTableHeader = regexp.MustCompile(`(?m)^[ \t]*\[`)
	tomlCommandValue    = regexp.MustCompile(`(?m)^[ \t]*command[ \t]*=\s*("(?:[^"\\]|\\.)*")`)
)
