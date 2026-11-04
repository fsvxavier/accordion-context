package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/attach"
	"github.com/fsvxavier/accordion-context/internal/launcher"
)

// repeated is a flag that may be given several times.
type repeated []string

func (r *repeated) String() string { return strings.Join(*r, ",") }

func (r *repeated) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// runAttach handles "attach [--profiles file] [--mitm-domain host]... -- <cmd> [args]".
// The flags are parsed up to the first non-flag or "--", so the client's own
// arguments are never read as acd-ctx flags.
func runAttach(args []string, s IO) int {
	fset := newFlagSet("attach", s)
	systemProxy := fset.Bool("system-proxy", false, "use the system proxy degree (not implemented in this build)")
	transparent := fset.Bool("transparent", false, "use the transparent redirect degree (not implemented in this build)")
	profilesFile := fset.String("profiles", "", "declarative client profile file (YAML) matched before the built-in profiles")
	var domains repeated
	fset.Var(&domains, "mitm-domain", "extra exact host to intercept (repeatable)")
	if err := fset.Parse(args); err != nil {
		return ExitUsage
	}
	cmd := fset.Args()
	if len(cmd) == 0 {
		return usageError(s, "usage: attach [--system-proxy|--transparent] [--profiles file] [--mitm-domain host] -- <cmd> [args]")
	}
	if *systemProxy || *transparent {
		name := "--system-proxy"
		if *transparent {
			name = "--transparent"
		}
		return fail(s, fmt.Errorf("attach: %s is not implemented in this build", name))
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
	res, err := attach.Run(s.Ctx, attach.Options{
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
	})
	for _, n := range res.Notes {
		fmt.Fprintf(s.Stderr, "acd-ctx: note: %s\n", n)
	}
	if err != nil {
		fmt.Fprintf(s.Stderr, "acd-ctx: %v\n", err)
		if res.ExitCode != ExitOK {
			return res.ExitCode
		}
		return ExitFail
	}
	fmt.Fprintf(s.Stderr, "acd-ctx: attached via %s (verified before start)\n", res.Degree)
	return res.ExitCode
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
