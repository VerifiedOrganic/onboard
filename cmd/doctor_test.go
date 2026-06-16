package cmd

import (
	"strings"
	"testing"
)

func TestDoctorUnknownAgentFails(t *testing.T) {
	resetCLIState(t)
	rootCmd.SetArgs([]string{"doctor", "--agent", "not-a-real-agent"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
	if !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("err = %v, want unknown agent message", err)
	}
}

func TestDoctorRunsWithoutAgentFlag(t *testing.T) {
	resetCLIState(t)
	rootCmd.SetArgs([]string{"doctor"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor: %v", err)
	}
}
