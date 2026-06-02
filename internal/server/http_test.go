package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestHTTPTransport boots the server behind a Streamable HTTP handler and drives it
// with a real MCP client, proving the same server works over HTTP as over stdio.
func TestHTTPTransport(t *testing.T) {
	srv := New("test")
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("connect over HTTP: %v", err)
	}
	defer func() { _ = cs.Close() }()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	got := map[string]bool{}
	for _, tl := range tools.Tools {
		got[tl.Name] = true
	}
	for _, want := range []string{"recon", "trace_flow", "impact", "render_map", "get_skill"} {
		if !got[want] {
			t.Errorf("HTTP transport missing tool %q", want)
		}
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_skills",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call list_skills over HTTP: %v", err)
	}
	if res.IsError {
		t.Fatalf("list_skills returned error: %v", res.Content)
	}
}
