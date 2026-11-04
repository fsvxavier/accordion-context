package toolargs

import "testing"

func TestParseSearch(t *testing.T) {
	cases := []struct {
		in    string
		query string
		limit int
		scope string
	}{
		{`{"query":"parser bug"}`, "parser bug", 0, ScopeSession},
		{`{"query":"x","limit":"5","scope":"PROJECT"}`, "x", 5, ScopeProject},
		{"```json\n{\"q\":\"alpha\",\"max\":3}\n```", "alpha", 3, ScopeSession},
		{`"{\"query\":\"double\"}"`, "double", 0, ScopeSession},
		{`Here you go: {"text":"alt names","scope":""}`, "alt names", 0, ScopeSession},
	}
	for _, c := range cases {
		got, err := ParseSearch([]byte(c.in))
		if err != nil {
			t.Fatalf("ParseSearch(%s): %v", c.in, err)
		}
		if got.Query != c.query || got.Limit != c.limit || got.Scope != c.scope {
			t.Errorf("ParseSearch(%s) = %+v", c.in, got)
		}
	}
	for _, bad := range []string{``, `{}`, `{"query":"  "}`, `{"query":"x","scope":"everywhere"}`, `{"query":"x","limit":"many"}`, `[1]`} {
		if _, err := ParseSearch([]byte(bad)); err == nil {
			t.Errorf("ParseSearch(%s) succeeded, want an error", bad)
		}
	}
}

func TestParseRetrieve(t *testing.T) {
	got, err := ParseRetrieve([]byte(`{"ref":42}`))
	if err != nil || got.Ref != "42" || got.BlockID != "" {
		t.Fatalf("numeric ref = %+v, %v", got, err)
	}
	got, err = ParseRetrieve([]byte(`{"block_id":"b12","range":"m00340..m00412"}`))
	if err != nil || got.BlockID != "b12" || got.Range != "m00340..m00412" {
		t.Fatalf("block = %+v, %v", got, err)
	}
	for _, bad := range []string{`{}`, `{"ref":"m1","blockId":"b1"}`, `{"range":"m1..m2"}`, `{"ref":true}`} {
		if _, err := ParseRetrieve([]byte(bad)); err == nil {
			t.Errorf("ParseRetrieve(%s) succeeded, want an error", bad)
		}
	}
}

// FuzzParseRecallArgs checks that neither parser panics, and that an accepted
// search has a query and a known scope.
func FuzzParseRecallArgs(f *testing.F) {
	f.Add([]byte(`{"query":"x","limit":2,"scope":"project"}`))
	f.Add([]byte(`{"ref":"m00042"}`))
	f.Add([]byte("```json\n{\"blockId\":\"b1\"}\n```"))
	f.Add([]byte(`"{\"query\":\"double\"}"`))
	f.Fuzz(func(t *testing.T, in []byte) {
		if s, err := ParseSearch(in); err == nil {
			if s.Query == "" || (s.Scope != ScopeSession && s.Scope != ScopeProject) {
				t.Fatalf("accepted search %+v", s)
			}
		}
		if r, err := ParseRetrieve(in); err == nil && (r.Ref == "") == (r.BlockID == "") {
			t.Fatalf("accepted retrieve %+v", r)
		}
	})
}
