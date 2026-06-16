package server

import (
	"context"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolHandler runs an MCP tool body and records structured latency logs.
func toolHandler[T any, U any](name string, fn func(context.Context, T) (U, error)) func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, U, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in T) (*mcp.CallToolResult, U, error) {
		start := time.Now()
		out, err := fn(ctx, in)
		logTool(name, start, err)
		return nil, out, err
	}
}

// toolHandlerNoCtx is for tools that do not accept context in their core handler.
func toolHandlerNoCtx[T any, U any](name string, fn func(T) (U, error)) func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, U, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, in T) (*mcp.CallToolResult, U, error) {
		start := time.Now()
		out, err := fn(in)
		logTool(name, start, err)
		return nil, out, err
	}
}

// withToolLog wraps an MCP tool handler and records structured latency logs.
func withToolLog[T any, U any](name string, fn func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, U, error)) func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, U, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in T) (*mcp.CallToolResult, U, error) {
		start := time.Now()
		res, out, err := fn(ctx, req, in)
		logTool(name, start, err)
		return res, out, err
	}
}
