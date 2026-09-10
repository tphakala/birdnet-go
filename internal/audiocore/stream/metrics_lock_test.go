package stream

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/go-audio-stream/supervisor"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// reentrantMetrics is a StreamMetrics stub that probes, from inside each emission
// callback, whether the stream's own mutex is free (via TryLock). onState and
// recordError must release s.mu before emitting metrics (Forgejo #1646); if they
// did not, TryLock here would fail because a sync.Mutex is not reentrant. TryLock
// is used rather than a blocking Lock so a regression fails the assertion instead
// of deadlocking the test.
type reentrantMetrics struct {
	s           *stream
	healthCalls atomic.Int32
	errorCalls  atomic.Int32
	lockWasFree atomic.Bool
	lastHealthy atomic.Bool
}

func (m *reentrantMetrics) probeLockFree() {
	if m.s.mu.TryLock() {
		m.lockWasFree.Store(true)
		m.s.mu.Unlock()
	}
}

func (m *reentrantMetrics) IncStreamErrors(string) {
	m.probeLockFree()
	m.errorCalls.Add(1)
}

func (m *reentrantMetrics) SetStreamHealth(_ string, healthy bool) {
	m.probeLockFree()
	m.lastHealthy.Store(healthy)
	m.healthCalls.Add(1)
}

func (m *reentrantMetrics) RecordDataRate(string, float64) {}
func (m *reentrantMetrics) RecordWireRate(string, float64) {}
func (m *reentrantMetrics) SetStreamEngine(string, string) {}
func (m *reentrantMetrics) DeleteStream(string)            {}

// TestOnState_EmitsMetricsOutsideLock is a regression test for the AB-BA hazard
// fixed in Forgejo #1646: onState must release s.mu before emitting stream
// metrics, matching onDeliver and snapshot. A metrics implementation that reads
// stream health back while holding its own lock would otherwise risk a deadlock.
// StateConnected is used because that transition drives onState without touching
// the pipeline or supervisor.
func TestOnState_EmitsMetricsOutsideLock(t *testing.T) {
	t.Parallel()

	s := &stream{
		spec: &audiocore.StreamSpec{SourceID: "src1"},
		opts: &Options{},
	}
	m := &reentrantMetrics{s: s}
	s.opts.Metrics = m

	s.onState(supervisor.StateChange{State: supervisor.StateConnected})

	assert.Equal(t, int32(1), m.healthCalls.Load(), "SetStreamHealth should be emitted once")
	assert.True(t, m.lockWasFree.Load(), "s.mu must be released before metrics are emitted (AB-BA guard)")
	assert.True(t, m.lastHealthy.Load(), "connected stream should report healthy=true")
}

// TestRecordError_ReportsRecorded locks the recordError contract used by onState
// to decide whether to emit IncStreamErrors after unlocking: it returns false for
// a nil error and true (appending one history entry) when it records a classified
// error.
func TestRecordError_ReportsRecorded(t *testing.T) {
	t.Parallel()

	s := &stream{
		spec:       &audiocore.StreamSpec{SourceID: "src1"},
		opts:       &Options{},
		targetHost: "cam.local",
		targetPort: 554,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	assert.False(t, s.recordError(nil), "nil error records nothing")
	assert.Empty(t, s.errorHistory, "nil error must not append history")

	require.True(t, s.recordError(fmt.Errorf("something broke")), "a classified error is recorded")
	assert.Len(t, s.errorHistory, 1, "a recorded error appends exactly one history entry")
}
