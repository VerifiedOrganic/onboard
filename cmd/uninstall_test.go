package cmd

import (
	"strings"
	"testing"
)

func TestUninstallDryRunClaude(t *testing.T) {
	resetCLIState(t)
	rootCmd.SetArgs([]string{"uninstall", "--agent", "claude", "--dry-run"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("uninstall --dry-run: %v", err)
	}
}

func TestUninstallRequiresTarget(t *testing.T) {
	resetCLIState(t)
	rootCmd.SetArgs([]string{"uninstall"})
	err := rootCmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "specify") {
		t.Fatalf("err = %v, want specify flag error", err)
	}
}
