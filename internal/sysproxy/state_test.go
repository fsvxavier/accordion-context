package sysproxy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStateCreateIsExclusive(t *testing.T) {
	dir := t.TempDir()
	st := &State{Mode: ModeSystemProxy, Backend: "gnome", PID: 4242, Created: time.Now()}
	p, err := createState(dir, st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := createState(dir, st); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("second create must refuse, got %v", err)
	}
	got, err := readState(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != OwnerTag || got.Version != stateVersion {
		t.Fatalf("owner/version not stamped: %+v", got)
	}
}

func TestReadStateRejectsForeignFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.json")
	_ = os.WriteFile(p, []byte(`{"owner":"someone-else","version":1}`), 0o600)
	if _, err := readState(p); err == nil {
		t.Fatal("foreign state file accepted")
	}
}

// TestCrashRecovery: a state file whose owner is dead is restored; one whose
// owner is alive is left untouched.
func TestCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	mem := &memGSettings{vals: gnomeStart()}
	rec := &recorder{inner: mem}
	ctx := context.Background()

	// Simulate a crashed run: the settings were applied, the process died.
	mem.vals["org.gnome.system.proxy mode"] = "'manual'"
	mem.vals["org.gnome.system.proxy ignore-hosts"] = "['localhost', '127.0.0.0/8', '::1']"
	mem.vals["org.gnome.system.proxy.https host"] = "'127.0.0.1'"
	mem.vals["org.gnome.system.proxy.https port"] = "45123"
	crashed := &State{
		Mode: ModeSystemProxy, Backend: "gnome", PID: 777, Created: time.Now(),
		Applied: []Setting{
			{Key: "gnome:mode", Value: "'manual'", Present: true},
			{Key: "gnome:ignore", Value: "['localhost', '127.0.0.0/8', '::1']", Present: true},
			{Key: "gnome:https.host", Value: "'127.0.0.1'", Present: true},
			{Key: "gnome:https.port", Value: "45123", Present: true},
		},
		Previous: []Setting{
			{Key: "gnome:mode", Value: "'none'", Present: true},
			{Key: "gnome:ignore", Value: "['localhost']", Present: true},
			{Key: "gnome:https.host", Value: "''", Present: true},
			{Key: "gnome:https.port", Value: "0", Present: true},
		},
	}
	if _, err := createState(dir, crashed); err != nil {
		t.Fatal(err)
	}

	e := gnomeEnv(rec, dir)
	e.Alive = func(pid int, _ uint64) bool { return pid != 777 }
	notes, err := RestoreStale(ctx, dir, e)
	if err != nil {
		t.Fatalf("recovery: %v (notes %v)", err, notes)
	}
	if !reflect.DeepEqual(mem.vals, gnomeStart()) {
		t.Fatalf("crashed settings not restored: %v", mem.vals)
	}
	if paths, _ := listStates(dir); len(paths) != 0 {
		t.Fatalf("state file kept after recovery: %v", paths)
	}

	// A live owner is not touched.
	live := &State{Mode: ModeSystemProxy, Backend: "gnome", PID: 888, Created: time.Now(),
		Applied: crashed.Applied, Previous: crashed.Previous}
	if _, err := createState(dir, live); err != nil {
		t.Fatal(err)
	}
	rec.reset()
	e.Alive = func(int, uint64) bool { return true }
	notes, err = RestoreStale(ctx, dir, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.argvs()) != 0 {
		t.Fatalf("live owner: commands ran: %v", rec.argvs())
	}
	if !containsNote(notes, "still running") {
		t.Fatalf("no note for live owner: %v", notes)
	}
	if paths, _ := listStates(dir); len(paths) != 1 {
		t.Fatalf("live owner state removed")
	}
}

func TestLeftoversReportsLiveAndStale(t *testing.T) {
	dir := t.TempDir()
	for _, pid := range []int{10, 20} {
		if _, err := createState(dir, &State{Mode: ModeTransparent, NFTTable: NFTTable, PID: pid}); err != nil {
			t.Fatal(err)
		}
	}
	e := Env{Alive: func(pid int, _ uint64) bool { return pid == 10 }}
	left, err := Leftovers(dir, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 || !left[0].Alive || left[1].Alive {
		t.Fatalf("leftovers = %+v", left)
	}
	if left, err := Leftovers(filepath.Join(dir, "missing"), e); err != nil || len(left) != 0 {
		t.Fatalf("missing dir: %v %v", left, err)
	}
}

func TestParseStartTime(t *testing.T) {
	// Field 22 is starttime. The command name contains spaces and a ')'.
	stat := "1234 (acd (x) ctx) S 1 1234 1234 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 1 0 987654 1000 10"
	got, err := parseStartTime(stat)
	if err != nil || got != 987654 {
		t.Fatalf("parseStartTime = %d, %v", got, err)
	}
	if _, err := parseStartTime("1 (x) S"); err == nil {
		t.Fatal("short line accepted")
	}
}

func TestStateJSONRoundTrip(t *testing.T) {
	st := State{Mode: ModeSystemProxy, Backend: "kde6", PID: 5, Applied: []Setting{{Key: "k", Value: "v", Present: true}}}
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var back State
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st, back) {
		t.Fatalf("round trip changed state: %+v vs %+v", st, back)
	}
}
