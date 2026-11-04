package sysproxy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helperSpawner runs the watchdog in this process instead of starting a
// binary. The helper shares the fake runner and the liveness answers of e, and
// it reads the pipe the owner holds the other end of. No process is started.
func helperSpawner(e Env) Spawner {
	return func(statePath string, parentPID int, pipe *os.File) (func() error, error) {
		var err error
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			defer pipe.Close()
			err = RunWatchdog(context.Background(), e, statePath, parentPID, pipe)
		}()
		// Every wait returns the same result once the helper has finished.
		return func() error { <-finished; return err }, nil
	}
}

// keepHelperDown stands the helper of a started transparent change down when
// the test ends, so no helper outlives its test (a helper whose pipe is closed
// later would restore into another test's fake).
func keepHelperDown(t *testing.T, a *Active) {
	t.Cleanup(func() {
		if a != nil && a.dog != nil {
			_ = a.dog.standDown()
		}
	})
}

// startFakeTransparent starts an nft redirect whose helper is in-process.
func startFakeTransparent(t *testing.T, f *fakeNFT, alive func(int, uint64) bool) (*Active, string) {
	t.Helper()
	dir := t.TempDir()
	e := linuxEnv(f, 0)
	if alive != nil {
		e.Alive = alive
		e.Spawn = helperSpawner(e)
	}
	plan := NFTPlan{TargetUID: 1000, ProxyUID: 0, RedirectPort: 8443, IPv4: ips(t, "1.1.1.1")}
	a, err := StartTransparent(context.Background(), e, dir, plan, FirewallNFT)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	keepHelperDown(t, a)
	return a, a.path
}

// The owner dies: its end of the pipe closes. The helper restores the table with
// exactly the verified nft commands, once, and removes the state file.
func TestWatchdogRestoresOnParentDeath(t *testing.T) {
	f := &fakeNFT{}
	a, path := startFakeTransparent(t, f, nil)
	f.reset()

	if err := a.dog.w.Close(); err != nil { // simulate the owner's death
		t.Fatal(err)
	}
	if err := a.dog.wait(); err != nil {
		t.Fatalf("helper: %v", err)
	}
	want := "nft list table inet acd_ctx|nft delete table inet acd_ctx|nft list table inet acd_ctx"
	if got := strings.Join(f.cmds, "|"); got != want {
		t.Fatalf("restore argv = %q, want %q", got, want)
	}
	if f.present {
		t.Fatal("table still present after the helper's restore")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state file kept after the helper's restore: %v", err)
	}

	// Running the helper again on the same state is a no-op: idempotent.
	f.reset()
	if err := RunWatchdog(context.Background(), linuxEnv(f, 0), path, os.Getpid(), bytes.NewReader(nil)); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(f.cmds) != 0 {
		t.Fatalf("second run must not touch the system: %v", f.cmds)
	}
}

// The owner stands down after its own verified restore: the helper exits and
// runs nothing.
func TestWatchdogStandDownRunsNothing(t *testing.T) {
	f := &fakeNFT{}
	a, _ := startFakeTransparent(t, f, nil)
	f.reset()
	if err := a.dog.standDown(); err != nil {
		t.Fatalf("stand down: %v", err)
	}
	if len(f.cmds) != 0 {
		t.Fatalf("stand-down ran commands: %v", f.cmds)
	}
	if !f.present {
		t.Fatal("stand-down must not touch the table")
	}
}

// A normal Stop restores, removes the state file, and stands the helper down.
// The helper exits without a second restore.
func TestStopStandsWatchdogDown(t *testing.T) {
	f := &fakeNFT{}
	a, path := startFakeTransparent(t, f, nil)
	f.reset()
	notes, err := a.Stop(RestoreContext2(t))
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(notes) == 0 {
		t.Fatal("stop reported nothing")
	}
	if got := strings.Join(f.cmds, "|"); got != "nft list table inet acd_ctx|nft delete table inet acd_ctx|nft list table inet acd_ctx" {
		t.Fatalf("stop argv = %q", got)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state file kept: %v", err)
	}
}

// The owner's pid vanishes without closing the pipe (a copy of the pipe outlived
// it): the poll notices and the helper restores.
func TestWatchdogRestoresWhenOwnerPIDGone(t *testing.T) {
	f := &fakeNFT{}
	dir := t.TempDir()
	st := &State{Mode: ModeTransparent, Firewall: FirewallNFT, NFTTable: NFTTable, PID: 4242, Created: linuxEnv(f, 0).now()}
	path, err := createState(dir, st)
	if err != nil {
		t.Fatal(err)
	}
	f.present = true
	f.text = "table inet acd_ctx {\n\tchain output {\n\t\tmeta skuid 0 accept comment \"acd-ctx\"\n\t}\n}\n"
	e := linuxEnv(f, 0)
	e.WatchdogPoll = time.Millisecond
	e.Alive = func(pid int, _ uint64) bool { return pid != 4242 }
	pr, pw, _ := os.Pipe()
	defer pw.Close()
	if err := RunWatchdog(context.Background(), e, path, 4242, pr); err != nil {
		t.Fatalf("helper: %v", err)
	}
	if f.present {
		t.Fatal("owner gone: the table must be restored")
	}
}

