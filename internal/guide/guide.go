// Package guide manages the durable, SHA-tagged codebase guide cache. The guide
// body (the prose walkthrough) is produced by the model; this package owns the
// deterministic parts: where the guide lives, stamping a machine-readable header,
// and reporting whether the cache is current with HEAD.
package guide

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/VerifiedOrganic/onboard/internal/git"
)

const fileName = "codebase-walkthrough.md"

// Header is the machine-readable cache header at the top of the guide file.
type Header struct {
	SHA       string `json:"sha"`
	Branch    string `json:"branch"`
	Generated string `json:"generated"`
	Mode      string `json:"mode"` // full | delta
}

// Guide is a loaded (or absent) guide cache.
type Guide struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Header Header `json:"header"`
	Body   string `json:"body"`
}

// Path returns where the guide is stored: inside the git common dir when root is a
// git work tree (so it is never accidentally committed), else under <root>/.onboard.
func Path(root string) string {
	if dir, err := git.CommonDir(root); err == nil {
		return filepath.Join(dir, fileName)
	}
	return filepath.Join(root, ".onboard", fileName)
}

// Read loads the guide cache for root. A missing file is not an error (Exists=false).
func Read(root string) (Guide, error) {
	p := Path(root)
	g := Guide{Path: p}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return g, nil
		}
		return g, err
	}
	g.Exists = true
	g.Header, g.Body = parse(string(data))
	return g, nil
}

// Write stamps a header (sha=HEAD, branch, generated=now, mode) and writes the body.
// now is injected so callers/tests control the timestamp.
func Write(root, body, mode string, now time.Time) (string, error) {
	if mode != "full" && mode != "delta" {
		return "", fmt.Errorf("unsupported guide mode %q", mode)
	}
	p := Path(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	h := Header{Mode: mode, Generated: now.UTC().Format(time.RFC3339)}
	h.SHA, _ = git.HeadSHA(root) // empty if not a git repo; that's fine
	h.Branch, _ = git.Branch(root)
	content := format(h) + "\n\n" + strings.TrimLeft(body, "\n")
	tmp, err := os.CreateTemp(filepath.Dir(p), fileName+".*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, p); err != nil {
		return "", err
	}
	cleanup = false
	return p, nil
}

func format(h Header) string {
	return fmt.Sprintf("<!-- walkthrough-cache\nsha: %s\nbranch: %s\ngenerated: %s\nmode: %s\n-->",
		h.SHA, h.Branch, h.Generated, h.Mode)
}

func parse(content string) (Header, string) {
	var h Header
	if !strings.HasPrefix(content, "<!-- walkthrough-cache") {
		return h, content
	}
	end := strings.Index(content, "-->")
	if end < 0 {
		return h, content
	}
	block := content[:end]
	body := strings.TrimLeft(content[end+len("-->"):], "\n")
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "sha:"):
			h.SHA = strings.TrimSpace(strings.TrimPrefix(line, "sha:"))
		case strings.HasPrefix(line, "branch:"):
			h.Branch = strings.TrimSpace(strings.TrimPrefix(line, "branch:"))
		case strings.HasPrefix(line, "generated:"):
			h.Generated = strings.TrimSpace(strings.TrimPrefix(line, "generated:"))
		case strings.HasPrefix(line, "mode:"):
			h.Mode = strings.TrimSpace(strings.TrimPrefix(line, "mode:"))
		}
	}
	return h, body
}
