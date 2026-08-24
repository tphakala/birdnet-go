package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference/vad"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// fakeDetector is a vad.Detector test double with scripted output.
type fakeDetector struct {
	prob   float32
	err    error
	calls  int
	closed bool
}

func (f *fakeDetector) SpeechProbability(_ []byte, _ int) (float32, error) {
	f.calls++
	return f.prob, f.err
}
func (f *fakeDetector) Strategy() string { return "fake" }
func (f *fakeDetector) Close() error     { f.closed = true; return nil }

// factory returns a newDetector func that always yields det (and counts builds).
func factory(det vad.Detector, buildErr error, builds *int) func(vad.Config) (vad.Detector, error) {
	return func(vad.Config) (vad.Detector, error) {
		if builds != nil {
			*builds++
		}
		if buildErr != nil {
			return nil, buildErr
		}
		return det, nil
	}
}

func TestResolveVADModel(t *testing.T) {
	t.Parallel()

	// Explicit path override wins.
	cfg, key := resolveVADModel(&conf.VADSettings{ModelPath: "/custom/silero.onnx"}, "/lib.so")
	assert.Equal(t, "path:/custom/silero.onnx", key)
	assert.Equal(t, "/custom/silero.onnx", cfg.ModelPath)
	assert.Empty(t, cfg.ModelData)
	assert.Equal(t, "/lib.so", cfg.LibraryPath)

	// No path: fall back to the embedded model (present in the default build).
	cfg, key = resolveVADModel(&conf.VADSettings{}, "")
	if vad.HasEmbeddedModel() {
		assert.Equal(t, "embedded", key)
		assert.NotEmpty(t, cfg.ModelData, "embedded model bytes must be supplied")
		assert.Empty(t, cfg.ModelPath)
	} else {
		assert.Empty(t, key, "no model available without an embedded model or a path")
	}
}

func TestVADGate_LazyLoadAndReuse(t *testing.T) {
	t.Parallel()
	det := &fakeDetector{prob: 0.9}
	builds := 0
	g := &vadGate{newDetector: factory(det, nil, &builds), lastRun: map[string]time.Time{}}

	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	// First call loads and scores.
	prob, ran, _, err := g.score(&vad.Config{}, "k", []byte{0, 0}, "s1", base, 48000)
	require.NoError(t, err)
	assert.True(t, ran)
	assert.InDelta(t, 0.9, prob, 1e-6)
	assert.Equal(t, 1, builds)
	assert.Equal(t, 1, det.calls)

	// A later chunk (past the throttle) reuses the same detector.
	_, ran, _, err = g.score(&vad.Config{}, "k", []byte{0, 0}, "s1", base.Add(2*time.Second), 48000)
	require.NoError(t, err)
	assert.True(t, ran)
	assert.Equal(t, 1, builds, "detector must not be rebuilt")
	assert.Equal(t, 2, det.calls)
}

func TestVADGate_ThrottlesPerSource(t *testing.T) {
	t.Parallel()
	det := &fakeDetector{prob: 0.9}
	g := &vadGate{newDetector: factory(det, nil, nil), lastRun: map[string]time.Time{}}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	_, ran, _, err := g.score(&vad.Config{}, "k", []byte{0, 0}, "s1", base, 48000)
	require.NoError(t, err)
	assert.True(t, ran)

	// Within the throttle window: skipped, detector not called again.
	_, ran, _, err = g.score(&vad.Config{}, "k", []byte{0, 0}, "s1", base.Add(500*time.Millisecond), 48000)
	require.NoError(t, err)
	assert.False(t, ran)
	assert.Equal(t, 1, det.calls)

	// A different source is scored independently.
	_, ran, _, err = g.score(&vad.Config{}, "k", []byte{0, 0}, "s2", base.Add(500*time.Millisecond), 48000)
	require.NoError(t, err)
	assert.True(t, ran)
	assert.Equal(t, 2, det.calls)
}

