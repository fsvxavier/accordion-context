package cli

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsvxavier/accordion-context/internal/store"
)

// recorder is a docker runner that records every argv and runs nothing.
type recorder struct{ calls [][]string }

func (r *recorder) Run(_ context.Context, name string, args []string, _ io.Reader, _, _ io.Writer) error {
	r.calls = append(r.calls, append([]string{name}, args...))
	return nil
}

func useRecorder(t *testing.T) *recorder {
	t.Helper()
	rec := &recorder{}
	prev := dockerRunner
	dockerRunner = rec
	t.Cleanup(func() { dockerRunner = prev })
	return rec
}

// interactive makes the confirmation read as a terminal for the test.
func interactive(t *testing.T) {
	t.Helper()
	prev := cleanStdinIsTerminal
	cleanStdinIsTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { cleanStdinIsTerminal = prev })
}

// reportLine returns the text after "<prefix> <path>: " for the first matching line.
func reportLine(out, prefix string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			if i := strings.Index(line, ": "); i >= 0 {
				return line[i+2:]
			}
		}
	}
	return ""
}

func TestCleanFlagValidation(t *testing.T) {
	f := newFixture(t)
	for _, args := range [][]string{
		{"clean"},
		{"clean", "--all", "--session", "s1"},
		{"clean", "--session", "s1", "--project", "x"},
		{"clean", "--older-than", "soon", "--all"},
		{"clean", "--older-than", "0d", "--all"},
		{"clean", "--all", "--keep-config=false"},
		{"clean", "--all", "extra"},
	} {
		if r := f.run("", args...); r.code != ExitUsage {
			t.Fatalf("%v: code %d, want usage error; stderr %q", args, r.code, r.stderr)
		}
	}
}

// The dry run prints the Report the real run then prints, and deletes nothing.
func TestCleanDryRunEqualsRealAndDeletesNothingOnDryRun(t *testing.T) {
	f := newFixture(t)
	id := globalProjectID(f.root)
	seed(t, f.db, id, f.root, "global", "s1", 6)

	dry := f.run("", "clean", "--project", id, "--dry-run")
	if dry.code != ExitOK || !strings.Contains(dry.stdout, "dry run: nothing deleted") {
		t.Fatalf("dry run: code %d, stdout %q, stderr %q", dry.code, dry.stdout, dry.stderr)
	}
	if got := count(t, f.db, id, "messages"); got != 6 {
		t.Fatalf("dry run deleted messages: %d left", got)
	}
	want := reportLine(dry.stdout, "would delete from")
	if want == "" || !strings.Contains(want, "sessions=1 messages=6") {
		t.Fatalf("dry report = %q", want)
	}

	real := f.run("", "clean", "--project", id, "--yes")
	if real.code != ExitOK {
		t.Fatalf("real clean: code %d, stdout %q, stderr %q", real.code, real.stdout, real.stderr)
	}
	if got := reportLine(real.stdout, "deleted from"); got != want {
		t.Fatalf("real report %q != dry report %q", got, want)
	}
	if got := count(t, f.db, id, "messages"); got != 0 {
		t.Fatalf("messages after clean = %d", got)
	}
}

// Without a terminal and without --yes the command refuses (exit 2), even when
// the answer is on stdin.
func TestCleanRefusesNonInteractiveWithoutYes(t *testing.T) {
	f := newFixture(t)
	id := globalProjectID(f.root)
	seed(t, f.db, id, f.root, "global", "s1", 4)
	if r := f.run("delete\n", "clean", "--all"); r.code != ExitUsage {
		t.Fatalf("non-interactive --all: code %d, stderr %q", r.code, r.stderr)
	}
	if got := count(t, f.db, id, "messages"); got != 4 {
		t.Fatalf("refused clean deleted data: %d left", got)
	}
}

// At a terminal the typed word must match exactly: the project ID for
// --project, and the word delete for --all.
func TestCleanTypedConfirmation(t *testing.T) {
	interactive(t)
	f := newFixture(t)
	id := globalProjectID(f.root)
	seed(t, f.db, id, f.root, "global", "s1", 4)

	for _, in := range []string{"no\n", "yes\n", "delete\n", ""} {
		if r := f.run(in, "clean", "--project", id); r.code != ExitFail {
			t.Fatalf("input %q: code %d, want refused", in, r.code)
		}
	}
	if got := count(t, f.db, id, "messages"); got != 4 {
		t.Fatalf("refused confirmation deleted data: %d left", got)
	}
	if r := f.run(id+"\n", "clean", "--project", id); r.code != ExitOK {
		t.Fatalf("typed project id: code %d, stderr %q", r.code, r.stderr)
	}
	if got := count(t, f.db, id, "messages"); got != 0 {
		t.Fatalf("confirmed clean left %d messages", got)
	}

	seed(t, f.db, id, f.root, "global", "s2", 3)
	if r := f.run("yes\n", "clean", "--all"); r.code != ExitFail {
		t.Fatalf("--all accepted yes: code %d", r.code)
	}
	if r := f.run("delete\n", "clean", "--all"); r.code != ExitOK {
		t.Fatalf("--all with delete: code %d, stderr %q", r.code, r.stderr)
	}
}

