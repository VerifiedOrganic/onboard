package cmd

import "testing"

func TestInstallDryRunClaude(t *testing.T) {
	resetCLIState(t)
	rootCmd.SetArgs([]string{"install", "--agent", "claude", "--dry-run"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("install --dry-run: %v", err)
	}
}
