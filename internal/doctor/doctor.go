// Package doctor checks an Accordion Context installation and, optionally, the
// integration of one client (spec §19.8). Every check is read-only: a missing
// database or CA is reported, never created.
package doctor

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fsvxavier/accordion-context/internal/config"
	"github.com/fsvxavier/accordion-context/internal/launcher"
	"github.com/fsvxavier/accordion-context/internal/mitm"
	"github.com/fsvxavier/accordion-context/internal/project"
	"github.com/fsvxavier/accordion-context/internal/store"
	"github.com/fsvxavier/accordion-context/internal/sysproxy"
)

// Status is the outcome of one check or of the whole report.
type Status string

const (
	Pass Status = "PASS"
	Warn Status = "WARN"
	Fail Status = "FAIL"
)

// Exit codes of a report. A failing check wins over warnings.
const (
	ExitPass = 0
	ExitFail = 1
	ExitWarn = 3
)

// Check is one named result.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
}

// Report is the outcome of Run.
type Report struct {
	Checks   []Check `json:"checks"`
	Summary  Status  `json:"summary"`
	ExitCode int     `json:"exit_code"`
}

// Options selects what Run inspects. Empty fields skip the related checks.
type Options struct {
	// Cwd is the project directory. Empty means the process working directory.
	Cwd     string
	Markers []string
	// GlobalConfigPath is the global configuration file. Empty means the
	// default user path.
	GlobalConfigPath string
	// DBPath is the global database. It is opened only when it exists.
	DBPath string
	// CADir is the per-install CA directory.
	CADir string
	// ProxyAddr is the loopback address to probe for a running proxy.
	ProxyAddr string
	// Version is the version a running proxy should report. Empty accepts any.
	Version string
	// Client is the command the integration is checked for. Empty skips that check.
	Client string
	// Profiles are extra declarative profiles, matched before the built-ins.
	Profiles []launcher.Profile
	// Env is the environment to inspect. Nil means os.Environ().
	Env []string
	// Rejections reports client refusals of the CA from a live MITM server.
	// Nil means no server is in this process, and the check says so.
	Rejections func() []mitm.Rejection
	// HTTPClient performs the loopback health requests. Nil means a 2s timeout.
	HTTPClient *http.Client
	// Now is the clock for expiry checks. Nil means time.Now.
	Now func() time.Time
	// SysRunner runs the read-only firewall probes (nft, iptables, ip6tables).
	// Nil uses SystemRunner, and nil there skips the probes.
	SysRunner sysproxy.Runner
	// ProcRoot is the /proc that watchdog helpers are looked up in. Empty means /proc.
	ProcRoot string
}

// Run performs every check and returns the report. The error is reserved for
// failures that stop the run itself; a failed check is reported in the Report.
func Run(ctx context.Context, o Options) (Report, error) {
	env := o.Env
	if env == nil {
		env = os.Environ()
	}
	vars := envMap(env)
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	client := o.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	profiles := append(append([]launcher.Profile{}, o.Profiles...), launcher.BuiltinProfiles()...)

	var r Report
	add := func(cs ...Check) { r.Checks = append(r.Checks, cs...) }

	root := ""
	cwd := o.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if res, err := project.ResolveWith(cwd, o.Markers, project.Options{ReadOnly: true}); err != nil {
		add(Check{"project", Fail, err.Error()})
	} else {
		root = res.Root
		add(Check{"project", Pass, fmt.Sprintf("id %s, root %s, storage %s", res.ID, res.Root, res.Storage)})
	}

	add(checkDatabase(ctx, o.DBPath))
	add(checkConfig(o.GlobalConfigPath, root, vars))
	add(checkProxy(ctx, client, o.ProxyAddr, o.Version))
	add(checkCAFiles(o.CADir, now())...)
	add(checkCAKey(o.CADir))
	add(checkCAEnv(vars))
	add(checkRejections(o.Rejections, now()))
	add(checkForeignBaseURLs(ctx, client, vars, o.ProxyAddr, profiles))
	add(checkNoProxy(vars, profiles))
	add(checkIntegration(o.Client, profiles))
	add(checkSystemLeftovers(o.DBPath, o.procRoot(), o.sysRunner()))

	r.Summary = Pass
	for _, c := range r.Checks {
		switch {
		case c.Status == Fail:
			r.Summary = Fail
		case c.Status == Warn && r.Summary == Pass:
			r.Summary = Warn
		}
	}
	switch r.Summary {
	case Fail:
		r.ExitCode = ExitFail
	case Warn:
		r.ExitCode = ExitWarn
	default:
		r.ExitCode = ExitPass
	}
	return r, nil
}

