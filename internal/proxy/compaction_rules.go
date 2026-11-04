package proxy

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Text rules recognise a client's own compaction request by fixed text in its
// compaction prompt (docs/compaction-commands.md, sections 2 and 5). They are used
// only when a request has no header or item signal. Every rule below is VERIFIED
// FROM THE CLIENT'S SOURCE ONLY, NOT ON THE WIRE: no capture of the request
// exists, so the text is what the source builds, and a client release may change
// it. The rules are tested with synthetic fixtures built from the same text.

// Signal names. They are the labels of the client_compaction metric, so they form a
// closed set: a signal is never built from request content.
const (
	signalClaudeCodeHeader  = "claude-code-header"        // x-claude-code-compaction
	signalClaudeCodeClass   = "claude-code-request-class" // x-claude-code-request-class: compaction
	signalCodexCompactPath  = "codex-compact-path"        // POST /v1/responses/compact (legacy Codex builds)
	signalCodexTrigger      = "codex-compaction-trigger"  // last input item is compaction_trigger
	signalKiloHeader        = "kilo-header"               // x-kilocode-mode: compaction (Kilo gateway)
	signalGeminiUtility     = "gemini-utility"            // non-streaming generateContent without tools
	signalGeminiSnapshot    = "gemini-state-snapshot"
	signalOpenCodeV1Summary = "opencode-v1-summary"
	signalClineSDKSummary   = "cline-sdk-summary"
	signalClineSummarize    = "cline-classic-summarize-task"
	signalClineCondense     = "cline-classic-condense"
	signalCodexLocal        = "codex-local-checkpoint"
	signalOpenCodeV2Sum     = "opencode-v2-summarize"
	signalOpenCodeV2Update  = "opencode-v2-update"
)

// ruleStrength separates the rules that are on by default from the weak ones.
type ruleStrength int

const (
	strengthStrong ruleStrength = iota // on with compaction.text_rules = strong (default)
	strengthWeak                       // on only with compaction.text_rules = all
)

// slot names the part of a request that a condition reads.
type slot int

const (
	slotSystem   slot = iota // the system prompt of the request, in any dialect
	slotLastUser             // the text of the last user message
)

// how is how a condition compares its text with the slot.
type how int

const (
	howPrefix   how = iota // the slot opens with the text (leading whitespace ignored)
	howContains            // the slot holds the text
)

// cond is one condition of a text rule. A rule matches when all its conditions do.
type cond struct {
	slot slot
	how  how
	text string
}

// textRule is one client's compaction prompt.
type textRule struct {
	signal   string
	client   string
	strength ruleStrength
	// dialects limits the rule to these dialect names. Empty means every dialect.
	dialects []string
	// probe is a plain ASCII substring (letters, digits, spaces, underscores) that
	// the JSON encoder never escapes. A body without it is not decoded at all.
	probe string
	conds []cond
	// source is the file in the client's repository, at the commit of
	// docs/compaction-commands.md.
	source string
}

