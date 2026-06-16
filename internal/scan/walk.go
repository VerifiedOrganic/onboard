package scan

import (
	"strings"

	"github.com/VerifiedOrganic/onboard/internal/ignore"
)

// ShouldSkipDir prunes the shared dependency/build directories plus dotdirs, but keeps
// .github (recon detects CI workflows there).
func ShouldSkipDir(name string) bool {
	if ignore.Dir(name) {
		return true
	}
	return strings.HasPrefix(name, ".") && name != ".github"
}
