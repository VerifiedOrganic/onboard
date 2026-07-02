// Package agents knows how each supported coding agent stores MCP server config
// and native skills, and installs onboard into them. The install does two writes
// per agent: it drops the embedded skill files into the agent's skills directory
// and registers this binary as an MCP server in the agent's config.
//
// The config shapes differ in non-obvious ways (verified against each agent's
// docs and live config files), so each agent declares a Shape that fully
// determines how its server entry is written:
//
//   - ShapeJSONMCPServers: JSON, top-level "mcpServers" object, entry is
//     {command, args}. Used by Claude Code, Cursor, and the npm grok-cli.
//   - ShapeJSONMCPServersWithTools: same as ShapeJSONMCPServers, plus type:"local"
//     and tools:["*"]. Used by GitHub Copilot CLI.
//   - ShapeJSONOpencode: JSON, top-level "mcp" object (the outlier), entry is
//     {type:"local", command:[bin, args...], enabled, environment}.
//   - ShapeTOMLMCPServers: TOML, [mcp_servers.<name>] table with command/args.
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
	// ShapeUnknown is the zero value and is treated as unsupported.
	ShapeUnknown Shape = iota
	// ShapeJSONMCPServers stores MCP servers under a top-level mcpServers object.
	ShapeJSONMCPServers
	// ShapeJSONMCPServersWithTools stores MCP servers under mcpServers with a tools allowlist.
	ShapeJSONMCPServersWithTools
	// ShapeJSONOpencode stores opencode local servers under a top-level mcp object.
	ShapeJSONOpencode
	// ShapeTOMLMCPServers stores MCP servers as mcp_servers TOML tables.
	ShapeTOMLMCPServers
)

