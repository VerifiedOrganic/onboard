package scan

import "testing"

func TestShouldSkipDir(t *testing.T) {
	cases := map[string]bool{
		"node_modules": true,
		"vendor":       true,
		".git":         true,
		".idea":        true,
		".github":      false,
		"src":          false,
		"cmd":          false,
	}
	for name, want := range cases {
		if got := ShouldSkipDir(name); got != want {
			t.Errorf("ShouldSkipDir(%q) = %v, want %v", name, got, want)
		}
	}
}
