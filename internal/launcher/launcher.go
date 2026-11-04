package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fsvxavier/accordion-context/internal/project"
)

// DefaultProxyAddr is the manual proxy port. Launcher-started proxies use a
// free loopback port instead.
const DefaultProxyAddr = "127.0.0.1:8787"

const (
	defaultStartTimeout = 5 * time.Second
	stopTimeout         = 5 * time.Second
	pollInterval        = 50 * time.Millisecond
	pathHealth          = "/__acd-ctx/health"
	pathStatus          = "/__acd-ctx/status"
)

var defaultMarkers = []string{"go.mod", "package.json", "pyproject.toml", "composer.json", "Cargo.toml"}

// Options configures one launch. Only Args is required.
type Options struct {
	// Args is the client command and its arguments. Args[0] is looked up in
	// PATH and, unless Profile is set, selects the built-in profile.
	Args []string
	// Profile overrides the profile chosen from Args[0].
	Profile *Profile
	// Cwd is the working directory for project resolution and the client.
	// Defaults to the process working directory.
	Cwd string
	// Markers are project root markers. Defaults to the proxy's markers.
	Markers []string
	// Version is the acd-ctx version a running proxy must report to be
	// reused. Empty accepts any healthy proxy.
	Version string
	// ProxyAddr is the loopback address checked for a running proxy.
	// Defaults to DefaultProxyAddr.
	ProxyAddr string
	// ServeExe is the binary started as "serve --listen <addr>". Defaults to
	// the running executable.
	ServeExe string
	// ProxyLog receives the started proxy's output. Nil discards it.
	ProxyLog io.Writer
	// StartTimeout bounds the wait for a started proxy to become healthy.
	StartTimeout time.Duration
	// Stdin, Stdout and Stderr default to the process's own streams.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run starts the client with its model traffic routed through the proxy and
// returns the client's exit code. It reuses a healthy proxy of the same
// version, or starts one on a free loopback port and stops it on return.
// Signals received by Run are forwarded to the client.
func Run(ctx context.Context, opts Options) (int, error) {
	if len(opts.Args) == 0 {
		return 2, errors.New("launcher: no client command")
	}
	cwd := opts.Cwd
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return 1, fmt.Errorf("launcher: working directory: %w", err)
		}
		cwd = wd
	}
	markers := opts.Markers
	if markers == nil {
		markers = defaultMarkers
	}
	res, err := project.Resolve(cwd, markers)
	if err != nil {
		return 1, fmt.Errorf("launcher: resolve project: %w", err)
	}

	prof, err := chooseProfile(opts)
	if err != nil {
		return 1, err
	}
	if prof.BaseURLEnv == "" {
		return 1, fmt.Errorf("%w (profile %q)", ErrNoBaseURL, prof.Name)
	}

	addr := opts.ProxyAddr
	if addr == "" {
		addr = DefaultProxyAddr
	}
	if err := checkLoopback(addr); err != nil {
		return 1, err
	}
	// Installed before the proxy starts so a signal during startup stops the
	// proxy instead of orphaning it.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigc)

	addr, stop, err := ensureProxy(ctx, addr, opts, sigc)
	if err != nil {
		return 1, err
	}
	defer stop()

	env := setEnv(os.Environ(), prof.BaseURLEnv, BaseURL(addr, res.ID, prof))
	for k, v := range prof.Env {
		env = setEnv(env, k, v)
	}

	cmd := exec.Command(opts.Args[0], opts.Args[1:]...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = opts.Stdin, opts.Stdout, opts.Stderr
	if cmd.Stdin == nil {
		cmd.Stdin = os.Stdin
	}
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return 127, fmt.Errorf("launcher: start %s: %w", opts.Args[0], err)
		}
		return 1, fmt.Errorf("launcher: start %s: %w", opts.Args[0], err)
	}

	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigc:
				_ = cmd.Process.Signal(s)
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				return
			case <-done:
				return
			}
		}
	}()
	waitErr := cmd.Wait()
	close(done)
	return exitStatus(waitErr), nil
}

// checkLoopback rejects proxy addresses reachable from other hosts; the proxy
// carries API credentials, so it must stay on loopback.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("launcher: proxy address %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("launcher: proxy address %q is not loopback", addr)
	}
	return nil
}

func chooseProfile(opts Options) (Profile, error) {
	if opts.Profile != nil {
		if err := validate(*opts.Profile); err != nil {
			return Profile{}, fmt.Errorf("launcher: profile %q: %w", opts.Profile.Name, err)
		}
		return *opts.Profile, nil
	}
	p, ok := ProfileFor(opts.Args[0])
	if !ok {
		return Profile{}, fmt.Errorf("launcher: no client profile for %q", opts.Args[0])
	}
	return p, nil
}

// ensureProxy returns the address of a healthy proxy and a stop function. The
// stop function is a no-op when the proxy was already running.
func ensureProxy(ctx context.Context, addr string, opts Options, sigc <-chan os.Signal) (string, func(), error) {
	if healthy(ctx, addr, opts.Version) {
		return addr, func() {}, nil
	}
	free, err := freeLoopbackAddr()
	if err != nil {
		return "", nil, err
	}
	exe := opts.ServeExe
	if exe == "" {
		if exe, err = os.Executable(); err != nil {
			return "", nil, fmt.Errorf("launcher: locate executable: %w", err)
		}
	}
	cmd := exec.Command(exe, "serve", "--listen", free)
	cmd.Stdout = opts.ProxyLog
	cmd.Stderr = opts.ProxyLog
	detachProxy(cmd)
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("launcher: start proxy: %w", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	stop := func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(stopTimeout):
			_ = cmd.Process.Kill()
			<-exited
		}
	}

	timeout := opts.StartTimeout
	if timeout <= 0 {
		timeout = defaultStartTimeout
	}
	deadline := time.Now().Add(timeout)
	for !healthy(ctx, free, opts.Version) {
		select {
		case <-exited:
			return "", nil, fmt.Errorf("launcher: proxy on %s exited before becoming healthy", free)
		case <-ctx.Done():
			stop()
			return "", nil, ctx.Err()
		case s := <-sigc:
			stop()
			return "", nil, fmt.Errorf("launcher: interrupted (%v) while starting proxy", s)
		case <-time.After(pollInterval):
		}
		if time.Now().After(deadline) {
			stop()
			return "", nil, fmt.Errorf("launcher: proxy on %s not healthy after %s", free, timeout)
		}
	}
	return free, stop, nil
}

// healthy reports whether a proxy answers the health endpoint and, when
// version is set, reports that version in its status.
func healthy(ctx context.Context, addr, version string) bool {
	client := &http.Client{Timeout: time.Second}
	var health struct {
		OK bool `json:"ok"`
	}
	if !getJSON(ctx, client, addr, pathHealth, &health) || !health.OK {
		return false
	}
	if version == "" {
		return true
	}
	var status struct {
		Version string `json:"version"`
	}
	return getJSON(ctx, client, addr, pathStatus, &status) && status.Version == version
}

func getJSON(ctx context.Context, client *http.Client, addr, path string, out any) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out) == nil
}

func freeLoopbackAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("launcher: free port: %w", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		return "", fmt.Errorf("launcher: free port: %w", err)
	}
	return addr, nil
}

// exitStatus maps a Wait error to a shell-style exit code: the client's own
// code, or 128+signal when it was killed by a signal.
func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
	}
	return 1
}

// setEnv replaces key in env, or appends it when absent.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}
	return append(out, prefix+value)
}
