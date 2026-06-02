package ignore

import "testing"

func TestDir(t *testing.T) {
	cases := map[string]bool{
		// dependency/build/cache dirs — skipped
		"node_modules": true,
		"vendor":       true,
		"dist":         true,
		"build":        true,
		"target":       true,
		"__pycache__":  true,
		"venv":         true,
		"coverage":     true,
		"bin":          true,
		"obj":          true,
		// real source dirs — kept
		"src":      false,
		"cmd":      false,
		"internal": false,
		"lib":      false,
		// dot-directories are NOT decided here (callers layer their own policy)
		".git":    false,
		".github": false,
		".venv":   false,
	}
	for name, want := range cases {
		if got := Dir(name); got != want {
			t.Errorf("Dir(%q) = %v, want %v", name, got, want)
		}
	}
}
