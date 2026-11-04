package acdctx

import (
	"fmt"

	"github.com/fsvxavier/accordion-context/internal/kernel"
)

// Role is the author of a message.
type Role string

// Message roles.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Kind is the content type of a message.
type Kind string

// Message kinds.
const (
	KindText       Kind = "text"
	KindToolCall   Kind = "tool_call"
	KindToolResult Kind = "tool_result"
	KindReasoning  Kind = "reasoning"
)

// Message is one entry of the conversation. Ref is set on messages returned by
// Turn; the input Tokens field is ignored because the library estimates it.
type Message struct {
	Seq        int64
	Ref        string // "m00042", stable reference used by Compress and Recall
	Role       Role
	Kind       Kind
	ToolName   string
	ToolCallID string
	Text       string
	Tokens     int  // estimated size, set on output
	CCRStored  bool // the text was shortened and the original is stored
}

// RecallKind names the signal behind a RecallHint.
type RecallKind string

// Recall signals (spec §13.2).
const (
	RecallReread RecallKind = "reread" // a tool call repeats a folded one
	RecallRef    RecallKind = "ref"    // the latest text cites a folded message ref
	RecallBlock  RecallKind = "block"  // the latest text cites a folded block id
)

// RecallHint points from folded content to the active block that covers it.
type RecallHint struct {
	Kind    RecallKind
	BlockID string // pass to Decompress
	Ref     string // folded message ref, when the hint names one
	Path    string // re-read target path, when one was found
	Text    string
}

// TurnResult is the plan of one turn.
type TurnResult struct {
	// Visible is the window to send to the model, in order.
	Visible []Message
	// Nudge is a note asking the model to compress finished ranges, or "". Append it
	// to the next user message when it is not empty.
	Nudge string
	// Hints are ephemeral recall pointers for this turn.
	Hints []RecallHint
	// Row is the decision row of spec §8.3 that produced the nudge decision.
	Row string
}

// CompressRange summarizes the messages from StartRef to EndRef (inclusive).
type CompressRange struct {
	StartRef string
	EndRef   string
	Summary  string
	Topic    string
}

// FoldRange merges active blocks of Tier-1 from StartBlock to EndBlock into one
// block of Tier (2 or 3).
type FoldRange struct {
	Tier       int
	StartBlock string
	EndBlock   string
	Summary    string
	Topic      string
}

// CompressArgs is the input of Compress.
type CompressArgs struct {
	Ranges       []CompressRange
	Folds        []FoldRange
	CloseIntents []int64
}

// Result reports what Compress wrote.
type Result struct {
	BlockIDs    []string
	TokensAfter int
}

// DecompressResult reports what Decompress expanded.
type DecompressResult struct {
	Target      string   // the block that became inactive
	Reactivated []string // its direct sources, visible again
	Redirected  bool     // the requested block was folded; its active ancestor was expanded
}

func (r Role) kernel() (kernel.Role, error) {
	switch r {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		return kernel.Role(r), nil
	}
	return "", fmt.Errorf("unknown role %q", r)
}

func (k Kind) kernel() (kernel.Kind, error) {
	switch k {
	case KindText, KindToolCall, KindToolResult, KindReasoning:
		return kernel.Kind(k), nil
	}
	return "", fmt.Errorf("unknown kind %q", k)
}

func fromKernelMessages(in []kernel.Message) []Message {
	out := make([]Message, len(in))
	for i, m := range in {
		out[i] = Message{
			Seq: m.Seq, Ref: m.Ref, Role: Role(m.Role), Kind: Kind(m.Kind),
			ToolName: m.ToolName, ToolCallID: m.ToolCallID, Text: m.Text,
			Tokens: m.Tokens, CCRStored: m.CCRStored,
		}
	}
	return out
}

func fromKernelHints(in []kernel.RecallHint) []RecallHint {
	out := make([]RecallHint, len(in))
	for i, h := range in {
		var k RecallKind
		switch h.Kind {
		case kernel.RecallReread:
			k = RecallReread
		case kernel.RecallRef:
			k = RecallRef
		case kernel.RecallBlockID:
			k = RecallBlock
		}
		out[i] = RecallHint{Kind: k, BlockID: h.BlockID, Ref: h.Ref, Path: h.Path, Text: h.Text}
	}
	return out
}
