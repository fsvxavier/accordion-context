package sysproxy

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// WatchdogArg is the hidden argument that makes the acd-ctx binary run the
// watchdog helper. The CLI checks for it before anything else runs.
const WatchdogArg = "__watchdog"

// watchdogPipeFD is the descriptor the helper reads its pipe from: the first
// ExtraFiles entry of the child process.
const watchdogPipeFD = 3

// standDownByte is written by the owner after it restored the redirect and
// removed the state file itself. The helper then exits without touching anything.
const standDownByte = 'S'

// standDownTimeout bounds the wait for the helper to exit after a stand-down.
const standDownTimeout = 10 * time.Second

// watchdogPollDefault is how often the helper checks that its owner is alive
// when Env.WatchdogPoll is zero. The pipe is the primary signal; the poll covers
// a pipe copy that outlived its owner.
const watchdogPollDefault = time.Second

// Spawner starts the helper for the state file at statePath, owned by
// parentPID. The helper reads from pipe. The spawner takes ownership of pipe
// and closes it once the helper has its own copy (or on failure). wait reports
// when the helper has exited.
type Spawner func(statePath string, parentPID int, pipe *os.File) (wait func() error, err error)

// watchdog is the owner's side of one helper: the write end of its pipe.
type watchdog struct {
	w    *os.File
	wait func() error
}

// startWatchdog starts the helper before the first firewall change. Without a
// spawner the change is refused: a redirect that nothing would restore after a
// crash must not be installed.
func startWatchdog(e Env, statePath string, parentPID int) (*watchdog, error) {
	if e.Spawn == nil {
		return nil, errors.New("sysproxy: no watchdog spawner configured; the redirect would not be restored after a crash")
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("sysproxy: watchdog pipe: %w", err)
	}
	wait, err := e.Spawn(statePath, parentPID, r)
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("sysproxy: start watchdog: %w", err)
	}
	return &watchdog{w: w, wait: wait}, nil
}

// standDown tells the helper to exit without restoring anything, then waits for
// it. A nil watchdog (no helper) is a no-op.
func (d *watchdog) standDown() error {
	if d == nil {
		return nil
	}
	_, werr := d.w.Write([]byte{standDownByte})
	cerr := d.w.Close()
	done := make(chan error, 1)
	go func() { done <- d.wait() }()
	select {
	case err := <-done:
		return errors.Join(werr, cerr, err)
	case <-time.After(standDownTimeout):
		return errors.Join(werr, cerr, errors.New("sysproxy: watchdog did not exit"))
	}
}

