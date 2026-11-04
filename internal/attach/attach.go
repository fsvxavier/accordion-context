// Package attach starts one client command with its model traffic routed
// through the Accordion Context proxy, without touching the client's own
// configuration (spec §19.9, "Escada automática de integração").
//
// The ladder has two working degrees, tried in order:
//
//  1. base-url-env: the client's base URL variable points at the proxy. For a
//     command without a known profile (console mode), every known base URL
//     variable is set at once, together with the MITM variables.
//  2. mitm: HTTPS_PROXY points at the in-process MITM listener and the CA
//     bundle variables trust the per-install CA, for this process only.
//
// Each degree is verified before the client starts. acd-ctx sends probe
// requests along the path the client's environment would use (the base URL,
// or HTTPS_PROXY plus the CA bundle). The listener answers a probe itself,
// without contacting any upstream, and a degree counts as working only when
// its probes arrive. The client then runs once with the winning environment.
// Requests the client sends are counted in Result.Hits. A client's own use of
// the integration cannot be probed before it runs, so Hits reports it after.
//
// The proxy runs in-process on a loopback port that lives as long as the
// command, so the hit counter sits on the real handler. The --system-proxy and
// --transparent degrees are not implemented in this build.
package attach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fsvxavier/accordion-context/internal/launcher"
	"github.com/fsvxavier/accordion-context/internal/project"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// Degree names, as reported in Result.Degree and Attempt.Degree.
const (
	DegreeBaseURL = "base-url-env"
	DegreeConsole = "console"
	DegreeMITM    = "mitm"
)

// ErrNotImplemented is wrapped by the degrees this build does not provide.
var ErrNotImplemented = errors.New("not implemented in this build")

const defaultProbeTimeout = 5 * time.Second

// Options configures one attach run. Args is required.
type Options struct {
	// Args is the client command. Args[0] selects a profile by name; an
	// unmatched command runs in console mode.
	Args []string
	// Cwd is the project directory. Empty means the process working directory.
	Cwd string
	// Markers are the project root markers. Nil means the proxy defaults.
	Markers []string
	// Profiles are extra declarative profiles (launcher.LoadProfiles). They
	// are matched before the built-in profiles.
	Profiles []launcher.Profile
	// DBPath is the SQLite database of the in-process proxy. Required.
	DBPath string
	// CADir holds the per-install CA. Required for the mitm degree.
	CADir string
	// Version is reported by the in-process proxy.
	Version string
	// MITMDomains are extra exact host names to intercept. The upstream hosts
	// of every profile are always included.
	MITMDomains []string
	// SystemProxy and Transparent request degrees this build does not have.
	SystemProxy bool
	Transparent bool
	// Environ is the base environment. Nil means os.Environ().
	Environ []string
	// Stdin, Stdout and Stderr are the client's streams. Nil uses the
	// os/exec defaults (no input, discarded output).
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	// Dial reaches upstream hosts and blind tunnels. Nil means the default
	// dialer with the mitm loopback guard. Tests set it to stay offline.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// ProbeTimeout bounds each probe. Zero means 5s.
	ProbeTimeout time.Duration
	// Logger receives proxy diagnostics. Nil discards them.
	Logger *slog.Logger

	skip map[string]bool // degrees to skip; set by tests
}

// Attempt records one degree of the ladder.
type Attempt struct {
	Degree string
	OK     bool
	Reason string // "verified" on success, otherwise why the degree failed
}

// Result reports the chosen degree and the outcome of the client run.
type Result struct {
	Degree   string
	Attempts []Attempt
	ExitCode int
	// Hits counts client requests that reached the proxy handler.
	Hits  int64
	Notes []string
}

