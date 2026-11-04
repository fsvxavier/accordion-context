// Package obs holds the observability surface of the proxy (spec §20): the
// Prometheus metrics, the status report of a session and the trace export that
// feeds the replay (spec §17.2 item 4). Everything is built per Metrics value
// with a private registry, so several proxies can live in one process.
package obs

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Label values. They are a closed set, so cardinality stays bounded: no label
// carries a session, project or message identifier.
const (
	OutcomeAccepted = "accepted"
	OutcomeRejected = "rejected"
	OutcomeRefused  = "refused"
	OutcomeFailOpen = "failopen"
	OutcomeCompact  = "compacted"

	TierNone = "none"
)

// Metrics is one set of collectors and the registry that holds them.
type Metrics struct {
	Registry *prometheus.Registry

	// ContextTokens is the calibrated size of each request's window, per protocol.
	ContextTokens *prometheus.HistogramVec // acd_ctx_context_tokens{protocol}
	// CompressTotal counts compress calls by the highest tier they produced
	// (none for refusals) and by outcome.
	CompressTotal *prometheus.CounterVec // acd_ctx_compress_total{tier,outcome}
	// CompressRatio is src/sum of each accepted block.
	CompressRatio prometheus.Histogram // acd_ctx_compress_ratio
	// CacheReadRatio is cached/input of each upstream call that reported usage.
	CacheReadRatio prometheus.Histogram // acd_ctx_cache_read_ratio
	// FoldCacheCost is the estimated cache cost (kind=cost) and benefit
	// (kind=benefit) of the best candidate fold, in tokens (spec §9.1).
	FoldCacheCost *prometheus.HistogramVec // acd_ctx_fold_cache_cost_tokens{kind}
	// RecallHints counts recall hints by signal, and whether the model acted on
	// them (a decompress of the hinted block before the next turn).
	RecallHints *prometheus.CounterVec // acd_ctx_recall_hint_total{signal,accepted}
	// CalibFactor is the last token calibration factor (spec §15).
	CalibFactor prometheus.Gauge // acd_ctx_token_calib_factor
	// Requests counts compaction requests by protocol and outcome.
	Requests *prometheus.CounterVec // acd_ctx_requests_total{protocol,outcome}
	// RequestSeconds is the time of a whole compaction request, tool loop included.
	RequestSeconds *prometheus.HistogramVec // acd_ctx_request_duration_seconds{protocol}
	// UpstreamSeconds is the latency of each upstream call.
	UpstreamSeconds prometheus.Histogram // acd_ctx_upstream_duration_seconds
	// ToolCalls counts the internal tool calls the proxy ran, by tool and outcome.
	ToolCalls *prometheus.CounterVec // acd_ctx_tool_calls_total{tool,outcome}
}

// NewMetrics builds the collectors and registers them in a new private registry.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		Registry: reg,
		ContextTokens: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "acd_ctx_context_tokens",
			Help:    "Calibrated size of the window sent upstream, in tokens.",
			Buckets: prometheus.ExponentialBuckets(1000, 2, 12),
		}, []string{"protocol"}),
		CompressTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "acd_ctx_compress_total",
			Help: "Compress calls by highest tier produced and outcome.",
		}, []string{"tier", "outcome"}),
		CompressRatio: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "acd_ctx_compress_ratio",
			Help:    "Source tokens divided by summary tokens, per accepted block.",
			Buckets: prometheus.ExponentialBuckets(1, 1.5, 12),
		}),
		CacheReadRatio: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "acd_ctx_cache_read_ratio",
			Help:    "Cached input tokens divided by input tokens, per upstream call.",
			Buckets: prometheus.LinearBuckets(0, 0.1, 11),
		}),
		FoldCacheCost: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "acd_ctx_fold_cache_cost_tokens",
			Help:    "Estimated cache cost and benefit of the best candidate fold, in tokens.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 18),
		}, []string{"kind"}),
		RecallHints: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "acd_ctx_recall_hint_total",
			Help: "Recall hints by signal and whether the model acted on them.",
		}, []string{"signal", "accepted"}),
		CalibFactor: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "acd_ctx_token_calib_factor",
			Help: "Last token calibration factor (reported over estimated).",
		}),
		Requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "acd_ctx_requests_total",
			Help: "Compaction requests by protocol and outcome.",
		}, []string{"protocol", "outcome"}),
		RequestSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "acd_ctx_request_duration_seconds",
			Help:    "Duration of a compaction request, tool loop included.",
			Buckets: prometheus.DefBuckets,
		}, []string{"protocol"}),
		UpstreamSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "acd_ctx_upstream_duration_seconds",
			Help:    "Latency of an upstream call.",
			Buckets: prometheus.DefBuckets,
		}),
		ToolCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "acd_ctx_tool_calls_total",
			Help: "Internal tool calls run by the proxy, by tool and outcome.",
		}, []string{"tool", "outcome"}),
	}
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.ContextTokens, m.CompressTotal, m.CompressRatio, m.CacheReadRatio,
		m.FoldCacheCost, m.RecallHints, m.CalibFactor, m.Requests,
		m.RequestSeconds, m.UpstreamSeconds, m.ToolCalls,
	)
	m.prime()
	return m
}

// prime creates the series of the closed label sets, so each family is exposed
// from the start, with zero counts, rather than appearing at its first event.
func (m *Metrics) prime() {
	for _, tier := range []string{"1", "2", "3", TierNone} {
		for _, outcome := range []string{OutcomeAccepted, OutcomeRejected, OutcomeRefused} {
			m.CompressTotal.WithLabelValues(tier, outcome)
		}
	}
	for _, signal := range []string{"reread", "ref", "block"} {
		for _, accepted := range []string{"true", "false"} {
			m.RecallHints.WithLabelValues(signal, accepted)
		}
	}
	for _, kind := range []string{"cost", "benefit"} {
		m.FoldCacheCost.WithLabelValues(kind)
	}
	for _, tool := range []string{"compress", "decompress", "search_context", "retrieve"} {
		for _, outcome := range []string{OutcomeAccepted, OutcomeRejected} {
			m.ToolCalls.WithLabelValues(tool, outcome)
		}
	}
}

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{Registry: m.Registry})
}

// Bool renders a boolean as the accepted label value.
func Bool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
