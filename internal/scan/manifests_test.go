package scan

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

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

func TestDetectWorkspacesAndToolsMonorepoFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		want string
	}{
		{name: "nx", file: "nx.json", want: "nx"},
		{name: "turborepo", file: "turbo.json", want: "turborepo"},
		{name: "lerna", file: "lerna.json", want: "lerna"},
		{name: "bazel workspace", file: "WORKSPACE.bazel", want: "bazel"},
		{name: "bazel module", file: "MODULE.bazel", want: "bazel"},
		{name: "bazel legacy workspace", file: "WORKSPACE", want: "bazel"},
		{name: "helm", file: "Chart.yaml", want: "helm chart"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tt.file), []byte("{}\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			got := detectWorkspacesAndTools(root, "package.json", rawPackageJSON{}, nil)
			if !slices.Contains(got, tt.want) {
				t.Fatalf("tools = %v, want %q", got, tt.want)
			}
		})
	}
}
