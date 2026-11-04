package responses

import (
	"encoding/json"
	"fmt"
)

// Tool names exposed to the model (spec §16).
const (
	ToolCompress   = "compress"
	ToolDecompress = "decompress"
)

// Function tool definitions in Responses format. The Responses API uses a flat
// shape, {"type":"function","name":...}, unlike Chat Completions.
const compressToolJSON = `{"type":"function","name":"compress","description":"Replace already-finished ranges of the conversation with summaries. Each summary is written in the past tense, keeps paths with line numbers, exact errors, decisions with their reasons and exact values, and marks finished user requests as concluded. Pass closeIntents for user requests that the summarized ranges completed. Do not compress the work in progress.","parameters":{"type":"object","properties":{"ranges":{"type":"array","description":"Ranges of message refs to summarize, for example m00340..m00412.","items":{"type":"object","properties":{"startRef":{"type":"string"},"endRef":{"type":"string"},"summary":{"type":"string"},"topic":{"type":"string"}},"required":["startRef","endRef","summary"]}},"folds":{"type":"array","description":"Folds of tier-2 or tier-3 blocks: the active blocks one tier below, from startBlock to endBlock, become one block of the given tier.","items":{"type":"object","properties":{"tier":{"type":"integer"},"startBlock":{"type":"string"},"endBlock":{"type":"string"},"summary":{"type":"string"},"topic":{"type":"string"}},"required":["tier","startBlock","endBlock","summary"]}},"closeIntents":{"type":"array","description":"Ids of open intents completed by the summarized ranges.","items":{"type":"integer"}}},"required":["ranges"]}}`

const decompressToolJSON = `{"type":"function","name":"decompress","description":"Recover the original messages behind a summary block. Use mode peek with a query to read only the relevant excerpts, or context to bring the whole block back.","parameters":{"type":"object","properties":{"blockId":{"type":"string","description":"Block id from a history envelope, for example b12."},"mode":{"type":"string","enum":["context","file","peek"]},"query":{"type":"string","description":"Optional search text, used with mode peek."}},"required":["blockId","mode"]}}`

// ToolDefinitions returns fresh copies of the compress and decompress function
// tool definitions.
func ToolDefinitions() [][]byte {
	return [][]byte{[]byte(compressToolJSON), []byte(decompressToolJSON)}
}

// InjectTools adds the compress and decompress function tools to the request's
// tools array, skipping any tool already declared under the same name. Existing
// tool definitions and every other field are copied byte for byte. When nothing
// is added the body is returned unchanged, so the call is idempotent.
func InjectTools(body []byte) ([]byte, error) {
	fields, err := parseObject(body)
	if err != nil {
		return nil, fmt.Errorf("responses: inject tools: %w", err)
	}
	var existing [][]byte
	if raw := lookup(fields, "tools"); raw != nil && !isNull(raw) {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("responses: inject tools: tools: %w", err)
		}
		for _, it := range items {
			existing = append(existing, []byte(it))
		}
	}
	have := make(map[string]bool, len(existing))
	for _, t := range existing {
		if name := toolName(t); name != "" {
			have[name] = true
		}
	}
	added := false
	for _, def := range ToolDefinitions() {
		if have[toolName(def)] {
			continue
		}
		existing = append(existing, def)
		added = true
	}
	if !added {
		return body, nil
	}
	return buildObject(setField(fields, "tools", joinArray(existing))), nil
}

// HasTool reports whether the request body declares a function tool with the
// given name. Malformed bodies report false.
func HasTool(body []byte, name string) bool {
	fields, err := parseObject(body)
	if err != nil {
		return false
	}
	raw := lookup(fields, "tools")
	if raw == nil || isNull(raw) {
		return false
	}
	var tools []json.RawMessage
	if json.Unmarshal(raw, &tools) != nil {
		return false
	}
	for _, t := range tools {
		if toolName(t) == name {
			return true
		}
	}
	return false
}

// toolName reads the name of a function tool. Only the flat form is read. Other
// tool types, such as web_search, have no name and yield "".
func toolName(def []byte) string {
	var h struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(def, &h) != nil || h.Type != "function" {
		return ""
	}
	return h.Name
}
