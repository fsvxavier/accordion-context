package obs

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/fsvxavier/accordion-context/internal/store"
)

// Trace is the replayable record of one session (spec §17.2 item 4). It is
// written as JSON lines: one session line, then the messages, intents, blocks and
// events in ledger order. A trace with anonymized text keeps each text's length
// and sha256 and drops the text itself.
type Trace struct {
	Session  SessionLine
	Messages []MessageLine
	Intents  []IntentLine
	Blocks   []BlockLine
	Events   []EventLine
}

// Line type tags, the "t" field of each JSON line.
const (
	TagSession = "session"
	TagMessage = "msg"
	TagIntent  = "intent"
	TagBlock   = "block"
	TagEvent   = "event"
)

// TraceVersion is the format version written in the session line.
const TraceVersion = 1

// Body is a text kept verbatim (Value set) or anonymized (Value nil).
type Body struct {
	Value  *string `json:"value,omitempty"`
	SHA256 string  `json:"sha256"`
	Len    int     `json:"len"`
}

// SessionLine describes the session the trace comes from.
type SessionLine struct {
	T            string `json:"t"`
	Version      int    `json:"version"`
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	Model        string `json:"model"`
	ContextLimit int    `json:"context_limit"`
	Mode         string `json:"mode"`
	Integration  string `json:"integration"`
	Anonymized   bool   `json:"anonymized"`
}

