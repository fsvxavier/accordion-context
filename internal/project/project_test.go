package project

import (
	"os"
	"path/filepath"
	"testing"
)

var defaultMarkers = []string{"go.mod", "package.json", "pyproject.toml", "composer.json", "Cargo.toml"}

func mkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func touch(t *testing.T, p string) {
	t.Helper()
	mkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(root string)
		cwdRel     string
		wantRoot   string // relative to root; "" means cwd-based (checked via Source)
		wantSource string
		wantStore  string
	}{
		{
			name: "git dir at root, cwd nested",
			setup: func(root string) {
				mkdir(t, filepath.Join(root, ".git"))
				mkdir(t, filepath.Join(root, "a", "b", "c"))
			},
			cwdRel:     "a/b/c",
			wantRoot:   "",
			wantSource: sourceGit,
			wantStore:  storageGlobal,
		},
		{
			name: "worktree .git file counts as git root",
			setup: func(root string) {
				touch(t, filepath.Join(root, "wt", ".git"))
				mkdir(t, filepath.Join(root, "wt", "pkg", "x"))
			},
			cwdRel:     "wt/pkg/x",
			wantRoot:   "wt",
			wantSource: sourceGit,
			wantStore:  storageGlobal,
		},
		{
			name: "closer .git wins over outer .git",
			setup: func(root string) {
				mkdir(t, filepath.Join(root, ".git"))
				mkdir(t, filepath.Join(root, "sub", ".git"))
				mkdir(t, filepath.Join(root, "sub", "deep"))
			},
			cwdRel:     "sub/deep",
			wantRoot:   "sub",
			wantSource: sourceGit,
			wantStore:  storageGlobal,
		},
		{
			name: "acd-ctx beats .git at same level",
			setup: func(root string) {
				mkdir(t, filepath.Join(root, ".git"))
				mkdir(t, filepath.Join(root, ".acd-ctx"))
				mkdir(t, filepath.Join(root, "src"))
			},
			cwdRel:     "src",
			wantRoot:   "",
			wantSource: sourceLocalDir,
			wantStore:  storageLocal,
		},
		{
			name: "nearest level wins: .git in cwd beats .acd-ctx in ancestor",
			setup: func(root string) {
				mkdir(t, filepath.Join(root, ".acd-ctx"))
				mkdir(t, filepath.Join(root, "mod", ".git"))
				mkdir(t, filepath.Join(root, "mod", "x"))
			},
			cwdRel:     "mod/x",
			wantRoot:   "mod",
			wantSource: sourceGit,
			wantStore:  storageGlobal,
		},
		{
			name: "marker fallback without git",
			setup: func(root string) {
				touch(t, filepath.Join(root, "svc", "go.mod"))
				mkdir(t, filepath.Join(root, "svc", "internal", "p"))
			},
			cwdRel:     "svc/internal/p",
			wantRoot:   "svc",
			wantSource: sourceMarker,
			wantStore:  storageGlobal,
		},
		{
			name: "cwd fallback when nothing matches",
			setup: func(root string) {
				mkdir(t, filepath.Join(root, "plain", "dir"))
			},
			cwdRel:     "plain/dir",
			wantRoot:   "plain/dir",
			wantSource: sourceCWD,
			wantStore:  storageGlobal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tt.setup(root)
			cwd := filepath.Join(root, tt.cwdRel)

			got, err := Resolve(cwd, defaultMarkers)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got.Source != tt.wantSource {
				t.Errorf("Source = %q, want %q", got.Source, tt.wantSource)
			}
			if got.Storage != tt.wantStore {
				t.Errorf("Storage = %q, want %q", got.Storage, tt.wantStore)
			}
			wantRoot := filepath.Join(root, tt.wantRoot)
			if tt.wantRoot == "" {
				wantRoot = root
				if tt.wantSource == sourceCWD {
					wantRoot = cwd
				}
			}
			if got.Root != wantRoot {
				t.Errorf("Root = %q, want %q", got.Root, wantRoot)
			}
		})
	}
}

