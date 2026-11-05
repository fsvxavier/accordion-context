package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/store"
)

// TestMCPCreatesSessionWhenProjectHasNone checks that an MCP server started in a
// project without sessions registers one, so ctx_status works for a chat that
// never went through the proxy. A second server reuses that session.
func TestMCPCreatesSessionWhenProjectHasNone(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(base, "data", "acd-ctx.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()

	m, err := NewMCP(MCPConfig{Store: st, DBPath: db, Cwd: root, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	sid, err := m.reader.latestSession(ctx, m.ProjectID())
	if err != nil {
		t.Fatalf("no session after MCP start: %v", err)
	}
	if !strings.HasPrefix(sid, "mcp-") {
		t.Fatalf("session %q, want an mcp- prefixed ID", sid)
	}
	row, err := st.GetSession(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	if row.Integration != "mcp" || row.Mode != "plugin" || row.ProjectID != m.ProjectID() {
		t.Fatalf("session row = %+v", row)
	}

	m2, err := NewMCP(MCPConfig{Store: st, DBPath: db, Cwd: root, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	again, err := m2.reader.latestSession(ctx, m2.ProjectID())
	if err != nil {
		t.Fatal(err)
	}
	if again != sid {
		t.Fatalf("second server created or chose %q, want the existing %q", again, sid)
	}
}