// Run verifies the ladder, starts the client once with the first working
// degree, and returns the client's exit code. An error means that setup failed
// or no degree could be verified, and then the client is not started.
func Run(ctx context.Context, o Options) (Result, error) {
	if o.SystemProxy {
		return Result{}, fmt.Errorf("attach: --system-proxy %w", ErrNotImplemented)
	}
	if o.Transparent {
		return Result{}, fmt.Errorf("attach: --transparent %w", ErrNotImplemented)
	}
	if len(o.Args) == 0 {
		return Result{}, errors.New("attach: no client command")
	}
	if o.DBPath == "" {
		return Result{}, errors.New("attach: database path is required")
	}
	if o.ProbeTimeout <= 0 {
		o.ProbeTimeout = defaultProbeTimeout
	}
	extra, err := normalizeDomains(o.MITMDomains)
	if err != nil {
		return Result{}, err
	}
	cwd := o.Cwd
	if cwd == "" {
		if cwd, err = os.Getwd(); err != nil {
			return Result{}, fmt.Errorf("attach: working directory: %w", err)
		}
	}
	pr, err := project.ResolveWith(cwd, o.Markers, project.Options{ReadOnly: true})
	if err != nil {
		return Result{}, fmt.Errorf("attach: resolve project: %w", err)
	}

	sc, err := newScope(o, extra)
	if err != nil {
		return Result{}, err
	}
	sc.projID = pr.ID
	sc.dir = cwd
	if len(sc.runHosts) == 0 {
		return Result{}, fmt.Errorf("attach: no host to intercept for %q; pass --mitm-domain", o.Args[0])
	}

	tmp, err := os.MkdirTemp("", "acd-ctx-attach-")
	if err != nil {
		return Result{}, fmt.Errorf("attach: temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	sc.tmp = tmp

	if err := os.MkdirAll(filepath.Dir(o.DBPath), 0o700); err != nil {
		return Result{}, fmt.Errorf("attach: database directory: %w", err)
	}
	st, err := store.Open(o.DBPath)
	if err != nil {
		return Result{}, fmt.Errorf("attach: open database: %w", err)
	}
	defer st.Close()

	logger := o.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	rt, err := startRuntime(o, st, logger, sc)
	if err != nil {
		return Result{}, err
	}
	defer rt.close()
	sc.rt = rt

	chosen, attempts, ok := sc.climb(ctx)
	out := Result{Attempts: attempts}
	if !ok {
		return out, fmt.Errorf("attach: no integration degree could be verified for %q: %s", o.Args[0], summarize(attempts))
	}
	out.Degree = chosen.degree
	if chosen.usesMITM {
		out.Notes = append(out.Notes, sc.mitm.notes...)
	}

	code, err := runChild(ctx, o, cwd, buildEnv(o.Environ, chosen.env))
	out.ExitCode = code
	out.Hits = rt.gate.hits.Load()
	if err != nil {
		return out, err
	}
	if out.Hits == 0 {
		out.Notes = append(out.Notes, "no client request reached the proxy during the run; the client may ignore this integration (see acd-ctx doctor)")
	}
	return out, nil
}

// scope is the per-run plan: which profile matched, which variables and hosts
// the degrees use, and the lazily prepared MITM state.
type scope struct {
	o        Options
	matched  bool
	prof     launcher.Profile
	projID   string
	upstream *url.URL           // default upstream of the in-process proxy
	targets  []launcher.Profile // profiles whose base URL variable is set
	caHosts  []string           // stable set: the CA's name constraints
	runHosts []string           // intercepted for this run
	tmp      string
	dir      string // project directory handed to the proxy as a header
	rt       *runtime

	mitmDone bool
	mitm     *mitmState
	mitmErr  error
}

// newScope matches the command to a profile and collects the hosts.
func newScope(o Options, extra []string) (*scope, error) {
	sc := &scope{o: o}
	profiles := append(slices.Clone(o.Profiles), launcher.BuiltinProfiles()...)
	sc.prof, sc.matched = launcher.MatchProfile(profiles, o.Args[0])

	hosts := slices.Clone(extra)
	for _, p := range profiles {
		h, err := upstreamHost(p)
		if err != nil {
			return nil, err
		}
		if h == "" {
			continue
		}
		hosts = append(hosts, h)
		if sc.upstream == nil {
			sc.upstream, _ = url.Parse(p.Upstream)
		}
	}
	sc.caHosts = uniqueSorted(hosts)

	if sc.matched {
		if sc.prof.BaseURLEnv != "" {
			sc.targets = []launcher.Profile{sc.prof}
		}
		own, _ := upstreamHost(sc.prof)
		sc.runHosts = uniqueSorted(append([]string{own}, extra...))
	} else {
		seen := map[string]bool{}
		for _, p := range profiles {
			if p.BaseURLEnv != "" && !seen[p.BaseURLEnv] {
				seen[p.BaseURLEnv] = true
				sc.targets = append(sc.targets, p)
			}
		}
		sc.runHosts = sc.caHosts
	}
	return sc, nil
}

// climb tries the degrees in order and returns the first verified plan.
func (sc *scope) climb(ctx context.Context) (plan, []Attempt, bool) {
	var attempts []Attempt
	try := func(p plan) bool {
		att := Attempt{Degree: p.degree}
		if sc.o.skip[p.degree] {
			att.Reason = "skipped"
		} else {
			att.Reason, att.OK = runProbes(ctx, p)
		}
		attempts = append(attempts, att)
		return att.OK
	}

	if len(sc.targets) > 0 {
		if sc.matched {
			if p := sc.baseURLPlan(); try(p) {
				return p, attempts, true
			}
		} else {
			p := sc.consolePlan()
			if try(p) {
				return p, attempts, true
			}
		}
	}

	m, err := sc.getMITM()
	if err != nil {
		attempts = append(attempts, Attempt{Degree: DegreeMITM, Reason: err.Error()})
		return plan{}, attempts, false
	}
	if p := sc.mitmPlan(m); try(p) {
		return p, attempts, true
	}
	return plan{}, attempts, false
}

// baseURLPlan points the matched profile's base URL variable at the proxy.
func (sc *scope) baseURLPlan() plan {
	env, probes := sc.baseProbes()
	return plan{degree: DegreeBaseURL, env: env, probes: probes, all: true}
}

// consolePlan exports every known base URL variable, plus the MITM variables
// when the MITM state can be prepared. One working path is enough.
func (sc *scope) consolePlan() plan {
	env, probes := sc.baseProbes()
	p := plan{degree: DegreeConsole, env: env, probes: probes}
	if m, err := sc.getMITM(); err == nil {
		p.env = append(p.env, m.env...)
		p.probes = append(p.probes, sc.mitmProbe(m))
		p.usesMITM = true
	}
	return p
}

// mitmPlan trusts the CA and routes HTTPS through the listener, for this
// process only.
func (sc *scope) mitmPlan(m *mitmState) plan {
	return plan{degree: DegreeMITM, env: m.env, all: true, usesMITM: true, probes: []probe{sc.mitmProbe(m)}}
}

func (sc *scope) baseProbes() ([]kv, []probe) {
	var env []kv
	var ps []probe
	for _, t := range sc.targets {
		base := launcher.BaseURL(sc.rt.addr, sc.projID, t)
		env = append(env, kv{t.BaseURLEnv, base})
		ps = append(ps, probe{name: t.BaseURLEnv, run: func(ctx context.Context) error {
			return sc.rt.probeBase(ctx, base, sc.o.ProbeTimeout)
		}})
	}
	return env, ps
}

func (sc *scope) mitmProbe(m *mitmState) probe {
	host := sc.runHosts[0]
	return probe{name: "mitm " + host, run: func(ctx context.Context) error {
		return sc.rt.probeMITM(ctx, m, host, sc.o.ProbeTimeout)
	}}
}

// getMITM prepares the CA, the bundle and the MITM server once per run.
func (sc *scope) getMITM() (*mitmState, error) {
	if !sc.mitmDone {
		sc.mitmDone = true
		sc.mitm, sc.mitmErr = sc.rt.setupMITM(sc)
	}
	return sc.mitm, sc.mitmErr
}

// upstreamHost returns the lower-case host of a profile's upstream, or "".
func upstreamHost(p launcher.Profile) (string, error) {
	if p.Upstream == "" {
		return "", nil
	}
	u, err := url.Parse(p.Upstream)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("attach: profile %q: invalid upstream %q", p.Name, p.Upstream)
	}
	return strings.ToLower(u.Hostname()), nil
}

// normalizeDomains checks the extra MITM domains: exact names only, because
// the proxy allowlist is exact origins.
func normalizeDomains(in []string) ([]string, error) {
	var out []string
	for _, d := range in {
		t := strings.ToLower(strings.TrimSpace(d))
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "*.") || strings.ContainsAny(t, "/: ") {
			return nil, fmt.Errorf("attach: MITM domain %q must be an exact host name", d)
		}
		out = append(out, t)
	}
	return out, nil
}