// MessageLine is one ledger message (spec §5.2).
type MessageLine struct {
	T          string `json:"t"`
	Seq        int64  `json:"seq"`
	Ref        string `json:"ref"`
	Role       string `json:"role"`
	Kind       string `json:"kind"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Tokens     int    `json:"tokens"`
	Text       Body   `json:"text"`
}

// IntentLine is one user intent (spec §11.3).
type IntentLine struct {
	T         string `json:"t"`
	ID        int64  `json:"id"`
	OriginSeq int64  `json:"origin_seq"`
	Status    string `json:"status"`
	Text      Body   `json:"text"`
}

// BlockLine is one compression block (spec §5.2). Sources lists the consumed
// blocks of a T2/T3 block, in ledger order.
type BlockLine struct {
	T         string   `json:"t"`
	ID        string   `json:"id"`
	Tier      int      `json:"tier"`
	Topic     Body     `json:"topic"`
	Summary   Body     `json:"summary"`
	StartSeq  int64    `json:"start_seq"`
	EndSeq    int64    `json:"end_seq"`
	SrcTokens int      `json:"src_tokens"`
	SumTokens int      `json:"sum_tokens"`
	Active    bool     `json:"active"`
	Sources   []string `json:"sources,omitempty"`
}

// EventLine is one row of the events log. Payload is the stored JSON.
type EventLine struct {
	T       string          `json:"t"`
	Seq     int64           `json:"seq"`
	At      int64           `json:"at"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// ExportTrace reads session sid from st into a Trace. With anonymize set, texts
// are replaced by their length and sha256, and the location keys of event payloads
// (see pathKeys) are dropped.
func ExportTrace(ctx context.Context, st *store.Store, sid string, anonymize bool) (Trace, error) {
	var t Trace
	row, err := st.GetSession(ctx, sid)
	if err != nil {
		return t, err
	}
	t.Session = SessionLine{
		T: TagSession, Version: TraceVersion, ID: row.ID, ProjectID: row.ProjectID,
		Model: row.Model, ContextLimit: row.ContextLimit, Mode: row.Mode,
		Integration: row.Integration, Anonymized: anonymize,
	}
	snap, err := st.Snapshot(ctx, sid)
	if err != nil {
		return t, err
	}
	for _, m := range snap.Messages {
		t.Messages = append(t.Messages, MessageLine{
			T: TagMessage, Seq: m.Seq, Ref: m.Ref, Role: string(m.Role), Kind: string(m.Kind),
			ToolName: m.ToolName, ToolCallID: m.ToolCallID, Tokens: m.Tokens,
			Text: body(m.Text, anonymize),
		})
	}
	for _, in := range snap.Intents {
		t.Intents = append(t.Intents, IntentLine{
			T: TagIntent, ID: in.ID, OriginSeq: in.OriginSeq, Status: in.Status,
			Text: body(in.Text, anonymize),
		})
	}
	for _, b := range snap.Blocks {
		t.Blocks = append(t.Blocks, BlockLine{
			T: TagBlock, ID: b.ID, Tier: b.Tier, Topic: body(b.Topic, anonymize),
			Summary: body(b.Summary, anonymize), StartSeq: b.StartSeq, EndSeq: b.EndSeq,
			SrcTokens: b.SrcTokens, SumTokens: b.SumTokens, Active: b.Active,
			Sources: b.Sources,
		})
	}
	evs, err := st.ListEvents(ctx, sid, 0)
	if err != nil {
		return t, err
	}
	for _, e := range evs {
		payload := e.Payload
		if anonymize {
			payload = dropPath(payload)
		}
		t.Events = append(t.Events, EventLine{T: TagEvent, Seq: e.Seq, At: e.At, Type: e.Type, Payload: payload})
	}
	return t, nil
}

func body(text string, anonymize bool) Body {
	sum := sha256.Sum256([]byte(text))
	b := Body{SHA256: hex.EncodeToString(sum[:]), Len: len(text)}
	if !anonymize {
		v := text
		b.Value = &v
	}
	return b
}

// pathKeys are the payload keys that name a location on the client's machine: a
// file a recall touched ("path") or the working directory of a plugin session ("cwd").
var pathKeys = []string{"path", "cwd"}

// dropPath removes the pathKeys of an object payload. A payload that is not an
// object is returned unchanged.
func dropPath(raw json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return raw
	}
	dropped := false
	for _, k := range pathKeys {
		if _, ok := m[k]; ok {
			delete(m, k)
			dropped = true
		}
	}
	if !dropped {
		return raw
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// WriteTrace writes t as JSON lines.
func WriteTrace(w io.Writer, t Trace) error {
	enc := json.NewEncoder(w)
	if err := enc.Encode(t.Session); err != nil {
		return err
	}
	for _, m := range t.Messages {
		if err := enc.Encode(m); err != nil {
			return err
		}
	}
	for _, in := range t.Intents {
		if err := enc.Encode(in); err != nil {
			return err
		}
	}
	for _, b := range t.Blocks {
		if err := enc.Encode(b); err != nil {
			return err
		}
	}
	for _, e := range t.Events {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

// ReadTrace parses the JSON lines written by WriteTrace. The session line must come
// first and be the only one of its kind; unknown tags are an error, so a
// truncated or edited file fails loudly.
func ReadTrace(r io.Reader) (Trace, error) {
	var t Trace
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	haveSession := false
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var tag struct {
			T string `json:"t"`
		}
		if err := json.Unmarshal([]byte(line), &tag); err != nil {
			return t, fmt.Errorf("trace line %d: %w", n, err)
		}
		if tag.T != TagSession && !haveSession {
			return t, fmt.Errorf("trace line %d: session line must come first", n)
		}
		var err error
		switch tag.T {
		case TagSession:
			if haveSession {
				return t, fmt.Errorf("trace line %d: second session line", n)
			}
			haveSession = true
			err = json.Unmarshal([]byte(line), &t.Session)
			if err == nil && t.Session.Version != TraceVersion {
				err = fmt.Errorf("unsupported trace version %d", t.Session.Version)
			}
		case TagMessage:
			var m MessageLine
			if err = json.Unmarshal([]byte(line), &m); err == nil {
				t.Messages = append(t.Messages, m)
			}
		case TagIntent:
			var in IntentLine
			if err = json.Unmarshal([]byte(line), &in); err == nil {
				t.Intents = append(t.Intents, in)
			}
		case TagBlock:
			var b BlockLine
			if err = json.Unmarshal([]byte(line), &b); err == nil {
				t.Blocks = append(t.Blocks, b)
			}
		case TagEvent:
			var e EventLine
			if err = json.Unmarshal([]byte(line), &e); err == nil {
				t.Events = append(t.Events, e)
			}
		default:
			err = fmt.Errorf("unknown record %q", tag.T)
		}
		if err != nil {
			return t, fmt.Errorf("trace line %d: %w", n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return t, err
	}
	if !haveSession {
		return t, fmt.Errorf("trace has no session line")
	}
	return t, nil
}
