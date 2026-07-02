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
	Graph  GraphIndexer
	Git    GitPort
	Guide  GuidePort
	Roots  pathutil.RootPolicy
	Logger *slog.Logger
}

type depsContextKey struct{}

func defaultDeps() Deps {
	return Deps{
		Graph:  graph.DefaultService(),
		Git:    gitPort{},
		Guide:  guidePort{},
		Roots:  pathutil.Unrestricted(),
		Logger: slog.Default(),
	}
}

var baseDeps = defaultDeps()

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

func newDeps(opts ...Option) *Deps {
	d := defaultDeps()
	for _, o := range opts {
		o(&d)
	}
	return &d
}

func contextWithDeps(ctx context.Context, deps *Deps) context.Context {
	return context.WithValue(ctx, depsContextKey{}, deps)
}

func depsForContext(ctx context.Context) Deps {
	if deps, ok := ctx.Value(depsContextKey{}).(*Deps); ok && deps != nil {
		return *deps
	}

	return baseDeps
}

func resolveRoot(ctx context.Context, root string) (string, error) {
	return depsForContext(ctx).Roots.ResolveRoot(root)
}

func indexGraph(ctx context.Context, root string, refresh, precise bool) (*providers.Graph, error) {
	return depsForContext(ctx).Graph.Index(ctx, root, refresh, precise)
}

func logTool(ctx context.Context, name string, start time.Time, err error) {
	logger := depsForContext(ctx).Logger
	if logger == nil {
		return
	}
	attrs := []any{"tool", name, "duration_ms", time.Since(start).Milliseconds()}
	if err != nil {
		attrs = append(attrs, "err", err)
	}
	logger.InfoContext(ctx, "mcp tool", attrs...)
}
