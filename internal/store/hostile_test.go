package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// hostileCopy returns a valid database file after running extra SQL on it.
func hostileCopy(t *testing.T, extra ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "h.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureProject(context.Background(), "p", "/r", "global", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Seal(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, q := range extra {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInspectFileRejectsForeignSchemaObjects(t *testing.T) {
	ctx := context.Background()
	if _, err := InspectFile(ctx, hostileCopy(t)); err != nil {
		t.Fatalf("clean file rejected: %v", err)
	}
	for name, q := range map[string]string{
		"trigger": `CREATE TRIGGER evil AFTER INSERT ON sessions BEGIN DELETE FROM projects; END`,
		"view":    `CREATE VIEW v AS SELECT * FROM projects`,
		"table":   `CREATE TABLE extra (x)`,
	} {
		_, err := InspectFile(ctx, hostileCopy(t, q))
		if err == nil || !strings.Contains(err.Error(), "unexpected") {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
}

func TestInspectFileLeavesFileUnchanged(t *testing.T) {
	path := hostileCopy(t)
	if _, err := InspectFile(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("projects = %d, %v", n, err)
	}
}

// Column names of a dump come from the source file and are spliced into SQL.
func TestInsertRowRejectsHostileColumnName(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	err = insertRow(context.Background(), tx, "INSERT", "projects", Row{"id) VALUES ('x'); DELETE FROM projects; --": "a"})
	if err == nil {
		t.Fatal("hostile column name accepted")
	}
}
