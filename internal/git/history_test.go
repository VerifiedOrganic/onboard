package git

import (
	"context"
	"testing"
)

func TestHistoryAggregatesChurn(t *testing.T) {
	repo := initRepo(t) // one commit touching a.txt
	commit(t, repo, "a.txt", "two\n", "edit a")
	commit(t, repo, "a.txt", "three\n", "edit a again")
	commit(t, repo, "b.txt", "b\n", "add b")

	stats, err := History(context.Background(), repo, 0)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]FileStat{}
	for _, s := range stats {
		by[s.Path] = s
	}

	if by["a.txt"].Commits != 3 {
		t.Errorf("a.txt commits = %d, want 3", by["a.txt"].Commits)
	}
	if by["b.txt"].Commits != 1 {
		t.Errorf("b.txt commits = %d, want 1", by["b.txt"].Commits)
	}
	// Sorted by churn descending, so the hottest file is first.
	if len(stats) == 0 || stats[0].Path != "a.txt" {
		t.Errorf("expected a.txt ranked first by churn, got %v", stats)
	}
	if by["a.txt"].Authors != 1 {
		t.Errorf("a.txt authors = %d, want 1 (single test author)", by["a.txt"].Authors)
	}
	if by["a.txt"].LastDate == "" || len(by["a.txt"].LastDate) != 10 {
		t.Errorf("a.txt last_date = %q, want YYYY-MM-DD", by["a.txt"].LastDate)
	}
}

func TestRenameTarget(t *testing.T) {
	cases := map[string]string{
		"a.txt":                    "a.txt",
		"old.go => new.go":         "new.go",
		"src/{old => new}/file.go": "src/new/file.go",
		"pkg/{a/b => c}/x.go":      "pkg/c/x.go",
	}
	for in, want := range cases {
		if got := renameTarget(in); got != want {
			t.Errorf("renameTarget(%q) = %q, want %q", in, got, want)
		}
	}
}
