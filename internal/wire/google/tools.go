package google

import (
	"encoding/json"
	"fmt"
)

// Tool names exposed to the model (spec §16).
const (
	ToolCompress   = "compress"
	ToolDecompress = "decompress"
)

// Function declarations in the Gemini format. Schema types use the upper-case
// OpenAPI subset that Gemini expects.
const compressDeclJSON = `{"name":"compress","description":"Replace already-finished ranges of the conversation with summaries. Each summary is written in the past tense, keeps paths with line numbers, exact errors, decisions with their reasons and exact values, and marks finished user requests as concluded. Pass closeIntents for user requests that the summarized ranges completed. Do not compress the work in progress.","parameters":{"type":"OBJECT","properties":{"ranges":{"type":"ARRAY","description":"Ranges of message refs to summarize, for example m00340..m00412.","items":{"type":"OBJECT","properties":{"startRef":{"type":"STRING"},"endRef":{"type":"STRING"},"summary":{"type":"STRING"},"topic":{"type":"STRING"}},"required":["startRef","endRef","summary"]}},"folds":{"type":"ARRAY","description":"Folds of tier-2 or tier-3 blocks: the active blocks one tier below, from startBlock to endBlock, become one block of the given tier.","items":{"type":"OBJECT","properties":{"tier":{"type":"INTEGER"},"startBlock":{"type":"STRING"},"endBlock":{"type":"STRING"},"summary":{"type":"STRING"},"topic":{"type":"STRING"}},"required":["tier","startBlock","endBlock","summary"]}},"closeIntents":{"type":"ARRAY","description":"Ids of open intents completed by the summarized ranges.","items":{"type":"INTEGER"}}},"required":["ranges"]}}`

const decompressDeclJSON = `{"name":"decompress","description":"Recover the original messages behind a summary block. Use mode peek with a query to read only the relevant excerpts, or context to bring the whole block back.","parameters":{"type":"OBJECT","properties":{"blockId":{"type":"STRING","description":"Block id from a history envelope, for example b12."},"mode":{"type":"STRING","enum":["context","file","peek"]},"query":{"type":"STRING","description":"Optional search text, used with mode peek."}},"required":["blockId","mode"]}}`

// ToolDefinitions returns fresh copies of the compress and decompress function
// declarations, each one a member of a functionDeclarations array.
func ToolDefinitions() [][]byte {
	return [][]byte{[]byte(compressDeclJSON), []byte(decompressDeclJSON)}
}

// InjectTools adds the compress and decompress declarations to the request's
// tools array. A declaration already present under the same name is skipped. The
// missing declarations are appended to the first existing tool entry that has a
// functionDeclarations array, so the tools array keeps its shape. When there is
// no such entry, a new {"functionDeclarations":[...]} entry is appended. Existing
// tools and every other field are copied byte for byte. When nothing is added the
// body is returned unchanged, so the call is idempotent.
func InjectTools(body []byte) ([]byte, error) {
	fields, err := parseObject(body)
	if err != nil {
		return nil, fmt.Errorf("google: inject tools: %w", err)
	}
	var items [][]byte
	if raw := lookup(fields, "tools"); raw != nil && !isNull(raw) {
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("google: inject tools: tools: %w", err)
		}
		for _, it := range list {
			items = append(items, []byte(it))
		}
	}
	have := make(map[string]bool)
	for _, it := range items {
		for _, n := range declNames(it) {
			have[n] = true
		}
	}
	var missing [][]byte
	for _, def := range ToolDefinitions() {
		if !have[declName(def)] {
			missing = append(missing, def)
		}
	}
	if len(missing) == 0 {
		return body, nil
	}

	merged := false
	for i, it := range items {
		ifields, err := parseObject(it)
		if err != nil {
			return nil, fmt.Errorf("google: inject tools: tools %d: %w", i, err)
		}
		raw := lookup(ifields, "functionDeclarations")
		if raw == nil || isNull(raw) {
			continue
		}
		var decls []json.RawMessage
		if err := json.Unmarshal(raw, &decls); err != nil {
			return nil, fmt.Errorf("google: inject tools: tools %d: functionDeclarations: %w", i, err)
		}
		all := make([][]byte, 0, len(decls)+len(missing))
		for _, d := range decls {
			all = append(all, []byte(d))
		}
		all = append(all, missing...)
		items[i] = buildObject(setField(ifields, "functionDeclarations", joinArray(all)))
		merged = true
		break
	}
	if !merged {
		items = append(items, buildObject([]field{{key: "functionDeclarations", raw: joinArray(missing)}}))
	}
	return buildObject(setField(fields, "tools", joinArray(items))), nil
}

// HasTool reports whether the request body declares a function with the given
// name. Malformed bodies report false.
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
		for _, n := range declNames(t) {
			if n == name {
				return true
			}
		}
	}
	return false
}

// declNames lists the function names declared by one tool entry.
func declNames(tool []byte) []string {
	var h struct {
		FunctionDeclarations []struct {
			Name string `json:"name"`
		} `json:"functionDeclarations"`
	}
	if json.Unmarshal(tool, &h) != nil {
		return nil
	}
	names := make([]string, 0, len(h.FunctionDeclarations))
	for _, d := range h.FunctionDeclarations {
		names = append(names, d.Name)
	}
	return names
}

// declName reads the name of one function declaration.
func declName(def []byte) string {
	var h struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(def, &h) != nil {
		return ""
	}
	return h.Name
}
