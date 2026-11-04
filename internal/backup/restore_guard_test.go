package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/store"
	"github.com/klauspost/compress/zstd"
)

// forgeContent rewrites the stored bytes of one contents row in the backup
// file at path, keeping its hash and size, so the row no longer matches them.
func forgeContent(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var hash string
	if err := db.QueryRow(`SELECT hash FROM contents ORDER BY hash LIMIT 1`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	if _, err := db.Exec(`UPDATE contents SET data = ?1 WHERE hash = ?2`, enc.EncodeAll([]byte("forged text"), nil), hash); err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestRestoreRefusesForgedContentInDryRunAndRealRun(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	seed(t, src, "p1", "/work/p1", "s1")
	good := filepath.Join(dir, "good.db")
	mustCreate(t, CreateRequest{Source: src, ProjectID: "p1", Out: good})
	forged := filepath.Join(dir, "forged.db")
	mustCopy(t, good, forged)
	hash := forgeContent(t, forged)

	dst := filepath.Join(dir, "dst.db")
	seed(t, dst, "p9", "/work/p9", "other")
	before := signature(t, dst, "p9")

	for _, dry := range []bool{true, false} {
		_, err := restoreFile(t, forged, RestoreRequest{Mode: ModeMerge, DryRun: dry, Dest: globalDest(dst)})
		if !errors.Is(err, store.ErrContentMismatch) || !strings.Contains(err.Error(), hash) {
			t.Fatalf("dry=%v: forged content accepted or unnamed: %v", dry, err)
		}
	}
	if signature(t, dst, "p9") != before || countRows(t, dst, "projects") != 1 {
		t.Fatal("a refused backup changed the destination")
	}
}

func TestRestoreRefusesStreamOverCapAndRemovesTemp(t *testing.T) {
	data := builtBackup(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dst := filepath.Join(t.TempDir(), "dst.db")

	_, err := Restore(context.Background(), RestoreRequest{
		Source: bytes.NewReader(data), MaxBytes: int64(len(data)) - 1,
		Mode: ModeMerge, Dest: globalDest(dst),
	})
	if !errors.Is(err, ErrRestoreTooLarge) {
		t.Fatalf("stream over cap: err = %v", err)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("temp files left behind: %d", len(entries))
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("destination created by a refused restore")
	}

	if _, err := Restore(context.Background(), RestoreRequest{
		Source: bytes.NewReader(data), MaxBytes: int64(len(data)),
		Mode: ModeMerge, Dest: globalDest(dst),
	}); err != nil {
		t.Fatalf("stream exactly at cap refused: %v", err)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("temp files left after success: %d", len(entries))
	}
}

func TestCapReaderBoundary(t *testing.T) {
	for _, tc := range []struct {
		size, max int64
		wantErr   bool
	}{
		{0, 0, false}, {5, 5, false}, {5, 4, true}, {1, 0, true}, {4096, 4095, true},
	} {
		src := bytes.Repeat([]byte("x"), int(tc.size))
		var out bytes.Buffer
		n, err := io.Copy(&out, NewCapReader(bytes.NewReader(src), tc.max))
		if tc.wantErr != errors.Is(err, ErrRestoreTooLarge) {
			t.Fatalf("size %d max %d: err = %v", tc.size, tc.max, err)
		}
		if !tc.wantErr && n != tc.size {
			t.Fatalf("size %d max %d: copied %d", tc.size, tc.max, n)
		}
		if tc.wantErr && n != tc.max {
			t.Fatalf("size %d max %d: copied %d, want exactly the cap", tc.size, tc.max, n)
		}
	}
}

// capContainer stands in for the container's restore: it reads stdin and
// fails when the stream is longer than max, as the real restore does.
type capContainer struct {
	fakeRunner
	max  int64
	read int64
}

func (c *capContainer) Run(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	n, _ := io.Copy(io.Discard, stdin)
	c.read = n
	if n > c.max {
		return errors.New("container: stream exceeds the restore size cap")
	}
	return nil
}

func TestDockerRestoreEnforcesCapAndForwardsIt(t *testing.T) {
	data := builtBackup(t)
	box := &capContainer{max: int64(len(data)) - 1}
	opts := DockerRestoreOptions{Mode: ModeMerge, To: StorageGlobal, MaxBytes: int64(len(data)) - 1}
	err := DockerRestore(context.Background(), box, "acd-box", bytes.NewReader(data), opts, io.Discard, io.Discard)
	if !errors.Is(err, ErrRestoreTooLarge) {
		t.Fatalf("docker stream over cap: err = %v", err)
	}
	if box.read != opts.MaxBytes+1 {
		t.Fatalf("container read %d bytes, want the cap plus one (%d) so it can detect the overflow", box.read, opts.MaxBytes+1)
	}

	opts.MaxBytes = 1 << 20
	fake := &fakeRunner{}
	if err := DockerRestore(context.Background(), fake, "acd-box", bytes.NewReader(data), opts, io.Discard, io.Discard); err != nil {
		t.Fatalf("DockerRestore: %v", err)
	}
	i := -1
	for j, a := range fake.args {
		if a == "--max-size" {
			i = j
		}
	}
	if i < 0 || i+1 >= len(fake.args) || fake.args[i+1] != strconv.FormatInt(opts.MaxBytes, 10) {
		t.Fatalf("container argv lacks the cap: %v", fake.args)
	}
}
