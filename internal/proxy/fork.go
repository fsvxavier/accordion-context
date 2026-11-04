package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/store"
)

// maxForkDepth bounds the walk from a base session to the session that fits.
const maxForkDepth = 64

// fork says how the session at the end of a walk was reached: it is a new
// child of parent, and the first common messages are shared with parent.
type fork struct {
	parent string
	common int
}

// resolveLedger walks from base to the session whose stored ledger is a prefix
// of the client transcript msgs. Each step that does not fit moves to a child
// keyed by the transcript up to the first divergent message. The key depends
// only on the transcript, so the walk is the same across turns and restarts,
// and no state is kept in memory. A fork is returned when the session reached
// is new and must inherit the parent's blocks.
func (s *Server) resolveLedger(ctx context.Context, p projectRef, base string, msgs []kernel.Message, model string, limit int) (string, *session.Session, *fork, error) {
	sid := base
	var f *fork
	for depth := 0; depth < maxForkDepth; depth++ {
		sess, err := s.session(ctx, p, sid, model, limit)
		if err != nil {
			return "", nil, nil, err
		}
		snap, err := s.cfg.Store.Snapshot(ctx, sid)
		if err != nil {
			return "", nil, nil, err
		}
		d := divergence(snap.Messages, msgs)
		if d < 0 {
			// Reached through a fork: the caller inherits until it succeeds.
			return sid, sess, f, nil
		}
		s.cfg.Logger.Warn("ledger diverges from transcript", "session", sid, "at", d)
		f = &fork{parent: sid, common: d}
		sid = forkSessionID(sid, msgs[:min(d+1, len(msgs))])
	}
	return "", nil, nil, errors.New("proxy: session fork depth exceeded")
}

// divergence returns the index of the first message where the stored ledger and
// the transcript differ, or -1 when the ledger is a prefix of the transcript. A
// ledger longer than the transcript diverges at len(msgs).
func divergence(stored []kernel.Message, msgs []kernel.Message) int {
	for i, m := range stored {
		if i >= len(msgs) {
			return len(msgs)
		}
		if !sameMsg(m, msgs[i]) {
			return i
		}
	}
	return -1
}

// forkSessionID names the child of parent for a transcript prefix. It hashes
// the prefix, so the same prefix always maps to the same session.
func forkSessionID(parent string, msgs []kernel.Message) string {
	type key struct{ Role, Kind, Tool, Call, Text string }
	keys := make([]key, len(msgs))
	for i, m := range msgs {
		keys[i] = key{string(m.Role), string(m.Kind), m.ToolName, m.ToolCallID, m.Text}
	}
	enc, _ := json.Marshal(keys)
	h := sha256.New()
	h.Write([]byte("fork\x00" + parent + "\x00"))
	h.Write(enc)
	return "s_" + hex.EncodeToString(h.Sum(nil))[:32]
}

// inheritBlocks gives a new fork the active tier-1 blocks of its parent that end
// inside the shared prefix. Their messages are identical in both ledgers, so the
// blocks stay valid. Blocks are renumbered from b1, so the session's next id
// stays free. Call it after the fork has ingested the transcript. Blocks of
// other tiers are not copied, because tier 1 is the only tier produced so far.
func (s *Server) inheritBlocks(ctx context.Context, child string, f *fork) error {
	parent, err := s.cfg.Store.Snapshot(ctx, f.parent)
	if err != nil {
		return err
	}
	var keep []kernel.Block
	for _, b := range parent.Blocks {
		if b.Active && b.Tier == 1 && b.EndSeq <= int64(f.common) {
			keep = append(keep, b)
		}
	}
	if len(keep) == 0 {
		return nil
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].StartSeq < keep[j].StartSeq })

	plan := store.CompressPlan{Now: time.Now().Unix()}
	for i, b := range keep {
		plan.NewBlocks = append(plan.NewBlocks, store.NewBlock{
			ID: "b" + strconv.Itoa(i+1), Tier: 1, Topic: b.Topic, Summary: b.Summary,
			StartSeq: b.StartSeq, EndSeq: b.EndSeq, SrcTokens: b.SrcTokens, SumTokens: b.SumTokens,
		})
	}
	snap, err := s.cfg.Store.Snapshot(ctx, child)
	if err != nil {
		return err
	}
	if len(snap.Blocks) > 0 {
		// Already inherited, or the fork compressed on its own.
		return nil
	}
	hyp := snap
	for _, nb := range plan.NewBlocks {
		hyp.Blocks = append(hyp.Blocks, kernel.Block{ID: nb.ID, Tier: 1, Summary: nb.Summary,
			StartSeq: nb.StartSeq, EndSeq: nb.EndSeq, SrcTokens: nb.SrcTokens, SumTokens: nb.SumTokens, Active: true})
	}
	for _, m := range kernel.Render(hyp) {
		plan.TokensAfter += m.Tokens
	}
	return s.cfg.Store.ApplyCompress(ctx, child, plan)
}
