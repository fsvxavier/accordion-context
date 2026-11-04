package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsvxavier/accordion-context/internal/kernel"
	"github.com/fsvxavier/accordion-context/internal/session"
	"github.com/fsvxavier/accordion-context/internal/store"
)

var (
	errUnknownTool = errors.New("unknown tool")
	errUnknownConv = errors.New("unknown conversation")
	errBadArgs     = errors.New("bad arguments")
	// errSearchUnavailable: the store exports no search API yet, so search_context
	// cannot run. The tool stays in the manifest and in tools/list.
	errSearchUnavailable = errors.New("search_context is not available: the store exposes no search API yet")
)

const (
	maxRangesPerCall  = 64
	maxPeekBytes      = 64 << 10
	maxSearchN        = 50
	maxCloseIntents   = 256
	ctxStatusToolName = "ctx_status"
)

// toolSpec is one entry of the manifest and of MCP tools/list.
type toolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func object(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

// toolSpecs are the tools of spec §16. compress is reachable over HTTP only;
// the MCP companion exposes the four read and status tools (spec §19.3, mode 5).
var toolSpecs = []toolSpec{
	{
		Name:        "compress",
		Description: "Summarize message ranges into a block. The summary replaces the originals in the window; they stay recoverable.",
		InputSchema: object(map[string]any{
			"ranges": map[string]any{"type": "array", "items": object(map[string]any{
				"startRef": str("first message ref, e.g. m00340"),
				"endRef":   str("last message ref, inclusive"),
				"summary":  str("past-tense summary: decisions with reasons, exact paths, literal errors, exact values"),
				"topic":    str("short topic label"),
			}, "startRef", "endRef", "summary")},
			"closeIntents": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
		}, "ranges"),
	},
	{
		Name:        "decompress",
		Description: "Expand a block one level (mode context), or return the original lines of a block that match query (mode peek).",
		InputSchema: object(map[string]any{
			"blockId": str("block id, e.g. b17"),
			"mode":    map[string]any{"type": "string", "enum": []string{"context", "peek"}},
			"query":   str("case-insensitive substring; required for peek"),
		}, "blockId"),
	},
	{
		Name:        "retrieve",
		Description: "Return the original content of one message by ref, including messages folded into a block.",
		InputSchema: object(map[string]any{"ref": str("message ref, e.g. m00042")}, "ref"),
	},
	{
		Name:        "search_context",
		Description: "Full-text search over block summaries of this session. Currently unavailable: the store has no search API yet.",
		InputSchema: object(map[string]any{
			"query": str("words to search for; all must match"),
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": maxSearchN},
		}, "query"),
	},
	{
		Name:        ctxStatusToolName,
		Description: "Context usage, active blocks, compressible ranges and open intents of this session.",
		InputSchema: object(map[string]any{}),
	},
}

// tools runs tool calls against one session. It is shared by HTTP and MCP.
type tools struct {
	st *store.Store
}

// call runs one tool. Errors wrap errUnknownTool, errBadArgs, or come from the
// session as refusals.
func (t tools) call(ctx context.Context, sess *session.Session, sid, name string, args json.RawMessage) (any, error) {
	switch name {
	case "compress":
		return t.compress(ctx, sess, args)
	case "decompress":
		return t.decompress(ctx, sess, sid, args)
	case "retrieve":
		var a struct {
			Ref string `json:"ref"`
		}
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		if a.Ref == "" {
			return nil, fmt.Errorf("%w: ref is required", errBadArgs)
		}
		text, err := sess.Recall(ctx, a.Ref)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ref": a.Ref, "text": text}, nil
	case "search_context":
		return nil, errSearchUnavailable
	case ctxStatusToolName:
		return t.status(ctx, sid)
	default:
		return nil, errUnknownTool
	}
}

func (t tools) compress(ctx context.Context, sess *session.Session, args json.RawMessage) (any, error) {
	var a struct {
		Ranges []struct {
			StartRef string `json:"startRef"`
			EndRef   string `json:"endRef"`
			Summary  string `json:"summary"`
			Topic    string `json:"topic"`
		} `json:"ranges"`
		CloseIntents []int64 `json:"closeIntents"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if len(a.Ranges) == 0 {
		return nil, fmt.Errorf("%w: ranges is required", errBadArgs)
	}
	if len(a.Ranges) > maxRangesPerCall || len(a.CloseIntents) > maxCloseIntents {
		return nil, fmt.Errorf("%w: too many ranges or intents", errBadArgs)
	}
	ca := session.CompressArgs{CloseIntents: a.CloseIntents}
	for _, r := range a.Ranges {
		ca.Ranges = append(ca.Ranges, session.CompressRange{
			StartRef: r.StartRef, EndRef: r.EndRef, Summary: r.Summary, Topic: r.Topic,
		})
	}
	res, err := sess.HandleCompress(ctx, ca)
	if err != nil {
		return nil, err
	}
	return map[string]any{"blockIds": res.BlockIDs, "tokensAfter": res.TokensAfter}, nil
}

func (t tools) decompress(ctx context.Context, sess *session.Session, sid string, args json.RawMessage) (any, error) {
	var a struct {
		BlockID string `json:"blockId"`
		Mode    string `json:"mode"`
		Query   string `json:"query"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.BlockID == "" {
		return nil, fmt.Errorf("%w: blockId is required", errBadArgs)
	}
	switch a.Mode {
	case "", "context":
		res, err := sess.Decompress(ctx, a.BlockID)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"target": res.Target, "reactivated": res.Reactivated, "redirected": res.Redirected,
		}, nil
	case "peek":
		if a.Query == "" {
			return nil, fmt.Errorf("%w: query is required for peek", errBadArgs)
		}
		text, err := t.peek(ctx, sess, sid, a.BlockID, strings.ToLower(a.Query))
		if err != nil {
			return nil, err
		}
		return map[string]any{"blockId": a.BlockID, "mode": "peek", "text": text}, nil
	default:
		return nil, fmt.Errorf("%w: mode %q is not available over this protocol", errBadArgs, a.Mode)
	}
}

// peek returns the original lines of a block's messages that contain q.
func (t tools) peek(ctx context.Context, sess *session.Session, sid, blockID, q string) (string, error) {
	snap, err := t.st.Snapshot(ctx, sid)
	if err != nil {
		return "", err
	}
	var blk *kernel.Block
	for i := range snap.Blocks {
		if snap.Blocks[i].ID == blockID {
			blk = &snap.Blocks[i]
			break
		}
	}
	if blk == nil {
		return "", fmt.Errorf("decompress: unknown block %q", blockID)
	}
	var b strings.Builder
	for _, m := range snap.Messages {
		if m.Seq < blk.StartSeq || m.Seq > blk.EndSeq {
			continue
		}
		text, err := sess.Recall(ctx, m.Ref)
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(strings.ToLower(line), q) {
				fmt.Fprintf(&b, "[%s] %s\n", m.Ref, line)
			}
		}
		if b.Len() > maxPeekBytes {
			break
		}
	}
	return b.String(), nil
}