// RunWatchdog is the helper's body. It holds the redirect's state until its
// owner ends:
//   - the stand-down byte: the owner already restored the redirect; nothing runs;
//   - EOF on the pipe (the owner died, or closed its end): the redirect is restored;
//   - the owner's pid is gone or reused: the redirect is restored;
//   - the helper's own context ends (a signal): the redirect is restored, because
//     a leftover redirect would cut the client off.
//
// The restore is the verified code that attach --restore uses. A state file that
// is already gone means nothing is left to do. A state file that does not belong
// to parentPID, or whose redirect names something other than acd-ctx's own table
// or chain, is refused before any command runs.
func RunWatchdog(ctx context.Context, e Env, statePath string, parentPID int, in io.Reader) error {
	st, err := readState(statePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := checkWatchdogState(st, parentPID); err != nil {
		return err
	}
	r, err := e.runner()
	if err != nil {
		return err
	}
	pipeEnd := make(chan pipeEvent, 1)
	go func() {
		var b [1]byte
		n, err := io.ReadFull(in, b[:])
		pipeEnd <- pipeEvent{standDown: n == 1 && err == nil && b[0] == standDownByte, err: err}
	}()
	poll := e.WatchdogPoll
	if poll <= 0 {
		poll = watchdogPollDefault
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		select {
		case ev := <-pipeEnd:
			if ev.standDown {
				return nil
			}
			if ev.err != nil && !errors.Is(ev.err, io.EOF) {
				// Not an owner that went away: the pipe is broken or not ours.
				// Nothing is restored on a read error.
				return fmt.Errorf("sysproxy: watchdog pipe: %w", ev.err)
			}
			return restoreWatchdog(r, statePath, st)
		case <-tick.C:
			if !ownerAlive(e, st) {
				return restoreWatchdog(r, statePath, st)
			}
		case <-ctx.Done():
			return restoreWatchdog(r, statePath, st)
		}
	}
}

type pipeEvent struct {
	standDown bool
	err       error
}

// restoreWatchdog runs the verified restore, then removes the state file. A
// failed restore keeps the state file, so attach --restore can finish the job.
func restoreWatchdog(r Runner, statePath string, st *State) error {
	ctx, cancel := RestoreContext()
	defer cancel()
	if _, err := restoreFirewall(ctx, r, st); err != nil {
		return fmt.Errorf("sysproxy: watchdog restore: %w (state kept; run acd-ctx attach --restore)", err)
	}
	return removeFile(statePath)
}

// checkWatchdogState refuses a state file the helper must not act on.
func checkWatchdogState(st *State, parentPID int) error {
	if st.Mode != ModeTransparent || st.PID != parentPID {
		return fmt.Errorf("sysproxy: watchdog: state file belongs to %s pid %d, not %s pid %d", st.Mode, st.PID, ModeTransparent, parentPID)
	}
	switch st.Firewall {
	case "", FirewallNFT:
		if st.NFTTable != NFTTable {
			return fmt.Errorf("sysproxy: watchdog: refusing table %q", st.NFTTable)
		}
	case FirewallIptables:
		if st.Chain != iptChain || st.UID < 0 {
			return fmt.Errorf("sysproxy: watchdog: refusing chain %q for uid %d", st.Chain, st.UID)
		}
	default:
		return fmt.Errorf("sysproxy: watchdog: unknown redirect tool %q", st.Firewall)
	}
	return nil
}

// WatchdogMain is the helper process's entry point. args are the arguments
// after WatchdogArg. Its pipe is descriptor 3, as set by ExecSpawner.
func WatchdogMain(args []string, stderr io.Writer) int {
	fset := flag.NewFlagSet(WatchdogArg, flag.ContinueOnError)
	fset.SetOutput(stderr)
	statePath := fset.String("state", "", "state file of the transparent run")
	parent := fset.Int("parent-pid", 0, "pid of the run that owns the redirect")
	if err := fset.Parse(args); err != nil || *statePath == "" || *parent <= 0 {
		return 2
	}
	pipe := os.NewFile(watchdogPipeFD, "acd-ctx-watchdog")
	ctx, stop := signalContext()
	defer stop()
	e := Env{GOOS: runtime.GOOS, Geteuid: os.Geteuid, Runner: ExecRunner{}}
	if err := RunWatchdog(ctx, e, *statePath, *parent, pipe); err != nil {
		fmt.Fprintf(stderr, "acd-ctx: watchdog: %v\n", err)
		return 1
	}
	return 0
}

// WatchdogPIDs lists the helper processes that serve statePath, read from
// /proc under procRoot. It only reads.
func WatchdogPIDs(procRoot, statePath string) []int {
	if procRoot == "" {
		procRoot = "/proc"
	}
	abs, err := filepath.Abs(statePath)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	var out []int
	for _, ent := range entries {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(procRoot, ent.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
		if len(args) < 2 || args[1] != WatchdogArg {
			continue
		}
		for i := 2; i+1 < len(args); i++ {
			if args[i] == "--state" && args[i+1] == abs {
				out = append(out, pid)
			}
		}
	}
	return out
}

// signalContext cancels on the signals that end a run.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
}

// FirewallLeftovers reports the acd-ctx nft table and iptables/ip6tables chains
// that are present, with the command that removes each one. It only reads: each
// check is a list command. A tool that is not installed reports nothing, and an
// unprivileged user may not see a root-owned table (the probe then says nothing).
func FirewallLeftovers(ctx context.Context, r Runner) []string {
	var out []string
	if ok, err := nftTableExists(ctx, r); err == nil && ok {
		out = append(out, fmt.Sprintf("nft table inet %s is present (remove with: sudo nft delete table inet %s)", NFTTable, NFTTable))
	}
	for _, v6 := range []bool{false, true} {
		if ok, err := iptChainExists(ctx, r, v6); err == nil && ok {
			out = append(out, fmt.Sprintf("%s chain %s is present (remove its OUTPUT jump with: sudo %s -t nat -D OUTPUT -m owner --uid-owner <uid> -m comment --comment %s -j %s; then -F and -X the chain)",
				iptBin(v6), iptChain, iptBin(v6), OwnerTag, iptChain))
		}
	}
	return out
}
