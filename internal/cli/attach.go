package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/attach"
	"github.com/fsvxavier/accordion-context/internal/doctor"
	"github.com/fsvxavier/accordion-context/internal/launcher"
	"github.com/fsvxavier/accordion-context/internal/sysproxy"
)

// repeated is a flag that may be given several times.
type repeated []string

func (r *repeated) String() string { return strings.Join(*r, ",") }

func (r *repeated) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// sysRunner runs the commands of the system degrees (gsettings, networksetup,
// nft, ...). Tests replace it, so they never change the machine.
var sysRunner sysproxy.Runner = sysproxy.ExecRunner{}

// init gives doctor's firewall probes the same runner as the system degrees. The
// hidden watchdog argument is dispatched explicitly by cmd/acd-ctx, not here.
func init() {
	doctor.SystemRunner = func() sysproxy.Runner { return sysRunner }
}

// sysEnv describes this process to the system degrees. Spawn starts the
// watchdog helper of a transparent run from this same binary.
func sysEnv(s IO) *sysproxy.Env {
	return &sysproxy.Env{
		Getenv:   func(k string) string { v, _ := s.LookupEnv(k); return v },
		LookPath: func(name string) bool { _, err := exec.LookPath(name); return err == nil },
		Geteuid:  os.Geteuid,
		Runner:   sysRunner,
		Spawn:    sysproxy.ExecSpawner,
	}
}

