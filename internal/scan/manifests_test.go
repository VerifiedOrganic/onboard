package scan

import "testing"

func TestSplitRequirement(t *testing.T) {
	cases := map[string][2]string{
		"Django>=4.2":                    {"Django", ">=4.2"},
		"requests":                       {"requests", ""},
		"numpy==1.26.0":                  {"numpy", "==1.26.0"},
		"requests[security]":             {"requests", ""},
		"requests[security]>=2":          {"requests", ">=2"},
		"pkg>=1.0; python_version<'3.9'": {"pkg", ">=1.0"},
		"pkg[extra] @ https://example.invalid/pkg.whl#sha256=abc": {"pkg", "@ https://example.invalid/pkg.whl#sha256=abc"},
	}
	for in, want := range cases {
		gotName, gotVer := SplitRequirement(in)
		if gotName != want[0] || gotVer != want[1] {
			t.Errorf("SplitRequirement(%q) = (%q,%q), want (%q,%q)", in, gotName, gotVer, want[0], want[1])
		}
	}
}