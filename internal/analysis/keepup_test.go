package analysis

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// keepUpTestWritten is the per-window audio volume used by the sequence tests;
// loss is expressed as a fraction of it.
const keepUpTestWritten = 1_000_000

const (
	keepUpTestHealthy = 0.0
	keepUpTestPollGap = 0.017 // average overlap-0 poll loss (see keepUpLostFractionThreshold)
	keepUpTestLossy   = 0.5
)

func newKeepUpTestBuffer(t *testing.T) *buffer.AnalysisBuffer {
	t.Helper()
	ab, err := buffer.NewAnalysisBuffer(16, 0, 8, "keepup-test", logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC), nil)
	require.NoError(t, err)
	return ab
}

// keepUpDriver feeds cumulative stats into a keepUpState, one window per call.
type keepUpDriver struct {
	t     *testing.T
	state keepUpState
	buf   *buffer.AnalysisBuffer
	stats buffer.AnalysisBufferStats
	now   time.Time
}

func newKeepUpDriver(t *testing.T) *keepUpDriver {
	t.Helper()
	d := &keepUpDriver{t: t, buf: newKeepUpTestBuffer(t), now: time.Unix(1_700_000_000, 0)}
	// The first observation only rebases.
	_, closed, tr := d.state.observe(d.buf, d.stats, d.now)
	require.False(t, closed)
	require.Equal(t, keepUpNone, tr)
	return d
}

// window advances one full window with the given lost fraction of written bytes
// (written == 0 simulates a source with no audio) and returns the outcome.
func (d *keepUpDriver) window(written int64, lostFraction float64) (keepUpWindowResult, keepUpTransition) {
	d.t.Helper()
	d.stats.WrittenBytes += written
	d.stats.LostBytes += int64(float64(written) * lostFraction)
	d.now = d.now.Add(keepUpWindow)
	res, closed, tr := d.state.observe(d.buf, d.stats, d.now)
	require.True(d.t, closed, "a full window must close")
	return res, tr
}

func (d *keepUpDriver) lossy(n int) []keepUpTransition {
	d.t.Helper()
	out := make([]keepUpTransition, 0, n)
	for range n {
		_, tr := d.window(keepUpTestWritten, keepUpTestLossy)
		out = append(out, tr)
	}
	return out
}

func (d *keepUpDriver) clean(n int) []keepUpTransition {
	d.t.Helper()
	out := make([]keepUpTransition, 0, n)
	for range n {
		_, tr := d.window(keepUpTestWritten, keepUpTestHealthy)
		out = append(out, tr)
	}
	return out
}

func TestKeepUpState_CleanWindowsNeverTransition(t *testing.T) {
	t.Parallel()
	d := newKeepUpDriver(t)
	for _, tr := range d.clean(20) {
		assert.Equal(t, keepUpNone, tr)
	}
}

// TestKeepUpState_PollGapLossNeverWarns pins that the overlap-0 poll loss of a
// healthy pipeline stays below the threshold.
func TestKeepUpState_PollGapLossNeverWarns(t *testing.T) {
	t.Parallel()
	d := newKeepUpDriver(t)
	for range 20 {
		res, tr := d.window(keepUpTestWritten, keepUpTestPollGap)
		assert.Equal(t, keepUpNone, tr)
		assert.False(t, res.lossy)
	}
}

func TestKeepUpState_ShortStallDoesNotWarn(t *testing.T) {
	t.Parallel()
	d := newKeepUpDriver(t)
	for _, tr := range d.lossy(2) {
		assert.Equal(t, keepUpNone, tr)
	}
	for _, tr := range d.clean(10) {
		assert.Equal(t, keepUpNone, tr)
	}
}

func TestKeepUpState_EpisodeStartsOnceRecoversOnceAndStartsAgain(t *testing.T) {
	t.Parallel()
	d := newKeepUpDriver(t)

	assert.Equal(t, []keepUpTransition{keepUpNone, keepUpNone, keepUpStarted}, d.lossy(3))
	assert.Equal(t, []keepUpTransition{keepUpNone}, d.lossy(1), "no repeat while the episode persists")
	assert.Equal(t, []keepUpTransition{keepUpNone, keepUpRecovered}, d.clean(2))
	assert.Equal(t, []keepUpTransition{keepUpNone, keepUpNone, keepUpStarted}, d.lossy(3), "a new episode warns again")
}

