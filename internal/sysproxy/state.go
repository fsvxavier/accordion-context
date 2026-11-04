package sysproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// stateVersion is the state file format version.
const stateVersion = 1

// State is what one run changed and what it found before. It is written before
// the first change and removed only after the restore is verified.
type State struct {
	Version  int    `json:"version"`
	Owner    string `json:"owner"`
	Mode     string `json:"mode"`
	Backend  string `json:"backend,omitempty"`
	NFTTable string `json:"nft_table,omitempty"`
	// Firewall is the transparent redirect tool: "nft" (or empty, for files
	// written before iptables support) or "iptables". Chain, UID and IPv6 hold
	// the iptables state: the chain name, the uid of its OUTPUT jump, and
	// whether the ip6tables chain was installed too.
	Firewall  string    `json:"firewall,omitempty"`
	Chain     string    `json:"chain,omitempty"`
	UID       int       `json:"uid,omitempty"`
	IPv6      bool      `json:"ipv6,omitempty"`
	PID       int       `json:"pid"`
	StartTime uint64    `json:"start_time,omitempty"`
	Created   time.Time `json:"created"`
	// Applied holds the values this run set. Previous holds the values found
	// before, in the same order and with the same keys.
	Applied  []Setting `json:"applied,omitempty"`
	Previous []Setting `json:"previous,omitempty"`
}

// StateDir is where the state files live: a "sysproxy" directory beside the
// database. --restore and doctor read the same directory.
func StateDir(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "sysproxy")
}

// createState writes a new state file with O_EXCL and mode 0600, then syncs it.
// An existing file means another run (or a crash) still owns the settings.
func createState(dir string, st *State) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("sysproxy: state directory: %w", err)
	}
	if err := checkTrusted(dir, true); err != nil {
		return "", err
	}
	st.Version = stateVersion
	st.Owner = OwnerTag
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.json", st.Mode, st.PID))
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("sysproxy: state file %s exists; run acd-ctx attach --restore first", path)
		}
		return "", fmt.Errorf("sysproxy: create state file: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("sysproxy: write state file: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("sysproxy: sync state file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// readState loads and checks a state file.
func readState(path string) (*State, error) {
	if err := checkTrusted(filepath.Dir(path), true); err != nil {
		return nil, err
	}
	if err := checkTrusted(path, false); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("sysproxy: state file %s: %w", path, err)
	}
	if st.Owner != OwnerTag || st.Version != stateVersion {
		return nil, fmt.Errorf("sysproxy: state file %s is not an acd-ctx state file", path)
	}
	return &st, nil
}

// listStates returns the state file paths in dir, sorted. A missing directory
// has none.
func listStates(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// Leftover is a state file found on disk, with its owner's liveness.
type Leftover struct {
	Path    string
	Mode    string
	Backend string
	PID     int
	Alive   bool
}

// Leftovers lists the state files in dir. A live owner is an active change; a
// dead one is a stale change from a crashed run. Unreadable files are reported
// with an error and are never restored.
func Leftovers(dir string, e Env) ([]Leftover, error) {
	paths, err := listStates(dir)
	if err != nil {
		return nil, err
	}
	var out []Leftover
	for _, p := range paths {
		st, err := readState(p)
		if err != nil {
			return out, err
		}
		out = append(out, Leftover{
			Path: p, Mode: st.Mode, Backend: st.Backend, PID: st.PID,
			Alive: ownerAlive(e, st),
		})
	}
	return out, nil
}

// RestoreStale restores every state file whose owner is gone. Live owners are
// skipped. Each restore is verified, and its state file is removed only when
// the restore succeeded.
func RestoreStale(ctx context.Context, dir string, e Env) ([]string, error) {
	left, err := Leftovers(dir, e)
	if err != nil {
		return nil, err
	}
	r, err := e.runner()
	if err != nil {
		return nil, err
	}
	var notes, errs []string
	for _, l := range left {
		if l.Alive {
			notes = append(notes, fmt.Sprintf("%s: owner pid %d is still running; not touched", l.Path, l.PID))
			continue
		}
		st, err := readState(l.Path)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		// Without privilege the nft and iptables list probes fail like an absent
		// table, which would drop the state of a redirect that is still in place.
		if st.Mode == ModeTransparent && !e.Privileged() {
			errs = append(errs, fmt.Sprintf("%s: restoring a transparent redirect needs root or CAP_NET_ADMIN; state kept", l.Path))
			continue
		}
		n, err := restoreState(ctx, r, e, st)
		notes = append(notes, n...)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", l.Path, err))
			continue
		}
		if err := os.Remove(l.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Sprintf("%s: remove state file: %v", l.Path, err))
		}
		notes = append(notes, fmt.Sprintf("restored %s state of pid %d", st.Mode, st.PID))
	}
	if len(errs) > 0 {
		return notes, errors.New(strings.Join(errs, "; "))
	}
	return notes, nil
}

// restoreState dispatches the restore of one state by its mode.
func restoreState(ctx context.Context, r Runner, e Env, st *State) ([]string, error) {
	switch st.Mode {
	case ModeSystemProxy:
		b, err := backendByName(st.Backend, e)
		if err != nil {
			return nil, err
		}
		return restoreSystem(ctx, r, b, st)
	case ModeTransparent:
		return restoreFirewall(ctx, r, st)
	}
	return nil, fmt.Errorf("unknown mode %q", st.Mode)
}

// owned reports whether the owner of a state file is still the same process.
// The start time guards against a pid reused by another process.
func owned(e Env, st *State) bool {
	return st.PID > 0 && owner(e, st.PID, st.StartTime)
}

func ownerAlive(e Env, st *State) bool {
	if e.Alive != nil {
		return e.Alive(st.PID, st.StartTime)
	}
	return owned(e, st)
}

// startTime returns the kernel start time of pid, or 0 when unknown.
func startTime(procRoot string, pid int) uint64 {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	v, err := parseStartTime(string(data))
	if err != nil {
		return 0
	}
	return v
}

// parseStartTime reads field 22 (starttime) of a /proc/<pid>/stat line. The
// command name may contain spaces and parentheses, so parsing starts after the
// last ')'.
func parseStartTime(stat string) (uint64, error) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, errors.New("sysproxy: stat without command name")
	}
	fields := strings.Fields(stat[i+1:])
	// fields[0] is field 3 (state); starttime is field 22.
	if len(fields) < 20 {
		return 0, errors.New("sysproxy: short stat line")
	}
	return strconv.ParseUint(fields[19], 10, 64)
}
