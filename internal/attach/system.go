package attach

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsvxavier/accordion-context/internal/store"
	"github.com/fsvxavier/accordion-context/internal/sysproxy"
)

// SystemOptions configures the machine-wide degrees (spec §19.9, degrees 6 and
// 7). Both are off by default. They change settings that other programs use,
// so both refuse without Yes, and the state they change is saved and restored.
type SystemOptions struct {
	// Yes is the explicit consent (--yes). Required for both degrees.
	Yes bool
	// Apply installs the transparent rules. Without it they are only printed.
	Apply bool
	// UID is the user whose TCP 443 traffic is redirected. A negative value
	// means SUDO_UID from the environment.
	UID int
	// AllDestinations redirects all TCP 443 traffic of UID, not only the
	// connections to the MITM domains (--all-443).
	AllDestinations bool
	// NoPeerAuth is --i-understand-no-peer-auth. It is needed on platforms
	// without a peer-uid check, where any local process can use the listener.
	NoPeerAuth bool
	// DataDirExplicit says the data directory was chosen by the user
	// (ACD_CTX_DB) instead of derived from HOME. It is one of the two things
	// that make --transparent under sudo safe when HOME is not the user's.
	DataDirExplicit bool
	// StateDir holds the state files. Empty means "sysproxy" beside the database.
	StateDir string
	// Env gives access to commands and to the machine. It is required, and
	// the CLI sets the real runner in it.
	Env *sysproxy.Env
}

