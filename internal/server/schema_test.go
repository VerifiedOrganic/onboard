package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/scan"
)

func schemaFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	ddl := `
CREATE TABLE users (
    id        SERIAL PRIMARY KEY,
    email     VARCHAR(255) NOT NULL UNIQUE,
    balance   DECIMAL(10,2) DEFAULT 0
);

CREATE TABLE IF NOT EXISTS "orders" (
    id       INTEGER PRIMARY KEY,
    user_id  INTEGER NOT NULL REFERENCES users (id),
    total    DECIMAL(10,2),
    CONSTRAINT fk_items FOREIGN KEY (item_id) REFERENCES items (id)
);
`
	p := filepath.Join(root, "migrations", "001_init.sql")
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(ddl), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func entityByName(out schemaOutput, name string) (scan.Entity, bool) {
	for _, e := range out.Entities {
		if e.Name == name {
			return e, true
		}
	}
	return scan.Entity{}, false
}

func colByName(e scan.Entity, name string) (scan.Column, bool) {
	for _, c := range e.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return scan.Column{}, false
}

func TestSchemaExtractsEntitiesAndKeys(t *testing.T) {
	root := schemaFixture(t)
	out, err := schemaExtract(context.Background(), schemaInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entities) != 2 {
		t.Fatalf("expected 2 entities, got %d: %+v", len(out.Entities), out.Entities)
	}

	users, ok := entityByName(out, "users")
	if !ok {
		t.Fatal("missing users entity")
	}
	if id, ok := colByName(users, "id"); !ok || !id.PK {
		t.Errorf("users.id should be a PK, got %+v", id)
	}
	if bal, ok := colByName(users, "balance"); !ok || !strings.HasPrefix(strings.ToUpper(bal.Type), "DECIMAL") {
		t.Errorf("users.balance type mis-parsed (top-level comma split?): %+v", bal)
	}
	if len(users.Columns) != 3 {
		t.Errorf("users should have 3 columns, got %d: %+v", len(users.Columns), users.Columns)
	}

	orders, ok := entityByName(out, "orders")
	if !ok {
		t.Fatal("missing orders entity (quoted identifier not cleaned?)")
	}
	if uid, ok := colByName(orders, "user_id"); !ok || !uid.FK {
		t.Errorf("orders.user_id should be an inline-REFERENCES FK, got %+v", uid)
	}
}

func TestSchemaRelationships(t *testing.T) {
	root := schemaFixture(t)
	out, err := schemaExtract(context.Background(), schemaInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	var sawUsers bool
	for _, r := range out.Relationships {
		if r.From == "orders" && r.To == "users" {
			sawUsers = true
		}
		if r.To == "items" {
			t.Errorf("relationship to unknown table 'items' should be filtered out: %+v", r)
		}
	}
	if !sawUsers {
		t.Errorf("expected an orders->users relationship; got %+v", out.Relationships)
	}
}

func TestSchemaMermaid(t *testing.T) {
	root := schemaFixture(t)
	out, err := schemaExtract(context.Background(), schemaInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Mermaid, "erDiagram") {
		t.Errorf("expected an erDiagram, got:\n%s", out.Mermaid)
	}
	if !strings.Contains(out.Mermaid, "users ||--o{ orders") {
		t.Errorf("erDiagram missing the users->orders relationship:\n%s", out.Mermaid)
	}
	if !strings.Contains(out.Mermaid, "PK") {
		t.Errorf("erDiagram should mark primary keys:\n%s", out.Mermaid)
	}
}

func TestSchemaEmptyRepo(t *testing.T) {
	out, err := schemaExtract(context.Background(), schemaInput{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entities) != 0 || out.Note == "" {
		t.Errorf("no .sql files should yield no entities with a note; got %d", len(out.Entities))
	}
}
