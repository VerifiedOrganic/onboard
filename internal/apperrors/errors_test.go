package apperrors_test

import (
	"fmt"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/apperrors"
)

func TestIsMatchesWrappedSentinel(t *testing.T) {
	err := fmt.Errorf("resolve root: %w", apperrors.ErrRootNotDirectory)
	if !apperrors.Is(err, apperrors.ErrRootNotDirectory) {
		t.Fatalf("Is = false, want true for %v", err)
	}
	if apperrors.Is(err, apperrors.ErrRootNotAllowed) {
		t.Fatal("unexpected match for different sentinel")
	}
}