func TestVADGate_ReloadOnModelPathChange(t *testing.T) {
	t.Parallel()
	det1 := &fakeDetector{prob: 0.9}
	det2 := &fakeDetector{prob: 0.1}
	builds := 0
	g := &vadGate{
		newDetector: func(vad.Config) (vad.Detector, error) {
			builds++
			if builds == 1 {
				return det1, nil
			}
			return det2, nil
		},
		lastRun: map[string]time.Time{},
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	_, _, _, err := g.score(&vad.Config{}, "a", []byte{0, 0}, "s1", base, 48000)
	require.NoError(t, err)
	_, _, _, err = g.score(&vad.Config{}, "b", []byte{0, 0}, "s1", base.Add(2*time.Second), 48000)
	require.NoError(t, err)

	assert.Equal(t, 2, builds, "changing model path must rebuild")
	assert.True(t, det1.closed, "old detector must be closed on reload")
}

func TestVADGate_EmptyPathUnloads(t *testing.T) {
	t.Parallel()
	det := &fakeDetector{prob: 0.9}
	g := &vadGate{newDetector: factory(det, nil, nil), lastRun: map[string]time.Time{}}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	_, ran, _, err := g.score(&vad.Config{}, "k", []byte{0, 0}, "s1", base, 48000)
	require.NoError(t, err)
	assert.True(t, ran)

	// Empty path (uninstalled): detector dropped, no scoring.
	_, ran, _, err = g.score(&vad.Config{}, "", []byte{0, 0}, "s1", base.Add(2*time.Second), 48000)
	require.NoError(t, err)
	assert.False(t, ran)
	assert.True(t, det.closed, "detector must be closed when the model path is cleared")
}

func TestVADGate_LoadFailureBacksOff(t *testing.T) {
	t.Parallel()
	builds := 0
	g := &vadGate{
		newDetector: factory(nil, errors.NewStd("load failed"), &builds),
		lastRun:     map[string]time.Time{},
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	_, ran, _, err := g.score(&vad.Config{}, "k", []byte{0, 0}, "s1", base, 48000)
	require.Error(t, err)
	assert.False(t, ran)
	assert.Equal(t, 1, builds)

	// Immediately after a failure, a different source does not re-attempt (backoff).
	_, ran, _, err = g.score(&vad.Config{}, "k", []byte{0, 0}, "s2", base.Add(2*time.Second), 48000)
	require.NoError(t, err)
	assert.False(t, ran)
	assert.Equal(t, 1, builds, "load must not be retried within the backoff window")
}

// TestVADGate_InferenceErrorBacksOff proves a repeated inference failure drops
// the detector and enters the failure backoff instead of re-running inference
// (and logging) on every chunk (Sentry review finding).
func TestVADGate_InferenceErrorBacksOff(t *testing.T) {
	t.Parallel()
	det := &fakeDetector{prob: 0.9, err: errors.NewStd("inference boom")}
	builds := 0
	g := &vadGate{newDetector: factory(det, nil, &builds), lastRun: map[string]time.Time{}}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	// First chunk loads, scores, and inference errors -> detector dropped + backoff.
	_, ran, _, err := g.score(&vad.Config{}, "k", []byte{0, 0}, "s1", base, 48000)
	require.Error(t, err)
	assert.False(t, ran)
	assert.Equal(t, 1, det.calls)
	assert.True(t, det.closed, "detector must be dropped on inference failure")

	// A later chunk within the backoff must NOT re-run inference or rebuild.
	_, ran, _, err = g.score(&vad.Config{}, "k", []byte{0, 0}, "s2", base.Add(2*time.Second), 48000)
	require.NoError(t, err)
	assert.False(t, ran, "inference failure must back off, not retry every chunk")
	assert.Equal(t, 1, det.calls, "no re-inference within the backoff window")
	assert.Equal(t, 1, builds, "no rebuild within the backoff window")
}

// makeVADItem builds a minimal results item for the given source and chunk.
func makeVADItem(modelID, source string, start time.Time, pcmLen int) *classifier.Results {
	return &classifier.Results{
		ModelID:   modelID,
		PCMdata:   make([]byte, pcmLen),
		Source:    datastore.AudioSource{ID: source},
		StartTime: start,
	}
}

func vadSettings(enabled bool, threshold float64) *conf.Settings {
	s := &conf.Settings{}
	s.Realtime.PrivacyFilter.Enabled = true
	s.Realtime.PrivacyFilter.VAD.Enabled = enabled
	s.Realtime.PrivacyFilter.VAD.Threshold = threshold
	s.Realtime.PrivacyFilter.VAD.ModelPath = "/fake/silero.onnx"
	return s
}

func TestRunVADGate_SpeechWritesLastHumanDetection(t *testing.T) {
	t.Parallel()
	det := &fakeDetector{prob: 0.9} // above threshold
	p := &Processor{
		LastHumanDetection: map[string]HumanDetection{},
		vadGate:            &vadGate{newDetector: factory(det, nil, nil), lastRun: map[string]time.Time{}},
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	item := makeVADItem("BirdNET_V2.4", "src1", base, 96000)

	p.runVADGate(vadSettings(true, 0.35), item)

	got, ok := p.LastHumanDetection["src1"]
	require.True(t, ok, "speech hit must record LastHumanDetection")
	assert.Equal(t, base, got.Time)
	assert.Equal(t, metrics.TriggerVAD, got.Trigger, "VAD path must tag the trigger")
}

func TestRunVADGate_BelowThresholdDoesNotWrite(t *testing.T) {
	t.Parallel()
	det := &fakeDetector{prob: 0.1} // below threshold
	p := &Processor{
		LastHumanDetection: map[string]HumanDetection{},
		vadGate:            &vadGate{newDetector: factory(det, nil, nil), lastRun: map[string]time.Time{}},
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	p.runVADGate(vadSettings(true, 0.35), makeVADItem("BirdNET_V2.4", "src1", base, 96000))

	_, ok := p.LastHumanDetection["src1"]
	assert.False(t, ok, "below-threshold speech must not record a human detection")
	// Prove the "no write" verdict came from the threshold comparison, not an
	// upstream early-return (bad model spec, empty PCM): scoring must have run.
	assert.Equal(t, 1, det.calls, "the chunk must actually have been scored")
}

func TestRunVADGate_AtThresholdWrites(t *testing.T) {
	t.Parallel()
	// 0.5 is exactly representable in both float32 and float64, so the boundary
	// is unambiguous and this genuinely pins the >= (not >) comparison.
	det := &fakeDetector{prob: 0.5}
	p := &Processor{
		LastHumanDetection: map[string]HumanDetection{},
		vadGate:            &vadGate{newDetector: factory(det, nil, nil), lastRun: map[string]time.Time{}},
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	p.runVADGate(vadSettings(true, 0.5), makeVADItem("BirdNET_V2.4", "src1", base, 96000))

	got, ok := p.LastHumanDetection["src1"]
	require.True(t, ok, "prob == threshold must record a human detection (>= comparison)")
	assert.Equal(t, base, got.Time)
}

func TestRunVADGate_DisabledIsInert(t *testing.T) {
	t.Parallel()
	det := &fakeDetector{prob: 0.99}
	builds := 0
	p := &Processor{
		LastHumanDetection: map[string]HumanDetection{},
		vadGate:            &vadGate{newDetector: factory(det, nil, &builds), lastRun: map[string]time.Time{}},
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	p.runVADGate(vadSettings(false, 0.35), makeVADItem("BirdNET_V2.4", "src1", base, 96000))

	assert.Empty(t, p.LastHumanDetection, "disabled VAD must not write")
	assert.Equal(t, 0, builds, "disabled VAD must not build a detector")
}

func TestRunVADGate_SkipsBatAndEmptyPCM(t *testing.T) {
	t.Parallel()
	det := &fakeDetector{prob: 0.99}
	p := &Processor{
		LastHumanDetection: map[string]HumanDetection{},
		vadGate:            &vadGate{newDetector: factory(det, nil, nil), lastRun: map[string]time.Time{}},
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	// Ultrasonic bat chunk: skipped.
	p.runVADGate(vadSettings(true, 0.35), makeVADItem(classifier.RegistryIDBat, "src1", base, 96000))
	// Empty PCM: skipped.
	p.runVADGate(vadSettings(true, 0.35), makeVADItem("BirdNET_V2.4", "src2", base, 0))

	assert.Empty(t, p.LastHumanDetection)
	assert.Equal(t, 0, det.calls, "bat and empty-PCM chunks must not be scored")
}

func TestNewVADGate(t *testing.T) {
	t.Parallel()
	g := newVADGate()
	require.NotNil(t, g)
	assert.NotNil(t, g.lastRun, "lastRun must be initialised to avoid a nil-map write panic")
	assert.NotNil(t, g.newDetector, "newDetector must default to vad.New")
}

func TestClampVADThreshold(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{name: "in range unchanged", in: 0.35, want: 0.35},
		{name: "zero clamps up to min", in: 0, want: vadMinThreshold},
		{name: "negative clamps up to min", in: -1, want: vadMinThreshold},
		{name: "above one clamps to max", in: 2.0, want: vadMaxThreshold},
		{name: "exactly max stays", in: 1.0, want: 1.0},
		{name: "exactly min stays", in: vadMinThreshold, want: vadMinThreshold},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.InDelta(t, tt.want, clampVADThreshold(tt.in), 1e-9)
		})
	}
}

// TestVADGate_ConfigChangeClearsBackoff proves that correcting the model source
// after a failed load retries immediately instead of waiting out the failed
// source's backoff window (Gemini review finding).
func TestVADGate_ConfigChangeClearsBackoff(t *testing.T) {
	t.Parallel()
	good := &fakeDetector{prob: 0.9}
	builds := 0
	g := &vadGate{
		newDetector: func(vad.Config) (vad.Detector, error) {
			builds++
			if builds == 1 {
				return nil, errors.NewStd("bad model path")
			}
			return good, nil
		},
		lastRun: map[string]time.Time{},
	}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	// First source fails to load -> enters 30s backoff.
	_, ran, _, err := g.score(&vad.Config{}, "path:/bad.onnx", []byte{0, 0}, "s1", base, 48000)
	require.Error(t, err)
	assert.False(t, ran)

	// Immediately switching to a corrected source must load NOW, not wait out
	// the previous source's backoff.
	_, ran, _, err = g.score(&vad.Config{}, "path:/good.onnx", []byte{0, 0}, "s1", base.Add(2*time.Second), 48000)
	require.NoError(t, err)
	assert.True(t, ran, "a corrected model source must be retried immediately")
	assert.Equal(t, 2, builds)
}

// TestVADGate_OutOfOrderDoesNotRegressThrottle proves an out-of-order (earlier)
// chunk does not roll the per-source throttle timestamp backwards and cause a
// cascade of redundant inferences (Gemini review finding).
func TestVADGate_OutOfOrderDoesNotRegressThrottle(t *testing.T) {
	t.Parallel()
	det := &fakeDetector{prob: 0.9}
	g := &vadGate{newDetector: factory(det, nil, nil), lastRun: map[string]time.Time{}}
	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

	// Advance the throttle to base+2s.
	_, ran, _, _ := g.score(&vad.Config{}, "k", []byte{0, 0}, "s1", base.Add(2*time.Second), 48000)
	require.True(t, ran)
	assert.Equal(t, 1, det.calls)

	// An out-of-order older chunk runs once but must NOT regress lastRun.
	_, ran, _, _ = g.score(&vad.Config{}, "k", []byte{0, 0}, "s1", base.Add(500*time.Millisecond), 48000)
	require.True(t, ran)
	assert.Equal(t, 2, det.calls)

	// A chunk within the throttle of the (un-regressed) base+2s must be skipped.
	_, ran, _, _ = g.score(&vad.Config{}, "k", []byte{0, 0}, "s1", base.Add(2300*time.Millisecond), 48000)
	assert.False(t, ran, "throttle must still key off the newest start time, not the out-of-order one")
	assert.Equal(t, 2, det.calls)
}
