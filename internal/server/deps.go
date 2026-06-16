package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/VerifiedOrganic/onboard/internal/graph"
	"github.com/VerifiedOrganic/onboard/internal/pathutil"
	"github.com/VerifiedOrganic/onboard/internal/providers"
)

// Deps holds injectable server dependencies.
type Deps struct {
	Graph  *graph.Service
	Git    GitPort
	Guide  GuidePort
	Roots  pathutil.RootPolicy
	Logger *slog.Logger
}

func defaultDeps() Deps {
	return Deps{
		Graph:  graph.DefaultService(),
		Git:    gitPort{},
		Guide:  guidePort{},
		Roots:  pathutil.Unrestricted(),
		Logger: slog.Default(),
	}
}

var serverDeps = defaultDeps()

// Option configures server dependencies.
type Option func(*Deps)

// WithRootPolicy sets the MCP root allowlist.
func WithRootPolicy(p pathutil.RootPolicy) Option {
	return func(d *Deps) { d.Roots = p }
}

// WithLogger sets structured logging for MCP tools.
func WithLogger(l *slog.Logger) Option {
	return func(d *Deps) { d.Logger = l }
}

// Configure applies options to the package-level dependency set.
func Configure(opts ...Option) {
	for _, o := range opts {
		o(&serverDeps)
	}
}

func resolveRoot(root string) (string, error) {
	return serverDeps.Roots.ResolveRoot(root)
}

func indexGraph(ctx context.Context, root string, refresh, precise bool) (*providers.Graph, error) {
	return serverDeps.Graph.Index(ctx, root, refresh, precise)
}

func logTool(name string, start time.Time, err error) {
	if serverDeps.Logger == nil {
		return
	}
	attrs := []any{"tool", name, "duration_ms", time.Since(start).Milliseconds()}
	if err != nil {
		attrs = append(attrs, "error", err.Error())
	}
	serverDeps.Logger.Info("mcp tool", attrs...)
}

func resetDeps() {
	serverDeps = defaultDeps()
}