// status reports the session the way spec §16 describes ctx_status.
func (t tools) status(ctx context.Context, sid string) (map[string]any, error) {
	snap, err := t.st.Snapshot(ctx, sid)
	if err != nil {
		return nil, err
	}
	type block struct {
		ID        string `json:"id"`
		Tier      int    `json:"tier"`
		StartRef  string `json:"startRef"`
		EndRef    string `json:"endRef"`
		SrcTokens int    `json:"srcTokens"`
		SumTokens int    `json:"sumTokens"`
	}
	blocks := []block{}
	activeTokens := 0
	for _, b := range snap.Blocks {
		if !b.Active {
			continue
		}
		activeTokens += b.SumTokens
		blocks = append(blocks, block{
			ID: b.ID, Tier: b.Tier, StartRef: kernel.FormatRef(b.StartSeq),
			EndRef: kernel.FormatRef(b.EndSeq), SrcTokens: b.SrcTokens, SumTokens: b.SumTokens,
		})
	}
	spans := kernel.CompressibleSpans(snap, session.RecentKeep)
	type span struct {
		Start  string `json:"start"`
		End    string `json:"end"`
		Tokens int    `json:"tokens"`
	}
	ranges := make([]span, 0, len(spans))
	for _, s := range spans {
		ranges = append(ranges, span{Start: s.Start, End: s.End, Tokens: s.Tokens})
	}
	type intent struct {
		ID     int64  `json:"id"`
		Text   string `json:"text"`
		Status string `json:"status"`
	}
	intents := []intent{}
	for _, in := range snap.Intents {
		if in.Status == "open" {
			intents = append(intents, intent{ID: in.ID, Text: in.Text, Status: in.Status})
		}
	}
	return map[string]any{
		"sessionId":         sid,
		"contextLimit":      snap.ContextLimit,
		"messages":          len(snap.Messages),
		"activeBlockTokens": activeTokens,
		"activeBlocks":      blocks,
		"compressible":      ranges,
		"openIntents":       intents,
		"lowWaterTokens":    snap.Trigger.LowWater,
		"nudgePending":      snap.Trigger.NudgePending,
		"refusals":          snap.Trigger.Refusals,
	}, nil
}

func decodeArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%w: %v", errBadArgs, err)
	}
	return nil
}

func nowUnix() int64 { return time.Now().Unix() }

// reader is a read-only handle on the database file, used only by MCP to find
// the project's latest session: the store exports no session-listing API. It
// never writes: the connection is opened with mode=ro and query_only.
type reader struct {
	db *sql.DB
}

func openReader(path string) (*reader, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(abs) + "?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("plugin: open read-only database: %w", err)
	}
	return &reader{db: db}, nil
}

// Close releases the handle.
func (r *reader) Close() error { return r.db.Close() }

// latestSession returns the id of the project's most recently active session:
// the one with the newest message, or the newest creation if it has none.
// sql.ErrNoRows means the project has no session yet.
func (r *reader) latestSession(ctx context.Context, projectID string) (string, error) {
	var sid string
	err := r.db.QueryRowContext(ctx, `
		SELECT s.id FROM sessions s
		WHERE s.project_id = ?
		ORDER BY COALESCE((SELECT MAX(m.created_at) FROM messages m WHERE m.session_id = s.id), s.created_at) DESC,
		         s.created_at DESC
		LIMIT 1`, projectID).Scan(&sid)
	return sid, err
}
