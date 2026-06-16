package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect wires a client to a fresh server over the in-memory transport (server
// first, then client) and returns the client session.
func connect(t *testing.T, opts ...Option) (*mcp.ClientSession, context.Context) {
	t.Helper()
	ctx := context.Background()
	srv := New("test", opts...)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)

	st, ct := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, ctx
}

func TestIntegrationToolsAdvertised(t *testing.T) {
	cs, ctx := connect(t)
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tl := range res.Tools {
		got[tl.Name] = true
	}
	want := []string{
		"list_skills", "get_skill", "recon",
		"guide_read", "guide_write", "guide_delta",
		"trace_flow", "impact", "repo_map", "history", "context_pack", "deps", "schema", "routes", "stacks", "render_map",
		"dead_code", "explain_diff",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("tool %q not advertised", w)
		}
	}
	if len(res.Tools) != len(want) {
		t.Errorf("advertised %d tools, want %d: %v", len(res.Tools), len(want), got)
	}
}

func TestIntegrationResourcesAndPrompts(t *testing.T) {
	cs, ctx := connect(t)

	rl, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var hasWalkthrough bool
	for _, r := range rl.Resources {
		if r.URI == "onboard://skills/onboard-codebase-walkthrough" {
			hasWalkthrough = true
		}
	}
	if !hasWalkthrough {
		t.Error("onboard-codebase-walkthrough skill resource not advertised")
	}

	rr, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "onboard://skills/onboard-codebase-walkthrough"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rr.Contents) == 0 || !strings.Contains(rr.Contents[0].Text, "# Codebase Walkthrough") {
		t.Error("walkthrough resource body missing expected heading")
	}

	pl, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var onboard, onboardSkills *mcp.Prompt
	for _, p := range pl.Prompts {
		if p.Name == "onboard" {
			onboard = p
		}
		if p.Name == "onboard-skills" {
			onboardSkills = p
		}
	}
	if onboard == nil {
		t.Fatal("onboard prompt not advertised")
	}
	if onboardSkills == nil {
		t.Fatal("onboard-skills prompt not advertised")
	}
	// The tour advertises an optional `direction` argument so clients that collect
	// prompt arguments can preselect inside-out / outside-in.
	var hasDirectionArg bool
	for _, a := range onboard.Arguments {
		if a.Name == "direction" {
			hasDirectionArg = true
		}
	}
	if !hasDirectionArg {
		t.Error("onboard prompt does not advertise the direction argument")
	}

	gp, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "onboard"})
	if err != nil {
		t.Fatal(err)
	}
	// The tour composes two messages: the conductor protocol, then the unchanged
	// walkthrough skill as the analysis engine.
	if len(gp.Messages) < 2 {
		t.Fatalf("onboard prompt returned %d messages, want >= 2 (conductor + skill)", len(gp.Messages))
	}
	conductor := promptText(t, gp.Messages[0])
	// The conductor frames the tour and names its adaptive four-phase spine.
	for _, marker := range []string{"Guided tour", "choose a direction", "Orient", "Explore", "Wrap-up"} {
		if !strings.Contains(conductor, marker) {
			t.Errorf("conductor protocol missing expected marker %q", marker)
		}
	}
	engine := promptText(t, gp.Messages[1])
	if !strings.Contains(engine, "# Codebase Walkthrough") {
		t.Error("second message is not the walkthrough skill engine")
	}

	// A preselected direction is honored: the conductor is prefixed with a note to
	// skip the Step 0 question.
	gpDir, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "onboard",
		Arguments: map[string]string{"direction": "inside_out"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pre := promptText(t, gpDir.Messages[0]); !strings.Contains(pre, "Preselected direction: **inside-out**") {
		t.Errorf("preselected direction not normalized/honored, conductor began: %.80q", pre)
	}

	gpCatalog, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "onboard-skills"})
	if err != nil {
		t.Fatal(err)
	}
	if len(gpCatalog.Messages) != 1 {
		t.Fatalf("onboard-skills prompt returned %d messages, want 1", len(gpCatalog.Messages))
	}
	catalog := promptText(t, gpCatalog.Messages[0])
	for _, marker := range []string{"Onboard Skill Catalog", "onboard-codebase-walkthrough", "onboard-dependency-impact-analyzer"} {
		if !strings.Contains(catalog, marker) {
			t.Errorf("onboard-skills catalog missing %q", marker)
		}
	}
}

