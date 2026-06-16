package server

import (
	"context"

	"github.com/VerifiedOrganic/onboard/internal/graph"
	"github.com/VerifiedOrganic/onboard/internal/providers"
)

// Deps holds injectable server dependencies. Tests may replace individual fields.
type Deps struct {
	Graph *graph.Service
}

func defaultDeps() Deps {
	return Deps{Graph: graph.DefaultService()}
}

var serverDeps = defaultDeps()

func indexGraph(ctx context.Context, root string, refresh, precise bool) (*providers.Graph, error) {
	return serverDeps.Graph.Index(ctx, root, refresh, precise)
}
