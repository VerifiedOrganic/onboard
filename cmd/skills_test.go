package cmd

import (
	"strings"
	"testing"
)

func TestSkillsCommandRuns(t *testing.T) {
	resetCLIState(t)
	rootCmd.SetArgs([]string{"skills"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("skills: %v", err)
	}
}

func TestTruncateShortStringUnchanged(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Fatalf("truncate = %q, want hello", got)
	}
}

func TestTruncateLongStringEllipsis(t *testing.T) {
	got := truncate(strings.Repeat("a", 20), 10)
	if len([]rune(got)) != 11 || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncate = %q, want 10 runes plus ellipsis", got)
	}
}