// Write prints a report as text, one check per line, then the summary.
func Write(w io.Writer, r Report) {
	for _, c := range r.Checks {
		fmt.Fprintf(w, "%-4s %-20s %s\n", c.Status, c.Name, c.Detail)
	}
	fmt.Fprintf(w, "\nsummary: %s\n", r.Summary)
}

// WriteJSON prints a report as indented JSON.
func WriteJSON(w io.Writer, r Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func checkDatabase(ctx context.Context, path string) Check {
	const name = "database"
	if path == "" {
		return Check{name, Warn, "no database path given"}
	}
	if _, err := os.Stat(path); err != nil {
		return Check{name, Warn, "no database yet (the first proxy run creates it)"}
	}
	st, err := store.Open(path)
	if err != nil {
		return Check{name, Fail, "open: " + err.Error()}
	}
	defer st.Close()
	projects, err := st.ListProjects(ctx)
	if err != nil {
		return Check{name, Fail, "list projects: " + err.Error()}
	}
	sessions := 0
	var broken []string
	for _, p := range projects {
		ids, err := st.ListSessionIDs(ctx, p.ID)
		if err != nil {
			return Check{name, Fail, fmt.Sprintf("list sessions of %s: %v", p.ID, err)}
		}
		for _, sid := range ids {
			sessions++
			if err := st.CheckInvariants(ctx, sid); err != nil {
				broken = append(broken, sid)
			}
		}
	}
	if len(broken) > 0 {
		return Check{name, Fail, fmt.Sprintf("invariant violations in %d session(s): %s", len(broken), strings.Join(broken, ", "))}
	}
	return Check{name, Pass, fmt.Sprintf("%d project(s), %d session(s), invariants hold", len(projects), sessions)}
}

func checkConfig(path, root string, vars map[string]string) Check {
	const name = "config"
	res, err := config.Load(config.Options{GlobalPath: path, Root: root, LookupEnv: func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}})
	if err != nil {
		return Check{name, Fail, err.Error()}
	}
	if len(res.Warnings) > 0 {
		return Check{name, Warn, strings.Join(res.Warnings, "; ")}
	}
	return Check{name, Pass, fmt.Sprintf("%d keys loaded, no warnings", len(res.Effective()))}
}

func checkProxy(ctx context.Context, client *http.Client, addr, version string) Check {
	const name = "proxy"
	if addr == "" {
		return Check{name, Warn, "no proxy address given"}
	}
	if !isLoopbackAddr(addr) {
		return Check{name, Fail, fmt.Sprintf("%s is not a loopback address; refusing to probe it", addr)}
	}
	var health struct {
		OK bool `json:"ok"`
	}
	if !getJSON(ctx, client, addr, "/__acd-ctx/health", &health) || !health.OK {
		return Check{name, Warn, fmt.Sprintf("no proxy answering on %s (launchers start one on demand)", addr)}
	}
	var status struct {
		Version string `json:"version"`
	}
	if !getJSON(ctx, client, addr, "/__acd-ctx/status", &status) {
		return Check{name, Warn, fmt.Sprintf("proxy on %s is healthy but reports no status", addr)}
	}
	if version != "" && status.Version != version {
		return Check{name, Warn, fmt.Sprintf("proxy on %s runs version %q, expected %q", addr, status.Version, version)}
	}
	return Check{name, Pass, fmt.Sprintf("proxy on %s healthy, version %q", addr, status.Version)}
}

