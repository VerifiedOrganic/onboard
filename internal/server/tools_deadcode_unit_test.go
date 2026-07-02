package server

import (
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/providers"
)

func TestDeadCodeIsEntryName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "empty", in: "", want: false},
		{name: "uppercase first", in: "Exported", want: false},
		{name: "lowercase first", in: "helper", want: false},
		{name: "underscore prefixed", in: "_hidden", want: false},
		{name: "non ascii lowercase", in: "über", want: false},
		{name: "main", in: "main", want: true},
		{name: "init", in: "init", want: true},
		{name: "test entry", in: "TestThing", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isEntryName(tt.in); got != tt.want {
				t.Fatalf("isEntryName(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDeadCodeExportedByCapitalization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "empty", in: "", want: false},
		{name: "uppercase first", in: "Exported", want: true},
		{name: "lowercase first", in: "helper", want: false},
		{name: "underscore prefixed", in: "_hidden", want: false},
		{name: "non ascii lowercase", in: "über", want: false},
		{name: "main", in: "main", want: false},
		{name: "init", in: "init", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := exportedByCapitalization(tt.in); got != tt.want {
				t.Fatalf("exportedByCapitalization(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDeadCodePrivateByConvention(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "empty", in: "", want: false},
		{name: "uppercase first", in: "Exported", want: false},
		{name: "lowercase first", in: "helper", want: false},
		{name: "underscore prefixed", in: "_hidden", want: true},
		{name: "non ascii lowercase", in: "über", want: false},
		{name: "main", in: "main", want: false},
		{name: "init", in: "init", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := privateByConvention(tt.in); got != tt.want {
				t.Fatalf("privateByConvention(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDeadCodeSymbolExported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sym  *providers.Symbol
		want bool
	}{
		{name: "nil symbol", sym: nil, want: false},
		// NOTE: possible defect, see GAP F-009: a zero-value non-nil symbol is currently treated as exported.
		{name: "zero value symbol", sym: &providers.Symbol{}, want: true},
		{name: "go uppercase first", sym: &providers.Symbol{Name: "Exported", Lang: "go"}, want: true},
		{name: "go lowercase first", sym: &providers.Symbol{Name: "helper", Lang: "go"}, want: false},
		{name: "go underscore prefixed", sym: &providers.Symbol{Name: "_hidden", Lang: "go"}, want: false},
		{name: "go non ascii lowercase", sym: &providers.Symbol{Name: "über", Lang: "go"}, want: false},
		{name: "go main", sym: &providers.Symbol{Name: "main", Lang: "go"}, want: false},
		{name: "go init", sym: &providers.Symbol{Name: "init", Lang: "go"}, want: false},
		{name: "rust public", sym: &providers.Symbol{Name: "helper", Lang: "rust", Public: true}, want: true},
		{name: "javascript lowercase", sym: &providers.Symbol{Name: "helper", Lang: "javascript"}, want: true},
		{name: "python underscore", sym: &providers.Symbol{Name: "_hidden", Lang: "python"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := symbolExported(tt.sym); got != tt.want {
				t.Fatalf("symbolExported(%+v) = %v, want %v", tt.sym, got, tt.want)
			}
		})
	}
}

func TestDeadCodeSymbolCallableKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sym  *providers.Symbol
		want string
	}{
		{name: "nil symbol", sym: nil, want: ""},
		{name: "zero value symbol", sym: &providers.Symbol{}, want: ""},
		{name: "uppercase function", sym: &providers.Symbol{Name: "Exported", Kind: "function"}, want: "function"},
		{name: "lowercase function", sym: &providers.Symbol{Name: "helper", Kind: "function"}, want: "function"},
		{name: "underscore function", sym: &providers.Symbol{Name: "_hidden", Kind: "function"}, want: "function"},
		{name: "non ascii lowercase function", sym: &providers.Symbol{Name: "über", Kind: "function"}, want: "function"},
		{name: "method kind", sym: &providers.Symbol{Name: "main", Kind: "method"}, want: "method"},
		{name: "receiver wins", sym: &providers.Symbol{Name: "init", Kind: "function", Recv: "T"}, want: "method"},
		{name: "rust function", sym: &providers.Symbol{Name: "helper", Kind: "function", Lang: "rust"}, want: "function"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := symbolCallableKind(tt.sym); got != tt.want {
				t.Fatalf("symbolCallableKind(%+v) = %q, want %q", tt.sym, got, tt.want)
			}
		})
	}
}

func TestDeadCodeOrphanConfidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		kind           string
		exported       bool
		precise        bool
		wantConfidence string
	}{
		{name: "empty private imprecise", kind: "", exported: false, precise: false, wantConfidence: "high"},
		{name: "function exported", kind: "function", exported: true, precise: false, wantConfidence: "medium"},
		{name: "function private precise", kind: "function", exported: false, precise: true, wantConfidence: "high"},
		{name: "method imprecise", kind: "method", exported: false, precise: false, wantConfidence: "low"},
		{name: "method precise", kind: "method", exported: false, precise: true, wantConfidence: "medium"},
		{name: "method exported precise", kind: "method", exported: true, precise: true, wantConfidence: "medium"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, reason := orphanConfidence(tt.kind, tt.exported, tt.precise)
			if got != tt.wantConfidence {
				t.Fatalf("orphanConfidence(%q, %v, %v) = %q (%s), want %q", tt.kind, tt.exported, tt.precise, got, reason, tt.wantConfidence)
			}
			if reason == "" {
				t.Fatal("orphanConfidence reason is empty")
			}
		})
	}
}

func TestDeadCodeIsGeneratedFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "empty", path: "", want: false},
		{name: "uppercase first", path: "Generated.go", want: true},
		{name: "lowercase first", path: "helper.go", want: false},
		{name: "underscore prefixed", path: "_hidden.go", want: false},
		{name: "non ascii lowercase", path: "über.go", want: false},
		{name: "main", path: "main.go", want: false},
		{name: "init", path: "init.go", want: false},
		{name: "protobuf", path: "service.pb.go", want: true},
		{name: "gen infix", path: "client.gen.ts", want: true},
		{name: "mock prefix", path: "mock_client.go", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isGeneratedFile(tt.path); got != tt.want {
				t.Fatalf("isGeneratedFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestDeadCodeIsStorybookFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "empty", path: "", want: false},
		{name: "uppercase first", path: "Button.stories.tsx", want: true},
		{name: "lowercase first", path: "button.story.jsx", want: true},
		{name: "underscore prefixed", path: "_hidden.stories.ts", want: true},
		{name: "non ascii lowercase", path: "über.story.ts", want: true},
		{name: "main", path: "main.ts", want: false},
		{name: "init", path: "init.ts", want: false},
		{name: "ordinary story word", path: "storyboard.ts", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isStorybookFile(tt.path); got != tt.want {
				t.Fatalf("isStorybookFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestDeadCodeIsAngularFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "empty", path: "", want: false},
		{name: "uppercase first", path: "User.component.ts", want: true},
		{name: "lowercase first", path: "user.service.ts", want: true},
		{name: "underscore prefixed", path: "_hidden.module.ts", want: true},
		{name: "non ascii lowercase", path: "über.pipe.ts", want: true},
		{name: "main", path: "main.ts", want: false},
		{name: "init", path: "init.ts", want: false},
		{name: "directive", path: "focus.directive.ts", want: true},
		{name: "ordinary file", path: "component.ts", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isAngularFile(tt.path); got != tt.want {
				t.Fatalf("isAngularFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
