package cmd

import (
	"strings"
	"testing"
)

func TestResolveTargetsMutualExclusion(t *testing.T) {
	_, err := resolveTargets("claude", true, "onboard install --help")
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("err = %v, want mutual exclusion", err)
	}
}

func TestResolveTargetsRequiresFlag(t *testing.T) {
	_, err := resolveTargets("", false, "onboard install --help")
	if err == nil || !strings.Contains(err.Error(), "specify") {
		t.Fatalf("err = %v, want specify flag error", err)
	}
}

func TestResolveTargetsByName(t *testing.T) {
	targets, err := resolveTargets("claude", false, "onboard install --help")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Name != "claude" {
		t.Fatalf("targets = %+v, want single claude agent", targets)
	}
}