// textRules is the table. The text of each condition is copied from the source
// named in the comment above it.
var textRules = []textRule{
	{
		// Gemini CLI, packages/core/src/prompts/snippets.ts:898-912 (commit 89e9d25).
		// The first sentence of systemInstruction only. The user contents of later
		// turns carry <state_snapshot> too, so they are never matched.
		signal: signalGeminiSnapshot, client: "Gemini CLI", strength: strengthStrong,
		dialects: []string{"google"},
		probe:    "distilling chat history into a structured XML",
		conds: []cond{{slot: slotSystem, how: howPrefix,
			text: "You are a specialized system component responsible for distilling chat history into a structured XML <state_snapshot>."}},
		source: "packages/core/src/prompts/snippets.ts (VERIFIED from source, not on the wire)",
	},
	{
		// OpenCode v1 packages/opencode/src/agent/prompt/compaction.txt, and Kilo Code,
		// whose file is byte-identical (commit 810c0eb and c140bcd). The system prompt
		// and the single user message built by buildPrompt both have to match.
		signal: signalOpenCodeV1Summary, client: "OpenCode v1 and Kilo Code", strength: strengthStrong,
		probe: "context summarization agent",
		conds: []cond{
			{slot: slotSystem, how: howContains,
				text: "You are a context summarization agent. You are given a conversation between a user and an agent."},
			{slot: slotLastUser, how: howContains,
				text: "Create a new anchored summary from the conversation history in the <conversation> tags above"},
		},
		source: "packages/opencode/src/agent/prompt/compaction.txt and session/compaction.ts (VERIFIED from source, not on the wire)",
	},
	{
		// Cline SDK, sdk/packages/core/src/extensions/context/compaction-shared.ts and
		// summary-generation.ts (commit 7a7878c). A separate tool-less request.
		signal: signalClineSDKSummary, client: "Cline SDK", strength: strengthStrong,
		probe: "Summarize the provided coding session",
		conds: []cond{
			{slot: slotSystem, how: howContains,
				text: "Summarize the provided coding session into a concise continuation note with detailed next steps."},
			{slot: slotLastUser, how: howPrefix,
				text: "Summarize this session for continuation. Be concise and factual."},
		},
		source: "sdk/packages/core/src/extensions/context (VERIFIED from source, not on the wire)",
	},
	{
		// Cline classic, src/core/prompts/contextManagement.ts and commands.ts (tag
		// v3.40.0). The instruction is appended to the last user message of a normal turn.
		signal: signalClineSummarize, client: "Cline classic", strength: strengthStrong,
		probe: "explicit_instructions",
		conds: []cond{{slot: slotLastUser, how: howContains,
			text: `<explicit_instructions type="summarize_task">`}},
		source: "src/core/prompts/contextManagement.ts:10 (VERIFIED from source, not on the wire)",
	},
	{
		// Cline classic, src/core/prompts/commands.ts:60-61 (/smol and /compact).
		signal: signalClineCondense, client: "Cline classic", strength: strengthStrong,
		probe: "explicit_instructions",
		conds: []cond{{slot: slotLastUser, how: howContains,
			text: `<explicit_instructions type="condense">`}},
		source: "src/core/prompts/commands.ts:60-61 (VERIFIED from source, not on the wire)",
	},
	{
		// Codex CLI, codex-rs/prompts/templates/compact/prompt.md, used by compact.rs
		// on providers without remote compaction (commit 48524f3).
		signal: signalCodexLocal, client: "Codex CLI (local compaction)", strength: strengthStrong,
		probe: "CONTEXT CHECKPOINT COMPACTION",
		conds: []cond{{slot: slotLastUser, how: howPrefix,
			text: "You are performing a CONTEXT CHECKPOINT COMPACTION."}},
		source: "codex-rs/prompts/templates/compact/prompt.md (VERIFIED from source, not on the wire)",
	},
	{
		// OpenCode v2 packages/core/src/session/compaction.ts buildPrompt (commit
		// 8568952). Weak: the prompt is the last user message of a normal-looking request.
		signal: signalOpenCodeV2Sum, client: "OpenCode v2", strength: strengthWeak,
		probe: "summarize the conversation above",
		conds: []cond{{slot: slotLastUser, how: howPrefix,
			text: "You MUST summarize the conversation above into a structured summary"}},
		source: "packages/core/src/session/compaction.ts:207-242 (VERIFIED from source, not on the wire)",
	},
	{
		// OpenCode v2, the later-compaction prompt of the same buildPrompt.
		signal: signalOpenCodeV2Update, client: "OpenCode v2", strength: strengthWeak,
		probe: "Update the existing checkpoint",
		conds: []cond{{slot: slotLastUser, how: howPrefix,
			text: "Update the existing checkpoint in the conversation above"}},
		source: "packages/core/src/session/compaction.ts:207-242 (VERIFIED from source, not on the wire)",
	},
}

// enabled reports whether a rule takes part in matching for a mode and dialect.
// The modes are "strong" (strong rules), "all" (every rule) and "off" (none).
func (r textRule) enabled(mode, dialect string) bool {
	if mode == "off" || (mode == "strong" && r.strength != strengthStrong) {
		return false
	}
	if len(r.dialects) == 0 {
		return true
	}
	for _, d := range r.dialects {
		if d == dialect {
			return true
		}
	}
	return false
}

// holds reports whether every condition of the rule holds for the slots of a request.
func (r textRule) holds(sl requestSlots) bool {
	for _, c := range r.conds {
		text := sl.system
		if c.slot == slotLastUser {
			text = sl.lastUser
		}
		switch c.how {
		case howPrefix:
			if !strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), c.text) {
				return false
			}
		case howContains:
			if !strings.Contains(text, c.text) {
				return false
			}
		}
	}
	return true
}

// requestSlots are the only parts of a request the text rules read.
type requestSlots struct {
	system   string
	lastUser string
}

