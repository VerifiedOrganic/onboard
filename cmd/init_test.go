package cmd

import "testing"

func TestInitDryRunSucceeds(t *testing.T) {
	resetCLIState(t)
	rootCmd.SetArgs([]string{"init", "--dry-run"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init --dry-run: %v", err)
	}
}