// checkCAFiles covers the CA certificate and its directory.
func checkCAFiles(dir string, now time.Time) []Check {
	const cname, dname = "ca-cert", "ca-dir"
	if dir == "" {
		return []Check{{cname, Warn, "no CA directory given"}}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return []Check{{cname, Warn, "no CA yet (the first MITM run creates it)"}, {dname, Pass, "absent"}}
	}
	dcheck := Check{dname, Pass, fmt.Sprintf("mode %o", info.Mode().Perm())}
	if info.Mode().Perm() != 0o700 {
		dcheck = Check{dname, Warn, fmt.Sprintf("mode %o, want 0700", info.Mode().Perm())}
	}
	data, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return []Check{{cname, Warn, "no CA certificate yet"}, dcheck}
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return []Check{{cname, Fail, "ca.crt is not a PEM certificate"}, dcheck}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return []Check{{cname, Fail, "parse ca.crt: " + err.Error()}, dcheck}
	}
	left := cert.NotAfter.Sub(now)
	ccheck := Check{cname, Pass, fmt.Sprintf("valid until %s", cert.NotAfter.UTC().Format(time.RFC3339))}
	switch {
	case left <= 0:
		ccheck = Check{cname, Fail, "expired at " + cert.NotAfter.UTC().Format(time.RFC3339) + "; the next MITM run regenerates it"}
	case left < 30*24*time.Hour:
		ccheck = Check{cname, Warn, fmt.Sprintf("expires in %d day(s); the next MITM run regenerates it", int(left.Hours()/24))}
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.prev.crt")); err == nil {
		ccheck.Detail += "; previous CA kept in the bundle for its grace period"
	}
	return []Check{ccheck, dcheck}
}

// checkCAKey verifies the key file's mode and that it matches the certificate.
func checkCAKey(dir string) Check {
	const name = "ca-key"
	if dir == "" {
		return Check{name, Warn, "no CA directory given"}
	}
	path := filepath.Join(dir, "ca.key")
	info, err := os.Lstat(path)
	if err != nil {
		return Check{name, Warn, "no CA key yet"}
	}
	if !info.Mode().IsRegular() {
		return Check{name, Fail, "ca.key is not a regular file"}
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Check{name, Warn, fmt.Sprintf("mode %o, want 0600 (the next run tightens it)", info.Mode().Perm())}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Check{name, Fail, err.Error()}
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return Check{name, Fail, "ca.key is not a PEM PKCS#8 key"}
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return Check{name, Fail, "parse ca.key: invalid key data"}
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return Check{name, Fail, "ca.key is not an ECDSA key"}
	}
	cdata, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return Check{name, Warn, "ca.key present but ca.crt unreadable; cannot confirm they match"}
	}
	cb, _ := pem.Decode(cdata)
	if cb == nil {
		return Check{name, Fail, "ca.crt is not PEM; cannot confirm the key matches"}
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return Check{name, Fail, "ca.crt does not parse; cannot confirm the key matches"}
	}
	if !key.PublicKey.Equal(cert.PublicKey) {
		return Check{name, Fail, "ca.key does not match ca.crt"}
	}
	return Check{name, Pass, "private key matches the certificate, mode 0600"}
}

// mitmCAVars are the CA variables the MITM degree sets for the client process.
var mitmCAVars = []string{"SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE"}

func checkCAEnv(vars map[string]string) Check {
	const name = "ca-env"
	set := mitm.ClientSupport(vars)
	if len(set) == 0 {
		return Check{name, Pass, "no CA variable in the environment; the MITM degree sets " + strings.Join(mitmCAVars, ", ")}
	}
	return Check{name, Pass, "already set: " + strings.Join(set, ", ") + "; the MITM degree overrides them for the client process only"}
}

func checkRejections(fn func() []mitm.Rejection, now time.Time) Check {
	const name = "ca-rejections"
	if fn == nil {
		return Check{name, Pass, "not inspected: the MITM listener lives in an attach run, not in this process"}
	}
	rej := fn()
	if len(rej) == 0 {
		return Check{name, Pass, "no client refused the CA"}
	}
	var parts []string
	failing := false
	for _, r := range rej {
		parts = append(parts, fmt.Sprintf("%s %s x%d", r.Host, r.Reason, r.Count))
		if now.Sub(r.Last) < 24*time.Hour {
			failing = true
		}
	}
	sort.Strings(parts)
	if failing {
		return Check{name, Fail, "client refused the acd-ctx CA in the last 24h: " + strings.Join(parts, "; ")}
	}
	return Check{name, Warn, "client refused the acd-ctx CA earlier: " + strings.Join(parts, "; ")}
}

