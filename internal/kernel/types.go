// Package kernel is pure: no I/O, no imports from store/wire/session (spec §4, §6).
package kernel

import "fmt"

type Role string
type Kind string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"

	KindText       Kind = "text"
	KindToolCall   Kind = "tool_call"
	KindToolResult Kind = "tool_result"
	KindReasoning  Kind = "reasoning"
	// KindOpaque is an input item of a type the codec does not model (for example
	// a Responses compaction item). It is kept in place and sent back unchanged. It
	// is never summarized, dropped by truncation, or joined to a block: see
	// CompressibleSpans, EmergencyTruncateWith and Render.
	KindOpaque Kind = "opaque"
)

// Message is the protocol-independent normalized view.
type Message struct {
	Seq        int64
	Ref        string // "m00042"
	Role       Role
	Kind       Kind
	ToolName   string
	ToolCallID string
	Text       string
	Tokens     int
	CCRStored  bool
}

type Block struct {
	ID        string // "b17"
	Tier      int    // 1..3
	Topic     string
	Summary   string
	StartSeq  int64
	EndSeq    int64
	SrcTokens int
	SumTokens int
	Active    bool
	Sources   []string // consumed blocks (T2/T3)
}

type Intent struct {
	ID        int64
	OriginSeq int64
	Text      string
	Status    string // open | done | superseded
}

type TriggerState struct {
	LowWater     int
	NudgePending bool
	NudgeShownAt int
	LastShownT2  int
	LastShownT3  int
	Refusals     int
}

// Snapshot is everything the kernel needs to decide a turn.
// Phase 1 subset: cache stats, idle time and token count arrive with §8/§9.
type Snapshot struct {
	SessionID    string
	ContextLimit int
	Messages     []Message // full ledger, ordered by Seq
	Blocks       []Block
	Intents      []Intent
	Trigger      TriggerState
}

// TurnPlan is what the kernel returns. Phase 1 subset: Visible only.
type TurnPlan struct {
	Visible []Message
}

// FormatRef renders a ledger position as a stable reference, e.g. 42 -> "m00042".
func FormatRef(seq int64) string { return fmt.Sprintf("m%05d", seq) }
