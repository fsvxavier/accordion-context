package store

import (
	"context"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// twoProjects: p1 holds s1 (ten messages, two tier-1 blocks, one tier-2 block)
// and p2 holds s2 (one message whose content s1 shares).
func twoProjects(t *testing.T) *Store {
	t.Helper()
	s := foldedFixture(t)
	ctx := context.Background()
	if err := s.EnsureProject(ctx, "p2", "/work/p2", "global", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, Session{ID: "s2", ProjectID: "p2", Model: "m", ContextLimit: 1000, Mode: "proxy", Integration: "url"}); err != nil {
		t.Fatal(err)
	}
	appendN(t, s, "s2", 1)
	return s
}

func rowsOf(t *testing.T, s *Store, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := s.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestNoDeleteOutsidePurge is the grep proof of §5.2: DELETE statements exist
// only in purge.go. It walks the module from this package directory.
func TestNoDeleteOutsidePurge(t *testing.T) {
	root := filepath.Join("..", "..")
	deleteRE := regexp.MustCompile(`DELETE\s+FROM`)
	purgeAbs, err := filepath.Abs("purge.go")
	if err != nil {
		t.Fatal(err)
	}
	var visited int
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		isGo := strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
		if !isGo && !strings.HasSuffix(name, ".sql") {
			return nil
		}
		visited++
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if abs == purgeAbs {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if deleteRE.Match(data) {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if visited < 20 {
		t.Fatalf("walk saw only %d source files; the root is wrong", visited)
	}
	if len(offenders) > 0 {
		t.Fatalf("DELETE FROM outside internal/store/purge.go: %v", offenders)
	}
}

// Every mode's dry run returns the Report the real run then returns, and the
// dry run leaves the database and the epoch untouched.
func TestDryRunEqualsRealReport(t *testing.T) {
	ctx := context.Background()
	modes := map[string]func(s *Store, o PurgeOpts) (Report, error){
		"session": func(s *Store, o PurgeOpts) (Report, error) { return s.PurgeSession(ctx, "s1", o) },
		"project": func(s *Store, o PurgeOpts) (Report, error) { return s.PurgeProjectReport(ctx, "p1", o) },
		"older": func(s *Store, o PurgeOpts) (Report, error) {
			return s.PurgeOlderThan(ctx, "", time.Now().Add(time.Hour), o)
		},
		"all": func(s *Store, o PurgeOpts) (Report, error) { return s.PurgeAll(ctx, o) },
	}
	for name, run := range modes {
		t.Run(name, func(t *testing.T) {
			s := twoProjects(t)
			before := rowsOf(t, s, `SELECT COUNT(*) FROM messages`)
			epoch0, _ := s.Epoch(ctx)
			dry, err := run(s, PurgeOpts{DryRun: true})
			if err != nil {
				t.Fatal(err)
			}
			if got := rowsOf(t, s, `SELECT COUNT(*) FROM messages`); got != before {
				t.Fatalf("dry run deleted messages: %d -> %d", before, got)
			}
			if e, _ := s.Epoch(ctx); e != epoch0 {
				t.Fatalf("dry run bumped the epoch")
			}
			real, err := run(s, PurgeOpts{})
			if err != nil {
				t.Fatal(err)
			}
			if dry != real {
				t.Fatalf("dry run %+v != real %+v", dry, real)
			}
			if e, _ := s.Epoch(ctx); e != epoch0+1 {
				t.Fatalf("epoch = %d after one purge, want %d", e, epoch0+1)
			}
			if real.Messages == 0 {
				t.Fatal("report counted no messages")
			}
		})
	}
}

// PurgeSession removes one session, keeps the content another session still
// refers to, and leaves the other session valid.
func TestPurgeSessionIsWholeAndSharedContentSurvives(t *testing.T) {
	s := twoProjects(t)
	ctx := context.Background()
	if _, err := s.PurgeSession(ctx, "nope", PurgeOpts{}); err == nil {
		t.Fatal("unknown session purged")
	}
	rep, err := s.PurgeSession(ctx, "s1", PurgeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sessions != 1 || rep.Messages != 10 || rep.Blocks != 3 || rep.Projects != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Contents != 9 {
		t.Fatalf("contents removed = %d, want 9 (message 1 is shared with s2)", rep.Contents)
	}
	if rep.BytesFreed <= 0 {
		t.Fatalf("bytes freed = %d", rep.BytesFreed)
	}
	if n := rowsOf(t, s, `SELECT COUNT(*) FROM search_idx WHERE session_id = 's1'`); n != 0 {
		t.Fatalf("%d search rows left for s1", n)
	}
	if n := rowsOf(t, s, `SELECT COUNT(*) FROM projects WHERE id = 'p1'`); n != 1 {
		t.Fatal("PurgeSession removed the project")
	}
	if err := s.CheckInvariants(ctx, "s2"); err != nil {
		t.Fatal(err)
	}
}

// PurgeOlderThan removes whole sessions by last activity and never a part of one.
func TestPurgeOlderThanRemovesWholeSessionsByActivity(t *testing.T) {
	s := twoProjects(t)
	ctx := context.Background()
	if _, err := s.PurgeOlderThan(ctx, "", time.Time{}, PurgeOpts{}); err == nil {
		t.Fatal("zero cutoff accepted")
	}
	past := time.Now().Add(-24 * time.Hour)
	rep, err := s.PurgeOlderThan(ctx, "", past, PurgeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if rep != (Report{}) {
		t.Fatalf("sessions newer than the cutoff were purged: %+v", rep)
	}
	ids, err := s.ScopeSessionIDs(ctx, "", time.Time{})
	if err != nil || len(ids) != 2 {
		t.Fatalf("sessions = %v, %v", ids, err)
	}
	if ids, _ := s.ScopeSessionIDs(ctx, "", time.Now().Add(time.Hour)); len(ids) != 2 {
		t.Fatalf("scope with future cutoff = %v", ids)
	}

	rep, err = s.PurgeOlderThan(ctx, "p2", time.Now().Add(time.Hour), PurgeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sessions != 1 || rep.Messages != 1 {
		t.Fatalf("project-scoped report = %+v", rep)
	}
	if n := rowsOf(t, s, `SELECT COUNT(*) FROM projects WHERE id = 'p2'`); n != 1 {
		t.Fatal("older-than removed the project row")
	}
	if err := s.CheckInvariants(ctx, "s1"); err != nil {
		t.Fatalf("remaining session broken: %v", err)
	}
}

func TestPurgeAllEmptiesEveryProjectAndOrphans(t *testing.T) {
	s := twoProjects(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`INSERT INTO contents(hash, size_bytes, data) VALUES ('orphan', 7, x'00')`); err != nil {
		t.Fatal(err)
	}
	dry, err := s.PurgeAll(ctx, PurgeOpts{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if dry.Projects != 2 || dry.Sessions != 2 || dry.Contents != 11 {
		t.Fatalf("dry report = %+v", dry)
	}
	real, err := s.PurgeAll(ctx, PurgeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if dry != real {
		t.Fatalf("dry %+v != real %+v", dry, real)
	}
	for _, tbl := range []string{"projects", "sessions", "messages", "blocks", "contents", "events", "search_idx", "trigger_state"} {
		if n := rowsOf(t, s, `SELECT COUNT(*) FROM `+tbl); n != 0 {
			t.Fatalf("%s: %d rows left", tbl, n)
		}
	}
	if err := s.Vacuum(ctx); err != nil {
		t.Fatalf("vacuum after purge: %v", err)
	}
}

// Random sequences of purges over random states keep the invariants of every
// remaining session, and each dry run matches its real run.
func TestPurgeRandomStatesKeepInvariants(t *testing.T) {
	ctx := context.Background()
	for seed := int64(1); seed <= 12; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s := newMemStore(t)
		projects := []string{"pa", "pb", "pc"}
		for _, p := range projects {
			if err := s.EnsureProject(ctx, p, "/work/"+p, "global", 1); err != nil {
				t.Fatal(err)
			}
		}
		var sessions []string
		for i := 0; i < 4; i++ {
			sid := fmt.Sprintf("r%d", i)
			p := projects[rng.Intn(len(projects))]
			if err := s.CreateSession(ctx, Session{ID: sid, ProjectID: p, Model: "m", ContextLimit: 1000, Mode: "proxy", Integration: "url"}); err != nil {
				t.Fatal(err)
			}
			n := 8 + 2*rng.Intn(3)
			appendN(t, s, sid, n)
			for k, blk := range []string{"b1", "b2"} {
				start := int64(1 + 4*k)
				if err := s.ApplyCompress(ctx, sid, CompressPlan{
					NewBlocks: []NewBlock{{ID: blk, Tier: 1, Summary: "x", StartSeq: start, EndSeq: start + 3, SrcTokens: 40, SumTokens: 4}},
					Now:       1, TokensAfter: 1,
				}); err != nil {
					t.Fatal(err)
				}
			}
			sessions = append(sessions, sid)
		}

		for step := 0; step < 6; step++ {
			var (
				dry, real Report
				err       error
			)
			switch rng.Intn(3) {
			case 0:
				sid := sessions[rng.Intn(len(sessions))]
				dry, err = s.PurgeSession(ctx, sid, PurgeOpts{DryRun: true})
				if err == nil {
					real, err = s.PurgeSession(ctx, sid, PurgeOpts{})
				}
			case 1:
				p := projects[rng.Intn(len(projects))]
				dry, err = s.PurgeProjectReport(ctx, p, PurgeOpts{DryRun: true})
				if err == nil {
					real, err = s.PurgeProjectReport(ctx, p, PurgeOpts{})
				}
			default:
				cut := time.Now().Add(time.Hour)
				dry, err = s.PurgeOlderThan(ctx, "", cut, PurgeOpts{DryRun: true})
				if err == nil {
					real, err = s.PurgeOlderThan(ctx, "", cut, PurgeOpts{})
				}
			}
			if err != nil && !isNotFound(err) {
				t.Fatalf("seed %d step %d: %v", seed, step, err)
			}
			if dry != real {
				t.Fatalf("seed %d step %d: dry %+v != real %+v", seed, step, dry, real)
			}
			left, err := s.ScopeSessionIDs(ctx, "", time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			for _, sid := range left {
				if err := s.CheckInvariants(ctx, sid); err != nil {
					t.Fatalf("seed %d step %d: session %s: %v", seed, step, sid, err)
				}
			}
		}
	}
}

func isNotFound(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "not found"))
}

func TestPurgeProjectUnknownChangesNothingAndEpochHolds(t *testing.T) {
	s := twoProjects(t)
	ctx := context.Background()
	e0, _ := s.Epoch(ctx)
	if _, err := s.PurgeProject(ctx, "nope"); err == nil {
		t.Fatal("unknown project purged")
	}
	if e, _ := s.Epoch(ctx); e != e0 {
		t.Fatalf("failed purge bumped the epoch %d -> %d", e0, e)
	}
	if n := rowsOf(t, s, `SELECT COUNT(*) FROM messages`); n != 11 {
		t.Fatalf("failed purge changed messages: %d", n)
	}
}

// The cutoff is strict (activity < cutoff) in whole seconds, a session with no
// messages or events ages by its own row, and a cutoff at Unix time 0 is a real
// cutoff that matches nothing instead of meaning "no age filter".
func TestPurgeOlderThanBoundaries(t *testing.T) {
	s := twoProjects(t)
	ctx := context.Background()
	for _, q := range []string{
		`UPDATE sessions SET created_at = 1000`, `UPDATE messages SET created_at = 1000`,
		`UPDATE events SET at = 1000`, `UPDATE cache_ledger SET at = 1000`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateSession(ctx, Session{ID: "empty", ProjectID: "p2", Model: "m", ContextLimit: 1000, Mode: "proxy", Integration: "url"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET created_at = 500 WHERE id = 'empty'`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		cut  int64
		want int
	}{{0, 0}, {500, 0}, {501, 1}, {1000, 1}, {1001, 3}} {
		ids, err := s.ScopeSessionIDs(ctx, "", time.Unix(c.cut, 0))
		if err != nil || len(ids) != c.want {
			t.Fatalf("cutoff %d: sessions %v, %v; want %d", c.cut, ids, err, c.want)
		}
		rep, err := s.PurgeOlderThan(ctx, "", time.Unix(c.cut, 0), PurgeOpts{DryRun: true})
		if err != nil || int(rep.Sessions) != c.want {
			t.Fatalf("cutoff %d: dry run %+v, %v; want %d sessions", c.cut, rep, err, c.want)
		}
	}
}