// checkForeignBaseURLs finds base URL variables that point somewhere other
// than the proxy, and double compression through another acd-ctx.
func checkForeignBaseURLs(ctx context.Context, client *http.Client, vars map[string]string, proxyAddr string, profiles []launcher.Profile) Check {
	const name = "base-urls"
	var foreign, double []string
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !isBaseURLVar(k) {
			continue
		}
		raw := strings.TrimSpace(vars[k])
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			foreign = append(foreign, k+" (unparseable)")
			continue
		}
		if !isLoopbackHost(u.Hostname()) {
			foreign = append(foreign, fmt.Sprintf("%s -> %s", k, u.Host))
			continue
		}
		if u.Host == proxyAddr || !(strings.Contains(u.Path, "/ctx/") || strings.HasPrefix(u.Path, "/p/")) {
			continue
		}
		var health struct {
			OK bool `json:"ok"`
		}
		if getJSON(ctx, client, u.Host, "/__acd-ctx/health", &health) && health.OK {
			double = append(double, fmt.Sprintf("%s -> another acd-ctx at %s", k, u.Host))
		}
	}
	if native := vars["ACD_CTX_NATIVE"]; strings.TrimSpace(native) != "" {
		double = append(double, "ACD_CTX_NATIVE="+native+" (a native plugin also compacts)")
	}
	var parts []string
	status := Pass
	if len(double) > 0 {
		status = Warn
		parts = append(parts, "double compression risk: "+strings.Join(double, "; "))
	}
	if len(foreign) > 0 {
		status = Warn
		parts = append(parts, "foreign base URL (bypasses acd-ctx unless attach overrides it): "+strings.Join(foreign, "; "))
	}
	if len(parts) == 0 {
		return Check{name, Pass, "no foreign base URL, no double compression"}
	}
	return Check{name, status, strings.Join(parts, " | ")}
}

// checkNoProxy warns when NO_PROXY would send an intercepted host around the
// proxy.
func checkNoProxy(vars map[string]string, profiles []launcher.Profile) Check {
	const name = "no-proxy"
	noProxy := vars["NO_PROXY"] + "," + vars["no_proxy"]
	var hit []string
	for _, p := range profiles {
		if p.Upstream == "" {
			continue
		}
		u, err := url.Parse(p.Upstream)
		if err != nil {
			continue
		}
		host := strings.ToLower(u.Hostname())
		for _, e := range strings.Split(strings.ToLower(noProxy), ",") {
			e = strings.TrimSpace(e)
			if e == "*" {
				hit = append(hit, host)
				continue
			}
			e = strings.TrimPrefix(e, "*")
			if i := strings.LastIndex(e, ":"); i >= 0 && !strings.Contains(e[i:], "]") {
				e = e[:i] // an entry with a port still names the host
			}
			if e != "" && (e == host || strings.HasSuffix(host, "."+strings.TrimPrefix(e, "."))) {
				hit = append(hit, host)
			}
		}
	}
	if len(hit) == 0 {
		return Check{name, Pass, "no intercepted host is in NO_PROXY"}
	}
	return Check{name, Warn, "NO_PROXY lists " + strings.Join(uniqueStrings(hit), ", ") + "; HTTPS to these hosts would bypass the MITM degree"}
}

func checkIntegration(client string, profiles []launcher.Profile) Check {
	const name = "integration"
	if client == "" {
		var vars []string
		for _, p := range profiles {
			if p.BaseURLEnv != "" {
				vars = append(vars, p.BaseURLEnv)
			}
		}
		return Check{name, Pass, fmt.Sprintf("%d known base URL variable(s); console mode exports them all", len(uniqueStrings(vars)))}
	}
	base := filepath.Base(client)
	p, ok := launcher.MatchProfile(profiles, base)
	if !ok {
		return Check{name, Warn, fmt.Sprintf("no profile for %q: attach uses console mode (every known base URL variable plus MITM)", client)}
	}
	if p.IgnoresBaseURL() {
		return Check{name, Pass, fmt.Sprintf("profile %q ignores base URLs (quirks.ignores_base_url): the mitm degree applies", p.Name)}
	}
	var degrees []string
	if p.BaseURLEnv != "" {
		degrees = append(degrees, "base-url-env via "+p.BaseURLEnv)
	}
	if p.BaseURLFlag != "" {
		degrees = append(degrees, "base-url-flag")
	}
	if p.TempConfig != nil {
		degrees = append(degrees, "temp-config")
	}
	if len(degrees) == 0 {
		return Check{name, Pass, fmt.Sprintf("profile %q has no base URL setting: the mitm degree applies", p.Name)}
	}
	return Check{name, Pass, fmt.Sprintf("profile %q: %s, mitm degree as fallback", p.Name, strings.Join(degrees, ", "))}
}

