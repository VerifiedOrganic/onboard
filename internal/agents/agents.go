// Package agents knows how each supported coding agent stores MCP server config
// and native skills, and installs onboard into them. The install does two writes
// per agent: it drops the embedded skill files into the agent's skills directory
// and registers this binary as an MCP server in the agent's config.
//
// The config shapes differ in non-obvious ways (verified against each agent's
// docs and live config files), so each agent declares a Shape that fully
// determines how its server entry is written:
//
//   - ShapeJSONMcpServers: JSON, top-level "mcpServers" object, entry is
//     {command, args}. Used by Claude Code, Cursor, and the npm grok-cli.
//   - ShapeJSONMcpServersWithTools: same as ShapeJSONMcpServers, plus type:"local"
//     and tools:["*"]. Used by GitHub Copilot CLI.
//   - ShapeJSONOpencode: JSON, top-level "mcp" object (the outlier), entry is
//     {type:"local", command:[bin, args...], enabled, environment}.
//   - ShapeTOMLMcpServers: TOML, [mcp_servers.<name>] table with command/args.
//     Used by Codex and the xAI Grok Build CLI.
package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/VerifiedOrganic/onboard/internal/skills"
)

// Shape is how an agent encodes an MCP server entry.
type Shape int

const (
	// ShapeJSONMcpServers stores MCP servers under a top-level mcpServers object.
	ShapeJSONMcpServers Shape = iota
	// ShapeJSONMcpServersWithTools stores MCP servers under mcpServers with a tools allowlist.
	ShapeJSONMcpServersWithTools
	// ShapeJSONOpencode stores opencode local servers under a top-level mcp object.
	ShapeJSONOpencode
	// ShapeTOMLMcpServers stores MCP servers as mcp_servers TOML tables.
	ShapeTOMLMcpServers
)

