package google

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestInjectToolsIntoEmptyBody(t *testing.T) {
	out, err := InjectTools([]byte(`{"contents":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !HasTool(out, ToolCompress) || !HasTool(out, ToolDecompress) {
		t.Fatalf("tools not injected: %s", out)
	}
	if !json.Valid(out) || !bytes.Contains(out, []byte(`"contents":[]`)) {
		t.Errorf("body changed: %s", out)
	}
}

func TestInjectToolsIdempotent(t *testing.T) {
	once, err := InjectTools([]byte(`{"contents":[]}`))
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

func TestInjectToolsMergesIntoExistingEntry(t *testing.T) {
	body := `{"tools":[{"googleSearch":{}},{"functionDeclarations":[{"name":"ls","parameters":{"type":"OBJECT"}}],"x":1}],"contents":[]}`
	out, err := InjectTools([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Tools []struct {
			GoogleSearch         json.RawMessage `json:"googleSearch"`
			FunctionDeclarations []struct {
				Name string `json:"name"`
			} `json:"functionDeclarations"`
			X int `json:"x"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Tools) != 2 {
		t.Fatalf("want 2 tool entries, got %d: %s", len(doc.Tools), out)
	}
	names := []string{}
	for _, d := range doc.Tools[1].FunctionDeclarations {
		names = append(names, d.Name)
	}
	if strings.Join(names, ",") != "ls,compress,decompress" || doc.Tools[1].X != 1 {
		t.Errorf("merge wrong: %v x=%d", names, doc.Tools[1].X)
	}
	if !strings.Contains(string(out), `{"googleSearch":{}}`) {
		t.Errorf("existing tool changed: %s", out)
	}
}

func TestInjectToolsAddsOnlyMissing(t *testing.T) {
	body := `{"tools":[{"functionDeclarations":[{"name":"compress","parameters":{}}]}]}`
	out, err := InjectTools([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(out), `"name":"compress"`) != 1 {
		t.Errorf("compress duplicated: %s", out)
	}
	if !HasTool(out, ToolDecompress) {
		t.Errorf("decompress not added: %s", out)
	}
}

func TestInjectToolsNoChangeReturnsSameBody(t *testing.T) {
	body := []byte(`{"tools":[{"functionDeclarations":[{"name":"compress"},{"name":"decompress"}]}]}`)
	out, err := InjectTools(body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, body) {
		t.Errorf("body changed: %s", out)
	}
}

func TestInjectToolsErrors(t *testing.T) {
	for name, body := range map[string]string{
		"not object": `[]`,
		"tools str":  `{"tools":"x"}`,
		"decls bad":  `{"tools":[{"functionDeclarations":"x"}]}`,
	} {
		if _, err := InjectTools([]byte(body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if HasTool([]byte(`{bad`), ToolCompress) {
		t.Error("malformed body must report false")
	}
}

func TestToolDefinitionsAreDeclarations(t *testing.T) {
	for _, def := range ToolDefinitions() {
		if !json.Valid(def) || !strings.Contains(string(def), `"parameters":{"type":"OBJECT"`) {
			t.Errorf("bad declaration: %s", def)
		}
	}
}
