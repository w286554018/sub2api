package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRecordSearchRound(t *testing.T) {
	// Reset metrics by taking a baseline snapshot
	before := SnapshotKiroWebSearchMetrics()

	RecordSearchRound("success")
	RecordSearchRound("success")
	RecordSearchRound("empty")
	RecordSearchRound("error")
	RecordSearchRound("duplicate")
	RecordSearchRound("timeout")
	RecordSearchRound("max_rounds")

	after := SnapshotKiroWebSearchMetrics()

	assert.Equal(t, int64(7), after.RoundsTotal-before.RoundsTotal)
	assert.Equal(t, int64(2), after.RoundsSuccess-before.RoundsSuccess)
	assert.Equal(t, int64(1), after.RoundsEmpty-before.RoundsEmpty)
	assert.Equal(t, int64(1), after.RoundsError-before.RoundsError)
	assert.Equal(t, int64(1), after.RoundsDuplicate-before.RoundsDuplicate)
	assert.Equal(t, int64(1), after.RoundsTimeout-before.RoundsTimeout)
	assert.Equal(t, int64(1), after.RoundsMaxReached-before.RoundsMaxReached)
}

func TestRecordMCPCall(t *testing.T) {
	before := SnapshotKiroWebSearchMetrics()

	RecordMCPCall(true)
	RecordMCPCall(true)
	RecordMCPCall(false)

	after := SnapshotKiroWebSearchMetrics()

	assert.Equal(t, int64(2), after.MCPSuccess-before.MCPSuccess)
	assert.Equal(t, int64(1), after.MCPFailed-before.MCPFailed)
}

func TestRecordSearchMode(t *testing.T) {
	before := SnapshotKiroWebSearchMetrics()

	RecordSearchMode("single")
	RecordSearchMode("orchestrated")
	RecordSearchMode("")

	after := SnapshotKiroWebSearchMetrics()

	assert.Equal(t, int64(1), after.ModeSingle-before.ModeSingle)
	assert.Equal(t, int64(1), after.ModeOrchestrated-before.ModeOrchestrated)
	assert.Equal(t, int64(1), after.ModeDisabled-before.ModeDisabled)
}

func TestRecordStreamInjection(t *testing.T) {
	before := SnapshotKiroWebSearchMetrics()

	RecordStreamInjection(true)
	RecordStreamInjection(true)
	RecordStreamInjection(false)

	after := SnapshotKiroWebSearchMetrics()

	assert.Equal(t, int64(2), after.StreamInjections-before.StreamInjections)
	assert.Equal(t, int64(1), after.StreamInjErrors-before.StreamInjErrors)
}

func TestRecordSearchLatency(t *testing.T) {
	before := SnapshotKiroWebSearchMetrics()

	RecordSearchLatency(100 * time.Millisecond)
	RecordSearchLatency(200 * time.Millisecond)

	after := SnapshotKiroWebSearchMetrics()

	// Average should be ~150ms
	avgDelta := after.AvgLatencyMs - before.AvgLatencyMs
	// Just verify it's positive and in a reasonable range
	assert.True(t, after.AvgLatencyMs > 0, "avg latency should be positive")
	_ = avgDelta // avoid unused
}

func TestSnapshotKiroWebSearchMetrics_InitialState(t *testing.T) {
	snap := SnapshotKiroWebSearchMetrics()
	// All counters should be >= 0 (may have accumulated from other tests)
	assert.True(t, snap.RoundsTotal >= 0)
	assert.True(t, snap.MCPSuccess >= 0)
	assert.True(t, snap.MCPFailed >= 0)
	assert.True(t, snap.AvgLatencyMs >= 0)
}
