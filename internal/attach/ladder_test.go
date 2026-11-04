package attach

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsvxavier/accordion-context/internal/launcher"
)

// testProfile matches the test binary and declares the given base URL settings.
func testProfile(mut func(*launcher.Profile)) launcher.Profile {
	p := launcher.Profile{
		Name:     "attachladder",
		Match:    []string{filepath.Base(os.Args[0])},
		Upstream: "https://api.acd-ctx.invalid/v1",
	}
	mut(&p)
	return p
}

// jsonConfig is a temp config whose path comes from ATTACH_TEST_CFG.
func jsonConfig() *launcher.TempConfig {
	return &launcher.TempConfig{PathEnv: "ATTACH_TEST_CFG", Format: "json", Template: `{"base_url": "{url}"}`}
}

// checkTempFiles asserts the run's temp dir is 0700 and holds one config file
// of mode 0600, and returns that file's path.
func checkTempFiles(t *testing.T, root string) string {
	t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(root, "acd-ctx-attach-*"))
	if len(dirs) != 1 {
		t.Fatalf("temp dirs = %v, want exactly one", dirs)
	}
	if fi, err := os.Stat(dirs[0]); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("temp dir mode = %v (%v), want 0700", fi.Mode().Perm(), err)
	}
	files, _ := filepath.Glob(filepath.Join(dirs[0], "client-config.*"))
	if len(files) != 1 {
		t.Fatalf("config files = %v, want exactly one", files)
	}
	if fi, err := os.Stat(files[0]); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %v (%v), want 0600", fi.Mode().Perm(), err)
	}
	return files[0]
}

func assertNoLeftovers(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("temp dir not cleaned up: %v", names)
	}
}

func fileOptions(t *testing.T, mode string) (Options, string) {
	t.Helper()
	o, _ := baseOptions(t, mode, "")
	root := t.TempDir()
	o.tmpRoot = root
	return o, root
}

func TestTempConfigIsPrivateAndRemovedAfterNormalExit(t *testing.T) {
	o, root := fileOptions(t, "file")
	o.Profiles = []launcher.Profile{testProfile(func(p *launcher.Profile) { p.TempConfig = jsonConfig() })}
	var cfgSeen string
	o.beforeProbe = func(_ context.Context, degree string) {
		if degree == DegreeTempConfig {
			cfgSeen = checkTempFiles(t, root)
		}
	}
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Degree != DegreeTempConfig || res.ExitCode != 0 || res.Hits < 1 {
		t.Fatalf("result = %+v", res)
	}
	if cfgSeen == "" {
		t.Fatal("temp config was never checked")
	}
	assertNoLeftovers(t, root)
}

func TestTempConfigRemovedAfterChildFailure(t *testing.T) {
	o, root := fileOptions(t, "file")
	o.Environ = append(o.Environ, "ATTACH_TEST_EXIT=5")
	o.Profiles = []launcher.Profile{testProfile(func(p *launcher.Profile) { p.TempConfig = jsonConfig() })}
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 5 {
		t.Fatalf("exit = %d, want the client's 5", res.ExitCode)
	}
	assertNoLeftovers(t, root)
}

func TestInterruptDuringProbeRemovesTempFilesAndDoesNotStartClient(t *testing.T) {
	o, root := fileOptions(t, "file")
	o.Profiles = []launcher.Profile{testProfile(func(p *launcher.Profile) { p.TempConfig = jsonConfig() })}
	sigs := make(chan os.Signal, 1)
	o.signals = sigs
	o.beforeProbe = func(ctx context.Context, degree string) {
		if degree != DegreeTempConfig {
			return
		}
		checkTempFiles(t, root)
		sigs <- os.Interrupt
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			t.Error("interrupt did not cancel the integration check")
		}
	}
	res, err := Run(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("err = %v, want an interrupted error", err)
	}
	if res.Hits != 0 || res.ExitCode != 0 {
		t.Fatalf("result = %+v, the client must not have run", res)
	}
	assertNoLeftovers(t, root)
}

func TestFlagDegreePassesFlagAfterUserArgs(t *testing.T) {
	o, stderr := baseOptions(t, "flag", "")
	o.Args = append(helperArgs(), "--") // the Go test flags stop at "--"
	o.Profiles = []launcher.Profile{testProfile(func(p *launcher.Profile) {
		p.BaseURLFlag = "--api-base {url}"
		p.FlagPosition = "after"
	})}
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v (stderr %q)", err, stderr.String())
	}
	if res.Degree != DegreeFlag || res.ExitCode != 0 || res.Hits < 1 {
		t.Fatalf("result = %+v, attempts %+v", res, res.Attempts)
	}
}

