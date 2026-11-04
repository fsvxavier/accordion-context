package kernel

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// RecallKind classifies a recall hint (spec §13.2).
type RecallKind string

const (
	RecallReread  RecallKind = "reread" // a recent tool call repeats a folded one
	RecallRef     RecallKind = "ref"    // latest text cites a folded message ref
	RecallBlockID RecallKind = "block"  // latest text cites a folded block id
)

// RecallHint is an ephemeral pointer from folded content to the active block
// that covers it. The hint is never written to history; BlockID is the block
// the model should pass to decompress.
type RecallHint struct {
	Kind    RecallKind
	BlockID string // active block covering the folded content
	Ref     string // folded message ref ("m00340"), when the hint names one
	Path    string // target path of a re-read, when one was found in the args
	Text    string // hint wording (spec §13.2 style)
}

var (
	refPattern   = regexp.MustCompile(`\bm\d{5,}\b`)
	blockPattern = regexp.MustCompile(`\bb\d+\b`)
	// pathKeys are the argument names that identify a file target.
	pathKeys = []string{"file_path", "filePath", "path", "file", "filename", "target"}
)

// DetectRecall returns recall hints for the current turn (spec §13.2). It is
// deterministic and pure. Two signals are checked:
//
//   - re-read: a tool call after the latest user text has the same tool name and
//     the same target path (or the same canonical args when no path exists) as a
//     tool call that is already folded into an active block;
//   - reference: the latest user or assistant text cites a folded message ref
//     (m00340) or a folded block id (b12). The hint points at the active block
//     that covers it.
//
// Hints are sorted by block, kind, ref and path, and duplicates are removed.
func DetectRecall(s Snapshot) []RecallHint {
	active := activeSorted(s.Blocks)
	byID := indexBlocks(s.Blocks)

	refSeq := make(map[string]int64, len(s.Messages))
	var lastUser, lastAsst *Message
	for i := range s.Messages {
		m := &s.Messages[i]
		ref := m.Ref
		if ref == "" {
			ref = FormatRef(m.Seq)
		}
		refSeq[ref] = m.Seq
		if m.Kind != KindText {
			continue
		}
		switch m.Role {
		case RoleUser:
			lastUser = m
		case RoleAssistant:
			lastAsst = m
		}
	}
	// The re-read window starts at the latest user text (hints are per turn).
	var windowStart int64
	if lastUser != nil {
		windowStart = lastUser.Seq
	}

	var hints []RecallHint

	// Re-reads. Index folded tool calls by signature, keeping the latest one.
	type folded struct {
		seq   int64
		block string
		ref   string
	}
	latest := map[string]folded{}
	for _, m := range s.Messages {
		if m.Kind != KindToolCall || m.ToolName == "" {
			continue
		}
		b, ok := coveringBlock(active, m.Seq)
		if !ok {
			continue
		}
		key, _ := callSignature(m)
		if f, seen := latest[key]; !seen || m.Seq > f.seq {
			latest[key] = folded{seq: m.Seq, block: b.ID, ref: refOf(m)}
		}
	}
	for _, m := range s.Messages {
		if m.Kind != KindToolCall || m.ToolName == "" || m.Seq < windowStart {
			continue
		}
		if _, covered := coveringBlock(active, m.Seq); covered {
			continue
		}
		key, path := callSignature(m)
		f, ok := latest[key]
		if !ok || f.seq >= m.Seq {
			continue
		}
		subject := path
		if subject == "" {
			subject = m.ToolName
		}
		hints = append(hints, RecallHint{
			Kind:    RecallReread,
			BlockID: f.block,
			Ref:     f.ref,
			Path:    path,
			Text: fmt.Sprintf("%s já foi dobrado em %s (bloco %s). Se precisar do conteúdo exato, use decompress(%q, mode=\"peek\", query=%q).",
				subject, f.ref, f.block, f.block, subject),
		})
	}

	// References in the latest user and assistant text.
	for _, m := range []*Message{lastUser, lastAsst} {
		if m == nil {
			continue
		}
		for _, tok := range refPattern.FindAllString(m.Text, -1) {
			seq, ok := refSeq[tok]
			if !ok {
				continue
			}
			b, ok := coveringBlock(active, seq)
			if !ok {
				continue
			}
			hints = append(hints, RecallHint{
				Kind:    RecallRef,
				BlockID: b.ID,
				Ref:     tok,
				Text:    fmt.Sprintf("%s está no bloco %s; recupere com decompress(%q).", tok, b.ID, b.ID),
			})
		}
		for _, tok := range blockPattern.FindAllString(m.Text, -1) {
			blk, ok := byID[tok]
			if !ok || blk.Active {
				continue // unknown, or visible already
			}
			anc, ok := activeAncestor(s.Blocks, tok)
			if !ok {
				continue
			}
			hints = append(hints, RecallHint{
				Kind:    RecallBlockID,
				BlockID: anc.ID,
				Text:    fmt.Sprintf("O bloco %s está dobrado no bloco %s; recupere com decompress(%q).", tok, anc.ID, anc.ID),
			})
		}
	}

	return dedupeHints(hints)
}

