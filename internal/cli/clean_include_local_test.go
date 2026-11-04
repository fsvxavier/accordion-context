package cli

import (
	"strings"
	"testing"
)

// The local database of the current directory is opt-in for --all and
// --older-than without --project; it is listed as skipped otherwise.
func TestCleanAllSkipsLocalDatabaseUnlessIncluded(t *testing.T) {
	f := newFixture(t)
	seed(t, f.db, globalProjectID(f.root)+"g", "/g", "global", "g1", 2)
	local := localDBPath(f.root)
	seed(t, local, "localproj", f.root, "local", "l1", 3)

	dry := f.run("", "clean", "--all", "--dry-run")
	if dry.code != ExitOK {
		t.Fatalf("dry run: code %d, stderr %q", dry.code, dry.stderr)
	}
	if !strings.Contains(dry.stdout, "skipped (use --include-local): "+local) {
		t.Fatalf("local database not listed as skipped:\n%s", dry.stdout)
	}
	if strings.Contains(dry.stdout, "would delete from "+local) {
		t.Fatalf("local database planned without --include-local:\n%s", dry.stdout)
	}

	if r := f.run("", "clean", "--all", "--yes"); r.code != ExitOK {
		t.Fatalf("real clean: code %d, stderr %q", r.code, r.stderr)
	}
	if got := count(t, local, "localproj", "messages"); got != 3 {
		t.Fatalf("local messages = %d after --all without --include-local", got)
	}

	older := f.run("", "clean", "--older-than", "1d", "--dry-run")
	if !strings.Contains(older.stdout, "skipped (use --include-local): "+local) {
		t.Fatalf("--older-than did not list the local database as skipped:\n%s", older.stdout)
	}

	inc := f.run("", "clean", "--all", "--dry-run", "--include-local")
	if inc.code != ExitOK || !strings.Contains(inc.stdout, "would delete from "+local) || strings.Contains(inc.stdout, "skipped") {
		t.Fatalf("--include-local: code %d, stdout %q", inc.code, inc.stdout)
	}
}

func TestCleanSkippedOnlyLocalFailsWithHint(t *testing.T) {
	f := newFixture(t)
	seed(t, localDBPath(f.root), "localproj", f.root, "local", "l1", 1)
	r := f.run("", "clean", "--all", "--dry-run")
	if r.code == ExitOK || !strings.Contains(r.stderr, "--include-local") {
		t.Fatalf("only local database: code %d, stderr %q", r.code, r.stderr)
	}
}

func TestCleanDockerForwardsIncludeLocal(t *testing.T) {
	interactive(t)
	rec := useRecorder(t)
	f := newFixture(t)
	if r := f.run("", "clean", "--docker", "box", "--all", "--dry-run", "--include-local"); r.code != ExitOK {
		t.Fatalf("code %d, stderr %q", r.code, r.stderr)
	}
	joined := strings.Join(rec.calls[0], " ")
	if !strings.Contains(joined, "--include-local") {
		t.Fatalf("container argv lacks --include-local: %s", joined)
	}
}

// The confirmation word is matched exactly, minus only the trailing newline.
func TestTypedEqualsIsExact(t *testing.T) {
	for in, want := range map[string]bool{
		"delete\n":   true,
		"delete":     true,
		" delete\n":  false,
		"delete \n":  false,
		"delete\r\n": false,
		"Delete\n":   false,
		"delete\n\n": true,
		"":           false,
	} {
		if got := typedEquals(strings.NewReader(in), "delete"); got != want {
			t.Fatalf("typedEquals(%q) = %v, want %v", in, got, want)
		}
	}
}

// --session without --project also leaves the local database out unless
// --include-local is given, like --all and --older-than.
func TestCleanSessionSkipsLocalDatabaseUnlessIncluded(t *testing.T) {
	f := newFixture(t)
	seed(t, f.db, globalProjectID(f.root), f.root, "global", "s1", 2)
	local := localDBPath(f.root)
	seed(t, local, "localproj", f.root, "local", "ls1", 3)

	dry := f.run("", "clean", "--session", "ls1", "--dry-run")
	if dry.code == ExitOK || !strings.Contains(dry.stderr, "--include-local") {
		t.Fatalf("local-only session without --include-local: code %d, stdout %q, stderr %q", dry.code, dry.stdout, dry.stderr)
	}
	if !strings.Contains(dry.stdout, "skipped (use --include-local): "+local) {
		t.Fatalf("local database not listed as skipped:\n%s", dry.stdout)
	}

	inc := f.run("", "clean", "--session", "ls1", "--dry-run", "--include-local")
	if inc.code != ExitOK || !strings.Contains(inc.stdout, "would delete from "+local) {
		t.Fatalf("--include-local: code %d, stdout %q, stderr %q", inc.code, inc.stdout, inc.stderr)
	}
	if got := count(t, local, "localproj", "messages"); got != 3 {
		t.Fatalf("dry run deleted local messages: %d", got)
	}
}
