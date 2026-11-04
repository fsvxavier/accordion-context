package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/backup"
)

func TestParseByteSize(t *testing.T) {
	for in, want := range map[string]int64{
		"1024": 1024, "2GiB": 2 << 30, "512MiB": 512 << 20, "4KiB": 4 << 10, "3G": 3 << 30, " 7 ": 7,
	} {
		got, err := parseByteSize(in)
		if err != nil || got != want {
			t.Fatalf("parseByteSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "-5", "2TB", "abc", "99999999999999999GiB", "1.5GiB"} {
		if _, err := parseByteSize(in); err == nil {
			t.Fatalf("parseByteSize(%q) accepted", in)
		}
	}
}

func TestRestoreCapFlagOverridesEnvAndDefault(t *testing.T) {
	env := func(vars map[string]string) IO {
		return IO{LookupEnv: func(k string) (string, bool) { v, ok := vars[k]; return v, ok }}
	}
	if n, err := restoreCap(env(nil), ""); err != nil || n != backup.DefaultMaxRestoreBytes || backup.DefaultMaxRestoreBytes != 2<<30 {
		t.Fatalf("default = %d, %v", n, err)
	}
	if n, err := restoreCap(env(map[string]string{maxRestoreEnv: "512MiB"}), ""); err != nil || n != 512<<20 {
		t.Fatalf("env = %d, %v", n, err)
	}
	if n, err := restoreCap(env(map[string]string{maxRestoreEnv: "512MiB"}), "1KiB"); err != nil || n != 1<<10 {
		t.Fatalf("flag over env = %d, %v", n, err)
	}
	if _, err := restoreCap(env(map[string]string{maxRestoreEnv: "lots"}), ""); err == nil || !strings.Contains(err.Error(), maxRestoreEnv) {
		t.Fatalf("bad env accepted: %v", err)
	}
}

func TestRestoreStdinOverCapIsRefusedWithUsageCode(t *testing.T) {
	f := newFixture(t)
	seed(t, f.db, globalProjectID(f.root), f.root, "global", "s1", 3)
	backupFile := filepath.Join(t.TempDir(), "p.db")
	if _, err := backup.Create(context.Background(), backup.CreateRequest{Source: f.db, Out: backupFile}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(backupFile)
	if err != nil {
		t.Fatal(err)
	}

	if r := f.run(string(data), "restore", "--stdin", "--max-size", "not-a-size"); r.code != ExitUsage {
		t.Fatalf("bad --max-size: code %d", r.code)
	}
	r := f.run(string(data), "restore", "--stdin", "--max-size", "100")
	if r.code == ExitOK || !strings.Contains(r.stderr, "size cap") {
		t.Fatalf("stream over cap: code %d, stderr %q", r.code, r.stderr)
	}
	fresh := newFixture(t)
	if r := fresh.run(string(data), "restore", "--stdin", "--max-size", "1GiB", "--dry-run"); r.code != ExitOK {
		t.Fatalf("stream under cap refused: code %d, stderr %q", r.code, r.stderr)
	}
}
