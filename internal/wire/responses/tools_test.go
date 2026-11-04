package responses

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func toolNames(t *testing.T, body []byte) []string {
	t.Helper()
	var env struct {
		Tools []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("tools body: %v\n%s", err, body)
	}
	var names []string
	for _, tl := range env.Tools {
		names = append(names, tl.Name)
	}
	return names
}

func TestInjectToolsAddsBothWhenAbsent(t *testing.T) {
	in := []byte(`{"model":"m","input":"x"}`)
	out, err := InjectTools(in)
	if err != nil {
		t.Fatal(err)
	}
	names := toolNames(t, out)
	want := []string{ToolCompress, ToolDecompress, ToolSearchContext, ToolRetrieve}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tool names = %v", names)
	}
	if !HasTool(out, ToolCompress) || !HasTool(out, ToolDecompress) {
		t.Fatal("HasTool false after injection")
	}
	if !strings.HasPrefix(string(out), `{"model":"m","input":"x","tools":[`) {
		t.Errorf("other fields not kept in order: %s", out)
	}
}

func TestInjectToolsIsIdempotent(t *testing.T) {
	in := []byte(`{"model":"m","input":"x"}`)
	once, err := InjectTools(in)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := InjectTools(once)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("second injection changed the body:\n%s\n%s", once, twice)
	}
}

func TestInjectToolsKeepsExistingTools(t *testing.T) {
	in := []byte(`{"input":"x","tools":[{"type":"function","name":"read_file","parameters":{"type":"object"}},{"type":"web_search"},{"type":"function","name":"compress","parameters":{}}]}`)
	out, err := InjectTools(in)
	if err != nil {
		t.Fatal(err)
	}
	// The existing read_file and web_search entries and the user's compress tool
	// are copied byte for byte. Only decompress is added.
	for _, frag := range []string{
		`{"type":"function","name":"read_file","parameters":{"type":"object"}}`,
		`{"type":"web_search"}`,
		`{"type":"function","name":"compress","parameters":{}}`,
	} {
		if !strings.Contains(string(out), frag) {
			t.Errorf("existing tool not kept: missing %s\n%s", frag, out)
		}
	}
	if strings.Count(string(out), `"name":"compress"`) != 1 {
		t.Errorf("compress declared twice:\n%s", out)
	}
	if !HasTool(out, ToolDecompress) {
		t.Errorf("decompress not added:\n%s", out)
	}
}

func TestInjectToolsUnchangedWhenAllPresent(t *testing.T) {
	in, err := InjectTools([]byte(`{"input":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := InjectTools(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(in, out) {
		t.Errorf("body changed although both tools exist")
	}
}

func TestInjectToolsRejectsBadTools(t *testing.T) {
	if _, err := InjectTools([]byte(`{"tools":"nope"}`)); err == nil {
		t.Error("non-array tools accepted")
	}
	if _, err := InjectTools([]byte(`[1]`)); err == nil {
		t.Error("non-object body accepted")
	}
}

func TestHasToolMalformedAndNullTools(t *testing.T) {
	if HasTool([]byte(`{`), ToolCompress) {
		t.Error("malformed body reported a tool")
	}
	if HasTool([]byte(`{"tools":null}`), ToolCompress) {
		t.Error("null tools reported a tool")
	}
}

func TestToolDefinitionsAreFreshCopies(t *testing.T) {
	a := ToolDefinitions()
	a[0][0] = 'X'
	b := ToolDefinitions()
	if b[0][0] != '{' {
		t.Error("ToolDefinitions shares its backing array")
	}
	for _, def := range b {
		if !json.Valid(def) {
			t.Errorf("invalid tool JSON: %s", def)
		}
	}
}