func TestKeepUpState_OscillatingOverloadStillWarns(t *testing.T) {
	t.Parallel()
	d := newKeepUpDriver(t)

	got := make([]keepUpTransition, 0, 6)
	for range 2 {
		got = append(got, d.lossy(2)...)
		got = append(got, d.clean(1)...)
	}
	// lossy lossy clean lossy lossy clean: the 3rd lossy of the last 5 is window 4.
	assert.Equal(t, keepUpStarted, got[3])
	for i, tr := range got {
		if i != 3 {
			assert.Equal(t, keepUpNone, tr, "window %d", i)
		}
	}
}

func TestKeepUpState_NoAudioWindowsAreNotEvidence(t *testing.T) {
	t.Parallel()
	d := newKeepUpDriver(t)

	_, tr := d.window(keepUpTestWritten, keepUpTestLossy)
	assert.Equal(t, keepUpNone, tr)
	res, tr := d.window(0, 0)
	assert.False(t, res.hasAudio)
	assert.Equal(t, keepUpNone, tr)
	assert.Equal(t, []keepUpTransition{keepUpNone, keepUpStarted}, d.lossy(2), "started on the 4th step")

	// While lagging, silent windows never recover the episode.
	for range 5 {
		res, tr = d.window(0, 0)
		assert.False(t, res.hasAudio)
		assert.Equal(t, keepUpNone, tr)
	}
	assert.Equal(t, []keepUpTransition{keepUpNone, keepUpRecovered}, d.clean(2))
}

func TestKeepUpState_BufferSwapRebases(t *testing.T) {
	t.Parallel()
	d := newKeepUpDriver(t)
	d.lossy(1)

	// A reallocated buffer starts counting from zero, but its counters can also
	// already exceed the old base. Either way the swap rebases, so the new
	// buffer's history is not billed to the old window.
	d.buf = newKeepUpTestBuffer(t)
	d.stats.WrittenBytes += 10 * keepUpTestWritten
	d.stats.LostBytes += 10 * keepUpTestWritten
	d.now = d.now.Add(keepUpWindow / 2)
	res, closed, tr := d.state.observe(d.buf, d.stats, d.now)
	assert.False(t, closed)
	assert.Equal(t, keepUpNone, tr)
	assert.Zero(t, res)

	// The rebased window runs a full keepUpWindow from the swap and judges only
	// what the new buffer wrote since.
	d.now = d.now.Add(keepUpWindow - time.Second)
	_, closed, _ = d.state.observe(d.buf, d.stats, d.now)
	assert.False(t, closed)
	d.stats.WrittenBytes += keepUpTestWritten
	d.now = d.now.Add(time.Second)
	res, closed, _ = d.state.observe(d.buf, d.stats, d.now)
	require.True(t, closed)
	assert.Equal(t, int64(keepUpTestWritten), res.written)
	assert.Zero(t, res.lost)
}

func TestKeepUpState_SwapWhileLaggingThenCleanRecovers(t *testing.T) {
	t.Parallel()
	d := newKeepUpDriver(t)
	assert.Equal(t, keepUpStarted, d.lossy(3)[2])

	// The new buffer's counters already exceed the old base, so only the pointer
	// check can tell it is a different buffer.
	d.buf = newKeepUpTestBuffer(t)
	d.stats.WrittenBytes += 10 * keepUpTestWritten
	d.stats.LostBytes += 10 * keepUpTestWritten
	d.now = d.now.Add(time.Second)
	_, closed, tr := d.state.observe(d.buf, d.stats, d.now)
	require.False(t, closed)
	require.Equal(t, keepUpNone, tr)

	assert.Equal(t, []keepUpTransition{keepUpNone, keepUpRecovered}, d.clean(2))
}

func TestKeepUpState_WindowsCloserThanKeepUpWindowNeverClose(t *testing.T) {
	t.Parallel()
	d := newKeepUpDriver(t)
	const steps = 10
	for i := 1; i < steps; i++ {
		d.stats.WrittenBytes += keepUpTestWritten
		d.stats.LostBytes += keepUpTestWritten
		d.now = d.now.Add(keepUpWindow / steps)
		_, closed, tr := d.state.observe(d.buf, d.stats, d.now)
		assert.False(t, closed, "step %d", i)
		assert.Equal(t, keepUpNone, tr)
	}
}