// matchText returns the signal of the first rule that matches a body of the given
// dialect, or "". It runs in three steps, so that ordinary turns cost a few byte
// searches and no JSON decoding:
//  1. Each rule's probe is searched for in the raw bytes. No probe, no match.
//  2. mode is read only when a probe was found, so the configuration is not loaded
//     for ordinary requests. The enabled rules are those of that mode.
//  3. Only then is the body decoded, and only its system prompt and last user
//     message are read.
func matchText(dialect string, body []byte, mode func() string) string {
	if len(body) == 0 {
		return ""
	}
	var hit []textRule
	for _, r := range textRules {
		if bytes.Contains(body, []byte(r.probe)) {
			hit = append(hit, r)
		}
	}
	if len(hit) == 0 {
		return ""
	}
	m := mode()
	var probed []textRule
	for _, r := range hit {
		if r.enabled(m, dialect) {
			probed = append(probed, r)
		}
	}
	if len(probed) == 0 {
		return ""
	}
	sl, ok := slotsOf(dialect, body)
	if !ok {
		return ""
	}
	for _, r := range probed {
		if r.holds(sl) {
			return r.signal
		}
	}
	return ""
}

// slotsOf decodes the system prompt and the last user message of a body in one of
// the four dialects. It reads no other field of the text.
func slotsOf(dialect string, body []byte) (requestSlots, bool) {
	switch dialect {
	case "anthropic":
		var v struct {
			System   json.RawMessage `json:"system"`
			Messages []chatItem      `json:"messages"`
		}
		if json.Unmarshal(body, &v) != nil {
			return requestSlots{}, false
		}
		return requestSlots{system: contentText(v.System), lastUser: lastUserText(v.Messages)}, true
	case "openai":
		var v struct {
			Messages []chatItem `json:"messages"`
		}
		if json.Unmarshal(body, &v) != nil {
			return requestSlots{}, false
		}
		var sys []string
		for _, m := range v.Messages {
			if m.Role == "system" || m.Role == "developer" {
				sys = append(sys, contentText(m.Content))
			}
		}
		return requestSlots{system: strings.Join(sys, "\n"), lastUser: lastUserText(v.Messages)}, true
	case "responses":
		var v struct {
			Instructions json.RawMessage `json:"instructions"`
			Input        json.RawMessage `json:"input"`
		}
		if json.Unmarshal(body, &v) != nil {
			return requestSlots{}, false
		}
		sl := requestSlots{system: contentText(v.Instructions)}
		if t := bytes.TrimSpace(v.Input); len(t) > 0 && t[0] == '"' {
			sl.lastUser = contentText(v.Input)
			return sl, true
		}
		var items []chatItem
		if json.Unmarshal(v.Input, &items) != nil {
			return requestSlots{}, false
		}
		sl.lastUser = lastUserText(items)
		return sl, true
	case "google":
		var v struct {
			SystemInstruction struct {
				Parts []textPart `json:"parts"`
			} `json:"systemInstruction"`
			Contents []struct {
				Role  string     `json:"role"`
				Parts []textPart `json:"parts"`
			} `json:"contents"`
		}
		if json.Unmarshal(body, &v) != nil {
			return requestSlots{}, false
		}
		sl := requestSlots{system: joinParts(v.SystemInstruction.Parts)}
		for _, c := range v.Contents {
			if c.Role == "user" {
				sl.lastUser = joinParts(c.Parts)
			}
		}
		return sl, true
	}
	return requestSlots{}, false
}

// chatItem is a message of the chat, Messages or Responses input shape.
type chatItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// textPart is a Gemini part; only its text is read.
type textPart struct {
	Text *string `json:"text"`
}

// lastUserText is the text of the last user message. A Responses function output
// or a call is not a message and is skipped.
func lastUserText(items []chatItem) string {
	for i := len(items) - 1; i >= 0; i-- {
		m := items[i]
		if m.Role == "user" && (m.Type == "" || m.Type == "message") {
			return contentText(m.Content)
		}
	}
	return ""
}

// contentText flattens content that is a string, or an array whose parts have
// text. Other parts are ignored.
func contentText(raw json.RawMessage) string {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return ""
	}
	if t[0] == '"' {
		var s string
		if json.Unmarshal(t, &s) != nil {
			return ""
		}
		return s
	}
	if t[0] != '[' {
		return ""
	}
	var parts []textPart
	if json.Unmarshal(t, &parts) != nil {
		return ""
	}
	return joinParts(parts)
}

// joinParts joins the texts of the parts that have one.
func joinParts(parts []textPart) string {
	var texts []string
	for _, p := range parts {
		if p.Text != nil {
			texts = append(texts, *p.Text)
		}
	}
	return strings.Join(texts, "\n")
}