// runSystem runs the system-proxy or transparent degree. Without a command it
// holds the change until an interrupt. With a command (system-proxy only) it
// runs the command while the change is in place.
func runSystem(ctx context.Context, o Options) (res Result, err error) {
	flag := "--system-proxy"
	degree := DegreeSystemProxy
	if o.SystemProxy && o.Transparent {
		return res, errors.New("attach: choose one of --system-proxy and --transparent")
	}
	if o.Transparent {
		flag, degree = "--transparent", DegreeTransparent
	}
	res.Degree = degree
	sys := o.System
	if sys == nil || !sys.Yes {
		return res, fmt.Errorf("attach: %s needs --yes: it changes machine-wide network settings until this run ends", flag)
	}
	if sys.Env == nil || sys.Env.Runner == nil {
		return res, errors.New("attach: no command runner configured for the system degrees")
	}
	env := *sys.Env
	if err := checkSudoHome(env, sys, o.Transparent); err != nil {
		return res, err
	}
	if o.Transparent && len(o.Args) > 0 {
		return res, errors.New("attach: --transparent does not run a command; run the client yourself as the target uid")
	}
	if o.DBPath == "" {
		return res, errors.New("attach: database path is required")
	}
	cwd := o.Cwd
	if cwd == "" {
		if cwd, err = os.Getwd(); err != nil {
			return res, fmt.Errorf("attach: working directory: %w", err)
		}
	}
	dir := sys.StateDir
	if dir == "" {
		dir = sysproxy.StateDir(o.DBPath)
	}

	// With no client command every known host is in scope, as in console mode.
	oo := o
	if len(oo.Args) == 0 {
		oo.Args = []string{""}
	}
	extra, err := normalizeDomains(o.MITMDomains)
	if err != nil {
		return res, err
	}
	sc, err := newScope(oo, extra)
	if err != nil {
		return res, err
	}
	sc.dir = cwd
	if len(sc.runHosts) == 0 {
		return res, errors.New("attach: no host to intercept; pass --mitm-domain")
	}
	for _, h := range sc.runHosts {
		if !validHostName(h) {
			return res, fmt.Errorf("attach: host %q to intercept is not a plain host name", h)
		}
	}
	tmp, err := os.MkdirTemp(o.tmpRoot, "acd-ctx-system-")
	if err != nil {
		return res, fmt.Errorf("attach: temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	sc.tmp = tmp

	if err := os.MkdirAll(filepath.Dir(o.DBPath), 0o700); err != nil {
		return res, fmt.Errorf("attach: database directory: %w", err)
	}
	st, err := store.Open(o.DBPath)
	if err != nil {
		return res, fmt.Errorf("attach: open database: %w", err)
	}
	defer st.Close()

	logger := o.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if o.Transparent {
		return runTransparent(ctx, o, env, sc, st, logger, dir)
	}
	return runSystemProxy(ctx, o, env, sc, st, logger, dir)
}

// systemListener opens the loopback listener of a system degree. On Linux the
// peer uid is checked; elsewhere the user must accept that no check happens.
// The returned runtime is in system mode and sets up the MITM server.
func systemListener(o Options, env sysproxy.Env, sc *scope, st *store.Store, logger *slog.Logger) (*runtime, *mitmState, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("attach: listen: %w", err)
	}
	ap, ok := netAddrPortOf(ln.Addr())
	if !ok {
		_ = ln.Close()
		return nil, nil, errors.New("attach: listener is not IPv4")
	}
	var check func(netip.AddrPort) error
	switch {
	case envGOOS(env) == "linux":
		check = sysproxy.LinuxPeerCheck(env.ProcRoot, ap, os.Getuid())
	case o.System.NoPeerAuth:
		check = nil
	default:
		_ = ln.Close()
		return nil, nil, errors.New("attach: peer-uid authentication is not available on this platform; " +
			"pass --i-understand-no-peer-auth to accept any local process that can reach 127.0.0.1")
	}
	rt, err := startRuntimeOn(o, st, logger, sc, sysproxy.PeerListener{Listener: ln, Check: check}, true)
	if err != nil {
		return nil, nil, err
	}
	m, err := rt.setupMITM(sc)
	if err != nil {
		rt.close()
		return nil, nil, err
	}
	if err := rt.probeMITM(context.Background(), m, sc.runHosts[0], defaultProbeTimeout); err != nil {
		rt.close()
		return nil, nil, fmt.Errorf("attach: the listener failed its own probe: %w", err)
	}
	return rt, m, nil
}

// runSystemProxy points the system HTTPS proxy at the listener for the length
// of the run and restores it on every exit path.
func runSystemProxy(ctx context.Context, o Options, env sysproxy.Env, sc *scope, st *store.Store, logger *slog.Logger, dir string) (res Result, err error) {
	res.Degree = DegreeSystemProxy
	// The signals are held from before the first change until after the
	// restore (defers run last-in first-out), so SIGHUP cannot kill the run
	// between the change and its restore.
	hctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	rt, _, err := systemListener(o, env, sc, st, logger)
	if err != nil {
		return res, err
	}
	defer rt.close()
	port := rt.port()

	ep := sysproxy.Endpoint{Host: "127.0.0.1", Port: port, Bypass: sysproxy.DefaultBypass}
	actx, cancel := context.WithTimeout(hctx, 60*time.Second)
	act, err := sysproxy.StartSystemProxy(actx, env, dir, ep)
	cancel()
	if err != nil {
		return res, fmt.Errorf("attach: system proxy: %w", err)
	}
	defer func() {
		rctx, rcancel := sysproxy.RestoreContext()
		defer rcancel()
		notes, serr := act.Stop(rctx)
		res.Notes = append(res.Notes, notes...)
		if serr != nil {
			err = errors.Join(err, fmt.Errorf("attach: restore system proxy: %w (state kept; run acd-ctx attach --restore)", serr))
		}
	}()
	res.Notes = append(res.Notes,
		"the acd-ctx CA is not installed in the system trust store: apps that do not trust it fail on the MITM domains (acd-ctx doctor reports this)",
		"only the HTTPS proxy is set; other traffic and the listed domains are tunnelled without inspection, except the MITM domains")
	fmt.Fprintf(o.stderr(), "acd-ctx: system HTTPS proxy set to 127.0.0.1:%d (restored when this run ends)\n", port)

	if len(o.Args) == 0 {
		fmt.Fprintln(o.stderr(), "acd-ctx: the system proxy stays set until you interrupt this run (Ctrl-C restores it)")
		<-hctx.Done()
		res.Hits = rt.gate.hits.Load()
		return res, nil
	}
	if hctx.Err() != nil {
		return res, nil // interrupted while the change was being made
	}
	cctx, cancelChild := context.WithCancel(ctx)
	defer cancelChild()
	rl := newRelay(o.signals, cancelChild)
	defer rl.close()
	cwd := o.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	code, err := runChild(cctx, o, rl, o.Args, cwd, buildEnv(o.Environ, nil))
	res.ExitCode = code
	res.Hits = rt.gate.hits.Load()
	return res, err
}

// runTransparent redirects TCP 443 of one uid to the listeners. Without --apply
// it only prints the rules that would be installed: nft when it is installed,
// iptables otherwise. IPv4 and IPv6 are both covered.
func runTransparent(ctx context.Context, o Options, env sysproxy.Env, sc *scope, st *store.Store, logger *slog.Logger, dir string) (res Result, err error) {
	res.Degree = DegreeTransparent
	sys := o.System
	if envGOOS(env) != "linux" {
		return res, fmt.Errorf("attach: --transparent: %w (Linux only)", sysproxy.ErrUnsupported)
	}
	uid := sys.UID
	if uid < 0 {
		n, ok := sudoUID(env)
		if !ok {
			return res, errors.New("attach: --transparent needs --transparent-uid N (or run it under sudo, which sets SUDO_UID)")
		}
		uid = n
	}
	proxyUID := env.Euid()
	if proxyUID < 0 {
		return res, errors.New("attach: the process uid is unknown")
	}

	plan := sysproxy.NFTPlan{TargetUID: uid, ProxyUID: proxyUID, AllDestinations: sys.AllDestinations}
	var resolveErr error
	if !sys.AllDestinations {
		rctx, rcancel := context.WithTimeout(ctx, 15*time.Second)
		plan.IPv4, plan.IPv6, resolveErr = sysproxy.ResolveDestinations(rctx, env, sc.runHosts)
		rcancel()
	}

	if !sys.Apply {
		fw, ferr := sysproxy.ChooseFirewall(env)
		if ferr != nil {
			fw = sysproxy.FirewallNFT
			res.Notes = append(res.Notes, "neither nft nor iptables is installed here; the nft form is shown")
		}
		// The preview shows the IPv6 rules too; the port is only known when applied.
		plan.RedirectIPv6 = true
		text, err := sysproxy.TransparentPreview(fw, plan)
		if err != nil {
			return res, err
		}
		out := o.Stdout
		if out == nil {
			out = io.Discard
		}
		fmt.Fprint(out, text)
		res.Notes = append(res.Notes, "dry run: nothing changed; the redirect port is chosen when the rules are applied",
			"IPv6 TCP 443 of the uid is redirected through [::1] when that port is free there; if it is not, the client's IPv6 bypasses acd-ctx")
		if resolveErr != nil {
			res.Notes = append(res.Notes, "some MITM domains did not resolve: "+resolveErr.Error())
		}
		return res, nil
	}
	if !env.Privileged() {
		return res, &sysproxy.ErrNeedPrivilege{Commands: transparentCommands(o, uid)}
	}
	fw, err := sysproxy.ChooseFirewall(env)
	if err != nil {
		return res, fmt.Errorf("attach: transparent redirect: %w", err)
	}
	// iptables needs ip6tables for the IPv6 half; without it IPv6 is not redirected.
	wantIPv6 := fw == sysproxy.FirewallNFT || (env.LookPath != nil && env.LookPath("ip6tables"))

	rt, _, err := systemListener(o, env, sc, st, logger)
	if err != nil {
		return res, err
	}
	defer rt.close()

	ln4, ln6, err := listenRedirect(wantIPv6)
	if err != nil {
		return res, err
	}
	defer ln4.Close()
	if ln6 != nil {
		defer ln6.Close()
	}
	plan.RedirectPort = ln4.Addr().(*net.TCPAddr).Port
	plan.RedirectIPv6 = ln6 != nil
	if !plan.RedirectIPv6 {
		res.Notes = append(res.Notes, "IPv6 TCP 443 is not redirected: [::1] was not available on the redirect port, so the client's IPv6 bypasses acd-ctx")
	}
	if resolveErr != nil {
		res.Notes = append(res.Notes, "some MITM domains did not resolve: "+resolveErr.Error())
	}

	hctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	act, err := sysproxy.StartTransparent(hctx, env, dir, plan, fw)
	if err != nil {
		return res, fmt.Errorf("attach: transparent redirect: %w", err)
	}
	defer func() {
		rctx, rcancel := sysproxy.RestoreContext()
		defer rcancel()
		notes, serr := act.Stop(rctx)
		res.Notes = append(res.Notes, notes...)
		if serr != nil {
			err = errors.Join(err, fmt.Errorf("attach: restore %s redirect: %w (state kept; run acd-ctx attach --restore)", fw, serr))
		}
	}()

	hosts := slices.Clone(sc.runHosts)
	mitmHost := func(h string) bool { return slices.Contains(hosts, h) }
	router := &sysproxy.Router{
		IsMITM: mitmHost,
		MITM: func(c context.Context, conn net.Conn, host string, raw []byte) error {
			return relayToMITM(rt.addr, host, conn, raw)
		},
		Blind: func(c context.Context, conn net.Conn, dst string, raw []byte) error {
			up, err := sysproxy.Dial(c, dst)
			if err != nil {
				return err
			}
			if _, err := up.Write(raw); err != nil {
				_ = up.Close()
				return err
			}
			sysproxy.Splice(conn, up)
			return nil
		},
	}
	// A panic in a background goroutine would kill the process before the
	// deferred restore runs. Instead it ends this run, and the restore runs on
	// the normal path below.
	rctx, endRun := context.WithCancel(hctx)
	defer endRun()
	onPanic := func(v any) {
		fmt.Fprintf(o.stderr(), "acd-ctx: internal error (%v); ending the run to restore the redirect\n", v)
		endRun()
	}
	router.OnPanic = onPanic
	go refreshDestinations(rctx, env, hosts, act, onPanic)

	lns := []net.Listener{ln4}
	if ln6 != nil {
		lns = append(lns, ln6)
	}
	for _, ln := range lns {
		go func() { <-rctx.Done(); _ = ln.Close() }()
	}

	fmt.Fprintf(o.stderr(), "acd-ctx: TCP 443 of uid %d is redirected for the MITM domains (%s, IPv4 and IPv6; Ctrl-C restores it)\n", uid, fw)
	errc := make(chan error, len(lns))
	for _, ln := range lns {
		go func(ln net.Listener) { errc <- router.Serve(rctx, ln) }(ln)
	}
	var serveErr error
	for range lns {
		if serr := <-errc; serr != nil && serveErr == nil {
			serveErr = serr
			endRun()
		}
	}
	res.Hits = rt.gate.hits.Load()
	return res, serveErr
}

// listenRedirect opens the IPv4 redirect listener on a loopback port and, when
// wantIPv6, the IPv6 listener on [::1] with the same port. The redirect target is
// the same port for both families. When [::1] is not free on that port, the
// attempt is retried on another port; after the last try the IPv6 listener is
// nil and the caller leaves IPv6 alone (an IPv6 redirect with nothing listening
// would cut the client off).
func listenRedirect(wantIPv6 bool) (net.Listener, net.Listener, error) {
	const attempts = 3
	for i := 0; i < attempts && wantIPv6; i++ {
		ln4, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return nil, nil, fmt.Errorf("attach: redirect listener: %w", err)
		}
		port := ln4.Addr().(*net.TCPAddr).Port
		ln6, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(port)))
		if err == nil {
			return ln4, ln6, nil
		}
		_ = ln4.Close()
	}
	ln4, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("attach: redirect listener: %w", err)
	}
	return ln4, nil, nil
}

