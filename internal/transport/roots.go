package transport

import (
	"os"
	"strings"

	"github.com/VerifiedOrganic/onboard/internal/pathutil"
)

// RootPolicyFromEnv builds a root allowlist from ONBOARD_ALLOWED_ROOT.
// Comma-separated absolute paths are permitted; when unset, allowed is used as the sole permitted root.
func RootPolicyFromEnv(allowed string) pathutil.RootPolicy {
	if v := strings.TrimSpace(os.Getenv("ONBOARD_ALLOWED_ROOT")); v != "" {
		var roots []string
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				roots = append(roots, p)
			}
		}
		if len(roots) > 0 {
			return pathutil.NewRootPolicy(roots...)
		}
	}
	if allowed != "" {
		return pathutil.NewRootPolicy(allowed)
	}
	return pathutil.Unrestricted()
}
