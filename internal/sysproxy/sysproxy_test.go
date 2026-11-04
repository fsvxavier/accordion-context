package sysproxy

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"
)

var testEP = Endpoint{Host: "127.0.0.1", Port: 45123, Bypass: DefaultBypass}

// wantGnomeStart is the exact write sequence for testEP on GNOME.
var wantGnomeStart = []string{
	"gsettings set org.gnome.system.proxy mode 'manual'",
	"gsettings set org.gnome.system.proxy ignore-hosts ['localhost', '127.0.0.0/8', '::1']",
	"gsettings set org.gnome.system.proxy.https host '127.0.0.1'",
	"gsettings set org.gnome.system.proxy.https port 45123",
}

// wantGnomeReads is the read sequence (the snapshot, and the verify after the write).
var wantGnomeReads = []string{
	"gsettings get org.gnome.system.proxy mode",
	"gsettings get org.gnome.system.proxy ignore-hosts",
	"gsettings get org.gnome.system.proxy.https host",
	"gsettings get org.gnome.system.proxy.https port",
}

var wantGnomeRestore = []string{
	"gsettings set org.gnome.system.proxy mode 'none'",
	"gsettings set org.gnome.system.proxy ignore-hosts ['localhost']",
	"gsettings set org.gnome.system.proxy.https host ''",
	"gsettings set org.gnome.system.proxy.https port 0",
}

func concat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestGnomeRoundTripArgv(t *testing.T) {
	dir := t.TempDir()
	mem := &memGSettings{vals: gnomeStart()}
	rec := &recorder{inner: mem}
	ctx := context.Background()

	a, err := StartSystemProxy(ctx, gnomeEnv(rec, dir), dir, testEP)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	wantStart := concat(wantGnomeReads, wantGnomeStart, wantGnomeReads)
	if got := rec.argvs(); !reflect.DeepEqual(got, wantStart) {
		t.Fatalf("start argv\n got %q\nwant %q", got, wantStart)
	}
	if mem.vals["org.gnome.system.proxy.https port"] != "45123" {
		t.Fatalf("setting not applied: %v", mem.vals)
	}

	// The state file is 0600 and names the owner.
	paths, _ := listStates(dir)
	if len(paths) != 1 {
		t.Fatalf("want one state file, got %v", paths)
	}
	fi, err := os.Stat(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode %v, want 0600", fi.Mode().Perm())
	}

	rec.reset()
	notes, err := a.Stop(ctx)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	// Restore works key by key: read the applied value, write the previous one, verify.
	var wantStop []string
	for i := range wantGnomeRestore {
		wantStop = append(wantStop, wantGnomeReads[i], wantGnomeRestore[i], wantGnomeReads[i])
	}
	if got := rec.argvs(); !reflect.DeepEqual(got, wantStop) {
		t.Fatalf("stop argv\n got %q\nwant %q", got, wantStop)
	}
	if !reflect.DeepEqual(mem.vals, gnomeStart()) {
		t.Fatalf("values not restored: %v", mem.vals)
	}
	if len(notes) != 4 {
		t.Fatalf("notes = %v", notes)
	}
	if _, err := os.Stat(paths[0]); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("state file kept after verified restore: %v", err)
	}
	// Stop is idempotent.
	rec.reset()
	if _, err := a.Stop(ctx); err != nil || len(rec.argvs()) != 0 {
		t.Fatalf("second stop ran commands or failed: %v %v", err, rec.argvs())
	}
}

func TestForeignChangeIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	mem := &memGSettings{vals: gnomeStart()}
	rec := &recorder{inner: mem}
	ctx := context.Background()
	a, err := StartSystemProxy(ctx, gnomeEnv(rec, dir), dir, testEP)
	if err != nil {
		t.Fatal(err)
	}
	// Another program moves the proxy to a different port.
	mem.vals["org.gnome.system.proxy.https port"] = "9999"
	rec.reset()
	notes, err := a.Stop(ctx)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if mem.vals["org.gnome.system.proxy.https port"] != "9999" {
		t.Fatalf("foreign value was overwritten: %v", mem.vals["org.gnome.system.proxy.https port"])
	}
	found := false
	for _, n := range notes {
		if strings.Contains(n, "https.port") || strings.Contains(n, "https port") {
			found = true
		}
	}
	if !found && !containsNote(notes, "changed by another program") {
		t.Fatalf("no note about the foreign change: %v", notes)
	}
	// The other keys are still restored.
	if mem.vals["org.gnome.system.proxy mode"] != "'none'" {
		t.Fatalf("mode not restored: %v", mem.vals["org.gnome.system.proxy mode"])
	}
}

func containsNote(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func TestFailedWriteRollsBack(t *testing.T) {
	dir := t.TempDir()
	mem := &memGSettings{vals: gnomeStart(), failSet: "org.gnome.system.proxy.https port"}
	rec := &recorder{inner: mem}
	ctx := context.Background()
	_, err := StartSystemProxy(ctx, gnomeEnv(rec, dir), dir, testEP)
	if err == nil {
		t.Fatal("start succeeded despite a failed write")
	}
	if !reflect.DeepEqual(mem.vals, gnomeStart()) {
		t.Fatalf("rollback left values changed: %v", mem.vals)
	}
	if paths, _ := listStates(dir); len(paths) != 0 {
		t.Fatalf("state file kept after a clean rollback: %v", paths)
	}
}

func TestStartRefusesWithoutRunner(t *testing.T) {
	e := gnomeEnv(nil, t.TempDir())
	e.Runner = nil
	if _, err := StartSystemProxy(context.Background(), e, t.TempDir(), testEP); err == nil {
		t.Fatal("start without a runner must fail")
	}
}

func TestEndpointValidation(t *testing.T) {
	bad := []Endpoint{
		{Host: "", Port: 1},
		{Host: "127.0.0.1", Port: 0},
		{Host: "127.0.0.1'; rm", Port: 1},
		{Host: "127.0.0.1", Port: 1, Bypass: []string{"a,b"}},
	}
	for _, ep := range bad {
		if err := ep.Validate(); err == nil {
			t.Fatalf("Validate(%+v) accepted", ep)
		}
	}
	if err := testEP.Validate(); err != nil {
		t.Fatal(err)
	}
}

// A leftover state file blocks a new start: its snapshot would hold a crashed
// run's values as the "previous" ones.
func TestStartRefusesLeftoverState(t *testing.T) {
	dir := t.TempDir()
	if _, err := createState(dir, &State{Mode: ModeSystemProxy, Backend: "gnome", PID: 31337}); err != nil {
		t.Fatal(err)
	}
	mem := &memGSettings{vals: gnomeStart()}
	rec := &recorder{inner: mem}
	_, err := StartSystemProxy(context.Background(), gnomeEnv(rec, dir), dir, testEP)
	if err == nil || !strings.Contains(err.Error(), "--restore") {
		t.Fatalf("start with leftover state: %v", err)
	}
	if len(rec.argvs()) != 0 {
		t.Fatalf("commands ran despite leftover state: %v", rec.argvs())
	}
}