// A backup that cannot be written aborts the clean before anything is deleted.
func TestCleanBackupFailureAbortsBeforeDelete(t *testing.T) {
	f := newFixture(t)
	id := globalProjectID(f.root)
	seed(t, f.db, id, f.root, "global", "s1", 4)

	bad := filepath.Join(t.TempDir(), "no", "such", "dir", "bk.db")
	r := f.run("", "clean", "--project", id, "--yes", "--backup", bad)
	if r.code != ExitFail || !strings.Contains(r.stderr, "nothing deleted") {
		t.Fatalf("backup failure: code %d, stderr %q", r.code, r.stderr)
	}
	if got := count(t, f.db, id, "messages"); got != 4 {
		t.Fatalf("data deleted after failed backup: %d left", got)
	}

	existing := filepath.Join(t.TempDir(), "bk.db")
	if err := os.WriteFile(existing, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := f.run("", "clean", "--project", id, "--yes", "--backup", existing); r.code != ExitFail {
		t.Fatalf("existing backup file replaced: code %d", r.code)
	}
	if b, _ := os.ReadFile(existing); string(b) != "keep me" {
		t.Fatal("existing backup overwritten")
	}
	if got := count(t, f.db, id, "messages"); got != 4 {
		t.Fatalf("data deleted: %d left", got)
	}

	good := filepath.Join(t.TempDir(), "good.db")
	if r := f.run("", "clean", "--project", id, "--yes", "--backup", good); r.code != ExitOK {
		t.Fatalf("clean with backup: code %d, stderr %q", r.code, r.stderr)
	}
	info, err := store.InspectFile(context.Background(), good)
	if err != nil {
		t.Fatalf("backup does not verify: %v", err)
	}
	if len(info.Sessions) != 1 {
		t.Fatalf("backup sessions = %v, want the one removed", info.Sessions)
	}
	if got := count(t, f.db, id, "messages"); got != 0 {
		t.Fatalf("clean after backup left %d messages", got)
	}
}

// --session removes only that session; --older-than removes sessions by their
// last activity, whole, across every activity table.
func TestCleanSessionAndOlderThan(t *testing.T) {
	f := newFixture(t)
	id := globalProjectID(f.root)
	seed(t, f.db, id, f.root, "global", "s1", 4)
	seed(t, f.db, id, f.root, "global", "s2", 3)

	if r := f.run("", "clean", "--session", "s1", "--yes"); r.code != ExitOK {
		t.Fatalf("session clean: code %d, stderr %q", r.code, r.stderr)
	}
	if got := count(t, f.db, id, "messages"); got != 3 {
		t.Fatalf("after --session s1: %d messages, want s2's 3", got)
	}
	if r := f.run("", "clean", "--session", "missing", "--yes"); r.code != ExitFail {
		t.Fatalf("unknown session: code %d", r.code)
	}

	if r := f.run("", "clean", "--older-than", "30d", "--yes"); r.code != ExitOK {
		t.Fatalf("older-than on fresh data: code %d, stderr %q", r.code, r.stderr)
	}
	if got := count(t, f.db, id, "messages"); got != 3 {
		t.Fatalf("fresh session removed by --older-than 30d: %d left", got)
	}

	ageSession(t, f.db, "s2", 1)
	r := f.run("", "clean", "--project", id, "--older-than", "30d", "--yes")
	if r.code != ExitOK {
		t.Fatalf("older-than with project: code %d, stderr %q", r.code, r.stderr)
	}
	if got := count(t, f.db, id, "messages"); got != 0 {
		t.Fatalf("aged session survived: %d messages", got)
	}
}

// ageSession moves every activity timestamp of sid to unix second at.
func ageSession(t *testing.T, path, sid string, at int64) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`UPDATE sessions SET created_at = ?1 WHERE id = ?2`,
		`UPDATE messages SET created_at = ?1 WHERE session_id = ?2`,
		`UPDATE events SET at = ?1 WHERE session_id = ?2`,
		`UPDATE cache_ledger SET at = ?1 WHERE session_id = ?2`,
	} {
		if _, err := db.Exec(q, at, sid); err != nil {
			t.Fatal(err)
		}
	}
}

