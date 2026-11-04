package attach

import (
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/sysproxy"
)

func sudoEnv(vars map[string]string, euid int) sysproxy.Env {
	return sysproxy.Env{
		Getenv:  func(k string) string { return vars[k] },
		Geteuid: func() int { return euid },
	}
}

func TestCheckSudoHome(t *testing.T) {
	old := lookupHome
	defer func() { lookupHome = old }()
	lookupHome = func(string) (string, error) { return "/home/alice", nil }

	// Not under sudo, or not root: nothing to check.
	if err := checkSudoHome(sudoEnv(map[string]string{"HOME": "/root"}, 0), &SystemOptions{UID: -1}, true); err != nil {
		t.Fatal(err)
	}
	if err := checkSudoHome(sudoEnv(map[string]string{"SUDO_USER": "alice", "HOME": "/root"}, 1000), &SystemOptions{UID: -1}, true); err != nil {
		t.Fatal(err)
	}
	root := map[string]string{"SUDO_USER": "alice", "HOME": "/root"}
	if err := checkSudoHome(sudoEnv(root, 0), &SystemOptions{UID: -1}, true); err == nil || !strings.Contains(err.Error(), "SUDO_USER=alice") {
		t.Fatalf("root HOME under sudo accepted: %v", err)
	}
	if err := checkSudoHome(sudoEnv(root, 0), &SystemOptions{UID: 1000}, true); err == nil {
		t.Fatal("explicit uid without an explicit data dir accepted")
	}
	if err := checkSudoHome(sudoEnv(root, 0), &SystemOptions{UID: 1000, DataDirExplicit: true}, true); err != nil {
		t.Fatalf("explicit uid and data dir refused: %v", err)
	}
	if err := checkSudoHome(sudoEnv(root, 0), &SystemOptions{UID: 1000, DataDirExplicit: true}, false); err == nil {
		t.Fatal("--system-proxy under sudo accepted")
	}
	// sudo that keeps the user's HOME is fine.
	keep := map[string]string{"SUDO_USER": "alice", "HOME": "/home/alice"}
	if err := checkSudoHome(sudoEnv(keep, 0), &SystemOptions{UID: -1}, true); err != nil {
		t.Fatal(err)
	}
}

func TestValidHostName(t *testing.T) {
	for _, h := range []string{"api.anthropic.com", "a-b_c.example", "1.2.3.4"} {
		if !validHostName(h) {
			t.Fatalf("%q refused", h)
		}
	}
	for _, h := range []string{"", "a b", "a\nb", "a\r\nHost: x", "a;b", "a}b", `a"b`, "-x", "a/b", "a:443", strings.Repeat("a", 300)} {
		if validHostName(h) {
			t.Fatalf("%q accepted", h)
		}
	}
}