// Agent describes how to install onboard into a particular coding agent.
type Agent struct {
	Name       string // canonical id: claude, codex, grok, kimi, gemini, opencode, cursor, copilot, junie
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
	grok := Agent{Name: "grok", SkillsDir: j(".grok", "skills"), ConfigPath: j(".grok", "config.toml"), Shape: ShapeTOMLMCPServers}
	if exists(j(".grok", "user-settings.json")) && !exists(j(".grok", "config.toml")) {
		grok.ConfigPath = j(".grok", "user-settings.json")
		grok.Shape = ShapeJSONMCPServers
	}

	kimiHome := j(".kimi-code")
	if env := strings.TrimSpace(os.Getenv("KIMI_CODE_HOME")); env != "" {
		kimiHome = env
		if !filepath.IsAbs(kimiHome) {
			kimiHome = filepath.Join(home, kimiHome)
		}
		if abs, err := filepath.Abs(kimiHome); err == nil {
			kimiHome = abs
		}
	}

	return []Agent{
		{Name: "claude", SkillsDir: j(".claude", "skills"), ConfigPath: j(".claude.json"), Shape: ShapeJSONMCPServers},
		{Name: "codex", SkillsDir: filepath.Join(codexHome, "skills"), ConfigPath: filepath.Join(codexHome, "config.toml"), Shape: ShapeTOMLMCPServers},
		grok,
		{Name: "kimi", SkillsDir: filepath.Join(kimiHome, "skills"), ConfigPath: filepath.Join(kimiHome, "mcp.json"), Shape: ShapeJSONMCPServers},
		{Name: "gemini", SkillsDir: j(".gemini", "skills"), ConfigPath: j(".gemini", "settings.json"), Shape: ShapeJSONMCPServers},
		{Name: "opencode", SkillsDir: j(".config", "opencode", "skills"), ConfigPath: j(".config", "opencode", "opencode.json"), Shape: ShapeJSONOpencode},
		{Name: "cursor", SkillsDir: j(".cursor", "skills"), ConfigPath: j(".cursor", "mcp.json"), Shape: ShapeJSONMCPServers},
		{Name: "copilot", SkillsDir: filepath.Join(copilotHome, "skills"), ConfigPath: filepath.Join(copilotHome, "mcp-config.json"), Shape: ShapeJSONMCPServersWithTools},
		{Name: "junie", SkillsDir: j(".junie", "skills"), ConfigPath: j(".junie", "mcp", "mcp.json"), Shape: ShapeJSONMCPServers},
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
	return Agent{}, fmt.Errorf("unknown agent %q (known: claude, codex, grok, kimi, gemini, opencode, cursor, copilot, junie)", name)
}

// Detected reports whether the agent appears installed (its config exists or its
// agent-specific skills/config directory exists). Used by --all to avoid creating
// dirs for absent agents.
func Detected(a Agent) bool {
	if a.ConfigPath != "" && exists(a.ConfigPath) {
		return true
	}
	if a.SkillsDir != "" && exists(filepath.Dir(a.SkillsDir)) {
		return true
	}
	if a.SkillsDir != "" {
		return false
	}
	return exists(filepath.Dir(a.ConfigPath))
}

// Result reports what an install did for one agent.
type Result struct {
	Agent            string
	SkillFiles       int
	SkillDirsCleaned int
	SkillDirsRemoved int
	ConfigAction     string // merged | appended | refreshed | already-present | skipped
	ConfigPath       string
	SkillsDir        string
	BackupPath       string
}

// Install writes the skill bundle and registers the onboard MCP server in the
// agent's config. binPath must be the absolute path to this binary.
func Install(a Agent, binPath string) (Result, error) {
	res := Result{Agent: a.Name, ConfigPath: a.ConfigPath, SkillsDir: a.SkillsDir}

	if a.SkillsDir != "" {
		n, removed, err := installSkills(a.SkillsDir)
		if err != nil {
			return res, err
		}
		res.SkillFiles = n
		res.SkillDirsCleaned = removed
	}

	cfg, err := registerMCP(a, binPath)
	if err != nil {
		return res, err
	}
	res.ConfigAction = cfg.Action
	res.BackupPath = cfg.BackupPath
	return res, nil
}

// PreviewInstall reports what Install would do without writing skill or config files.
func PreviewInstall(a Agent, binPath string) (Result, error) {
	res := Result{Agent: a.Name, ConfigPath: a.ConfigPath, SkillsDir: a.SkillsDir}
	if a.SkillsDir != "" {
		n, err := countSkillFiles()
		if err != nil {
			return res, err
		}
		res.SkillFiles = n
	}
	cfg, err := planRegisterMCP(a, binPath)
	if err != nil {
		return res, err
	}
	res.ConfigAction = cfg.Action
	res.BackupPath = cfg.BackupPath
	return res, nil
}

// Uninstall removes onboard's MCP config entry and embedded skill directories for one agent.
func Uninstall(a Agent) (Result, error) {
	res := Result{Agent: a.Name, ConfigPath: a.ConfigPath, SkillsDir: a.SkillsDir}
	if a.SkillsDir != "" {
		removed, err := uninstallSkills(a.SkillsDir)
		if err != nil {
			return res, err
		}
		res.SkillDirsRemoved = removed
	}
	cfg, err := unregisterMCP(a)
	if err != nil {
		return res, err
	}
	res.ConfigAction = cfg.Action
	return res, nil
}

// PreviewUninstall reports what Uninstall would remove without modifying files.
func PreviewUninstall(a Agent) (Result, error) {
	res := Result{Agent: a.Name, ConfigPath: a.ConfigPath, SkillsDir: a.SkillsDir}
	if a.SkillsDir != "" {
		removed, err := countInstalledSkillDirs(a.SkillsDir)
		if err != nil {
			return res, err
		}
		res.SkillDirsRemoved = removed
	}
	cfg, err := planUnregisterMCP(a)
	if err != nil {
		return res, err
	}
	res.ConfigAction = cfg.Action
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
			if strings.Contains(rel, "..") || filepath.IsAbs(rel) {
				continue
			}
			dst := filepath.Join(base, rel)
			if relPath, err := filepath.Rel(base, dst); err != nil || strings.HasPrefix(relPath, "..") {
				continue
			}
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

func countSkillFiles() (int, error) {
	all, err := skills.List()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, sk := range all {
		if sk.Name == "" || strings.ContainsAny(sk.Name, `/\`) || strings.Contains(sk.Name, "..") {
			continue
		}
		files, err := sk.Files()
		if err != nil {
			return count, err
		}
		count += len(files)
	}
	return count, nil
}

func uninstallSkills(skillsDir string) (int, error) {
	dirs, err := onboardSkillDirs(skillsDir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, dir := range dirs {
		if !exists(dir) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func countInstalledSkillDirs(skillsDir string) (int, error) {
	dirs, err := onboardSkillDirs(skillsDir)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, dir := range dirs {
		if exists(dir) {
			count++
		}
	}
	return count, nil
}

func onboardSkillDirs(skillsDir string) ([]string, error) {
	all, err := skills.List()
	if err != nil {
		return nil, err
	}
	var dirs []string
	seen := map[string]bool{}
	add := func(name string) {
		if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") || seen[name] {
			return
		}
		seen[name] = true
		dirs = append(dirs, filepath.Join(skillsDir, name))
	}
	for _, sk := range all {
		add(sk.Name)
	}
	for legacy := range skills.LegacyAliases() {
		legacyDir := filepath.Join(skillsDir, legacy)
		if looksLikeLegacyOnboardSkill(legacyDir, legacy) {
			add(legacy)
		}
	}
	return dirs, nil
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
