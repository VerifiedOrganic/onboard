// Package git provides the minimal git queries the guide cache needs. It shells
// out to the git binary; callers should check Available first, as every function
// returns an error when git or a repository is absent.
package git

import (
	"archive/tar"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/VerifiedOrganic/onboard/internal/apperrors"
)

const gitCommandTimeout = 30 * time.Second
const (
	maxArchiveFileBytes  int64 = 100 << 20
	maxArchiveTotalBytes int64 = 500 << 20
)

// Available reports whether git is on PATH and root is inside a work tree.
func Available(root string) bool {
	if _, err := exec.LookPath("git"); err != nil {
		return false
	}
	out, err := run(context.Background(), root, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// CommonDir returns the absolute path of the repo's common git directory.
// (Using the *common* dir keeps caches stable across worktrees.)
func CommonDir(root string) (string, error) {
	out, err := run(context.Background(), root, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(out)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filepath.Abs(dir)
}

// HeadSHA returns the full commit SHA of HEAD.
func HeadSHA(root string) (string, error) {
	out, err := run(context.Background(), root, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// Branch returns the current branch name (or "HEAD" when detached).
func Branch(root string) (string, error) {
	out, err := run(context.Background(), root, "rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(out), err
}

// Change is one entry from `git diff --name-status`.
type Change struct {
	Status  string `json:"status"` // A, M, D, Rxxx, ...
	Path    string `json:"path"`   // current/new path
	OldPath string `json:"old_path,omitempty"`
}

// DiffNameStatus returns the files changed from fromSHA to HEAD.
func DiffNameStatus(ctx context.Context, root, fromSHA string) ([]Change, error) {
	out, err := run(ctx, root, "diff", "--name-status", "-z", fromSHA+"..HEAD")
	if err != nil {
		return nil, err
	}
	return parseNameStatusZ(out), nil
}

func parseNameStatusZ(out string) []Change {
	var changes []Change
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); {
		status := fields[i]
		i++
		if status == "" {
			continue
		}
		if i >= len(fields) {
			break
		}
		path := fields[i]
		i++
		oldPath := ""
		// Rename/copy records are status, old path, new path. Keep both.
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			oldPath = path
			if i >= len(fields) {
				break
			}
			path = fields[i]
			i++
		}
		changes = append(changes, Change{Status: status, Path: path, OldPath: oldPath})
	}
	return changes
}

// Hunk is a contiguous range of new-side line numbers touched by a diff (inclusive).
type Hunk struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// FileDiff is the change to one file between two states: its status and the new-side line
// ranges touched. Hunks is empty for a pure deletion.
type FileDiff struct {
	Path   string `json:"path"`
	Status string `json:"status"` // A, M, D, R
	Hunks  []Hunk `json:"-"`
}

// ValidateRef checks that ref is safe to pass to git and resolves to a commit.
func ValidateRef(root, ref string) error {
	if ref == "" {
		return fmt.Errorf("%w: empty ref", apperrors.ErrInvalidGitRef)
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("%w: %q looks like a flag", apperrors.ErrInvalidGitRef, ref)
	}
	if strings.Contains(ref, "\x00") {
		return fmt.Errorf("%w: null byte in ref", apperrors.ErrInvalidGitRef)
	}
	if !RefExists(root, ref) {
		return fmt.Errorf("%w: %q does not resolve to a commit", apperrors.ErrInvalidGitRef, ref)
	}
	return nil
}

// Diff returns the per-file changes between base and the working tree — committed *and*
// uncommitted work since base, for tracked files (untracked files are not shown by git
// diff). unified=0 keeps the hunks tight so they attribute to symbols precisely.
func Diff(ctx context.Context, root, base string) ([]FileDiff, error) {
	if err := ValidateRef(root, base); err != nil {
		return nil, err
	}
	out, err := run(ctx, root, "diff", "--unified=0", "--no-color", "--find-renames", base, "--")
	if err != nil {
		return nil, err
	}
	return parseUnifiedDiff(out), nil
}

// ArchiveTree materializes ref into dst using `git archive`. It is intended for read-only
// analysis of base-side state, such as computing the blast radius of deleted symbols.
func ArchiveTree(ctx context.Context, root, ref, dst string) error {
	if err := ValidateRef(root, ref); err != nil {
		return err
	}
	// #nosec G204 -- git is the fixed executable and arguments are not shell-expanded.
	cmd := exec.CommandContext(ctx, "git", "-C", root, "archive", "--format=tar", ref)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	tr := tar.NewReader(stdout)
	var totalBytes int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = cmd.Wait()
			return err
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if name == "." || name == ".." || filepath.IsAbs(name) || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			_ = cmd.Wait()
			return fmt.Errorf("git archive contained unsafe path %q", hdr.Name)
		}
		target := filepath.Join(dst, name)
		rel, err := filepath.Rel(dst, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			_ = cmd.Wait()
			return fmt.Errorf("git archive path %q escapes destination", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				_ = cmd.Wait()
				return err
			}
		case tar.TypeReg:
			if hdr.Size < 0 || hdr.Size > maxArchiveFileBytes {
				_ = cmd.Wait()
				return fmt.Errorf("git archive file %q has unsupported size %d", hdr.Name, hdr.Size)
			}
			totalBytes += hdr.Size
			if totalBytes > maxArchiveTotalBytes {
				_ = cmd.Wait()
				return fmt.Errorf("git archive exceeds maximum extracted size %d", maxArchiveTotalBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				_ = cmd.Wait()
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, tarFilePerm(hdr.Mode))
			if err != nil {
				_ = cmd.Wait()
				return err
			}
			// #nosec G110 -- git archive extraction is bounded by per-file and total byte limits above.
			_, copyErr := io.CopyN(f, tr, hdr.Size)
			closeErr := f.Close()
			if copyErr != nil {
				_ = cmd.Wait()
				return copyErr
			}
			if closeErr != nil {
				_ = cmd.Wait()
				return closeErr
			}
		}
	}
	return cmd.Wait()
}

func tarFilePerm(mode int64) os.FileMode {
	if mode < 0 {
		return 0o600
	}
	perm := mode & 0o777
	// #nosec G115 -- perm is masked to Unix permission bits before conversion.
	return os.FileMode(uint32(perm))
}

// parseUnifiedDiff parses `git diff --unified=0` output into per-file changes. It is a
// pure function (no git invocation) so the parsing is unit-testable on canned input.
func parseUnifiedDiff(out string) []FileDiff {
	var files []FileDiff
	var cur *FileDiff
	flush := func() {
		if cur != nil {
			files = append(files, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			cur = &FileDiff{Status: "M", Path: diffGitBPath(line)}
		case cur == nil:
			// preamble before the first file header — ignore
		case strings.HasPrefix(line, "new file"):
			cur.Status = "A"
		case strings.HasPrefix(line, "deleted file"):
			cur.Status = "D"
		case strings.HasPrefix(line, "rename to "):
			cur.Status = "R"
			cur.Path = strings.TrimSpace(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "+++ "):
			if p := plusPath(line); p != "" { // authoritative new-side path (unless /dev/null)
				cur.Path = p
			}
		case strings.HasPrefix(line, "@@"):
			if h, ok := parseHunkHeader(line); ok {
				cur.Hunks = append(cur.Hunks, h)
			}
		}
	}
	flush()
	return files
}

// diffGitBPath extracts the new-side path from a "diff --git a/old b/new" header.
func diffGitBPath(line string) string {
	if i := strings.Index(line, " b/"); i >= 0 {
		return strings.TrimSpace(line[i+3:])
	}
	return ""
}

// plusPath extracts the path from a "+++ b/path" diff header, or "" for /dev/null.
func plusPath(line string) string {
	p := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
	if p == "/dev/null" {
		return ""
	}
	p = strings.TrimPrefix(p, "b/")
	return p
}

// parseHunkHeader reads the new-side range from a "@@ -a,b +c,d @@" hunk header.
func parseHunkHeader(line string) (Hunk, bool) {
	plus := strings.IndexByte(line, '+')
	if plus < 0 {
		return Hunk{}, false
	}
	tok := line[plus+1:]
	if sp := strings.IndexByte(tok, ' '); sp >= 0 {
		tok = tok[:sp]
	}
	start, count := 0, 1
	if comma := strings.IndexByte(tok, ','); comma >= 0 {
		start, _ = strconv.Atoi(tok[:comma])
		count, _ = strconv.Atoi(tok[comma+1:])
	} else {
		start, _ = strconv.Atoi(tok)
	}
	if start == 0 { // a 0,0 new-side means the change is a pure deletion — no new lines
		return Hunk{}, false
	}
	if count <= 0 {
		count = 1
	}
	return Hunk{Start: start, End: start + count - 1}, true
}

// RefExists reports whether ref resolves to a commit.
func RefExists(root, ref string) bool {
	_, err := run(context.Background(), root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// MergeBase returns the best common ancestor of HEAD and ref, or "" with an error.
func MergeBase(root, ref string) (string, error) {
	out, err := run(context.Background(), root, "merge-base", "HEAD", ref)
	return strings.TrimSpace(out), err
}

// DefaultBase picks a sensible review base: the merge-base of HEAD with the first of
// origin/main, main, origin/master, master that exists. Returns "" if none resolve (e.g.
// a repo with no default branch), leaving the caller to ask for an explicit base.
func DefaultBase(root string) string {
	for _, ref := range []string{"origin/main", "main", "origin/master", "master"} {
		if RefExists(root, ref) {
			if mb, err := MergeBase(root, ref); err == nil && mb != "" {
				return mb
			}
		}
	}
	return ""
}

// FileStat is the aggregated change history of one file: how often it changes (churn),
// how much, when it last changed, and how many distinct authors have touched it.
type FileStat struct {
	Path      string `json:"path"`
	Commits   int    `json:"commits"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	LastDate  string `json:"last_date"` // YYYY-MM-DD of the most recent change
	Authors   int    `json:"authors"`   // distinct author count
}

// History aggregates per-file change statistics from `git log --numstat` over the most
// recent maxCommits commits (0 = all history). Merge commits are excluded. The result
// is sorted by churn (commit count) descending, then path. High-churn, multi-author
// files are onboarding hotspots and prime risk-audit targets.
func History(ctx context.Context, root string, maxCommits int) ([]FileStat, error) {
	// \x1f-delimited header per commit, then numstat lines: "<adds>\t<dels>\t<path>".
	args := []string{"log", "--no-merges", "--numstat", "--format=\x1f%an\x1f%aI"}
	if maxCommits > 0 {
		args = append(args, fmt.Sprintf("-n%d", maxCommits))
	}
	out, err := run(ctx, root, args...)
	if err != nil {
		return nil, err
	}

	type agg struct {
		commits, adds, dels int
		last                string
		authors             map[string]bool
	}
	stats := map[string]*agg{}
	var author, date string
	touched := map[string]bool{} // files seen in the current commit (count it once)

	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "\x1f") {
			parts := strings.Split(line, "\x1f")
			if len(parts) >= 3 {
				author, date = parts[1], parts[2]
			}
			touched = map[string]bool{}
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		path := renameTarget(fields[2])
		a := stats[path]
		if a == nil {
			a = &agg{authors: map[string]bool{}}
			stats[path] = a
		}
		adds, _ := strconv.Atoi(fields[0]) // "-" (binary) parses to 0
		dels, _ := strconv.Atoi(fields[1])
		a.adds += adds
		a.dels += dels
		if !touched[path] {
			a.commits++
			touched[path] = true
		}
		if author != "" {
			a.authors[author] = true
		}
		if a.last == "" { // log is newest-first, so the first sighting is most recent
			a.last = date
		}
	}

	result := make([]FileStat, 0, len(stats))
	for p, a := range stats {
		last := a.last
		if len(last) > 10 {
			last = last[:10]
		}
		result = append(result, FileStat{
			Path: p, Commits: a.commits, Additions: a.adds,
			Deletions: a.dels, LastDate: last, Authors: len(a.authors),
		})
	}
	slices.SortFunc(result, func(a, b FileStat) int {
		if c := cmp.Compare(b.Commits, a.Commits); c != 0 {
			return c
		}
		return cmp.Compare(a.Path, b.Path)
	})
	return result, nil
}

// renameTarget extracts the destination path from a git numstat rename entry, handling
// both the "old => new" and the "dir/{old => new}/file" brace forms.
func renameTarget(p string) string {
	if !strings.Contains(p, "=>") {
		return p
	}
	if i := strings.Index(p, "{"); i >= 0 {
		if j := strings.Index(p, "}"); j > i {
			inner := p[i+1 : j]
			if k := strings.Index(inner, "=>"); k >= 0 {
				inner = inner[k+2:]
			}
			return strings.ReplaceAll(p[:i]+strings.TrimSpace(inner)+p[j+1:], "//", "/")
		}
	}
	if k := strings.Index(p, "=>"); k >= 0 {
		return strings.TrimSpace(p[k+2:])
	}
	return p
}

func run(ctx context.Context, root string, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, gitCommandTimeout)
	defer cancel()
	// #nosec G204 -- git is the fixed executable and arguments are not shell-expanded.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return string(out), fmt.Errorf("git %s timed out after %s: %w", strings.Join(args, " "), gitCommandTimeout, ctx.Err())
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return string(out), ctx.Err()
	}
	if err != nil {
		combined := strings.TrimSpace(string(out) + " " + err.Error())
		if strings.Contains(combined, "not a git repository") {
			return string(out), fmt.Errorf("%w: %w", apperrors.ErrNotGitRepository, err)
		}
		return string(out), fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}
