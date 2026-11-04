package cli

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/backup"
	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// backupEnv builds IO with an isolated HOME and ACD_CTX_DB, so no test reads
// the developer's own configuration or database.
func backupEnv(t *testing.T, cwd, db string) (IO, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	env := map[string]string{"ACD_CTX_DB": db, "HOME": t.TempDir()}
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	return IO{
		Stdin:  strings.NewReader(""),
		Stdout: out,
		Stderr: errb,
		Cwd:    cwd,
		LookupEnv: func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		},
	}, out, errb
}

// seedCLI writes one project with sessions and messages into the database at path.
func seedCLI(t *testing.T, path, project, root string, sessions ...string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.EnsureProject(ctx, project, root, "global", 1); err != nil {
		t.Fatal(err)
	}
	for _, sid := range sessions {
		if err := st.CreateSession(ctx, store.Session{
			ID: sid, ProjectID: project, Model: "m", ContextLimit: 1000, Mode: "proxy", Integration: "url",
		}); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 3; i++ {
			if _, err := st.AppendMessage(ctx, sid, store.NewMessage{
				Role: kernel.RoleUser, Kind: kernel.KindText, Text: fmt.Sprintf("%s %d", sid, i), Tokens: 5,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func countIn(t *testing.T, path, table string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// fakeDocker replaces the docker runner for one test and restores it after.
type fakeDocker struct {
	name  string
	args  []string
	calls int
}

func useFakeDocker(t *testing.T) *fakeDocker {
	t.Helper()
	f := &fakeDocker{}
	prev := dockerRunner
	dockerRunner = runnerFunc(func(name string, args []string) {
		f.calls++
		f.name = name
		f.args = append([]string(nil), args...)
	})
	t.Cleanup(func() { dockerRunner = prev })
	return f
}

type runnerFunc func(name string, args []string)

func (fn runnerFunc) Run(_ context.Context, name string, args []string, _ io.Reader, _, _ io.Writer) error {
	fn(name, args)
	return nil
}

var _ backup.Runner = runnerFunc(nil)

func TestBackupDockerArgv(t *testing.T) {
	dir := t.TempDir()
	f := useFakeDocker(t)
	s, _, _ := backupEnv(t, dir, filepath.Join(dir, "g.db"))
	if code := Run([]string{"backup", "--docker", "box", "--project", "p1", "--stdout"}, s); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	want := []string{"exec", "-i", "box", "acd-ctx", "backup", "--stdout", "--project", "p1"}
	if f.name != "docker" || !reflect.DeepEqual(f.args, want) {
		t.Fatalf("argv = %s %v, want docker %v", f.name, f.args, want)
	}
}

func TestRestoreDockerArgvAndBadContainer(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "in.db")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := useFakeDocker(t)
	s, _, _ := backupEnv(t, dir, filepath.Join(dir, "g.db"))
	code := Run([]string{"restore", file, "--docker", "box", "--project", "p1",
		"--mode", "replace", "--yes", "--dry-run"}, s)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	want := []string{"exec", "-i", "box", "acd-ctx", "restore", "--stdin",
		"--mode", "replace", "--to", "global", "--project", "p1", "--yes", "--dry-run"}
	if !reflect.DeepEqual(f.args, want) {
		t.Fatalf("argv = %v, want %v", f.args, want)
	}

	before := f.calls
	if code := Run([]string{"restore", file, "--docker", "-privileged"}, s); code == ExitOK || f.calls != before {
		t.Fatalf("bad container name reached docker (exit %d)", code)
	}
}

func TestBackupRestoreRoundTripCLI(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	seedCLI(t, src, "p1", "/work/p1", "s1", "s2")
	seedCLI(t, src, "p2", "/work/p2", "s3")

	out := filepath.Join(dir, "p1.db")
	s, _, errb := backupEnv(t, dir, src)
	if code := Run([]string{"backup", "--project", "p1", "-o", out}, s); code != ExitOK {
		t.Fatalf("backup exit %d: %s", code, errb)
	}
	if fi, err := os.Stat(out); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("backup file perm/err: %v %v", fi, err)
	}
	if n := countIn(t, out, "projects"); n != 1 {
		t.Fatalf("projects in backup = %d, want 1", n)
	}

	dst := filepath.Join(dir, "dst.db")
	s, outb, errb := backupEnv(t, dir, dst)
	if code := Run([]string{"restore", out}, s); code != ExitOK {
		t.Fatalf("restore exit %d: %s", code, errb)
	}
	if n := countIn(t, dst, "messages"); n != 6 {
		t.Fatalf("messages after restore = %d, want 6", n)
	}
	if !strings.Contains(outb.String(), "restored project p1") {
		t.Fatalf("report = %q", outb.String())
	}

	s, _, errb = backupEnv(t, dir, dst)
	if code := Run([]string{"restore", out}, s); code != ExitFail {
		t.Fatalf("second merge exit %d, want %d", code, ExitFail)
	}
	if !strings.Contains(errb.String(), "restore refused, nothing written") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

func TestBackupLocalProjectAndRestoreToLocal(t *testing.T) {
	dir := t.TempDir()
	projA := filepath.Join(dir, "a")
	projB := filepath.Join(dir, "b")
	for _, p := range []string{projA, projB} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "go.mod"), []byte("module x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		s, _, errb := backupEnv(t, p, filepath.Join(dir, "unused.db"))
		if code := Run([]string{"project", "init", "--local-storage"}, s); code != ExitOK {
			t.Fatalf("project init exit %d: %s", code, errb)
		}
	}
	localA := filepath.Join(projA, ".acd-ctx", "acd-ctx.db")
	id, err := readLocalProjectID(projA)
	if err != nil {
		t.Fatal(err)
	}
	seedCLI(t, localA, id, projA, "s1")
	seedLocalStorage(t, localA)

	// "." from the project resolves to its local database, which is copied whole.
	out := filepath.Join(dir, "local.db")
	s, _, errb := backupEnv(t, projA, filepath.Join(dir, "unused.db"))
	if code := Run([]string{"backup", "--project", ".", "-o", out}, s); code != ExitOK {
		t.Fatalf("local backup exit %d: %s", code, errb)
	}

	if n := countIn(t, out, "messages"); n != 3 {
		t.Fatalf("local backup messages = %d, want 3", n)
	}

	// Restore into project B's local database. Its ID comes from B's project-id.
	s, _, errb = backupEnv(t, projB, filepath.Join(dir, "unused.db"))
	if code := Run([]string{"restore", out, "--to", "local"}, s); code != ExitOK {
		t.Fatalf("restore --to local exit %d: %s", code, errb)
	}
	localB := filepath.Join(projB, ".acd-ctx", "acd-ctx.db")
	if n := countIn(t, localB, "messages"); n != 3 {
		t.Fatalf("messages in project B = %d, want 3", n)
	}
	db, err := sql.Open("sqlite", localB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var pid string
	if err := db.QueryRow(`SELECT project_id FROM sessions LIMIT 1`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	idB, _ := readLocalProjectID(projB)
	if pid != idB {
		t.Fatalf("session project = %s, want project B's id %s", pid, idB)
	}
}

// seedLocalStorage marks the project's registration as local, the way a local
// project's database is stored.
func seedLocalStorage(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE projects SET storage = 'local'`); err != nil {
		t.Fatal(err)
	}
}
