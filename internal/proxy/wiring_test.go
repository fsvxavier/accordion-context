package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cockroachdb/apd/v3"

	"github.com/fsvxavier/accordion-context/internal/config"
	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// TestTriggerConfigReachesDecideNudge: trigger.* from the configuration changes the
// nudge decision. The default kernel nudges this transcript (U is near 0.9 of a
// 60K window); a configured u_high above that band must suppress it.
func TestTriggerConfigReachesDecideNudge(t *testing.T) {
	run := func(yaml string) bool {
		up := newFakeUpstream(t, func(int, map[string]json.RawMessage) (int, string, string) {
			return 200, "application/json", plainAnswer
		})
		ps, _, _ := newTuned(t, up, tuneOpts{windows: map[string]int{"claude-x": 60_000}, globalYAML: yaml})
		if resp, _ := postMessages(t, ps.URL, transcriptBody(false, 15, nil), nil); resp.StatusCode != 200 {
			t.Fatalf("status %d", resp.StatusCode)
		}
		return sawNudge(t, up, 0)
	}
	if !run("") {
		t.Fatal("default trigger: the transcript must be nudged")
	}
	if run("trigger:\n  u_high: 0.99\n  u_emerg: 0.995\n") {
		t.Fatal("trigger.u_high 0.99: the configured band must reach DecideNudge and suppress the nudge")
	}
}

// TestSessionOptionsFromConfig: the session gets the project's settings, and the
// nudge lists only spans the session would accept.
func TestSessionOptionsFromConfig(t *testing.T) {
	cfg := config.Defaults()
	cfg.PreserveRecentMessages = 25
	cfg.MinCompressRange = 1_000_000
	cfg.UHigh = decimalOf(t, "0.5")
	opts := sessionOptions(cfg)
	if opts.RecentKeep != 25 {
		t.Fatalf("RecentKeep = %d, want the configured 25", opts.RecentKeep)
	}
	if opts.MinCompressRange != 1_000_000 || opts.Kernel.UHigh.String() != "0.5" {
		t.Fatalf("options not carried: %+v", opts)
	}
	if got := sessionOptions(config.Defaults()).RecentKeep; got != session.RecentKeep {
		t.Fatalf("default RecentKeep = %d, want %d", got, session.RecentKeep)
	}

	var msgs []kernel.Message
	for i := 1; i <= 30; i++ {
		msgs = append(msgs, kernel.Message{Seq: int64(i), Ref: kernel.FormatRef(int64(i)), Role: kernel.RoleUser, Kind: kernel.KindText, Tokens: 1000})
	}
	snap := kernel.Snapshot{Messages: msgs}
	if n := len(nudgeSpans(snap, session.Options{RecentKeep: 10, MinCompressRange: 5_000})); n == 0 {
		t.Fatal("default options: spans must be offered")
	}
	if n := len(nudgeSpans(snap, session.Options{RecentKeep: 10, MinCompressRange: 1_000_000})); n != 0 {
		t.Fatalf("MinCompressRange 1e6: %d spans offered, want none", n)
	}
}

func decimalOf(t *testing.T, s string) apd.Decimal {
	t.Helper()
	var d apd.Decimal
	if _, _, err := d.SetString(s); err != nil {
		t.Fatal(err)
	}
	return d
}

// newBareServer builds a Server on st without an upstream, as a fresh process would.
func newBareServer(t *testing.T, st *store.Store) *Server {
	t.Helper()
	u, _ := url.Parse("http://127.0.0.1:1")
	s, err := New(Config{Upstream: u, Store: st, GlobalPath: filepath.Join(t.TempDir(), "absent.yaml")})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestCalibrationSurvivesRestart: a restarted proxy replays the stored cache
// samples, so the calibration keeps the scale the previous process learned.
func TestCalibrationSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "acd-ctx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p := projectRef{ID: "p-cal", Root: "urn:acd-ctx:project:p-cal", Storage: "global"}
	const sid = "s_cal"

	first := newBareServer(t, st)
	if _, err := first.session(ctx, p, sid, "claude-x", 200_000); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if err := st.RecordCache(ctx, sid, store.CacheCall{InputTokens: 200, EstTokens: 100}); err != nil {
			t.Fatal(err)
		}
	}

	second := newBareServer(t, st) // the same database, a new process
	if _, err := second.session(ctx, p, sid, "claude-x", 200_000); err != nil {
		t.Fatal(err)
	}
	if _, samples := second.calibFactor(sid); samples != 6 {
		t.Fatalf("restored %d samples, want 6", samples)
	}
	if got := second.calibrate(sid, 100); got < 150 {
		t.Fatalf("calibrated 100 -> %d, want the learned ~2x scale", got)
	}
	if _, samples := newBareServer(t, st).calibFactor("s_unseen"); samples != 0 {
		t.Fatalf("an unknown session must start uncalibrated, got %d samples", samples)
	}
}

