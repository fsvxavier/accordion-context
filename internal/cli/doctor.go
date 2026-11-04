package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsvxavier/accordion-context/internal/doctor"
	"github.com/fsvxavier/accordion-context/internal/launcher"
)

// runDoctor handles "doctor [--json] [client]". The exit code is the report's:
// 0 for PASS, 3 for WARN and 1 for FAIL.
func runDoctor(args []string, s IO) int {
	fset := newFlagSet("doctor", s)
	jsonOut := fset.Bool("json", false, "print the report as JSON")
	pos, err := parseFlags(fset, args)
	if err != nil || len(pos) > 1 {
		return usageError(s, "usage: doctor [--json] [client]")
	}
	client := ""
	if len(pos) == 1 {
		client = pos[0]
	}
	dbPath, err := globalDBPath(s.LookupEnv)
	if err != nil {
		return fail(s, err)
	}
	gp, err := globalConfigPath(s.LookupEnv)
	if err != nil {
		return fail(s, err)
	}
	profiles, err := loadClientProfiles(s, "")
	if err != nil {
		return fail(s, err)
	}
	rep, err := doctor.Run(s.Ctx, doctor.Options{
		Cwd:              s.Cwd,
		Markers:          projectMarkers,
		GlobalConfigPath: gp,
		DBPath:           dbPath,
		CADir:            filepath.Join(filepath.Dir(dbPath), "ca"),
		ProxyAddr:        launcher.DefaultProxyAddr,
		Version:          s.Version,
		Client:           client,
		Profiles:         profiles,
		Env:              os.Environ(),
	})
	if err != nil {
		return fail(s, err)
	}
	if *jsonOut {
		if err := doctor.WriteJSON(s.Stdout, rep); err != nil {
			return fail(s, fmt.Errorf("write report: %w", err))
		}
	} else {
		doctor.Write(s.Stdout, rep)
	}
	return rep.ExitCode
}