// runAttach handles "attach [--profiles file] [--mitm-domain host]... -- <cmd> [args]".
// The flags are parsed up to the first non-flag or "--", so the client's own
// arguments are never read as acd-ctx flags. The system degrees (--system-proxy,
// --transparent) and --restore are described in spec §19.9 and §19.7.
func runAttach(args []string, s IO) int {
	fset := newFlagSet("attach", s)
	systemProxy := fset.Bool("system-proxy", false, "degree 6: point the desktop HTTPS proxy at acd-ctx for this run (needs --yes)")
	transparent := fset.Bool("transparent", false, "degree 7: Linux redirect of TCP 443 for one uid over IPv4 and IPv6 (nftables, or iptables when nft is absent; IPv6 only when [::1] is free) (needs --yes, root or CAP_NET_ADMIN)")
	yes := fset.Bool("yes", false, "confirm the machine-wide change of --system-proxy or --transparent")
	apply := fset.Bool("apply", false, "with --transparent: install the rules (without it the rules are only printed)")
	dryRun := fset.Bool("dry-run", false, "with --transparent: print the rules and change nothing (the default)")
	restore := fset.Bool("restore", false, "restore system settings left behind by a crashed run, then exit")
	txUID := fset.Int("transparent-uid", -1, "with --transparent: the uid whose TCP 443 is redirected (default SUDO_UID)")
	all443 := fset.Bool("all-443", false, "with --transparent: redirect all TCP 443 of the uid, not only the MITM domains")
	noPeer := fset.Bool("i-understand-no-peer-auth", false, "with --system-proxy or --transparent on a platform without peer-uid checks")
	profilesFile := fset.String("profiles", "", "declarative client profile file (YAML) matched before the built-in profiles")
	var domains repeated
	fset.Var(&domains, "mitm-domain", "extra exact host to intercept (repeatable)")
	if err := fset.Parse(args); err != nil {
		return ExitUsage
	}
	if *restore {
		return runAttachRestore(s)
	}
	cmd := fset.Args()
	if *systemProxy && *transparent {
		return usageError(s, "choose one of --system-proxy and --transparent")
	}
	if *apply && *dryRun {
		return usageError(s, "--apply and --dry-run are exclusive")
	}
	sysMode := *systemProxy || *transparent
	if len(cmd) == 0 && !sysMode {
		return usageError(s, "usage: attach [--system-proxy|--transparent] [--profiles file] [--mitm-domain host] -- <cmd> [args]")
	}

	profiles, err := loadClientProfiles(s, *profilesFile)
	if err != nil {
		return fail(s, err)
	}
	dbPath, err := globalDBPath(s.LookupEnv)
	if err != nil {
		return fail(s, err)
	}
	logger := slog.New(slog.NewTextHandler(s.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	opts := attach.Options{
		Args:        cmd,
		Cwd:         s.Cwd,
		Markers:     projectMarkers,
		Profiles:    profiles,
		DBPath:      dbPath,
		CADir:       filepath.Join(filepath.Dir(dbPath), "ca"),
		Version:     s.Version,
		MITMDomains: domains,
		Stdin:       s.Stdin,
		Stdout:      s.Stdout,
		Stderr:      s.Stderr,
		Logger:      logger,
		SystemProxy: *systemProxy,
		Transparent: *transparent,
	}
	if sysMode {
		opts.System = &attach.SystemOptions{
			Yes:             *yes,
			Apply:           *apply,
			UID:             *txUID,
			AllDestinations: *all443,
			NoPeerAuth:      *noPeer,
			DataDirExplicit: explicitDB(s),
			Env:             sysEnv(s),
		}
	}
	res, err := attach.Run(s.Ctx, opts)
	for _, n := range res.Notes {
		fmt.Fprintf(s.Stderr, "acd-ctx: note: %s\n", n)
	}
	var need *sysproxy.ErrNeedPrivilege
	if errors.As(err, &need) {
		fmt.Fprintf(s.Stderr, "acd-ctx: %v\n", err)
		return ExitUsage
	}
	if err != nil {
		fmt.Fprintf(s.Stderr, "acd-ctx: %v\n", err)
		if res.ExitCode != ExitOK {
			return res.ExitCode
		}
		return ExitFail
	}
	switch {
	case sysMode && *transparent && !*apply:
		fmt.Fprintln(s.Stderr, "acd-ctx: transparent dry run complete; nothing was changed")
	case sysMode:
		fmt.Fprintf(s.Stderr, "acd-ctx: %s ended; the change was restored\n", res.Degree)
	default:
		fmt.Fprintf(s.Stderr, "acd-ctx: attached via %s (verified before start)\n", res.Degree)
	}
	return res.ExitCode
}

// runAttachRestore restores the settings of crashed runs. Live owners are skipped.
func runAttachRestore(s IO) int {
	dbPath, err := globalDBPath(s.LookupEnv)
	if err != nil {
		return fail(s, err)
	}
	ctx, cancel := sysproxy.RestoreContext()
	defer cancel()
	notes, err := sysproxy.RestoreStale(ctx, sysproxy.StateDir(dbPath), *sysEnv(s))
	for _, n := range notes {
		fmt.Fprintf(s.Stderr, "acd-ctx: %s\n", n)
	}
	if err != nil {
		return fail(s, err)
	}
	if len(notes) == 0 {
		fmt.Fprintln(s.Stderr, "acd-ctx: no leftover system settings")
	}
	return ExitOK
}

// loadClientProfiles reads every YAML profile in the clients directory next to
// the global config (XDG_CONFIG_HOME/acd-ctx/clients), then the file given by
// --profiles. User profiles come first, so they override the built-ins.
func loadClientProfiles(s IO, extra string) ([]launcher.Profile, error) {
	var out []launcher.Profile
	gp, err := globalConfigPath(s.LookupEnv)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(filepath.Dir(gp), "clients")
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read client profiles: %w", err)
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); !e.IsDir() && (strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml")) {
			names = append(names, filepath.Join(dir, n))
		}
	}
	sort.Strings(names)
	if extra != "" {
		names = append(names, extra)
	}
	for _, path := range names {
		ps, err := readProfiles(path)
		if err != nil {
			return nil, err
		}
		out = append(out, ps...)
	}
	return out, nil
}

func readProfiles(path string) ([]launcher.Profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	ps, err := launcher.LoadProfiles(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return ps, nil
}

// explicitDB reports whether the user chose the database path with ACD_CTX_DB.
func explicitDB(s IO) bool {
	v, ok := s.LookupEnv("ACD_CTX_DB")
	return ok && strings.TrimSpace(v) != ""
}
