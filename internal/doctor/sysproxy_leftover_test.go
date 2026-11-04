package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/sysproxy"
)

// A leftover state file from a dead run is a WARN that names the restore command.
// doctor reports it and changes nothing.
func TestSystemLeftoverIsWarn(t *testing.T) {
	db := filepath.Join(t.TempDir(), "acd-ctx.db")
	dir := sysproxy.StateDir(db)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := `{"owner":"acd-ctx","version":1,"mode":"system-proxy","backend":"gnome","pid":999999999,"created":"2026-01-01T00:00:00Z"}`
	p := filepath.Join(dir, "system-proxy-999999999.json")
	if err := os.WriteFile(p, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	c := checkSystemLeftovers(db)
	if c.Status != Warn {
		t.Fatalf("status = %s, want WARN: %s", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "attach --restore") || !strings.Contains(c.Detail, "stale") {
		t.Fatalf("detail = %q", c.Detail)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("doctor must not remove the state file: %v", err)
	}
}

func TestSystemLeftoverNonePasses(t *testing.T) {
	db := filepath.Join(t.TempDir(), "acd-ctx.db")
	if c := checkSystemLeftovers(db); c.Status != Pass {
		t.Fatalf("status = %s: %s", c.Status, c.Detail)
	}
}