func isBaseURLVar(k string) bool {
	return strings.HasSuffix(k, "_BASE_URL") || strings.HasSuffix(k, "_API_BASE") || k == "API_BASE"
}

func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackAddr(addr string) bool {
	h, _, err := net.SplitHostPort(addr)
	return err == nil && isLoopbackHost(h)
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

// envMap returns the environment as a map, the last assignment wins.
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// SystemRunner gives the read-only firewall probes a command runner when
// Options.SysRunner is nil. The CLI sets it; without one the probes are skipped
// and the check says so.
var SystemRunner func() sysproxy.Runner

// sysRunner returns the runner of the firewall probes, or nil.
func (o Options) sysRunner() sysproxy.Runner {
	if o.SysRunner != nil {
		return o.SysRunner
	}
	if SystemRunner != nil {
		return SystemRunner()
	}
	return nil
}

// procRoot is the /proc used to find watchdog helpers. Empty means /proc.
func (o Options) procRoot() string {
	if o.ProcRoot != "" {
		return o.ProcRoot
	}
	return "/proc"
}

// checkSystemLeftovers reports the leftovers of the system-proxy and transparent
// degrees (spec §19.9, degrees 6 and 7): state files, the watchdog helpers that
// serve them, and nft tables or iptables chains that are present. Any of them is
// a WARN. A live owner means a change is in effect now; a dead owner means a
// crashed run left settings behind. doctor never changes anything itself: it names
// the command that restores it.
func checkSystemLeftovers(dbPath, procRoot string, r sysproxy.Runner) Check {
	const name = "system proxy"
	if dbPath == "" {
		return Check{name, Pass, "not checked: no database path"}
	}
	left, err := sysproxy.Leftovers(sysproxy.StateDir(dbPath), sysproxy.Env{})
	if err != nil {
		return Check{name, Warn, "state files unreadable: " + err.Error() + "; inspect the sysproxy directory by hand"}
	}
	var problems []string
	hasProxy := false
	for _, l := range left {
		state := "stale (owner pid " + strconv.Itoa(l.PID) + " is gone)"
		if l.Alive {
			state = "active (owner pid " + strconv.Itoa(l.PID) + " is running)"
			if l.Mode == sysproxy.ModeTransparent && len(sysproxy.WatchdogPIDs(procRoot, l.Path)) == 0 {
				state += ", but no watchdog is running: a crash would leave the redirect in place"
			}
		} else if l.Mode == sysproxy.ModeTransparent {
			if pids := sysproxy.WatchdogPIDs(procRoot, l.Path); len(pids) > 0 {
				state += ", watchdog pid " + strconv.Itoa(pids[0]) + " is still running and restores it"
			} else {
				state += ", no watchdog running"
			}
		}
		if l.Mode == sysproxy.ModeSystemProxy {
			hasProxy = true
		}
		problems = append(problems, l.Mode+" "+state+" "+l.Path)
	}
	note := ""
	if r == nil {
		note = "firewall tables not checked: no command runner"
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		problems = append(problems, sysproxy.FirewallLeftovers(ctx, r)...)
		cancel()
	}
	if len(problems) == 0 {
		detail := "no system proxy or transparent redirect is left in place"
		if note != "" {
			detail += "; " + note
		}
		return Check{name, Pass, detail}
	}
	detail := strings.Join(problems, "; ")
	if len(left) > 0 {
		detail += "; restore with: acd-ctx attach --restore"
	}
	if hasProxy {
		detail += "; while the system proxy is set, apps that do not trust the acd-ctx CA fail on the MITM domains"
	}
	return Check{name, Warn, detail}
}
