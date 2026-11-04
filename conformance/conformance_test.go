// Package conformance replays the language-neutral plugin protocol vectors in
// conformance/vectors against the real plugin handler (spec §19.4, §19.9).
//
// Vector files are JSON objects {"suite", "description", "vectors"}; each vector
// is {"name", "description", "steps"}; each step is {"request", "expect"}:
//
//   - request.method, request.path (relative to /__acd-ctx/plugin/, query included),
//     request.body (JSON value) or request.raw (literal body), request.auth
//     (true = instance token, default; false = no header; "wrong" = bad token).
//   - expect.status, expect.json (subset match: objects match by key, arrays by
//     length and position, "$any" matches any present value), expect.keys.
//   - "{conv}" in any string is replaced by a conversation id unique to the vector.
//
// The Python and Node suites replay the same files, against a fake server or,
// when ACD_CTX_CONFORMANCE_URL and ACD_CTX_CONFORMANCE_TOKEN are set, a live one.
package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fsvxavier/accordion-context/internal/plugin"
	"github.com/fsvxavier/accordion-context/internal/project"
	"github.com/fsvxavier/accordion-context/internal/store"
)

const (
	pluginPrefix = "/__acd-ctx/plugin/"
	testToken    = "0123456789abcdef0123456789abcdef0123"
)

type suite struct {
	Suite       string   `json:"suite"`
	Description string   `json:"description"`
	Vectors     []vector `json:"vectors"`
}

type vector struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Steps       []step `json:"steps"`
}

type step struct {
	Request request `json:"request"`
	Expect  expect  `json:"expect"`
}

type request struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	// Auth is true (default: the instance token), false (none) or "wrong".
	Auth any     `json:"auth"`
	Body any     `json:"body"`
	Raw  *string `json:"raw"`
}

type expect struct {
	Status int      `json:"status"`
	JSON   any      `json:"json"`
	Keys   []string `json:"keys"`
}

var storeSeq atomic.Int64

// newHandler returns a plugin handler over a fresh in-memory store.
func newHandler(t *testing.T) *plugin.Handler {
	t.Helper()
	dsn := fmt.Sprintf("file:conformance-%d?mode=memory&cache=shared", storeSeq.Add(1))
	st, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	proj, err := project.ResolveWith(t.TempDir(), nil, project.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	h, err := plugin.New(plugin.Config{Store: st, Project: proj, Token: testToken,
		ListenHost: "127.0.0.1", Version: "conformance", ExportDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func loadSuites(t *testing.T) []suite {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("vectors", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no vectors found in conformance/vectors")
	}
	sort.Strings(files)
	var out []suite
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var s suite
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&s); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out = append(out, s)
	}
	return out
}

func TestConformance(t *testing.T) {
	for _, s := range loadSuites(t) {
		for _, v := range s.Vectors {
			t.Run(s.Suite+"/"+v.Name, func(t *testing.T) {
				srv := httptest.NewServer(newHandler(t))
				t.Cleanup(srv.Close)
				conv := "conf-" + v.Name
				for i, st := range v.Steps {
					runStep(t, srv.URL, conv, i, st)
				}
			})
		}
	}
}

// substitute replaces {conv} in every string of a JSON value.
func substitute(v any, conv string) any {
	switch x := v.(type) {
	case string:
		return strings.ReplaceAll(x, "{conv}", conv)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = substitute(e, conv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = substitute(e, conv)
		}
		return out
	default:
		return v
	}
}

func runStep(t *testing.T, base, conv string, i int, st step) {
	t.Helper()
	req := st.Request
	path := substitute(req.Path, conv).(string)
	var body io.Reader
	switch {
	case req.Raw != nil:
		body = strings.NewReader(*req.Raw)
	case req.Body != nil:
		b, err := json.Marshal(substitute(req.Body, conv))
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(b)
	case req.Method == http.MethodPost:
		body = bytes.NewReader(nil)
	}
	hr, err := http.NewRequest(req.Method, base+pluginPrefix+path, body)
	if err != nil {
		t.Fatalf("step %d: %v", i, err)
	}
	hr.Header.Set("Content-Type", "application/json")
	switch auth := req.Auth; {
	case auth == nil, auth == true:
		hr.Header.Set("Authorization", "Bearer "+testToken)
	case auth == "wrong":
		hr.Header.Set("Authorization", "Bearer "+strings.Repeat("x", 40))
	case auth == false:
	default:
		t.Fatalf("step %d: unsupported auth %v", i, auth)
	}
	resp, err := http.DefaultClient.Do(hr)
	if err != nil {
		t.Fatalf("step %d: %v", i, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != st.Expect.Status {
		t.Fatalf("step %d %s %s: status %d, want %d: %s", i, req.Method, path, resp.StatusCode, st.Expect.Status, raw)
	}
	if st.Expect.JSON == nil && len(st.Expect.Keys) == 0 {
		return
	}
	var got any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("step %d: response is not JSON: %v: %s", i, err, raw)
	}
	for _, k := range st.Expect.Keys {
		m, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("step %d: keys need a JSON object, got %s", i, raw)
		}
		if _, ok := m[k]; !ok {
			t.Errorf("step %d: missing key %q in %s", i, k, raw)
		}
	}
	if err := match(substitute(st.Expect.JSON, conv), got, "$"); err != nil {
		t.Errorf("step %d %s %s: %v\nresponse: %s", i, req.Method, path, err, raw)
	}
}

// match checks that actual contains expected. Objects are subsets, arrays must
// have the same length, "$any" matches any present value, other scalars must
// be equal.
func match(expected, actual any, at string) error {
	switch e := expected.(type) {
	case string:
		if e == "$any" {
			if actual == nil {
				return fmt.Errorf("%s: value is null or missing", at)
			}
			return nil
		}
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want object, got %T", at, actual)
		}
		for k, ev := range e {
			av, ok := a[k]
			if !ok {
				return fmt.Errorf("%s: missing %q", at, k)
			}
			if err := match(ev, av, at+"."+k); err != nil {
				return err
			}
		}
		return nil
	case []any:
		a, ok := actual.([]any)
		if !ok {
			return fmt.Errorf("%s: want array, got %T", at, actual)
		}
		if len(a) != len(e) {
			return fmt.Errorf("%s: want %d items, got %d", at, len(e), len(a))
		}
		for i := range e {
			if err := match(e[i], a[i], fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
		return nil
	}
	if !reflect.DeepEqual(expected, actual) {
		return fmt.Errorf("%s: want %v, got %v", at, expected, actual)
	}
	return nil
}
