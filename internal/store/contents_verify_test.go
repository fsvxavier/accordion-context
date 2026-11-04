package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/klauspost/compress/zstd"
)

func sumHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// storedData encodes s the way appendMessage stores it.
func storedData(t *testing.T, s string) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	return enc.EncodeAll([]byte(s), nil)
}

// contentsFile writes a database holding one project, one session and one
// message per text, and returns its path. The file is sealed, so it is a
// plain copy that InspectFile accepts.
func contentsFile(t *testing.T, path, project, root string, texts ...string) string {
	t.Helper()
	ctx := context.Background()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureProject(ctx, project, root, "global", 1); err != nil {
		t.Fatal(err)
	}
	sid := project + "-s1"
	if err := s.CreateSession(ctx, Session{ID: sid, ProjectID: project, Model: "m", ContextLimit: 1000, Mode: "proxy", Integration: "url"}); err != nil {
		t.Fatal(err)
	}
	for _, txt := range texts {
		if _, err := s.AppendMessage(ctx, sid, NewMessage{Role: kernel.RoleUser, Kind: kernel.KindText, Text: txt, Tokens: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Seal(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func rawExec(t *testing.T, path, q string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func rawCount(t *testing.T, path, table string) int {
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

// dumpContentRow returns the contents row of hash in d, for tampering in memory.
func dumpContentRow(t *testing.T, d ProjectDump, hash string) Row {
	t.Helper()
	for _, tr := range d.Tables {
		if tr.Table != "contents" {
			continue
		}
		for _, r := range tr.Rows {
			if r["hash"] == hash {
				return r
			}
		}
	}
	t.Fatalf("no contents row %s in dump", hash)
	return nil
}

func TestInspectFileAcceptsStoredContents(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	big := make([]byte, 300_000)
	for i := range big {
		big[i] = byte('a' + rng.Intn(26))
	}
	path := contentsFile(t, filepath.Join(t.TempDir(), "ok.db"), "p", "/r", "", "hello", strings.Repeat("ab", 10), string(big))
	if _, err := InspectFile(context.Background(), path); err != nil {
		t.Fatalf("valid contents rejected: %v", err)
	}
}

func TestInspectFileNamesBadContentHash(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	bad := sumHex("bbb")
	forged := contentsFile(t, filepath.Join(dir, "forged.db"), "p", "/r", "aaa", "bbb")
	rawExec(t, forged, `UPDATE contents SET data = ?1 WHERE hash = ?2`, storedData(t, "ccc"), bad)
	_, err := InspectFile(ctx, forged)
	if !errors.Is(err, ErrContentMismatch) || !strings.Contains(err.Error(), bad) {
		t.Fatalf("forged data: err = %v, want mismatch naming %s", err, bad)
	}

	sized := contentsFile(t, filepath.Join(dir, "sized.db"), "p", "/r", "aaa")
	rawExec(t, sized, `UPDATE contents SET size_bytes = size_bytes + 1 WHERE hash = ?1`, sumHex("aaa"))
	_, err = InspectFile(ctx, sized)
	if !errors.Is(err, ErrContentMismatch) || !strings.Contains(err.Error(), sumHex("aaa")) {
		t.Fatalf("wrong size: err = %v", err)
	}
}

func TestRestoreRefusesForgedDumpBeforeWrite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src, err := Open(contentsFile(t, filepath.Join(dir, "src.db"), "p", "/r", "aaa", "bbb"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	d, err := src.ExportProject(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	dumpContentRow(t, d, sumHex("bbb"))["data"] = storedData(t, "ccc")

	dstPath := filepath.Join(dir, "dst.db")
	dst, err := Open(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	target := ImportTarget{ProjectID: "p2", RootPath: "/r2", Storage: "global"}
	_, err = dst.RestoreDumps(ctx, []RestoreItem{{Dump: d, Target: target}}, false, false)
	if !errors.Is(err, ErrContentMismatch) || !strings.Contains(err.Error(), sumHex("bbb")) {
		t.Fatalf("RestoreDumps: err = %v", err)
	}
	if _, err := dst.ImportProject(ctx, d, target); !errors.Is(err, ErrContentMismatch) {
		t.Fatalf("ImportProject: err = %v", err)
	}
	if n := rawCount(t, dstPath, "contents") + rawCount(t, dstPath, "projects") + rawCount(t, dstPath, "messages"); n != 0 {
		t.Fatalf("refused restore wrote %d rows", n)
	}
}

func TestRestoreRefusesDestinationWithDifferentBytes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := mustOpenExport(t, contentsFile(t, filepath.Join(dir, "src.db"), "p", "/r", "aaa", "bbb"))
	if err != nil {
		t.Fatal(err)
	}

	// The destination already holds the hash of "bbb", with other bytes.
	dstPath := contentsFile(t, filepath.Join(dir, "dst.db"), "q", "/q", "bbb")
	rawExec(t, dstPath, `UPDATE contents SET data = ?1 WHERE hash = ?2`, storedData(t, "ccc"), sumHex("bbb"))
	dst, err := Open(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	_, err = dst.RestoreDumps(ctx, []RestoreItem{{Dump: d, Target: ImportTarget{ProjectID: "p", RootPath: "/r", Storage: "global"}}}, false, false)
	if !errors.Is(err, ErrContentMismatch) || !strings.Contains(err.Error(), sumHex("bbb")) {
		t.Fatalf("different destination bytes: err = %v", err)
	}
	if n := rawCount(t, dstPath, "projects"); n != 1 {
		t.Fatalf("projects = %d after refused restore, want the original 1", n)
	}
}

func TestRestoreAcceptsIdenticalDestinationContent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	d, err := mustOpenExport(t, contentsFile(t, filepath.Join(dir, "src.db"), "p", "/r", "aaa", "bbb"))
	if err != nil {
		t.Fatal(err)
	}
	dstPath := contentsFile(t, filepath.Join(dir, "dst.db"), "q", "/q", "bbb")
	before := rawCount(t, dstPath, "contents")
	dst, err := Open(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if _, err := dst.RestoreDumps(ctx, []RestoreItem{{Dump: d, Target: ImportTarget{ProjectID: "p", RootPath: "/r", Storage: "global"}}}, false, false); err != nil {
		t.Fatalf("identical shared content refused: %v", err)
	}
	if got := rawCount(t, dstPath, "contents"); got != before+1 {
		t.Fatalf("contents = %d, want %d (only aaa is new)", got, before+1)
	}
}

// mustOpenExport opens the file at path and exports its project "p".
func mustOpenExport(t *testing.T, path string) (ProjectDump, error) {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		return ProjectDump{}, err
	}
	defer s.Close()
	return s.ExportProject(context.Background(), "p")
}

func TestContentCheckerStreamStopsAtDeclaredSize(t *testing.T) {
	c, err := newContentChecker()
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	prefix, full := "hello", "hello world, much longer than the declared size"
	// The hash and size describe the prefix, but the stored frame holds more.
	err = c.verify(sumHex(prefix), int64(len(prefix)), storedData(t, full))
	if !errors.Is(err, ErrContentMismatch) || !strings.Contains(err.Error(), sumHex(prefix)) {
		t.Fatalf("oversized frame: err = %v", err)
	}
	if err := c.verify(sumHex(full), int64(len(full)), storedData(t, full)); err != nil {
		t.Fatalf("exact frame rejected: %v", err)
	}
	if err := c.verify(sumHex(""), 0, storedData(t, "")); err != nil {
		t.Fatalf("empty content rejected: %v", err)
	}
	for name, data := range map[string][]byte{
		"empty":     nil,
		"truncated": storedData(t, full)[:12],
		"not zstd":  []byte("plain text"),
	} {
		if err := c.verify(sumHex(full), int64(len(full)), data); !errors.Is(err, ErrContentMismatch) {
			t.Fatalf("%s data accepted: %v", name, err)
		}
	}
	if err := c.verify(sumHex(full), -1, storedData(t, full)); !errors.Is(err, ErrContentMismatch) {
		t.Fatalf("negative size accepted: %v", err)
	}
}

func TestRestoreRejectsMalformedContentRow(t *testing.T) {
	ctx := context.Background()
	dst, err := Open(filepath.Join(t.TempDir(), "dst.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	d := ProjectDump{ProjectID: "p", Tables: []TableRows{
		{Table: "projects", Rows: []Row{{"id": "p", "root_path": "/r", "storage": "global", "created_at": int64(1)}}},
		{Table: "contents", Rows: []Row{{"hash": []byte("x"), "size_bytes": int64(1), "data": []byte{1}}}},
	}}
	_, err = dst.ImportProject(ctx, d, ImportTarget{ProjectID: "p", RootPath: "/r", Storage: "global"})
	if !errors.Is(err, ErrContentMismatch) {
		t.Fatalf("malformed hash accepted: %v", err)
	}
}