// Agent describes how to install onboard into a particular coding agent.
type Agent struct {
	Name       string // canonical id: claude, codex, grok, opencode, cursor, copilot, junie
	SkillsDir  string // absolute dir for native skill files
	ConfigPath string // absolute path to the agent's MCP config file
	Shape      Shape  // how the server entry is encoded
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Registry returns the known agents with paths resolved against the user's home.
func Registry() ([]Agent, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	j := func(parts ...string) string {
		return filepath.Join(append([]string{home}, parts...)...)
	}
	codexHome := j(".codex")
	if env := strings.TrimSpace(os.Getenv("CODEX_HOME")); env != "" {
		codexHome = env
		if !filepath.IsAbs(codexHome) {
			codexHome = filepath.Join(home, codexHome)
		}
		if abs, err := filepath.Abs(codexHome); err == nil {
			codexHome = abs
		}
	}
	copilotHome := j(".copilot")
	if env := strings.TrimSpace(os.Getenv("COPILOT_HOME")); env != "" {
		copilotHome = env
		if !filepath.IsAbs(copilotHome) {
			copilotHome = filepath.Join(home, copilotHome)
		}
		if abs, err := filepath.Abs(copilotHome); err == nil {
			copilotHome = abs
		}
	}

	// Grok ships in two flavors: the xAI Grok Build CLI (TOML at
	// ~/.grok/config.toml) and the npm grok-cli (JSON at
	// ~/.grok/user-settings.json). Prefer TOML; fall back to JSON only if the
	// JSON variant is present and the TOML one is not.
	grok := Agent{Name: "grok", SkillsDir: j(".grok", "skills"), ConfigPath: j(".grok", "config.toml"), Shape: ShapeTOMLMcpServers}
	if exists(j(".grok", "user-settings.json")) && !exists(j(".grok", "config.toml")) {
		grok.ConfigPath = j(".grok", "user-settings.json")
		grok.Shape = ShapeJSONMcpServers
	}

	return []Agent{
		{Name: "claude", SkillsDir: j(".claude", "skills"), ConfigPath: j(".claude.json"), Shape: ShapeJSONMcpServers},
		{Name: "codex", SkillsDir: filepath.Join(codexHome, "skills"), ConfigPath: filepath.Join(codexHome, "config.toml"), Shape: ShapeTOMLMcpServers},
		grok,
		{Name: "opencode", SkillsDir: j(".config", "opencode", "skills"), ConfigPath: j(".config", "opencode", "opencode.json"), Shape: ShapeJSONOpencode},
		{Name: "cursor", SkillsDir: j(".cursor", "skills"), ConfigPath: j(".cursor", "mcp.json"), Shape: ShapeJSONMcpServers},
		{Name: "copilot", SkillsDir: filepath.Join(copilotHome, "skills"), ConfigPath: filepath.Join(copilotHome, "mcp-config.json"), Shape: ShapeJSONMcpServersWithTools},
		{Name: "junie", SkillsDir: j(".junie", "skills"), ConfigPath: j(".junie", "mcp", "mcp.json"), Shape: ShapeJSONMcpServers},
	}, nil
}

// Find returns one agent by canonical name.
func Find(name string) (Agent, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	all, err := Registry()
	if err != nil {
		return Agent{}, err
	}
	for _, a := range all {
		if a.Name == name {
			return a, nil
		}
	}
	return Agent{}, fmt.Errorf("unknown agent %q (known: claude, codex, grok, opencode, cursor, copilot, junie)", name)
}

// Detected reports whether the agent appears installed (its config or skills
// parent directory exists). Used by --all to avoid creating dirs for absent agents.
func Detected(a Agent) bool {
	if a.SkillsDir != "" && exists(filepath.Dir(a.SkillsDir)) {
		return true
	}
	return exists(filepath.Dir(a.ConfigPath))
}

// Result reports what an install did for one agent.
type Result struct {
	Agent            string
	SkillFiles       int
	SkillDirsCleaned int
	ConfigAction     string // merged | appended | already-present | skipped
}

// Install writes the skill bundle and registers the onboard MCP server in the
// agent's config. binPath must be the absolute path to this binary.
func Install(a Agent, binPath string) (Result, error) {
	res := Result{Agent: a.Name}

	if a.SkillsDir != "" {
		n, removed, err := installSkills(a.SkillsDir)
		if err != nil {
			return res, err
		}
		res.SkillFiles = n
		res.SkillDirsCleaned = removed
	}

	action, err := registerMCP(a, binPath)
	if err != nil {
		return res, err
	}
	res.ConfigAction = action
	return res, nil
}

func installSkills(skillsDir string) (int, int, error) {
	all, err := skills.List()
	if err != nil {
		return 0, 0, err
	}
	count := 0
	for _, sk := range all {
		// Defensive: skill names come from embedded frontmatter, but never let a
		// name escape the skills dir via path separators or "..".
		if sk.Name == "" || strings.ContainsAny(sk.Name, `/\`) || strings.Contains(sk.Name, "..") {
			continue
		}
		files, err := sk.Files()
		if err != nil {
			return count, 0, err
		}
		base := filepath.Join(skillsDir, sk.Name)
		for rel, content := range files {
			dst := filepath.Join(base, rel)
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return count, 0, err
			}
			if err := os.WriteFile(dst, content, 0o644); err != nil {
				return count, 0, err
			}
			count++
		}
	}
	removed, err := cleanupLegacySkillDirs(skillsDir)
	if err != nil {
		return count, removed, err
	}
	return count, removed, nil
}

func cleanupLegacySkillDirs(skillsDir string) (int, error) {
	removed := 0
	for legacy, canonical := range skills.LegacyAliases() {
		legacyDir := filepath.Join(skillsDir, legacy)
		canonicalDir := filepath.Join(skillsDir, canonical)
		if !exists(canonicalDir) || !looksLikeLegacyOnboardSkill(legacyDir, legacy) {
			continue
		}
		if err := os.RemoveAll(legacyDir); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

var legacySkillHeadings = map[string]string{
	"architecture-cartographer":  "# Architecture Cartographer",
	"codebase-walkthrough":       "# Codebase Walkthrough",
	"dependency-impact-analyzer": "# Dependency Impact Analyzer",
	"guide-maintainer":           "# Guide Maintainer",
	"test-gap-and-risk-auditor":  "# Test Gap and Risk Auditor",
}

func looksLikeLegacyOnboardSkill(dir, legacy string) bool {
	heading, ok := legacySkillHeadings[legacy]
	if !ok {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return false
	}
	text := string(data)
	return strings.Contains(text, "name: "+legacy) && strings.Contains(text, heading)
}

// MCP-config registration lives in agent_config.go.