// A signal ends the helper with a restore: a leftover redirect would cut the client off.
func TestWatchdogRestoresOnCancel(t *testing.T) {
	f := &fakeNFT{present: true, text: "table inet acd_ctx {\n\tchain output {\n\t\tmeta skuid 0 accept comment \"acd-ctx\"\n\t}\n}\n"}
	dir := t.TempDir()
	st := &State{Mode: ModeTransparent, Firewall: FirewallNFT, NFTTable: NFTTable, PID: 4343, Created: linuxEnv(f, 0).now()}
	path, err := createState(dir, st)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pr, pw, _ := os.Pipe()
	defer pw.Close()
	if err := RunWatchdog(ctx, linuxEnv(f, 0), path, 4343, pr); err != nil {
		t.Fatalf("helper: %v", err)
	}
	if f.present {
		t.Fatal("canceled helper must restore")
	}
}

// refuse runs the helper on a state file it must not act on: no command runs.
func refuseWatchdog(t *testing.T, mutate func(path string), parentPID int) {
	t.Helper()
	f := &fakeNFT{present: true, text: "table inet acd_ctx {\n\tchain output {\n\t\tmeta skuid 0 accept comment \"acd-ctx\"\n\t}\n}\n"}
	dir := t.TempDir()
	st := &State{Mode: ModeTransparent, Firewall: FirewallNFT, NFTTable: NFTTable, PID: 5151, Created: linuxEnv(f, 0).now()}
	path, err := createState(dir, st)
	if err != nil {
		t.Fatal(err)
	}
	mutate(path)
	pr, pw, _ := os.Pipe()
	defer pw.Close()
	if err := RunWatchdog(context.Background(), linuxEnv(f, 0), path, parentPID, pr); err == nil {
		t.Fatal("helper acted on a state file it must refuse")
	}
	if len(f.cmds) != 0 {
		t.Fatalf("refused state still ran commands: %v", f.cmds)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("refused state file must stay: %v", err)
	}
}

func TestWatchdogRefusesTamperedState(t *testing.T) {
	// A table name other than acd-ctx's own.
	refuseWatchdog(t, func(path string) {
		rewriteState(t, path, `"nft_table": "acd_ctx"`, `"nft_table": "filter"`)
	}, 5151)
	// A state file that names another owner pid.
	refuseWatchdog(t, func(path string) {
		rewriteState(t, path, `"pid": 5151`, `"pid": 6161`)
	}, 5151)
	// The helper is told a pid that is not the state's owner.
	refuseWatchdog(t, func(string) {}, 9999)
	// A state file another user could write is never replayed.
	refuseWatchdog(t, func(path string) {
		if err := os.Chmod(path, 0o666); err != nil {
			t.Fatal(err)
		}
	}, 5151)
	// A state file that is a symlink is refused.
	refuseWatchdog(t, func(path string) {
		target := path + ".real"
		data, _ := os.ReadFile(path)
		_ = os.WriteFile(target, data, 0o600)
		_ = os.Remove(path)
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}, 5151)
}

func rewriteState(t *testing.T, path, from, to string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), from) {
		t.Fatalf("state has no %q:\n%s", from, data)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), from, to, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A helper that cannot be started refuses the transparent change before any
// rule is applied, and leaves no state file behind.
func TestTransparentRefusesWithoutWatchdog(t *testing.T) {
	f := &fakeNFT{}
	e := linuxEnv(f, 0)
	e.Spawn = nil
	dir := t.TempDir()
	plan := NFTPlan{TargetUID: 1000, ProxyUID: 0, RedirectPort: 8443, IPv4: ips(t, "1.1.1.1")}
	if _, err := StartTransparent(context.Background(), e, dir, plan, FirewallNFT); err == nil || !strings.Contains(err.Error(), "watchdog") {
		t.Fatalf("start without a watchdog: %v", err)
	}
	for _, c := range f.cmds {
		if strings.Contains(c, "-f") {
			t.Fatalf("ruleset applied without a watchdog: %v", f.cmds)
		}
	}
	if left, _ := listStates(StateDir(filepath.Join(dir, "x.db"))); len(left) != 0 {
		t.Fatalf("state left: %v", left)
	}
}

// A spawner that fails leaves nothing applied and no state file.
func TestTransparentWatchdogStartFailureChangesNothing(t *testing.T) {
	f := &fakeNFT{}
	e := linuxEnv(f, 0)
	e.Spawn = func(string, int, *os.File) (func() error, error) { return nil, errors.New("no helper") }
	dir := t.TempDir()
	plan := NFTPlan{TargetUID: 1000, ProxyUID: 0, RedirectPort: 8443, IPv4: ips(t, "1.1.1.1")}
	if _, err := StartTransparent(context.Background(), e, dir, plan, FirewallNFT); err == nil {
		t.Fatal("start must fail when the helper does not start")
	}
	if f.present {
		t.Fatal("table applied although the helper did not start")
	}
	if left, _ := listStates(dir); len(left) != 0 {
		t.Fatalf("state left: %v", left)
	}
}