// callSignature returns the lookup key of a tool call and its target path. The
// key is the tool name plus the path when the args name one, or plus the
// canonical args otherwise. Non-JSON args are compared as trimmed text.
func callSignature(m Message) (key, path string) {
	text := strings.TrimSpace(m.Text)
	var args any
	if err := json.Unmarshal([]byte(text), &args); err != nil {
		return m.ToolName + "\x00args\x00" + text, ""
	}
	if obj, ok := args.(map[string]any); ok {
		for _, k := range pathKeys {
			if p, ok := obj[k].(string); ok && p != "" {
				return m.ToolName + "\x00path\x00" + p, p
			}
		}
	}
	canon, _ := json.Marshal(args) // maps marshal with sorted keys: canonical
	return m.ToolName + "\x00args\x00" + string(canon), ""
}

func refOf(m Message) string {
	if m.Ref != "" {
		return m.Ref
	}
	return FormatRef(m.Seq)
}

func dedupeHints(in []RecallHint) []RecallHint {
	sort.SliceStable(in, func(i, j int) bool {
		a, b := in[i], in[j]
		if a.BlockID != b.BlockID {
			return a.BlockID < b.BlockID
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Ref != b.Ref {
			return a.Ref < b.Ref
		}
		return a.Path < b.Path
	})
	seen := map[string]bool{}
	var out []RecallHint
	for _, h := range in {
		k := string(h.Kind) + "|" + h.BlockID + "|" + h.Ref + "|" + h.Path
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, h)
	}
	return out
}

// activeSorted returns the active blocks ordered by StartSeq. Active blocks do
// not overlap (I1/I2), so EndSeq is ordered too.
func activeSorted(blocks []Block) []Block {
	var out []Block
	for _, b := range blocks {
		if b.Active {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartSeq < out[j].StartSeq })
	return out
}

// coveringBlock finds the active block whose range contains seq. active must
// come from activeSorted.
func coveringBlock(active []Block, seq int64) (Block, bool) {
	i := sort.Search(len(active), func(i int) bool { return active[i].EndSeq >= seq })
	if i < len(active) && active[i].StartSeq <= seq && seq <= active[i].EndSeq {
		return active[i], true
	}
	return Block{}, false
}

func indexBlocks(blocks []Block) map[string]Block {
	m := make(map[string]Block, len(blocks))
	for _, b := range blocks {
		m[b.ID] = b
	}
	return m
}

// activeAncestor returns the active block whose lineage (transitive Sources)
// contains id. Under I6 there is at most one.
func activeAncestor(blocks []Block, id string) (Block, bool) {
	byID := indexBlocks(blocks)
	for _, a := range blocks {
		if a.Active && lineageHas(byID, a, id) {
			return a, true
		}
	}
	return Block{}, false
}

func lineageHas(byID map[string]Block, root Block, id string) bool {
	seen := map[string]bool{}
	stack := append([]string(nil), root.Sources...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == id {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		stack = append(stack, byID[cur].Sources...)
	}
	return false
}
