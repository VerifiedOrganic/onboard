package pathutil

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/VerifiedOrganic/onboard/internal/apperrors"
)

// RootPolicy constrains which directories MCP tools may index.
// An empty allowlist permits any directory (stdio mode default).
type RootPolicy struct {
	allowed []string
}

// NewRootPolicy returns a policy that only permits roots under the given absolute paths.
func NewRootPolicy(allowed ...string) RootPolicy {
	out := make([]string, 0, len(allowed))
	for _, a := range allowed {
		if a == "" {
			continue
		}
		if abs, err := filepath.Abs(a); err == nil {
			out = append(out, abs)
		}
	}
	return RootPolicy{allowed: out}
}

// Unrestricted returns a policy with no path constraints.
func Unrestricted() RootPolicy { return RootPolicy{} }

// Restricted reports whether the policy enforces an allowlist.
func (p RootPolicy) Restricted() bool { return len(p.allowed) > 0 }

// ResolveRoot resolves and optionally validates root against the allowlist.
func (p RootPolicy) ResolveRoot(root string) (string, error) {
	abs, err := ResolveRoot(root)
	if err != nil {
		return "", err
	}
	if !p.Restricted() {
		return abs, nil
	}
	for _, a := range p.allowed {
		if underRoot(a, abs) {
			return abs, nil
		}
	}
	return "", fmt.Errorf("%w: %q", apperrors.ErrRootNotAllowed, abs)
}

func underRoot(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
