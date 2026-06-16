package server

import (
	"context"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type serverRuntime struct {
	deps *Deps
}

func newServerRuntime(opts ...Option) *serverRuntime {
	return &serverRuntime{deps: newDeps(opts...)}
}

func (r *serverRuntime) withDeps(ctx context.Context) context.Context {
	return contextWithDeps(ctx, r.deps)
}

// toolHandler runs an MCP tool body and records structured latency logs.
func toolHandler[T any, U any](rt *serverRuntime, name string, fn func(context.Context, T) (U, error)) func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, U, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in T) (*mcp.CallToolResult, U, error) {
		ctx = rt.withDeps(ctx)
		start := time.Now()
		out, err := fn(ctx, in)
		logTool(ctx, name, start, err)
		return nil, out, err
	}
}

// withToolLog wraps an MCP tool handler and records structured latency logs.
func withToolLog[T any, U any](rt *serverRuntime, name string, fn func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, U, error)) func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, U, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in T) (*mcp.CallToolResult, U, error) {
		ctx = rt.withDeps(ctx)
		start := time.Now()
		res, out, err := fn(ctx, req, in)
		logTool(ctx, name, start, err)
		return res, out, err
	}
}