func TestLadderTriesEnvBeforeFlag(t *testing.T) {
	o, stderr := baseOptions(t, "flag", "")
	o.Args = append(helperArgs(), "--")
	o.skip = map[string]bool{DegreeBaseURL: true}
	o.Profiles = []launcher.Profile{testProfile(func(p *launcher.Profile) {
		p.BaseURLEnv = "ATTACH_TEST_BASE"
		p.BaseURLFlag = "--api-base {url}"
		p.FlagPosition = "after"
	})}
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v (stderr %q)", err, stderr.String())
	}
	want := []string{DegreeBaseURL, DegreeFlag}
	if len(res.Attempts) != 2 || res.Attempts[0].Degree != want[0] || res.Attempts[0].Reason != "skipped" ||
		res.Attempts[1].Degree != want[1] || !res.Attempts[1].OK || res.Degree != DegreeFlag {
		t.Fatalf("attempts = %+v, degree %q", res.Attempts, res.Degree)
	}
}

func TestPreferSelectsAndOrdersBaseDegrees(t *testing.T) {
	o, root := fileOptions(t, "file")
	o.Profiles = []launcher.Profile{testProfile(func(p *launcher.Profile) {
		p.BaseURLFlag = "--api-base {url}"
		p.TempConfig = jsonConfig()
		p.Integration.Prefer = []string{"temp_config"}
	})}
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Degree != DegreeTempConfig || len(res.Attempts) != 1 {
		t.Fatalf("result = %+v: prefer must leave out base-url-flag", res)
	}
	assertNoLeftovers(t, root)
}

func TestQuirkIgnoresBaseURLSkipsBaseDegrees(t *testing.T) {
	o, root := fileOptions(t, "https")
	o.Profiles = []launcher.Profile{testProfile(func(p *launcher.Profile) {
		p.BaseURLEnv = "ATTACH_TEST_BASE"
		p.BaseURLFlag = "--api-base {url}"
		p.TempConfig = jsonConfig()
		p.Quirks = map[string]any{"ignores_base_url": true}
	})}
	o.beforeProbe = func(_ context.Context, degree string) {
		if degree == DegreeMITM {
			if files, _ := filepath.Glob(filepath.Join(root, "acd-ctx-attach-*", "client-config.*")); len(files) != 0 {
				t.Errorf("temp config written for a client that ignores base URLs: %v", files)
			}
		}
	}
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Degree != DegreeMITM {
		t.Fatalf("degree = %q, want mitm; attempts %+v", res.Degree, res.Attempts)
	}
	n := len(res.Attempts)
	if n != 4 {
		t.Fatalf("attempts = %+v", res.Attempts)
	}
	for _, a := range res.Attempts[:n-1] {
		if a.OK || !strings.HasPrefix(a.Reason, "skipped: quirks.ignores_base_url") {
			t.Fatalf("base degree not skipped by quirk: %+v", a)
		}
	}
	assertNoLeftovers(t, root)
}

func TestMergeFromNeverModifiesTheUserFile(t *testing.T) {
	userDir := t.TempDir()
	user := filepath.Join(userDir, "user.json")
	orig := []byte("{\"model\": \"m-1\", \"base_url\": \"https://old.example.invalid\"}\n")
	if err := os.WriteFile(user, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	o, root := fileOptions(t, "file")
	o.Profiles = []launcher.Profile{testProfile(func(p *launcher.Profile) {
		p.TempConfig = &launcher.TempConfig{PathEnv: "ATTACH_TEST_CFG", Format: "json", MergeFrom: user, Key: "base_url"}
	})}
	o.beforeProbe = func(_ context.Context, degree string) {
		if degree != DegreeTempConfig {
			return
		}
		var doc map[string]string
		b, err := os.ReadFile(checkTempFiles(t, root))
		if err != nil || json.Unmarshal(b, &doc) != nil {
			t.Errorf("merged config unreadable: %v", err)
			return
		}
		if doc["model"] != "m-1" || doc["base_url"] == "https://old.example.invalid" {
			t.Errorf("merged config = %v", doc)
		}
	}
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Degree != DegreeTempConfig {
		t.Fatalf("degree = %q", res.Degree)
	}
	got, err := os.ReadFile(user)
	if err != nil || !bytes.Equal(got, orig) {
		t.Fatalf("user file changed: %q (%v)", got, err)
	}
	assertNoLeftovers(t, root)
}
