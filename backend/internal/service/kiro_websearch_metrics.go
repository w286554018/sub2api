package service

import (
	"sync/atomic"
	"time"
)

// KiroWebSearchMetrics tracks web search performance and usage.
type KiroWebSearchMetrics struct {
	// Search round counters
	RoundsTotal          atomic.Int64 // Total search rounds executed
	RoundsSuccess        atomic.Int64 // Successful rounds (got results)
	RoundsEmpty          atomic.Int64 // Rounds with zero results
	RoundsError          atomic.Int64 // Rounds that failed
	RoundsDuplicate      atomic.Int64 // Rounds stopped by duplicate query detection
	RoundsTimeout        atomic.Int64 // Rounds stopped by timeout
	RoundsMaxReached     atomic.Int64 // Rounds stopped by max rounds limit

	// MCP call counters
	MCPCallsTotal        atomic.Int64 // Total MCP web_search calls
	MCPCallsSuccess      atomic.Int64 // Successful MCP calls
	MCPCallsFailed       atomic.Int64 // Failed MCP calls

	// Feature flag counters
	ModeSingle           atomic.Int64 // Requests using single mode
	ModeOrchestrated     atomic.Int64 // Requests using orchestrated mode
	ModeDisabled         atomic.Int64 // Requests with search disabled

	// Injection counters
	StreamInjections     atomic.Int64 // Successful stream search block injections
	StreamInjectionErrs  atomic.Int64 // Failed stream injections

	// Latency tracking (cumulative nanoseconds for averaging)
	SearchLatencyNsTotal atomic.Int64 // Total search latency in nanoseconds
	SearchLatencyCount   atomic.Int64 // Number of latency samples
}

// global singleton
var kiroWebSearchMetrics KiroWebSearchMetrics

// RecordSearchRound records the outcome of a search round.
func RecordSearchRound(outcome string) {
	kiroWebSearchMetrics.RoundsTotal.Add(1)
	switch outcome {
	case "success":
		kiroWebSearchMetrics.RoundsSuccess.Add(1)
	case "empty":
		kiroWebSearchMetrics.RoundsEmpty.Add(1)
	case "error":
		kiroWebSearchMetrics.RoundsError.Add(1)
	case "duplicate":
		kiroWebSearchMetrics.RoundsDuplicate.Add(1)
	case "timeout":
		kiroWebSearchMetrics.RoundsTimeout.Add(1)
	case "max_rounds":
		kiroWebSearchMetrics.RoundsMaxReached.Add(1)
	}
}

// RecordMCPCall records the outcome of an MCP web_search call.
func RecordMCPCall(success bool) {
	kiroWebSearchMetrics.MCPCallsTotal.Add(1)
	if success {
		kiroWebSearchMetrics.MCPCallsSuccess.Add(1)
	} else {
		kiroWebSearchMetrics.MCPCallsFailed.Add(1)
	}
}

// RecordSearchMode records which search mode was used.
func RecordSearchMode(mode string) {
	switch mode {
	case "single":
		kiroWebSearchMetrics.ModeSingle.Add(1)
	case "orchestrated":
		kiroWebSearchMetrics.ModeOrchestrated.Add(1)
	default:
		kiroWebSearchMetrics.ModeDisabled.Add(1)
	}
}

// RecordStreamInjection records a stream search block injection.
func RecordStreamInjection(success bool) {
	if success {
		kiroWebSearchMetrics.StreamInjections.Add(1)
	} else {
		kiroWebSearchMetrics.StreamInjectionErrs.Add(1)
	}
}

// RecordSearchLatency records the latency of a search operation.
func RecordSearchLatency(d time.Duration) {
	kiroWebSearchMetrics.SearchLatencyNsTotal.Add(d.Nanoseconds())
	kiroWebSearchMetrics.SearchLatencyCount.Add(1)
}

// KiroWebSearchMetricsSnapshot is a point-in-time snapshot of web search metrics.
type KiroWebSearchMetricsSnapshot struct {
	RoundsTotal      int64   `json:"rounds_total"`
	RoundsSuccess    int64   `json:"rounds_success"`
	RoundsEmpty      int64   `json:"rounds_empty"`
	RoundsError      int64   `json:"rounds_error"`
	RoundsDuplicate  int64   `json:"rounds_duplicate"`
	RoundsTimeout    int64   `json:"rounds_timeout"`
	RoundsMaxReached int64   `json:"rounds_max_reached"`
	MCPSuccess       int64   `json:"mcp_success"`
	MCPFailed        int64   `json:"mcp_failed"`
	ModeSingle       int64   `json:"mode_single"`
	ModeOrchestrated int64   `json:"mode_orchestrated"`
	ModeDisabled     int64   `json:"mode_disabled"`
	StreamInjections int64   `json:"stream_injections"`
	StreamInjErrors  int64   `json:"stream_injection_errors"`
	AvgLatencyMs     float64 `json:"avg_latency_ms"`
}

// SnapshotKiroWebSearchMetrics returns a point-in-time metrics snapshot.
func SnapshotKiroWebSearchMetrics() KiroWebSearchMetricsSnapshot {
	m := &kiroWebSearchMetrics
	snap := KiroWebSearchMetricsSnapshot{
		RoundsTotal:      m.RoundsTotal.Load(),
		RoundsSuccess:    m.RoundsSuccess.Load(),
		RoundsEmpty:      m.RoundsEmpty.Load(),
		RoundsError:      m.RoundsError.Load(),
		RoundsDuplicate:  m.RoundsDuplicate.Load(),
		RoundsTimeout:    m.RoundsTimeout.Load(),
		RoundsMaxReached: m.RoundsMaxReached.Load(),
		MCPSuccess:       m.MCPCallsSuccess.Load(),
		MCPFailed:        m.MCPCallsFailed.Load(),
		ModeSingle:       m.ModeSingle.Load(),
		ModeOrchestrated: m.ModeOrchestrated.Load(),
		ModeDisabled:     m.ModeDisabled.Load(),
		StreamInjections: m.StreamInjections.Load(),
		StreamInjErrors:  m.StreamInjectionErrs.Load(),
	}
	count := m.SearchLatencyCount.Load()
	if count > 0 {
		snap.AvgLatencyMs = float64(m.SearchLatencyNsTotal.Load()) / float64(count) / 1e6
	}
	return snap
}
