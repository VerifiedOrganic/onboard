package server

import (
	"context"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/scan"
)

// routes extracts an HTTP API surface from common router-registration patterns across
// frameworks (Go chi/gin/echo/gorilla/net-http, Express, Flask, FastAPI). Unlike deps and
// schema, route registration is not a single grammar, so this is a PATTERN matcher, not a
// parser: it favors recall and is honest that it can miss bespoke routing and occasionally
// over-match. The result is a method+path+location list — the endpoint map a newcomer wants.

type routesInput struct {
	Root string `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
}

type routesOutput struct {
	Routes    []scan.Route `json:"routes"`
	Total     int          `json:"total"`
	Truncated bool         `json:"truncated,omitempty"`
	Note      string       `json:"note,omitempty"`
}

func routesExtract(_ context.Context, in routesInput) (routesOutput, error) {
	out := routesOutput{}
	root, err := resolveRoot(in.Root)
	if err != nil {
		return out, err
	}
	result := scan.ExtractRoutes(root)
	out.Routes = result.Routes
	out.Total = result.Total
	out.Truncated = result.Truncated
	out.Note = result.Note
	return out, nil
}

func registerRoutesTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "routes",
		Description: "Extract the HTTP API surface — method, path, and source location for each route — from common framework registration patterns (Go chi/gin/echo/gorilla/net-http, Express, Flask, FastAPI). A recall-oriented heuristic across frameworks, not a parser. Use to map a service's endpoints.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in routesInput) (*mcp.CallToolResult, routesOutput, error) {
		out, err := routesExtract(ctx, in)
		return nil, out, err
	})
}