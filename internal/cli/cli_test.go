package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/project"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// fixture is one isolated invocation environment: a git project, a global
// database and config location, all under t.TempDir(). Nothing is read from
// the real environment or the working directory.
type fixture struct {
	t    *testing.T
	root string // project root (has .git, so it is a repository root)
	db   string // global database (ACD_CTX_DB)
	vars map[string]string
}

type result struct {
	code   int
	stdout string
	stderr string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "work", "app")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(base, "data", "acd-ctx.db")
	return &fixture{
		t:    t,
		root: root,
		db:   db,
		vars: map[string]string{
			"HOME":            filepath.Join(base, "home"),
			"XDG_CONFIG_HOME": filepath.Join(base, "config"),
			"ACD_CTX_DB":      db,
		},
	}
}

func (f *fixture) run(stdin string, args ...string) result {
	f.t.Helper()
	var out, errOut bytes.Buffer
	code := Run(args, IO{
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		Stderr: &errOut,
		LookupEnv: func(k string) (string, bool) {
			v, ok := f.vars[k]
			return v, ok
		},
		Cwd:     f.root,
		Version: "test",
	})
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

func (f *fixture) mustRun(args ...string) result {
	f.t.Helper()
	r := f.run("", args...)
	if r.code != ExitOK {
		f.t.Fatalf("%v: exit %d\nstdout: %s\nstderr: %s", args, r.code, r.stdout, r.stderr)
	}
	return r
}

// seed writes a project with one session of n messages into the database at
// path. The session has a compressed block, an intent, an event and a cache
// row, so every table a purge or migration must handle is populated.
func seed(t *testing.T, path, projectID, root, storage, sid string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.EnsureProject(ctx, projectID, root, storage, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(ctx, store.Session{
		ID: sid, ProjectID: projectID, Model: "m", ContextLimit: 200000, Mode: "proxy", Integration: "url",
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		role := kernel.RoleUser
		if i%2 == 1 {
			role = kernel.RoleAssistant
		}
		if _, err := st.AppendMessage(ctx, sid, store.NewMessage{
			Role: role, Kind: kernel.KindText, Text: fmt.Sprintf("%s message %d", sid, i+1), Tokens: 10,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ApplyCompress(ctx, sid, store.CompressPlan{
		NewBlocks: []store.NewBlock{{ID: "b1", Tier: 1, Topic: "t", Summary: "sum", StartSeq: 1, EndSeq: 4, SrcTokens: 40, SumTokens: 5}},
		Now:       2, TokensAfter: 30,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddIntent(ctx, sid, 1, "do the thing"); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendEvent(ctx, sid, "turn", map[string]int{"n": n}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordCache(ctx, sid, store.CacheCall{InputTokens: 100, CachedTokens: 50, EstTokens: 90}); err != nil {
		t.Fatal(err)
	}
}

// count returns the number of rows of a project in the database at path, by
// exporting it. A missing project counts as zero.
func count(t *testing.T, path, projectID, table string) int {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d, err := st.ExportProject(context.Background(), projectID)
	if err == store.ErrProjectNotFound || (err != nil && strings.Contains(err.Error(), "not found")) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range d.Tables {
		if tr.Table == table {
			return len(tr.Rows)
		}
	}
	t.Fatalf("no table %s in dump", table)
	return 0
}

// hasRow reports whether out has a line whose whitespace-separated fields
// start with want. The tabwriter pads columns, so exact spacing is not checked.
func hasRow(out string, want ...string) bool {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < len(want) {
			continue
		}
		ok := true
		for i := range want {
			if f[i] != want[i] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func TestUsageAndDispatch(t *testing.T) {
	f := newFixture(t)
	if r := f.run(""); r.code != ExitUsage || !strings.Contains(r.stderr, "project init") {
		t.Fatalf("no args: code %d, stderr %q", r.code, r.stderr)
	}
	if r := f.run("", "help"); r.code != ExitOK || !strings.Contains(r.stdout, "project migrate") {
		t.Fatalf("help: code %d, stdout %q", r.code, r.stdout)
	}
	if r := f.run("", "bogus"); r.code != ExitUsage {
		t.Fatalf("unknown command: code %d", r.code)
	}
	if r := f.run("", "project"); r.code != ExitUsage {
		t.Fatalf("project without subcommand: code %d", r.code)
	}
	if r := f.run("", "config", "show"); r.code != ExitUsage {
		t.Fatalf("config show without --effective: code %d", r.code)
	}
	if !strings.Contains(Usage(), "mcp") {
		t.Fatal("Usage lacks mcp")
	}
}

func TestInitCreatesFilesAndKeepsGlobalID(t *testing.T) {
	f := newFixture(t)
	expected, err := project.ResolveWith(f.root, projectMarkers, project.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	f.mustRun("project", "init")

	dir := filepath.Join(f.root, ".acd-ctx")
	id, err := os.ReadFile(filepath.Join(dir, "project-id"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(id)) != expected.ID {
		t.Fatalf("project-id = %q, want the global ID %q so history is kept", id, expected.ID)
	}
	if ign, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); !strings.Contains(string(ign), "acd-ctx.db") {
		t.Fatalf(".gitignore = %q", ign)
	}
	if _, err := os.Stat(filepath.Join(dir, "acd-ctx.db")); err == nil {
		t.Fatal("init without --local-storage must not create a local database")
	}

	show := f.mustRun("project", "show").stdout
	if !hasRow(show, "database", f.db) || !hasRow(show, "storage", "global") {
		t.Fatalf("project show = %q", show)
	}

	if r := f.run("", "project", "init"); r.code != ExitFail {
		t.Fatalf("second init: code %d, want %d", r.code, ExitFail)
	}
}

func TestInitLocalStorageLoadsAsLocal(t *testing.T) {
	f := newFixture(t)
	f.mustRun("project", "init", "--local-storage")

	if _, err := os.Stat(filepath.Join(f.root, ".acd-ctx", "acd-ctx.db")); err != nil {
		t.Fatalf("local database not created: %v", err)
	}
	out := f.mustRun("config", "show", "--effective").stdout
	if !hasRow(out, "storage", "local", "local:"+filepath.Join(f.root, ".acd-ctx", "config.yaml")) {
		t.Fatalf("storage not loaded from the local config layer:\n%s", out)
	}
	if show := f.mustRun("project", "show").stdout; !strings.Contains(show, filepath.Join(f.root, ".acd-ctx", "acd-ctx.db")) {
		t.Fatalf("project show does not use the local database:\n%s", show)
	}
}

func TestConfigShowEffectiveReportsSources(t *testing.T) {
	f := newFixture(t)
	f.vars["ACD_CTX_TRIGGER_STEP_BASE"] = "61000"
	out := f.mustRun("config", "show", "--effective").stdout
	if !hasRow(out, "trigger.step_base", "61000", "env:ACD_CTX_TRIGGER_STEP_BASE") {
		t.Fatalf("env layer not reported:\n%s", out)
	}
	if !hasRow(out, "trigger.u_floor", "0.3", "default") {
		t.Fatalf("default layer not reported:\n%s", out)
	}
}

func TestListAndShow(t *testing.T) {
	f := newFixture(t)
	if out := f.mustRun("project", "list").stdout; !strings.Contains(out, "no projects") {
		t.Fatalf("empty list = %q", out)
	}
	id := globalProjectID(f.root)
	seed(t, f.db, id, f.root, "global", "s1", 6)

	out := f.mustRun("project", "list").stdout
	if !strings.Contains(out, id) || !strings.Contains(out, f.root) {
		t.Fatalf("list lacks the project:\n%s", out)
	}
}

func TestMoveRepointsRootAndKeepsID(t *testing.T) {
	f := newFixture(t)
	oldRoot := filepath.Join(filepath.Dir(f.root), "old-app") // no longer exists
	id := globalProjectID(oldRoot)
	seed(t, f.db, id, oldRoot, "global", "s1", 6)

	f.mustRun("project", "move", oldRoot, f.root)

	st, err := store.Open(f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	info, err := st.GetProject(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if info.RootPath != f.root || info.ID != id {
		t.Fatalf("after move: root %q id %q, want root %q id %q", info.RootPath, info.ID, f.root, id)
	}

	// Moved project keeps its history: resolving the new root finds the old ID.
	if out := f.mustRun("project", "show").stdout; !strings.Contains(out, id) {
		t.Fatalf("project show after move lacks the preserved id %s:\n%s", id, out)
	}
	// A second move addressed by the current path still finds it (not by hash).
	newer := filepath.Join(filepath.Dir(f.root), "newer-app")
	if err := os.MkdirAll(newer, 0o755); err != nil {
		t.Fatal(err)
	}
	f.mustRun("project", "move", f.root, newer)
	if info, err := st.GetProject(context.Background(), id); err != nil || info.RootPath != newer {
		t.Fatalf("second move: %+v, %v", info, err)
	}

	if r := f.run("", "project", "move", "no-such-project", f.root); r.code != ExitFail {
		t.Fatalf("move of unknown project: code %d, want %d", r.code, ExitFail)
	}
	if r := f.run("", "project", "move", oldRoot); r.code != ExitUsage {
		t.Fatalf("move with one argument: code %d", r.code)
	}
}

func TestMigrateToLocalAndBackIsGuarded(t *testing.T) {
	f := newFixture(t)
	id := globalProjectID(f.root)
	seed(t, f.db, id, f.root, "global", "s1", 6)
	f.mustRun("project", "init")

	// Without .acd-ctx there is nowhere to migrate to.
	noInit := newFixture(t)
	if r := noInit.run("", "project", "migrate", "--to", "local"); r.code != ExitFail {
		t.Fatalf("migrate without init: code %d", r.code)
	}

	out := f.mustRun("project", "migrate", "--to", "local").stdout
	if !strings.Contains(out, "migrated project "+id) {
		t.Fatalf("migrate output = %q", out)
	}
	local := filepath.Join(f.root, ".acd-ctx", "acd-ctx.db")
	if got := count(t, local, id, "messages"); got != 6 {
		t.Fatalf("local messages = %d, want 6", got)
	}
	if got := count(t, local, id, "blocks"); got != 1 {
		t.Fatalf("local blocks = %d, want 1", got)
	}
	if got := count(t, f.db, id, "messages"); got != 6 {
		t.Fatalf("source messages = %d, want 6 (copy, not move)", got)
	}
	if got := f.mustRun("config", "show", "--effective").stdout; !hasRow(got, "storage", "local") {
		t.Fatalf("config not switched to local:\n%s", got)
	}

	// The same sessions already exist at the destination: refuse, change nothing.
	if r := f.run("", "project", "migrate", "--to", "local"); r.code != ExitFail {
		t.Fatalf("repeat migrate: code %d, want %d", r.code, ExitFail)
	}
	if got := count(t, local, id, "messages"); got != 6 {
		t.Fatalf("local messages after refused repeat = %d, want 6", got)
	}
}

func TestPurgeConfirmationAndDeletion(t *testing.T) {
	f := newFixture(t)
	id := globalProjectID(f.root)
	seed(t, f.db, id, f.root, "global", "s1", 6)

	if r := f.run("no\n", "project", "purge", id); r.code != ExitFail {
		t.Fatalf("declined purge: code %d", r.code)
	}
	if got := count(t, f.db, id, "messages"); got != 6 {
		t.Fatalf("declined purge deleted data: %d messages", got)
	}
	if r := f.run("", "project", "purge", id); r.code != ExitFail {
		t.Fatalf("purge at EOF: code %d", r.code)
	}

	r := f.run("yes\n", "project", "purge", id)
	if r.code != ExitOK || !strings.Contains(r.stdout, "purged project "+id) {
		t.Fatalf("confirmed purge: code %d, stdout %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	st, err := store.Open(f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.GetProject(context.Background(), id); err == nil {
		t.Fatal("project still present after purge")
	}
	if got := f.mustRun("project", "list").stdout; strings.Contains(got, id) {
		t.Fatalf("list still shows purged project:\n%s", got)
	}

	seed(t, f.db, id, f.root, "global", "s2", 3)
	if r := f.run("", "project", "purge", id, "--yes"); r.code != ExitOK {
		t.Fatalf("--yes purge after flag-last: code %d, stderr %q", r.code, r.stderr)
	}
	if r := f.run("", "project", "purge", "missing-id", "--yes"); r.code != ExitFail {
		t.Fatalf("purge of unknown id: code %d", r.code)
	}
}

func TestImportRollsBackWhenSessionExists(t *testing.T) {
	f := newFixture(t)
	id := globalProjectID(f.root)
	seed(t, f.db, id, f.root, "global", "s1", 6)

	src, err := store.Open(f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dump, err := src.ExportProject(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}

	dstPath := filepath.Join(filepath.Dir(f.db), "other.db")
	dst, err := store.Open(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	target := store.ImportTarget{ProjectID: id, RootPath: f.root, Storage: "global"}
	if _, err := dst.ImportProject(context.Background(), dump, target); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if _, err := dst.ImportProject(context.Background(), dump, target); err == nil {
		t.Fatal("second import of the same session succeeded")
	}
	d2, err := dst.ExportProject(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range d2.Tables {
		if tr.Table == "messages" && len(tr.Rows) != 6 {
			t.Fatalf("failed import changed messages: %d, want 6", len(tr.Rows))
		}
	}
	if len(dump.SessionIDs()) != 1 {
		t.Fatalf("SessionIDs = %v", dump.SessionIDs())
	}
}

func TestClientLaunchErrorsWithoutStartingAProxy(t *testing.T) {
	f := newFixture(t)
	if r := f.run("", "run"); r.code != ExitUsage {
		t.Fatalf("run without command: code %d", r.code)
	}
	if r := f.run("", "run", "--"); r.code != ExitUsage {
		t.Fatalf("run -- without command: code %d", r.code)
	}
	if r := f.run("", "run", "--", "nosuchclient", "x"); r.code != ExitFail || !strings.Contains(r.stderr, "no client profile") {
		t.Fatalf("unknown client: code %d, stderr %q", r.code, r.stderr)
	}
}

// TestOpencodeLaunchesThroughTempConfig runs a fake opencode that requires the
// OPENCODE_CONFIG file, written by the launcher, to point at a provider baseURL.
func TestOpencodeLaunchesThroughTempConfig(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		`[ -f "$OPENCODE_CONFIG" ] && grep -q '"baseURL"' "$OPENCODE_CONFIG" || exit 7` + "\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CLI_TEST_FAKE_SERVE", "1")
	t.Setenv("TMPDIR", t.TempDir())
	f := newFixture(t)
	if r := f.run("", "opencode"); r.code != ExitOK || strings.Contains(r.stderr, "MITM") {
		t.Fatalf("opencode: code %d, stderr %q", r.code, r.stderr)
	}
}

func TestMCPAnswersInitializeOverStdio(t *testing.T) {
	f := newFixture(t)
	r := f.run(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`+"\n", "mcp")
	if r.code != ExitOK {
		t.Fatalf("mcp: code %d, stderr %q", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, `"name":"acd-ctx"`) {
		t.Fatalf("no initialize response on stdout: %q", r.stdout)
	}
	if strings.Contains(r.stdout, "level=") {
		t.Fatalf("diagnostics leaked onto the protocol stream: %q", r.stdout)
	}
	if !fileExists(f.db) {
		t.Fatal("mcp did not open the global database")
	}
}

func TestGlobalProjectIDMatchesResolver(t *testing.T) {
	f := newFixture(t)
	res, err := project.ResolveWith(f.root, projectMarkers, project.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := globalProjectID(res.Root); got != res.ID {
		t.Fatalf("globalProjectID = %q, resolver says %q", got, res.ID)
	}
}
