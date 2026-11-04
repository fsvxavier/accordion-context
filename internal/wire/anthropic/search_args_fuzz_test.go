package anthropic

import "testing"

// FuzzSearchRetrieveArgs checks that the recall argument parsers never panic.
func FuzzSearchRetrieveArgs(f *testing.F) {
	f.Add([]byte(`{"query":"x","limit":2,"scope":"project"}`))
	f.Add([]byte(`{"ref":"m00042"}`))
	f.Add([]byte("```json\n{\"blockId\":\"b12\",\"range\":\"m00340..m00412\"}\n```"))
	f.Fuzz(func(t *testing.T, in []byte) {
		_, _ = ParseSearchArgs(in)
		_, _ = ParseRetrieveArgs(in)
	})
}