// The race: the table appears between the check and the apply. create refuses
// the script, the table is not ours, and it is not deleted.
func TestNFTCreateRaceRefusesForeignTable(t *testing.T) {
	f := &fakeNFT{appearOnApply: true}
	dir := t.TempDir()
	plan := NFTPlan{TargetUID: 1000, ProxyUID: 0, RedirectPort: 8443, IPv4: ips(t, "1.1.1.1")}
	if _, err := StartTransparent(context.Background(), linuxEnv(f, 0), dir, plan, FirewallNFT); err == nil {
		t.Fatal("a table that appeared before the apply must refuse the start")
	}
	for _, c := range f.cmds {
		if strings.HasPrefix(c, "nft delete") {
			t.Fatalf("foreign table deleted: %v", f.cmds)
		}
	}
	if !f.present {
		t.Fatal("foreign table removed")
	}
	if left, _ := listStates(dir); len(left) != 0 {
		t.Fatalf("state kept for a table that is not ours: %v", left)
	}
}

// The watchdog state is found by its own process name in /proc, read-only.
func TestWatchdogPIDsFindsHelperByStatePath(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "transparent-1.json")
	abs, _ := filepath.Abs(statePath)
	add := func(pid, cmdline string) {
		d := filepath.Join(root, pid)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "cmdline"), []byte(cmdline), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	add("100", "/usr/bin/acd-ctx\x00"+WatchdogArg+"\x00--state\x00"+abs+"\x00--parent-pid\x001\x00")
	add("101", "/usr/bin/acd-ctx\x00attach\x00--transparent\x00")
	add("102", "/usr/bin/acd-ctx\x00"+WatchdogArg+"\x00--state\x00/other.json\x00--parent-pid\x001\x00")
	got := WatchdogPIDs(root, statePath)
	if len(got) != 1 || got[0] != 100 {
		t.Fatalf("WatchdogPIDs = %v, want [100]", got)
	}
}

// The helper refuses a bad command line before it reads its pipe or touches
// anything.
func TestWatchdogMainRefusesBadArguments(t *testing.T) {
	var buf bytes.Buffer
	if code := WatchdogMain([]string{"--state", "", "--parent-pid", "0"}, &buf); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if code := WatchdogMain([]string{"--bogus"}, &buf); code != 2 {
		t.Fatalf("unknown flag: exit = %d, want 2", code)
	}
}

// A relative state path, stray arguments and a descriptor 3 that is not a pipe
// are all refused before anything is read or run.
func TestWatchdogMainRefusesRelativeExtraAndNonPipe(t *testing.T) {
	var buf bytes.Buffer
	if code := WatchdogMain([]string{"--state", "rel.json", "--parent-pid", "5"}, &buf); code != 2 {
		t.Fatalf("relative path: exit = %d", code)
	}
	if code := WatchdogMain([]string{"--state", "/x.json", "--parent-pid", "5", "extra"}, &buf); code != 2 {
		t.Fatalf("extra arg: exit = %d", code)
	}
	// In the test binary descriptor 3 is not the owner's pipe.
	if code := WatchdogMain([]string{"--state", "/nonexistent/x.json", "--parent-pid", "5"}, &buf); code != 2 {
		t.Fatalf("non-pipe fd 3: exit = %d, stderr %q", code, buf.String())
	}
}

// The helper must not delete a table once its owner has finished its own restore
// and removed the state file: a newer run may own a table of the same name.
func TestWatchdogDoesNothingAfterStateFileGone(t *testing.T) {
	f := &fakeNFT{}
	a, path := startFakeTransparent(t, f, nil)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	f.reset()
	if err := a.dog.w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.dog.wait(); err != nil {
		t.Fatalf("helper: %v", err)
	}
	if len(f.cmds) != 0 {
		t.Fatalf("helper ran commands without a state file: %v", f.cmds)
	}
}

// attach --restore without privilege must keep the state of a transparent
// redirect: the unprivileged list probes would look like an absent table.
func TestRestoreStaleKeepsTransparentStateWithoutPrivilege(t *testing.T) {
	f := &fakeNFT{}
	a, path := startFakeTransparent(t, f, nil)
	_ = a
	f.reset()
	e := linuxEnv(f, 1000)
	e.Alive = func(int, uint64) bool { return false }
	_, err := RestoreStale(context.Background(), filepath.Dir(path), e)
	if err == nil || !strings.Contains(err.Error(), "needs root") {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(path); serr != nil {
		t.Fatalf("state removed: %v", serr)
	}
	if len(f.cmds) != 0 {
		t.Fatalf("commands ran: %v", f.cmds)
	}
}
