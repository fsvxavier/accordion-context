package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/sysproxy"
)

func TestAttachUsageAndSystemDegreeConsent(t *testing.T) {
	f := newFixture(t)
	if r := f.run("", "attach"); r.code != ExitUsage || !strings.Contains(r.stderr, "usage: attach") {
		t.Fatalf("no command: code %d stderr %q", r.code, r.stderr)
	}
	// The system degrees need --yes, and they refuse before any command runs:
	// the runner fails the test if it is called.
	defer swapSysRunner(t, failingRunner{t})()
	for _, flag := range []string{"--system-proxy", "--transparent"} {
		r := f.run("", "attach", flag, "--", "true")
		if r.code != ExitFail || !strings.Contains(r.stderr, "--yes") {
			t.Fatalf("%s without --yes: code %d stderr %q", flag, r.code, r.stderr)
		}
	}
	if r := f.run("", "attach", "--system-proxy", "--transparent", "--yes", "--", "true"); r.code != ExitUsage {
		t.Fatalf("both degrees: code %d stderr %q", r.code, r.stderr)
	}
	if r := f.run("", "attach", "--transparent", "--apply", "--dry-run", "--yes"); r.code != ExitUsage {
		t.Fatalf("apply with dry-run: code %d stderr %q", r.code, r.stderr)
	}
	if r := f.run("", "attach", "--transparent", "--yes", "--", "true"); r.code != ExitFail || !strings.Contains(r.stderr, "does not run a command") {
		t.Fatalf("transparent with a command: code %d stderr %q", r.code, r.stderr)
	}
}

// failingRunner fails the test on any command.
type failingRunner struct{ t *testing.T }

func (f failingRunner) Run(_ context.Context, c sysproxy.Command) (string, error) {
	f.t.Fatalf("unexpected command: %s", c)
	return "", nil
}

// swapSysRunner replaces the system runner for one test.
func swapSysRunner(t *testing.T, r sysproxy.Runner) func() {
	old := sysRunner
	sysRunner = r
	return func() { sysRunner = old }
}

// TestAttachRestoreWithoutLeftovers: --restore with no state files changes
// nothing and succeeds.
func TestAttachRestoreWithoutLeftovers(t *testing.T) {
	f := newFixture(t)
	defer swapSysRunner(t, failingRunner{t})()
	if r := f.run("", "attach", "--restore"); r.code != ExitOK || !strings.Contains(r.stderr, "no leftover") {
		t.Fatalf("restore: code %d stderr %q", r.code, r.stderr)
	}
}

func TestAttachFlagsStopAtClientArguments(t *testing.T) {
	f := newFixture(t)
	// --profiles after the command belongs to the client, so the profile file
	// is never read and the failure comes from starting the missing command.
	r := f.run("", "attach", "--", filepath.Join(t.TempDir(), "missing-client"), "--profiles", "/nonexistent.yaml")
	if r.code == ExitOK || strings.Contains(r.stderr, "nonexistent.yaml") {
		t.Fatalf("client flag was parsed as acd-ctx flag: code %d stderr %q", r.code, r.stderr)
	}
}

func TestLoadClientProfilesFromConfigDirAndFlag(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.vars["XDG_CONFIG_HOME"], "acd-ctx", "clients")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "profiles:\n  - name: exemplo\n    match: [exemplo]\n    upstream: https://api.example.test/v1\n    base_url_env: EXEMPLO_BASE_URL\n"
	if err := os.WriteFile(filepath.Join(dir, "exemplo.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	s := IO{LookupEnv: func(k string) (string, bool) { v, ok := f.vars[k]; return v, ok }}
	ps, err := loadClientProfiles(s, "")
	if err != nil || len(ps) != 1 || ps[0].BaseURLEnv != "EXEMPLO_BASE_URL" {
		t.Fatalf("profiles = %+v, err %v", ps, err)
	}

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("profiles:\n  - name: x\n    bogus_key: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadClientProfiles(s, bad); err == nil {
		t.Fatal("unknown key accepted in a profile file")
	}
}

func TestDoctorUsageAndJSON(t *testing.T) {
	f := newFixture(t)
	if r := f.run("", "doctor", "a", "b"); r.code != ExitUsage {
		t.Fatalf("two clients: code %d", r.code)
	}
	r := f.run("", "doctor", "--json")
	if r.code != 0 && r.code != 1 && r.code != 3 {
		t.Fatalf("exit code %d, stderr %q", r.code, r.stderr)
	}
	var rep struct {
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
		Summary  string `json:"summary"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatalf("doctor --json is not JSON: %v\n%s", err, r.stdout)
	}
	if rep.ExitCode != r.code || rep.Summary == "" {
		t.Fatalf("report %+v does not match exit %d", rep, r.code)
	}
	found := false
	for _, c := range rep.Checks {
		if c.Name == "database" {
			found = true
		}
	}
	if !found {
		t.Fatal("no database check in report")
	}
}

func TestDoctorTextSummaryAndClientCheck(t *testing.T) {
	f := newFixture(t)
	r := f.run("", "doctor", "claude")
	if !strings.Contains(r.stdout, "summary: ") || !strings.Contains(r.stdout, "integration") {
		t.Fatalf("text report incomplete:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "console") {
		t.Fatalf("claude has a profile, so no console warning expected:\n%s", r.stdout)
	}
}