// TestAnalysisBuffer_ZeroOverlapPollGapIsBelowKeepUpThreshold drives the real
// ring with capacity == readSize (overlap 0), 20 ms-ish writes and a 100 ms poll,
// and asserts the resulting loss stays under the keep-up threshold. It pins the
// assumption behind keepUpLostFractionThreshold.
func TestAnalysisBuffer_ZeroOverlapPollGapIsBelowKeepUpThreshold(t *testing.T) {
	t.Parallel()

	const (
		capacity     = 288_000 // 3 s of 48 kHz 16-bit mono
		chunk        = 1_900   // just under 20 ms; deliberately not a divisor of capacity
		chunksPerMin = 3_000
		pollEvery    = 5 // 100 ms
	)
	ab, err := buffer.NewAnalysisBuffer(capacity, 0, capacity, "poll-gap", logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC), nil)
	require.NoError(t, err)

	payload := make([]byte, chunk)
	for i := 1; i <= chunksPerMin; i++ {
		require.NoError(t, ab.Write(payload))
		if i%pollEvery == 0 {
			_, release, readErr := ab.Read()
			require.NoError(t, readErr)
			release()
		}
	}

	st := ab.Stats()
	require.Positive(t, st.WindowsRead)
	fraction := float64(st.LostBytes) / float64(st.WrittenBytes)
	assert.Positive(t, st.LostBytes, "the poll gap loses some audio at overlap 0")
	assert.Less(t, fraction, keepUpLostFractionThreshold)
}

// TestProcessMonitorTick_WarnsOnceWhenAnalysisFallsBehind drives observeKeepUp
// with injected times and checks the log lines of one episode.
func TestProcessMonitorTick_WarnsOnceWhenAnalysisFallsBehind(t *testing.T) {
	t.Parallel()

	const (
		sourceID = "keepup-src"
		modelID  = classifier.RegistryIDBirdNETV24
		readSize = 480
	)
	mgr := buffer.NewManager(logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC))
	require.NoError(t, mgr.AllocateAnalysis(sourceID, modelID, readSize, 0, readSize))
	ab, err := mgr.AnalysisBuffer(sourceID, modelID)
	require.NoError(t, err)

	var logBuf bytes.Buffer
	bm := &BufferManager{
		bn:        cadenceBackend{effective: 1800 * time.Millisecond},
		bufferMgr: mgr,
		logger:    logger.NewSlogLogger(&logBuf, logger.LogLevelDebug, time.UTC),
	}
	cfg := &monitorConfig{sourceID: sourceID, modelID: modelID, readSize: readSize}
	var state keepUpState

	now := time.Unix(1_700_000_000, 0)
	bm.observeKeepUp(cfg, ab, &state, now)

	overfill := func() {
		require.NoError(t, ab.Write(make([]byte, readSize)))
		require.NoError(t, ab.Write(make([]byte, readSize))) // second write discards the first
		now = now.Add(keepUpWindow)
		bm.observeKeepUp(cfg, ab, &state, now)
	}
	healthy := func() {
		_, release, readErr := ab.Read()
		require.NoError(t, readErr)
		release()
		require.NoError(t, ab.Write(make([]byte, readSize/100)))
		now = now.Add(keepUpWindow)
		bm.observeKeepUp(cfg, ab, &state, now)
	}

	for range 5 {
		overfill()
	}
	assert.Equal(t, 1, strings.Count(logBuf.String(), "analysis is not keeping up with incoming audio"))
	assert.NotContains(t, logBuf.String(), "analysis caught up")

	for range 3 {
		healthy()
	}
	assert.Equal(t, 1, strings.Count(logBuf.String(), "analysis is not keeping up with incoming audio"))
	assert.Equal(t, 1, strings.Count(logBuf.String(), "analysis caught up with incoming audio"))
}

// TestProcessMonitorTick_EvaluatesKeepUpEachTick verifies the monitor tick feeds
// the buffer to the keep-up evaluator before it reads the window.
func TestProcessMonitorTick_EvaluatesKeepUpEachTick(t *testing.T) {
	t.Parallel()

	const (
		sourceID = "keepup-tick-src"
		modelID  = classifier.RegistryIDBirdNETV24
		readSize = 480
	)
	mgr := buffer.NewManager(logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC))
	require.NoError(t, mgr.AllocateAnalysis(sourceID, modelID, readSize, 0, readSize))
	ab, err := mgr.AnalysisBuffer(sourceID, modelID)
	require.NoError(t, err)

	bm := &BufferManager{
		bn:        &scriptedModelState{loaded: []bool{true}, active: false},
		bufferMgr: mgr,
		logger:    logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC),
	}
	cfg := &monitorConfig{sourceID: sourceID, modelID: modelID, readSize: readSize}
	state := &monitorTickState{}
	require.Nil(t, state.keepUp.buf)

	require.True(t, bm.processMonitorTick(make(chan struct{}), cfg, readSize, 0, state, 1))
	assert.Same(t, ab, state.keepUp.buf, "the tick must hand the buffer to the keep-up evaluator")
}