func TestLocalModeReadsAndKeepsProjectID(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	local := mkdir(t, filepath.Join(root, ".acd-ctx"))
	touch(t, filepath.Join(local, projectIDName))
	if err := os.WriteFile(filepath.Join(local, projectIDName), []byte("my-id\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Resolve(filepath.Join(root, "x"), nil)
	if err != nil {
		// x does not exist; EvalSymlinks fails, so create it
		mkdir(t, filepath.Join(root, "x"))
		got, err = Resolve(filepath.Join(root, "x"), nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if got.ID != "my-id" {
		t.Errorf("ID = %q, want my-id", got.ID)
	}
	if got.Root != root {
		t.Errorf("Root = %q, want %q", got.Root, root)
	}
}

func TestLocalIDCreatedWhenMissingAndStable(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mkdir(t, filepath.Join(root, ".acd-ctx"))

	first, err := Resolve(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" {
		t.Fatal("empty ID")
	}
	second, err := Resolve(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Errorf("ID not stable: %q vs %q", first.ID, second.ID)
	}
}

func TestLocalIDSurvivesMove(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	orig := mkdir(t, filepath.Join(base, "old"))
	mkdir(t, filepath.Join(orig, ".acd-ctx"))
	before, err := Resolve(orig, nil)
	if err != nil {
		t.Fatal(err)
	}

	moved := filepath.Join(base, "new")
	if err := os.Rename(orig, moved); err != nil {
		t.Fatal(err)
	}
	after, err := Resolve(moved, nil)
	if err != nil {
		t.Fatal(err)
	}
	if before.ID != after.ID {
		t.Errorf("local ID changed after move: %q vs %q", before.ID, after.ID)
	}
}

func TestGlobalIDStableAndPathDependent(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := mkdir(t, filepath.Join(root, "a"))
	b := mkdir(t, filepath.Join(root, "b"))
	mkdir(t, filepath.Join(a, ".git"))
	mkdir(t, filepath.Join(b, ".git"))

	ra1, _ := Resolve(a, nil)
	ra2, _ := Resolve(a, nil)
	rb, _ := Resolve(b, nil)

	if ra1.ID != ra2.ID {
		t.Errorf("global ID not stable: %q vs %q", ra1.ID, ra2.ID)
	}
	if len(ra1.ID) != 16 {
		t.Errorf("global ID len = %d, want 16 hex chars", len(ra1.ID))
	}
	if ra1.ID == rb.ID {
		t.Error("different roots produced same global ID")
	}
}

func TestResolveMissingCwdErrors(t *testing.T) {
	if _, err := Resolve(filepath.Join(t.TempDir(), "nope"), nil); err == nil {
		t.Error("expected error for missing cwd")
	}
}

func TestResolveWithReadOnlyNeverWritesProjectID(t *testing.T) {
	root := t.TempDir()
	localDir := mkdir(t, filepath.Join(root, ".acd-ctx"))
	cwd := mkdir(t, filepath.Join(root, "a"))

	res, err := ResolveWith(cwd, defaultMarkers, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(localDir, projectIDName)); !os.IsNotExist(err) {
		t.Fatalf("read-only resolve created project-id (stat err %v)", err)
	}
	if res.Storage != storageGlobal || res.ID == "" {
		t.Fatalf("read-only fallback = %+v, want global hash id", res)
	}

	// Without ReadOnly the same directory gets a local identity, as before.
	rw, err := Resolve(cwd, defaultMarkers)
	if err != nil {
		t.Fatal(err)
	}
	if rw.Storage != storageLocal || rw.ID == res.ID {
		t.Fatalf("writable resolve = %+v", rw)
	}
	if _, err := os.Stat(filepath.Join(localDir, projectIDName)); err != nil {
		t.Fatalf("writable resolve did not create project-id: %v", err)
	}
	// Once the ID exists, ReadOnly reads it.
	again, err := ResolveWith(cwd, defaultMarkers, Options{ReadOnly: true})
	if err != nil || again.ID != rw.ID {
		t.Fatalf("read-only after create = %+v, %v; want id %s", again, err, rw.ID)
	}
}

func TestIDByRootHookOverridesGlobalHashOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	plain, err := ResolveWith(dir, nil, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var asked string
	hook := Options{ReadOnly: true, IDByRoot: func(root string) (string, bool) { asked = root; return "moved-id", true }}
	got, err := ResolveWith(dir, nil, hook)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "moved-id" || asked != plain.Root {
		t.Fatalf("id %q asked %q, want moved-id / %q", got.ID, asked, plain.Root)
	}
	hook.IDByRoot = func(string) (string, bool) { return "", false }
	if got, _ = ResolveWith(dir, nil, hook); got.ID != plain.ID {
		t.Fatalf("miss must fall back to the hash: %q != %q", got.ID, plain.ID)
	}
	// Local projects ignore the hook: their ID is the project-id file.
	if err := os.MkdirAll(filepath.Join(dir, ".acd-ctx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".acd-ctx", "project-id"), []byte("local-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hook.IDByRoot = func(string) (string, bool) { return "moved-id", true }
	if got, _ = ResolveWith(dir, nil, hook); got.ID != "local-1" {
		t.Fatalf("local id = %q", got.ID)
	}
}
