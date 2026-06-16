package graph

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/VerifiedOrganic/onboard/internal/git"
	"github.com/VerifiedOrganic/onboard/internal/indexer"
	"github.com/VerifiedOrganic/onboard/internal/precision"
	"github.com/VerifiedOrganic/onboard/internal/providers"
)

const graphIndexTimeout = 10 * time.Minute

// Service indexes repositories with in-memory caching and optional semantic enrichment.
type Service struct {
	cache  *Cache
	group  singleflight.Group
	logger *slog.Logger
}

// DefaultService returns a Service with a fresh cache and stderr logging.
func DefaultService() *Service {
	return &Service{
		cache:  NewCache(),
		logger: slog.Default(),
	}
}

// Index builds or returns a cached code graph for root. precise requests type-checked enrichment.
func (s *Service) Index(ctx context.Context, root string, refresh, precise bool) (*providers.Graph, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	key := root
	if precise {
		key = root + "\x00precise"
	}
	if !refresh {
		if g, ok := s.cache.Get(key); ok {
			if s.logger != nil {
				s.logger.Debug("graph cache hit", "root", root, "precise", precise)
			}
			return g, nil
		}
	}

	start := time.Now()
	ch := s.group.DoChan(key, func() (any, error) {
		buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), graphIndexTimeout)
		defer cancel()
		if !refresh {
			if g, ok := s.cache.Get(key); ok {
				return g, nil
			}
		}
		g, err := (indexer.Builtin{}).IndexWithCache(buildCtx, root, diskCachePath(buildCtx, root))
		if err != nil {
			return nil, err
		}
		if g.Files == 0 {
			if ng, nerr := (indexer.Null{}).Index(buildCtx, root); nerr == nil && len(ng.Defs) > 0 {
				g = ng
			}
		}
		if precise {
			if _, err := precision.EnrichGo(buildCtx, root, g); err != nil && s.logger != nil {
				s.logger.Warn("go precision enrichment failed", "root", root, "err", err)
			}
			if _, err := precision.EnrichRust(buildCtx, root, g); err != nil && s.logger != nil {
				s.logger.Warn("rust precision enrichment failed", "root", root, "err", err)
			}
		}
		s.cache.Store(key, g)
		return g, nil
	})

	var res singleflight.Result
	select {
	case res = <-ch:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if res.Err != nil {
		return nil, res.Err
	}
	g := res.Val.(*providers.Graph)
	if s.logger != nil {
		s.logger.Info("graph indexed",
			"root", root,
			"precise", precise,
			"provider", g.Provider,
			"defs", len(g.Defs),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}
	return g, nil
}

func diskCachePath(ctx context.Context, root string) string {
	dir, err := git.CommonDir(ctx, root)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "onboard-graph.json")
}