// refreshDestinations re-resolves the MITM domains every few minutes so the
// destination sets follow DNS. A failed lookup keeps the previous addresses. A
// connection to a stale address is still checked by SNI, so it is never
// intercepted by mistake.
func refreshDestinations(ctx context.Context, env sysproxy.Env, hosts []string, act *sysproxy.Active, onPanic func(any)) {
	defer func() {
		if v := recover(); v != nil {
			onPanic(v)
		}
	}()
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			v4, v6, _ := sysproxy.ResolveDestinations(rctx, env, hosts)
			_ = act.SetDestinations(rctx, v4, v6)
			cancel()
		}
	}
}

// relayToMITM hands an intercepted connection to the runtime listener as a
// CONNECT, then splices the bytes already read from the client.
func relayToMITM(addr, host string, c net.Conn, raw []byte) error {
	up, err := net.DialTimeout("tcp4", addr, upstreamTLSTO)
	if err != nil {
		return err
	}
	defer up.Close()
	authority := net.JoinHostPort(host, "443")
	if _, err := fmt.Fprintf(up, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", authority, authority); err != nil {
		return err
	}
	br := bufio.NewReader(up)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mitm: CONNECT refused: %s", resp.Status)
	}
	if _, err := up.Write(raw); err != nil {
		return err
	}
	sysproxy.Splice(c, bufferedConn{Conn: up, r: br})
	return nil
}