// --exports removes only the files this tool writes for the removed session.
// Other files, another session's files and symbolic links stay.
func TestCleanExportsOnlyToolCreatedFiles(t *testing.T) {
	f := newFixture(t)
	id := globalProjectID(f.root)
	seed(t, f.db, id, f.root, "global", "s1", 4)
	seed(t, f.db, id, f.root, "global", "s2", 2)

	dir := filepath.Join(f.vars["HOME"], ".local", "share", "acd-ctx", "exports", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(dir, "b1-"+exportTag("s1")+".txt")
	other := filepath.Join(dir, "b1-"+exportTag("s2")+".txt")
	notes := filepath.Join(dir, "notes.txt")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	link := filepath.Join(dir, "b9-"+exportTag("s1")+".txt")
	for _, p := range []string{own, other, notes, outside} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if r := f.run("", "clean", "--session", "s1", "--exports", "--dry-run"); r.code != ExitOK || !strings.Contains(r.stdout, "would remove 1 exported file(s)") {
		t.Fatalf("dry run exports: code %d, stdout %q", r.code, r.stdout)
	}
	if _, err := os.Stat(own); err != nil {
		t.Fatal("dry run removed an export")
	}
	if r := f.run("", "clean", "--session", "s1", "--exports", "--yes"); r.code != ExitOK {
		t.Fatalf("clean exports: code %d, stderr %q", r.code, r.stderr)
	}
	for _, kept := range []string{other, notes, outside} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s removed: %v", kept, err)
		}
	}
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		t.Fatal("tool-created export of the removed session survived")
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symbolic link was touched")
	}
}

// --vacuum compacts after the delete.
func TestCleanVacuum(t *testing.T) {
	f := newFixture(t)
	id := globalProjectID(f.root)
	seed(t, f.db, id, f.root, "global", "s1", 4)
	r := f.run("", "clean", "--all", "--yes", "--vacuum")
	if r.code != ExitOK || !strings.Contains(r.stdout, "vacuumed") {
		t.Fatalf("vacuum: code %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
}

// The docker form runs the same flags inside the container, through the
// shared docker runner, after validating the container name.
func TestCleanDockerArgv(t *testing.T) {
	interactive(t)
	rec := useRecorder(t)
	f := newFixture(t)
	id := globalProjectID(f.root)

	if r := f.run("", "clean", "--docker", "bad name;rm", "--all"); r.code != ExitFail {
		t.Fatalf("bad container accepted: code %d", r.code)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("docker ran for a bad container name: %v", rec.calls)
	}

	if r := f.run("", "clean", "--docker", "box", "--project", id, "--dry-run"); r.code != ExitOK {
		t.Fatalf("docker dry run: code %d, stderr %q", r.code, r.stderr)
	}
	want := []string{"docker", "exec", "-i", "box", "acd-ctx", "clean", "--project", id, "--dry-run"}
	if len(rec.calls) != 1 || strings.Join(rec.calls[0], " ") != strings.Join(want, " ") {
		t.Fatalf("dry-run argv = %v, want %v", rec.calls, want)
	}

	rec.calls = nil
	if r := f.run(id+"\n", "clean", "--docker", "box", "--project", id, "--backup", filepath.Join(t.TempDir(), "x.db"), "--exports"); r.code == ExitOK {
		// The fake runner writes no backup file, so the backup step fails
		// first, and nothing inside the container may run.
		t.Fatal("docker clean ran after a failed backup")
	}
	for _, c := range rec.calls {
		if strings.Contains(strings.Join(c, " "), "--yes") {
			t.Fatalf("destructive call made after failed backup: %v", c)
		}
	}
	rec.calls = nil
	if r := f.run(id+"\n", "clean", "--docker", "box", "--project", id); r.code != ExitOK {
		t.Fatalf("docker clean: code %d, stderr %q", r.code, r.stderr)
	}
	last := rec.calls[len(rec.calls)-1]
	wantLast := []string{"docker", "exec", "-i", "box", "acd-ctx", "clean", "--project", id, "--yes"}
	if strings.Join(last, " ") != strings.Join(wantLast, " ") {
		t.Fatalf("real argv = %v, want %v", last, wantLast)
	}
	for _, c := range rec.calls {
		if strings.Contains(strings.Join(c, " "), "--docker") || strings.Contains(strings.Join(c, " "), "--backup") {
			t.Fatalf("host-only flag forwarded into the container: %v", c)
		}
	}
	if len(rec.calls) != 2 {
		t.Fatalf("calls = %d, want dry run then real run", len(rec.calls))
	}
}

func TestCleanDockerRejectsPathArgument(t *testing.T) {
	rec := useRecorder(t)
	f := newFixture(t)
	if r := f.run("", "clean", "--docker", "box", "--project", "."); r.code != ExitUsage {
		t.Fatalf("path with docker: code %d", r.code)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("docker ran: %v", rec.calls)
	}
}

// A day count that overflows time.Duration must be refused: wrapped around, it
// could put the cutoff in the future and select every session.
func TestParseAgeRejectsOverflow(t *testing.T) {
	for _, in := range []string{"9999999999d", "106752000d", "0d", "-3d", "abc", "0s"} {
		if d, err := parseAge(in); err == nil {
			t.Errorf("parseAge(%q) = %v, want error", in, d)
		}
	}
	for in, want := range map[string]time.Duration{"30d": 30 * 24 * time.Hour, "12h": 12 * time.Hour, "106751d": 106751 * 24 * time.Hour} {
		if d, err := parseAge(in); err != nil || d != want {
			t.Errorf("parseAge(%q) = %v, %v", in, d, err)
		}
	}
}