// TestProjectIDByRootKeepsMovedIdentity: a project moved to a new root keeps its
// id, because the proxy resolves the root through the store first.
func TestProjectIDByRootKeepsMovedIdentity(t *testing.T) {
	ctx := context.Background()
	s, st, _ := newServer(t, newFakeUpstream(t, nil), "")
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureProject(ctx, "kept-id", real, "global", 1); err != nil {
		t.Fatal(err)
	}
	res, ok := s.projectFromDir(root)
	if !ok {
		t.Fatal("project dir not usable")
	}
	if res.ID != "kept-id" {
		t.Fatalf("id = %q, want the registered kept-id", res.ID)
	}
	if _, ok := s.idByRoot(filepath.Join(root, "elsewhere")); ok {
		t.Fatal("unknown root must fall back to the hash")
	}
}

// TestCompressToolCarriesFoldsAndCloseIntents: every dialect passes folds and
// closeIntents on to the session.
func TestCompressToolCarriesFoldsAndCloseIntents(t *testing.T) {
	input := []byte(`{"ranges":[{"startRef":"m00001","endRef":"m00004","summary":"s"}],` +
		`"folds":[{"tier":2,"startBlock":"b1","endBlock":"b2","summary":"fold","topic":"t"}],"closeIntents":[3]}`)
	for _, p := range protocols {
		args, err := p.ParseTool(toolCompress, input)
		if err != nil {
			t.Fatalf("%s: %v", p.Name(), err)
		}
		if len(args.ranges) != 1 || len(args.folds) != 1 || len(args.closeIntents) != 1 || args.closeIntents[0] != 3 {
			t.Fatalf("%s: args %+v", p.Name(), args)
		}
		if f := args.folds[0]; f.Tier != 2 || f.StartBlock != "b1" || f.EndBlock != "b2" || f.Summary != "fold" {
			t.Fatalf("%s: fold %+v", p.Name(), f)
		}
	}
}

// TestDecompressExpandsBlockAndPeeksWithoutChange: mode context deactivates the
// block through the session; peek only reads it.
func TestDecompressExpandsBlockAndPeeksWithoutChange(t *testing.T) {
	ctx := context.Background()
	s, st, _ := newServer(t, newFakeUpstream(t, nil), "")
	p := projectRef{ID: "p-dec", Root: "urn:acd-ctx:project:p-dec", Storage: "global"}
	sess, err := s.session(ctx, p, "s_dec", "claude-x", 200_000)
	if err != nil {
		t.Fatal(err)
	}
	var msgs []kernel.Message
	for i := 0; i < 30; i++ {
		role := kernel.RoleUser
		if i%2 == 1 {
			role = kernel.RoleAssistant
		}
		msgs = append(msgs, kernel.Message{Role: role, Kind: kernel.KindText, Tokens: 1000,
			Text: fmt.Sprintf("line about the parser %d", i)})
	}
	if _, err := sess.Ingest(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.HandleCompress(ctx, session.CompressArgs{Ranges: []session.CompressRange{
		{StartRef: "m00002", EndRef: "m00007", Summary: "the agent read the parser code and fixed the bug in the lexer"},
	}}); err != nil {
		t.Fatal(err)
	}
	tn := &turn{ctx: ctx, sess: sess, sid: "s_dec"}

	if out, err := s.decompress(tn, toolArgs{block: "b1", mode: "peek", query: "parser"}); err != nil || !strings.Contains(out, "[m00002]") {
		t.Fatalf("peek: %q err %v", out, err)
	}
	snap, err := st.Snapshot(ctx, "s_dec")
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Blocks[0].Active {
		t.Fatal("peek must not change the block")
	}

	out, err := s.decompress(tn, toolArgs{block: "b1", mode: "context"})
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if !strings.Contains(out, "Expanded b1") {
		t.Fatalf("context reply %q", out)
	}
	snap, err = st.Snapshot(ctx, "s_dec")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Blocks[0].Active {
		t.Fatal("context mode must deactivate the block through session.Decompress")
	}
	if _, err := s.decompress(tn, toolArgs{block: "m00002"}); err != nil {
		t.Fatalf("message ref recall: %v", err)
	}
}