// bufferedConn reads through r, which may already hold bytes from the server.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

// transparentCommands are the commands a user runs as root when acd-ctx itself
// lacks the privilege.
func transparentCommands(o Options, uid int) []string {
	cmd := fmt.Sprintf("sudo acd-ctx attach --transparent --yes --apply --transparent-uid %d", uid)
	if o.System != nil && o.System.AllDestinations {
		cmd += " --all-443"
	}
	return []string{cmd}
}

// stderr returns the error stream, or discards output when none is set.
func (o Options) stderr() io.Writer {
	if o.Stderr == nil {
		return io.Discard
	}
	return o.Stderr
}

// port returns the TCP port of the runtime listener.
func (rt *runtime) port() int {
	_, p, _ := net.SplitHostPort(rt.addr)
	n, _ := strconv.Atoi(p)
	return n
}

func netAddrPortOf(a net.Addr) (netip.AddrPort, bool) {
	ta, ok := a.(*net.TCPAddr)
	if !ok {
		return netip.AddrPort{}, false
	}
	ip, ok := netip.AddrFromSlice(ta.IP)
	if !ok {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(ip.Unmap(), uint16(ta.Port)), true
}

// envGOOS is the platform the Env describes; "" means the running one.
func envGOOS(e sysproxy.Env) string {
	if e.GOOS != "" {
		return e.GOOS
	}
	return goruntime.GOOS
}

// sudoUID returns the invoking user's uid when the process runs under sudo.
func sudoUID(e sysproxy.Env) (int, bool) {
	if e.Getenv == nil {
		return 0, false
	}
	n, err := strconv.Atoi(e.Getenv("SUDO_UID"))
	return n, err == nil && n >= 0
}

// validHostName accepts plain DNS names and IPv4 literals: lower-case letters,
// digits, '.', '-' and '_'. Anything else (spaces, CR/LF, quotes, braces,
// semicolons) could break a CONNECT line or a command, so it is refused.
func validHostName(h string) bool {
	if h == "" || len(h) > 253 || h[0] == '-' {
		return false
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// lookupHome returns the home directory of a user from /etc/passwd (os/user
// would pull cgo into cross builds). Tests replace it.
var lookupHome = func(name string) (string, error) {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 6 && f[0] == name {
			return f[5], nil
		}
	}
	return "", fmt.Errorf("user %q not found", name)
}

// checkSudoHome refuses to run under sudo when the data directory would land in
// another user's home. Under sudo the data dir, the CA and the state files
// resolve against root's HOME (or the user's, when sudo keeps HOME), and the
// system proxy settings of root are not the desktop user's. The only safe
// combination is --transparent with an explicit uid and an explicit data dir.
func checkSudoHome(env sysproxy.Env, sys *SystemOptions, transparent bool) error {
	if env.Getenv == nil {
		return nil
	}
	user := env.Getenv("SUDO_USER")
	if user == "" || env.Euid() != 0 {
		return nil
	}
	home := env.Getenv("HOME")
	if uh, err := lookupHome(user); err == nil && uh != "" && uh == home {
		return nil
	}
	if transparent && sys.UID >= 0 && sys.DataDirExplicit {
		return nil
	}
	if transparent {
		return fmt.Errorf("attach: running under sudo (SUDO_USER=%s) with HOME=%q: the CA, database and state would be created in a home that is not %s's; "+
			"pass --transparent-uid N and set ACD_CTX_DB to an explicit path (or keep the user HOME with sudo --preserve-env=HOME)", user, home, user)
	}
	return fmt.Errorf("attach: --system-proxy changes the settings of the invoking desktop user; do not run it under sudo (SUDO_USER=%s)", user)
}
