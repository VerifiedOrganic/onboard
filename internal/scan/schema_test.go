package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func entityByName(entities []Entity, name string) (Entity, bool) {
	for _, e := range entities {
		if e.Name == name {
			return e, true
		}
	}
	return Entity{}, false
}

func colByName(e Entity, name string) (Column, bool) {
	for _, c := range e.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return Column{}, false
}

func TestParseDDLExtractsEntitiesAndKeys(t *testing.T) {
	root := schemaFixture(t)
	data, err := os.ReadFile(filepath.Join(root, "migrations", "001_init.sql"))
	if err != nil {
		t.Fatal(err)
	}
	entities, rels := ParseDDL(string(data), "migrations/001_init.sql")
	if len(entities) != 2 {
		t.Fatalf("expected 2 entities, got %d: %+v", len(entities), entities)
	}

	users, ok := entityByName(entities, "users")
	if !ok {
		t.Fatal("missing users entity")
	}
	if id, ok := colByName(users, "id"); !ok || !id.PK {
		t.Errorf("users.id should be a PK, got %+v", id)
	}
	if bal, ok := colByName(users, "balance"); !ok || !strings.HasPrefix(strings.ToUpper(bal.Type), "DECIMAL") {
		t.Errorf("users.balance type mis-parsed: %+v", bal)
	}
	if len(users.Columns) != 3 {
		t.Errorf("users should have 3 columns, got %d: %+v", len(users.Columns), users.Columns)
	}

	orders, ok := entityByName(entities, "orders")
	if !ok {
		t.Fatal("missing orders entity")
	}
	if uid, ok := colByName(orders, "user_id"); !ok || !uid.FK {
		t.Errorf("orders.user_id should be an inline-REFERENCES FK, got %+v", uid)
	}

	known := map[string]bool{"users": true, "orders": true}
	filtered := FilterRelationships(rels, known)
	var sawUsers bool
	for _, r := range filtered {
		if r.From == "orders" && r.To == "users" {
			sawUsers = true
		}
		if r.To == "items" {
			t.Errorf("relationship to unknown table 'items' should be filtered out: %+v", r)
		}
	}
	if !sawUsers {
		t.Errorf("expected an orders->users relationship; got %+v", filtered)
	}
}

func TestRenderERD(t *testing.T) {
	root := schemaFixture(t)
	data, err := os.ReadFile(filepath.Join(root, "migrations", "001_init.sql"))
	if err != nil {
		t.Fatal(err)
	}
	entities, rels := ParseDDL(string(data), "migrations/001_init.sql")
	known := map[string]bool{}
	for _, e := range entities {
		known[e.Name] = true
	}
	rels = FilterRelationships(rels, known)
	mermaid := RenderERD(entities, rels)
	if !strings.HasPrefix(mermaid, "erDiagram") {
		t.Errorf("expected an erDiagram, got:\n%s", mermaid)
	}
	if !strings.Contains(mermaid, "users ||--o{ orders") {
		t.Errorf("erDiagram missing the users->orders relationship:\n%s", mermaid)
	}
	if !strings.Contains(mermaid, "PK") {
		t.Errorf("erDiagram should mark primary keys:\n%s", mermaid)
	}
}
