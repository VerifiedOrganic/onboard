package precision

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/providers"
)

func TestRustCallableQNamesReportsTruncation(t *testing.T) {
	g := &providers.Graph{Defs: map[string]*providers.Symbol{}}
	for i := 0; i < maxRustPreciseSyms+5; i++ {
		q := fmt.Sprintf("src/lib.rs::f%03d", i)
		g.Defs[q] = &providers.Symbol{QName: q, Name: fmt.Sprintf("f%03d", i), Kind: "function", File: "src/lib.rs", Lang: "rust"}
	}
	qnames, total, truncated := rustCallableQNames(g)
	if total != maxRustPreciseSyms+5 {
		t.Fatalf("total = %d, want %d", total, maxRustPreciseSyms+5)
	}
	if len(qnames) != maxRustPreciseSyms {
		t.Fatalf("queried = %d, want cap %d", len(qnames), maxRustPreciseSyms)
	}
	if !truncated {
		t.Fatal("expected Rust precision symbol selection to report truncation")
	}
}

func TestRustCallableQNamesPrioritizesCentralSymbols(t *testing.T) {
	g := &providers.Graph{
		Defs:    map[string]*providers.Symbol{},
		Forward: map[string][]string{},
		Reverse: map[string][]string{},
	}
	hot := "src/lib.rs::zz_hot"
	g.Defs[hot] = &providers.Symbol{QName: hot, Name: "zz_hot", Kind: "function", File: "src/lib.rs", Lang: "rust"}
	for i := 0; i < maxRustPreciseSyms+5; i++ {
		q := fmt.Sprintf("src/lib.rs::f%03d", i)
		g.Defs[q] = &providers.Symbol{QName: q, Name: fmt.Sprintf("f%03d", i), Kind: "function", File: "src/lib.rs", Lang: "rust"}
		if i < 20 {
			g.Forward[q] = []string{hot}
			g.Reverse[hot] = append(g.Reverse[hot], q)
		}
	}

	qnames, _, truncated := rustCallableQNames(g)
	if !truncated {
		t.Fatal("expected truncation")
	}
	found := false
	for _, q := range qnames {
		if q == hot {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("central Rust symbol %q should be selected ahead of lexically earlier low-impact symbols", hot)
	}
}

func TestUTF16ColumnForFile(t *testing.T) {
	root := t.TempDir()
	content := "é😀 run\n"
	p := filepath.Join(root, "src", "lib.rs")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	byteCol := len("é😀 ")
	got := utf16ColumnForFile(p, 1, byteCol)
	if got != 4 {
		t.Fatalf("UTF-16 column = %d, want 4", got)
	}
}
