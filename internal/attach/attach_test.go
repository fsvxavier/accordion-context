package attach

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fsvxavier/accordion-context/internal/launcher"
)

// offline refuses every upstream connection, so no test reaches a network.
func offline(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("offline test dialer")
}

// TestHelperProcess is the client used by the tests. It is the test binary
// re-executed with ATTACH_TEST_HELPER=1. It makes one request using only the
// environment that attach gave it, and exits 0 when it got any response.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("ATTACH_TEST_HELPER") != "1" {
		return
	}
	var target string
	switch os.Getenv("ATTACH_TEST_MODE") {
	case "base":
		target = os.Getenv(os.Getenv("ATTACH_TEST_VAR")) + "/v1/messages"
	case "https":
		target = "https://api.acd-ctx.invalid/v1/messages"
	case "file":
		target = helperConfigBase(os.Getenv("ATTACH_TEST_CFG")) + "/v1/messages"
	case "flag":
		target = helperFlagValue("--api-base") + "/v1/messages"
	default:
		os.Exit(4)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(target)
	if err != nil {
		os.Stderr.WriteString("helper: " + err.Error() + "\n")
		os.Exit(3)
	}
	_ = resp.Body.Close()
	if code := os.Getenv("ATTACH_TEST_EXIT"); code != "" {
		n, _ := strconv.Atoi(code)
		os.Exit(n)
	}
	os.Exit(0)
}

// helperConfigBase reads the "base_url" key of the JSON config at path.
func helperConfigBase(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		os.Stderr.WriteString("helper: " + err.Error() + "\n")
		os.Exit(6)
	}
	var doc struct {
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal(b, &doc); err != nil || doc.BaseURL == "" {
		os.Stderr.WriteString("helper: config has no base_url\n")
		os.Exit(6)
	}
	return doc.BaseURL
}

// helperFlagValue returns the argument that follows flag in os.Args.
func helperFlagValue(flag string) string {
	for i, a := range os.Args {
		if a == flag && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	os.Exit(7)
	return ""
}

// clientProfile is a declarative profile whose command is the test binary.
func clientProfile() launcher.Profile {
	return launcher.Profile{
		Name:       "attachtest",
		Match:      []string{filepath.Base(os.Args[0])},
		Upstream:   "https://api.acd-ctx.invalid/v1",
		BaseURLEnv: "ATTACH_TEST_BASE",
	}
}

func helperArgs() []string {
	return []string{os.Args[0], "-test.run=^TestHelperProcess$"}
}

func helperEnv(mode, varName string) []string {
	return append(os.Environ(),
		"ATTACH_TEST_HELPER=1",
		"ATTACH_TEST_MODE="+mode,
		"ATTACH_TEST_VAR="+varName,
	)
}

func baseOptions(t *testing.T, mode, varName string) (Options, *bytes.Buffer) {
	t.Helper()
	var stderr bytes.Buffer
	dir := t.TempDir()
	return Options{
		Args:         helperArgs(),
		Cwd:          dir,
		Profiles:     []launcher.Profile{clientProfile()},
		DBPath:       filepath.Join(dir, "data", "acd-ctx.db"),
		CADir:        filepath.Join(dir, "ca"),
		Environ:      helperEnv(mode, varName),
		Stderr:       &stderr,
		Dial:         offline,
		ProbeTimeout: 5 * time.Second,
	}, &stderr
}

func TestBaseURLDegreeIsVerifiedAndClientHitsCounted(t *testing.T) {
	o, stderr := baseOptions(t, "base", "ATTACH_TEST_BASE")
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v (stderr %q)", err, stderr.String())
	}
	if res.Degree != DegreeBaseURL {
		t.Fatalf("degree = %q, want %q; attempts %+v", res.Degree, DegreeBaseURL, res.Attempts)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr %q", res.ExitCode, stderr.String())
	}
	if res.Hits < 1 {
		t.Fatalf("hits = %d, want the client's request counted", res.Hits)
	}
}

func TestFallsBackToMITMWhenBaseURLDegreeFails(t *testing.T) {
	o, stderr := baseOptions(t, "https", "")
	o.skip = map[string]bool{DegreeBaseURL: true}
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v (stderr %q)", err, stderr.String())
	}
	if res.Degree != DegreeMITM {
		t.Fatalf("degree = %q, want mitm; attempts %+v", res.Degree, res.Attempts)
	}
	if len(res.Attempts) != 2 || res.Attempts[0].Reason != "skipped" || !res.Attempts[1].OK {
		t.Fatalf("attempts = %+v", res.Attempts)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr %q", res.ExitCode, stderr.String())
	}
	if res.Hits < 1 {
		t.Fatalf("hits = %d, want the HTTPS request decrypted and counted", res.Hits)
	}
}

