// Package sysproxy changes machine-wide routing for the attach degrees 6 and 7
// (spec §19.9): the desktop or OS HTTPS proxy (--system-proxy, user scope) and
// a Linux nftables redirect for one user id (--transparent, needs privilege).
//
// Every change is reversible. Before anything is written, the previous values
// are saved in a 0600 state file under the data directory; Stop restores them
// and re-reads them to verify. A value changed by another program since acd-ctx
// set it is left alone. A crashed run leaves its state file behind, and
// Leftovers and RestoreStale find it.
//
// Nothing in this package runs a command by itself. Every command goes through
// a Runner that the caller injects; tests inject a fake. Only the CLI wires
// ExecRunner, the real os/exec runner.
//
// Limitation: the proxy answers only the loopback listener of the current run.
// The per-run MITM token cannot be placed in system settings, so the listener
// authenticates the client by its peer process uid instead (see peer.go).
package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// OwnerTag marks everything acd-ctx creates on the machine: the nft comment, the
// state files, and the owner field of each state.
const OwnerTag = "acd-ctx"

// Mode names, as stored in state files and reported to the user.
const (
	ModeSystemProxy = "system-proxy"
	ModeTransparent = "transparent"
)

// ErrNoBackend means no supported proxy mechanism was found on this machine.
var ErrNoBackend = errors.New("no supported system proxy mechanism detected")

// ErrNeedPrivilege means --transparent needs root or CAP_NET_ADMIN. Commands
// lists what the user can run as root.
type ErrNeedPrivilege struct {
	Commands []string
}

func (e *ErrNeedPrivilege) Error() string {
	return "transparent mode needs root or CAP_NET_ADMIN; run it yourself with:\n  " +
		strings.Join(e.Commands, "\n  ")
}

// Command is one external command: argv and optional stdin.
type Command struct {
	Name  string
	Args  []string
	Stdin string
}

// String renders the command for messages. It is not a shell quoting.
func (c Command) String() string {
	return strings.Join(append([]string{c.Name}, c.Args...), " ")
}

// Runner executes a command and returns its standard output. A non-zero exit
// status is returned as *ExitError.
type Runner interface {
	Run(ctx context.Context, c Command) (string, error)
}

// ExitError is a command that ran and exited non-zero.
type ExitError struct {
	Command Command
	Code    int
	Output  string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("%s: exit status %d: %s", e.Command, e.Code, strings.TrimSpace(e.Output))
}

// ExecRunner runs commands with os/exec. Only the CLI uses it.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, c Command) (string, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return stdout.String(), &ExitError{Command: c, Code: ee.ExitCode(), Output: stderr.String() + stdout.String()}
	}
	return "", fmt.Errorf("run %s: %w", c.Name, err)
}

// Setting is one machine setting that acd-ctx reads or writes. Key names it
// inside its backend. Value is the raw text the backend reads and writes back.
// Present=false means the setting does not exist.
type Setting struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Present bool   `json:"present"`
}

// same reports whether two readings of one setting are equal.
func same(a, b Setting) bool {
	return a.Key == b.Key && a.Present == b.Present && a.Value == b.Value
}

// Endpoint is where the system proxy points: the loopback listener of this run.
type Endpoint struct {
	Host   string
	Port   int
	Bypass []string
}

// DefaultBypass lists the hosts that never go through the proxy.
var DefaultBypass = []string{"localhost", "127.0.0.0/8", "::1"}

// Validate checks the endpoint before any value is written.
func (ep Endpoint) Validate() error {
	if ep.Host == "" || strings.ContainsAny(ep.Host, "'\"\\; \t\r\n") {
		return fmt.Errorf("sysproxy: invalid endpoint host %q", ep.Host)
	}
	// The listener is loopback only; a proxy setting that points elsewhere would
	// send the user's HTTPS traffic to another machine.
	if ip, err := netip.ParseAddr(ep.Host); err != nil && ep.Host != "localhost" || err == nil && !ip.IsLoopback() {
		return fmt.Errorf("sysproxy: endpoint host %q is not a loopback address", ep.Host)
	}
	if ep.Port <= 0 || ep.Port > 65535 {
		return fmt.Errorf("sysproxy: invalid endpoint port %d", ep.Port)
	}
	for _, b := range ep.Bypass {
		if strings.ContainsAny(b, "'\"\\; \t\r\n,") || strings.HasPrefix(b, "-") {
			return fmt.Errorf("sysproxy: invalid bypass entry %q", b)
		}
	}
	return nil
}

// Backend is one desktop or OS mechanism for the system HTTPS proxy.
type Backend interface {
	// Name identifies the backend in state files ("gnome", "kde5", "kde6",
	// "darwin", "windows").
	Name() string
	// Plan returns the settings that route HTTPS through ep. Every returned
	// setting is Present.
	Plan(ctx context.Context, r Runner, ep Endpoint) ([]Setting, error)
	// Read returns the current value of one key.
	Read(ctx context.Context, r Runner, key string) (Setting, error)
	// Write returns the commands that make the key equal to s. Present=false
	// removes the setting when the backend supports it.
	Write(s Setting) ([]Command, error)
}

// Env is everything the package reads from the machine. A zero Env is safe to
// build from, but a nil Runner makes every command-running operation fail.
type Env struct {
	GOOS       string                                                       // "" means runtime.GOOS
	Getenv     func(string) string                                          // nil returns ""
	LookPath   func(string) bool                                            // nil reports nothing installed
	Geteuid    func() int                                                   // nil reports -1 (unprivileged)
	ProcStatus func() string                                                // nil returns "" (/proc/self/status)
	ProcRoot   string                                                       // "" means /proc
	Runner     Runner                                                       // required for any command
	Alive      func(pid int, start uint64) bool                             // nil uses the host check
	Resolve    func(ctx context.Context, host string) ([]netip.Addr, error) // nil uses DNS
	Now        func() time.Time                                             // nil means time.Now
}

func (e Env) goos() string {
	if e.GOOS != "" {
		return e.GOOS
	}
	return runtime.GOOS
}

func (e Env) getenv(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

func (e Env) euid() int {
	if e.Geteuid == nil {
		return -1
	}
	return e.Geteuid()
}

func (e Env) procRoot() string {
	if e.ProcRoot == "" {
		return "/proc"
	}
	return e.ProcRoot
}

func (e Env) runner() (Runner, error) {
	if e.Runner == nil {
		return nil, errors.New("sysproxy: no command runner configured")
	}
	return e.Runner, nil
}

func (e Env) now() time.Time {
	if e.Now == nil {
		return time.Now()
	}
	return e.Now()
}