// promptText extracts the text of a prompt message, failing if it is not text.
func promptText(t *testing.T, m *mcp.PromptMessage) string {
	t.Helper()
	tc, ok := m.Content.(*mcp.TextContent)
	if !ok {
		t.Fatalf("prompt message content is %T, want *mcp.TextContent", m.Content)
	}
	return tc.Text
}

// callStructured calls a tool and decodes its structured output into v.
func callStructured(ctx context.Context, t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, v any) {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("call %s returned error: %v", name, res.Content)
	}
	if v != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatalf("decode %s output: %v", name, err)
		}
	}
}

func TestIntegrationToolCalls(t *testing.T) {
	cs, ctx := connect(t)
	root, _ := os.Getwd() // the server package dir — a real Go tree

	// recon
	var recon struct {
		Stack     []string `json:"stack"`
		FileCount int      `json:"file_count"`
	}
	callStructured(ctx, t, cs, "recon", map[string]any{"root": root}, &recon)
	if recon.FileCount == 0 {
		t.Error("recon found no files in the server package dir")
	}

	// list_skills
	var skills struct {
		Skills []struct {
			Name string `json:"name"`
		} `json:"skills"`
	}
	callStructured(ctx, t, cs, "list_skills", map[string]any{}, &skills)
	if len(skills.Skills) == 0 {
		t.Error("list_skills returned nothing")
	}

	// get_skill
	var skill struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	callStructured(ctx, t, cs, "get_skill", map[string]any{"name": "onboard-codebase-walkthrough"}, &skill)
	if !strings.Contains(skill.Content, "Phase 1") {
		t.Error("get_skill content missing expected text")
	}
	callStructured(ctx, t, cs, "get_skill", map[string]any{"name": "codebase-walkthrough"}, &skill)
	if skill.Name != "onboard-codebase-walkthrough" {
		t.Errorf("legacy get_skill alias returned %q", skill.Name)
	}

	// trace_flow + impact: minimal args (the omitempty fix path)
	var trace struct {
		Provider string `json:"provider"`
	}
	callStructured(ctx, t, cs, "trace_flow", map[string]any{"root": root, "entry": "New"}, &trace)
	if trace.Provider == "" {
		t.Error("trace_flow returned no provider")
	}

	var impact struct {
		Provider string `json:"provider"`
	}
	callStructured(ctx, t, cs, "impact", map[string]any{"root": root, "symbol": "New"}, &impact)

	// context_pack: seed on a known symbol; expect it (or its file's defs) bundled.
	var pack struct {
		Provider string `json:"provider"`
		Included int    `json:"included"`
		Pack     string `json:"pack"`
	}
	callStructured(ctx, t, cs, "context_pack", map[string]any{"root": root, "seed": "New"}, &pack)
	if pack.Included == 0 || !strings.Contains(pack.Pack, "func New") {
		t.Errorf("context_pack on New returned no usable bundle (included=%d):\n%s", pack.Included, pack.Pack)
	}

	// render_map (mermaid, minimal)
	var m struct {
		Content string `json:"content"`
	}
	callStructured(ctx, t, cs, "render_map", map[string]any{"root": root, "format": "mermaid"}, &m)
	if !strings.Contains(m.Content, "flowchart") {
		t.Error("render_map did not produce a flowchart")
	}
}

// Exercises the guide_write/read/delta MCP handlers (not just their advertisement)
// against a non-git temp root so it never touches a real repo's .git.
func TestIntegrationGuideTools(t *testing.T) {
	cs, ctx := connect(t)
	root := t.TempDir()

	var w struct {
		Path string `json:"path"`
	}
	callStructured(ctx, t, cs, "guide_write",
		map[string]any{"root": root, "body": "# Guide\n\nbody text", "mode": "full"}, &w)
	if w.Path == "" {
		t.Error("guide_write returned no path")
	}

	var r struct {
		Exists bool   `json:"exists"`
		Body   string `json:"body"`
	}
	callStructured(ctx, t, cs, "guide_read", map[string]any{"root": root}, &r)
	if !r.Exists || !strings.Contains(r.Body, "# Guide") {
		t.Errorf("guide_read did not round-trip: %+v", r)
	}

	// delta on a non-git root must degrade gracefully (a note), not error.
	var d struct {
		Note string `json:"note"`
	}
	callStructured(ctx, t, cs, "guide_delta", map[string]any{"root": root}, &d)
	if d.Note == "" {
		t.Error("guide_delta on a non-git root should return an explanatory note")
	}
}