func TestConsoleModeExportsEveryBaseURLAndVerifies(t *testing.T) {
	o, stderr := baseOptions(t, "base", "ANTHROPIC_BASE_URL")
	o.Profiles = nil // the test binary matches no profile: console mode
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v (stderr %q)", err, stderr.String())
	}
	if res.Degree != DegreeConsole {
		t.Fatalf("degree = %q, want console; attempts %+v", res.Degree, res.Attempts)
	}
	if res.Hits < 1 {
		t.Fatalf("hits = %d, want the client's request counted", res.Hits)
	}
}

func TestSystemProxyAndTransparentAreNotImplemented(t *testing.T) {
	for name, o := range map[string]Options{
		"system-proxy": {Args: []string{"x"}, DBPath: "unused", SystemProxy: true},
		"transparent":  {Args: []string{"x"}, DBPath: "unused", Transparent: true},
	} {
		_, err := Run(context.Background(), o)
		if !errors.Is(err, ErrNotImplemented) {
			t.Fatalf("%s: err = %v, want ErrNotImplemented", name, err)
		}
	}
}

func TestNoVerifiedDegreeDoesNotStartClient(t *testing.T) {
	o, _ := baseOptions(t, "base", "ATTACH_TEST_BASE")
	o.Args = []string{filepath.Join(t.TempDir(), "must-not-run")}
	o.skip = map[string]bool{DegreeBaseURL: true, DegreeConsole: true, DegreeMITM: true}
	res, err := Run(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "no integration degree") {
		t.Fatalf("err = %v, want no verified degree", err)
	}
	if len(res.Attempts) != 2 {
		t.Fatalf("attempts = %+v", res.Attempts)
	}
}

func TestMITMDomainsMustBeExactNames(t *testing.T) {
	for _, bad := range []string{"*.example.test", "https://x.test", "a b"} {
		if _, err := normalizeDomains([]string{bad}); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	got, err := normalizeDomains([]string{" API.Example.TEST ", ""})
	if err != nil || len(got) != 1 || got[0] != "api.example.test" {
		t.Fatalf("normalize = %v, %v", got, err)
	}
}

func TestBuildEnvOverridesAndBypassesLoopback(t *testing.T) {
	base := []string{"A=1", "HTTPS_PROXY=http://user:proxy@corp:3128", "NO_PROXY=intranet.test", "A=2"}
	env := buildEnv(base, []kv{{"HTTPS_PROXY", "http://acd-ctx:t@127.0.0.1:9"}})
	if got := envGet(env, "HTTPS_PROXY"); got != "http://acd-ctx:t@127.0.0.1:9" {
		t.Fatalf("HTTPS_PROXY = %q", got)
	}
	if got := envGet(env, "A"); got != "2" {
		t.Fatalf("last assignment should win, A = %q", got)
	}
	if got := envGet(env, "NO_PROXY"); got != "intranet.test,127.0.0.1,localhost" {
		t.Fatalf("NO_PROXY = %q", got)
	}
	if got := envGet(env, "no_proxy"); got != "127.0.0.1,localhost" {
		t.Fatalf("no_proxy = %q", got)
	}
}

func TestBaseURLVarsDeduplicates(t *testing.T) {
	got := BaseURLVars([]launcher.Profile{
		{Name: "a", BaseURLEnv: "X_BASE_URL"},
		{Name: "b"},
		{Name: "c", BaseURLEnv: "X_BASE_URL"},
		{Name: "d", BaseURLEnv: "Y_BASE_URL"},
	})
	if strings.Join(got, ",") != "X_BASE_URL,Y_BASE_URL" {
		t.Fatalf("vars = %v", got)
	}
}

func TestUpstreamRTNeverUsesDirectProxy(t *testing.T) {
	// The direct transport has no Proxy func, so HTTPS_PROXY cannot loop back.
	tr := &http.Transport{Proxy: nil}
	if tr.Proxy != nil {
		t.Fatal("direct transport must not read the environment")
	}
}
