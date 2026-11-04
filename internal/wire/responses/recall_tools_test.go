package responses

import (
	"bytes"
	"testing"
)

func TestToolDefinitionOrderIsFixed(t *testing.T) {
	want := []string{ToolCompress, ToolDecompress, ToolSearchContext, ToolRetrieve}
	var got []string
	for _, def := range ToolDefinitions() {
		got = append(got, toolName(def))
	}
	if !equalStrings(got, want) {
		t.Fatalf("definition order = %v, want %v", got, want)
	}
	once, err := InjectTools([]byte(`{"model":"m","input":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	again, err := InjectTools(once)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, again) {
		t.Fatalf("InjectTools is not byte-stable:\n%s\n%s", once, again)
	}
	if bytes.Index(once, []byte(`"name":"search_context"`)) < bytes.Index(once, []byte(`"name":"decompress"`)) {
		t.Fatalf("injected order is wrong: %s", once)
	}
}

func TestIsInternalToolCoversRecallTools(t *testing.T) {
	for _, n := range []string{ToolCompress, ToolDecompress, ToolSearchContext, ToolRetrieve} {
		if !IsInternalTool(n) {
			t.Errorf("%s is not internal", n)
		}
	}
	if IsInternalTool("bash") {
		t.Error("bash is internal")
	}
}

func TestParseRecallArgs(t *testing.T) {
	s, err := ParseSearchArgs([]byte(`{"query":"lexer","scope":"project","limit":4}`))
	if err != nil || s.Query != "lexer" || s.Scope != "project" || s.Limit != 4 {
		t.Fatalf("search = %+v, %v", s, err)
	}
	r, err := ParseRetrieveArgs([]byte(`{"blockId":"b12","range":"m00340..m00412"}`))
	if err != nil || r.BlockID != "b12" || r.Range != "m00340..m00412" {
		t.Fatalf("retrieve = %+v, %v", r, err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
