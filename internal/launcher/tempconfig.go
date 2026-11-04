package launcher

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// MergeConfig returns src with key set to value, in the given format. src is
// only read: the caller passes a copy of the user's file and writes the result
// to a temporary file. Comments and unrelated lines of env and ini files are
// kept as they are. json and yaml are re-marshalled, so their layout changes.
func MergeConfig(format string, src []byte, key, value string) ([]byte, error) {
	switch format {
	case "json":
		return mergeJSON(src, key, value)
	case "yaml":
		return mergeYAML(src, key, value)
	case "env":
		return mergeEnv(src, key, value), nil
	case "ini":
		return mergeINI(src, key, value), nil
	}
	return nil, fmt.Errorf("launcher: merge_from is not supported for format %q", format)
}

func mergeJSON(src []byte, key, value string) ([]byte, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(src)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(src))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			return nil, fmt.Errorf("launcher: merge_from json: %w", err)
		}
	}
	if doc == nil {
		doc = map[string]any{}
	}
	if err := setPath(doc, key, value); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("launcher: merge_from json: %w", err)
	}
	return append(out, '\n'), nil
}

func mergeYAML(src []byte, key, value string) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf("launcher: merge_from yaml: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	if err := setPath(doc, key, value); err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("launcher: merge_from yaml: %w", err)
	}
	return out, nil
}

// setPath sets the dotted key in doc, creating intermediate objects.
func setPath(doc map[string]any, key, value string) error {
	segs := strings.Split(key, ".")
	cur := doc
	for _, s := range segs[:len(segs)-1] {
		next, ok := cur[s]
		if !ok {
			nm := map[string]any{}
			cur[s] = nm
			cur = nm
			continue
		}
		nm, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("launcher: merge_from: %q is not an object", s)
		}
		cur = nm
	}
	cur[segs[len(segs)-1]] = value
	return nil
}

// mergeEnv replaces every KEY= line and appends one when none exists.
func mergeEnv(src []byte, key, value string) []byte {
	line := key + "=" + value
	lines := splitLines(src)
	found := false
	for i, l := range lines {
		t := strings.TrimPrefix(strings.TrimSpace(l), "export ")
		if strings.HasPrefix(t, key+"=") {
			lines[i] = line
			found = true
		}
	}
	if !found {
		lines = append(lines, line)
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// mergeINI sets key ("section.name", or "name" for the global section). The
// first matching line is replaced in place; otherwise the line goes at the top
// of the section, or a new section is appended.
func mergeINI(src []byte, key, value string) []byte {
	section, name := "", key
	if i := strings.Index(key, "."); i >= 0 {
		section, name = key[:i], key[i+1:]
	}
	line := name + " = " + value
	headerAt := -1
	if section == "" {
		headerAt = 0
	}
	cur := ""
	replaced := false
	var out []string
	for _, l := range splitLines(src) {
		t := strings.TrimSpace(l)
		if h, ok := iniHeader(t); ok {
			cur = h
			out = append(out, l)
			if cur == section && headerAt < 0 {
				headerAt = len(out)
			}
			continue
		}
		if cur == section && iniKeyMatch(t, name) {
			out = append(out, line)
			replaced = true
			continue
		}
		out = append(out, l)
	}
	switch {
	case replaced:
	case section == "":
		out = slices.Insert(out, 0, line)
	case headerAt >= 0:
		out = slices.Insert(out, headerAt, line)
	default:
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, "["+section+"]", line)
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

func iniHeader(t string) (string, bool) {
	if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
		return strings.TrimSpace(t[1 : len(t)-1]), true
	}
	return "", false
}

func iniKeyMatch(t, name string) bool {
	if !strings.HasPrefix(t, name) {
		return false
	}
	rest := strings.TrimLeft(t[len(name):], " \t")
	return strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, ":")
}

// splitLines splits text into lines without the final newline.
func splitLines(src []byte) []string {
	if len(src) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(src), "\n"), "\n")
}
