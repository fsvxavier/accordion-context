package obs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// Two Metrics values in one process must not share or re-register collectors.
func TestNewMetricsIsPerInstance(t *testing.T) {
	a, b := NewMetrics(), NewMetrics()
	a.Requests.WithLabelValues("anthropic", OutcomeCompact).Inc()
	if got := counterOf(t, b.Registry, "acd_ctx_requests_total"); got != 0 {
		t.Fatalf("second registry sees the first one's count: %v", got)
	}
	if got := counterOf(t, a.Registry, "acd_ctx_requests_total"); got != 1 {
		t.Fatalf("count = %v, want 1", got)
	}
}

func TestMetricsExposeClosedLabelSeries(t *testing.T) {
	m := NewMetrics()
	var buf bytes.Buffer
	rec := &recorder{buf: &buf}
	m.Handler().ServeHTTP(rec, newGet())
	out := buf.String()
	for _, want := range []string{
		`acd_ctx_compress_total{outcome="accepted",tier="1"} 0`,
		`acd_ctx_recall_hint_total{accepted="true",signal="block"} 0`,
		"acd_ctx_token_calib_factor",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("exposition lacks %q", want)
		}
	}
}

func TestReadTraceRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"no session":     `{"t":"msg","seq":1}` + "\n",
		"unknown record": `{"t":"session","version":1,"id":"s"}` + "\n" + `{"t":"nope"}` + "\n",
		"bad version":    `{"t":"session","version":9,"id":"s"}` + "\n",
		"second session": `{"t":"session","version":1,"id":"s"}` + "\n" + `{"t":"session","version":1,"id":"s"}` + "\n",
		"not json":       "{\n",
	}
	for name, in := range cases {
		if _, err := ReadTrace(strings.NewReader(in)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestTraceRoundTripKeepsText(t *testing.T) {
	v := "hello"
	tr := Trace{
		Session:  SessionLine{T: TagSession, Version: TraceVersion, ID: "s_1"},
		Messages: []MessageLine{{T: TagMessage, Seq: 1, Ref: "m00001", Role: "user", Kind: "text", Tokens: 2, Text: body(v, false)}},
		Events:   []EventLine{{T: TagEvent, Seq: 1, Type: "turn", Payload: json.RawMessage(`{"tokens":2}`)}},
	}
	var buf bytes.Buffer
	if err := WriteTrace(&buf, tr); err != nil {
		t.Fatal(err)
	}
	back, err := ReadTrace(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if back.Messages[0].Text.Value == nil || *back.Messages[0].Text.Value != v {
		t.Fatalf("text lost: %+v", back.Messages[0].Text)
	}
	if len(back.Events) != 1 || back.Events[0].Type != "turn" {
		t.Fatalf("events = %+v", back.Events)
	}
}

type recorder struct {
	buf    *bytes.Buffer
	header map[string][]string
	code   int
}

func (r *recorder) Header() http.Header {
	if r.header == nil {
		r.header = http.Header{}
	}
	return r.header
}
func (r *recorder) Write(p []byte) (int, error) { return r.buf.Write(p) }
func (r *recorder) WriteHeader(code int)        { r.code = code }

func newGet() *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	return req
}

// counterOf sums the samples of a counter family in reg.
func counterOf(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	fams, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for _, f := range fams {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			sum += m.GetCounter().GetValue()
		}
	}
	return sum
}
