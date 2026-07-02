// Package testenv gates tests on optional host toolchains while keeping them
// mandatory in CI jobs that provision those toolchains.
package testenv

import (
	"os"
	"testing"
)

// SkipUnlessTool skips the test unless ONBOARD_TEST_REQUIRE_TOOLCHAIN is set,
// in which case a missing toolchain is a failure, not a skip.
func SkipUnlessTool(t *testing.T, reason string) {
	t.Helper()
	if os.Getenv("ONBOARD_TEST_REQUIRE_TOOLCHAIN") != "" {
		t.Fatalf("toolchain required but unavailable: %s", reason)
	}
	t.Skip(reason)
}
