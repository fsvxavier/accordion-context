package backup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fakeRunner records every invocation and answers backup calls with canned bytes.
type fakeRunner struct {
	name  string
	args  []string
	stdin []byte
	reply []byte
	calls int
}

func (f *fakeRunner) Run(_ context.Context, name string, args []string, stdin io.Reader, stdout, _ io.Writer) error {
	f.calls++
	f.name = name
	f.args = append([]string(nil), args...)
	if stdin != nil {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		f.stdin = b
	}
	if stdout != nil && f.reply != nil {
		_, err := stdout.Write(f.reply)
		return err
	}
	return nil
}

func builtBackup(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	seed(t, src, "p1", "/work/p1", "s1")
	out := filepath.Join(dir, "p1.db")
	mustCreate(t, CreateRequest{Source: src, ProjectID: "p1", Out: out})
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDockerBackupArgvAndInstall(t *testing.T) {
	data := builtBackup(t)
	fake := &fakeRunner{reply: data}
	out := filepath.Join(t.TempDir(), "from-box.db")

	if err := DockerBackup(context.Background(), fake, "acd-box", "p1", out, false, nil, io.Discard); err != nil {
		t.Fatalf("DockerBackup: %v", err)
	}
	want := []string{"exec", "-i", "acd-box", "acd-ctx", "backup", "--stdout", "--project", "p1"}
	if fake.name != "docker" || !reflect.DeepEqual(fake.args, want) {
		t.Fatalf("argv = %s %v, want docker %v", fake.name, fake.args, want)
	}
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("installed file differs from the stream (%v)", err)
	}
	fi, _ := os.Stat(out)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o, want 600", fi.Mode().Perm())
	}

	// The file exists now: a second run without force must not run docker at all.
	fake2 := &fakeRunner{reply: data}
	if err := DockerBackup(context.Background(), fake2, "acd-box", "", out, false, nil, io.Discard); err == nil {
		t.Fatal("overwrite without force accepted")
	}
	if fake2.calls != 0 {
		t.Fatal("docker ran although the output exists")
	}
}

func TestDockerBackupRejectsBadStream(t *testing.T) {
	fake := &fakeRunner{reply: []byte("not a database")}
	out := filepath.Join(t.TempDir(), "bad.db")
	if err := DockerBackup(context.Background(), fake, "box", "", out, false, nil, io.Discard); err == nil {
		t.Fatal("garbage stream installed")
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("garbage stream left an output file")
	}
}

func TestDockerRestoreArgvAndStdin(t *testing.T) {
	data := builtBackup(t)
	fake := &fakeRunner{}
	opts := DockerRestoreOptions{Project: "p1", Mode: ModeReplace, To: StorageGlobal, Yes: true, DryRun: true}
	if err := DockerRestore(context.Background(), fake, "acd-box", bytes.NewReader(data), opts, io.Discard, io.Discard); err != nil {
		t.Fatalf("DockerRestore: %v", err)
	}
	want := []string{"exec", "-i", "acd-box", "acd-ctx", "restore", "--stdin",
		"--mode", "replace", "--to", "global", "--project", "p1", "--yes", "--dry-run"}
	if fake.name != "docker" || !reflect.DeepEqual(fake.args, want) {
		t.Fatalf("argv = %s %v\nwant docker %v", fake.name, fake.args, want)
	}
	if !bytes.Equal(fake.stdin, data) {
		t.Fatal("container stdin is not the backup file")
	}
}

func TestDockerContainerNameIsValidated(t *testing.T) {
	for _, ok := range []string{"acd-box", "box_1", "a.b-c", "9lives"} {
		if err := ValidContainer(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-privileged", "box;rm -rf /", "a b", "$(id)", ".hidden", "x/y"} {
		if err := ValidContainer(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	fake := &fakeRunner{}
	if err := DockerRestore(context.Background(), fake, "-x", nil, DockerRestoreOptions{Mode: ModeMerge, To: StorageGlobal}, io.Discard, io.Discard); err == nil || fake.calls != 0 {
		t.Fatal("invalid container reached the runner")
	}
}
