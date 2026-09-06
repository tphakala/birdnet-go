package streamtest

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// pathSeq gives each published stream a unique MediaMTX path within a run.
var pathSeq atomic.Uint64

func uniquePath(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, pathSeq.Add(1))
}

// pollHealth returns the health snapshot for sourceID, failing the test if the
// stream is unknown.
func pollHealth(t *testing.T, mgr Manager, sourceID string) Health {
	t.Helper()
	h, err := mgr.StreamHealth(sourceID)
	require.NoError(t, err, "StreamHealth(%s)", sourceID)
	require.NotNil(t, h)
	return h
}

// requireHealthy waits until the stream reports healthy or the budget elapses.
func requireHealthy(t *testing.T, mgr Manager, sourceID string, budget time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		h, err := mgr.StreamHealth(sourceID)
		return err == nil && h != nil && h.IsHealthy()
	}, budget, pollInterval, "stream %s should become healthy", sourceID)
}

// caseFrameShape (contract case 1): every dispatched frame carries the spec's
// identity and the expected PCM geometry, a pooled Ref when a buffer manager is
// wired and a nil Ref when it is not.
func caseFrameShape(t *testing.T, cfg *ContractConfig) {
	t.Helper()
	pub := cfg.Fixture.Publish(t, PublishOptions{Codec: CodecOpus, SampleRate: cfg.TargetSampleRate, Channels: monoChannels})
	t.Cleanup(func() { pub.Stop(t) })

	t.Run("pooled Ref when buffer manager wired", func(t *testing.T) {
		h := newHarness(t, cfg, bufferManagerFor(t), 0)
		spec := cfg.rtspSpec(uniquePath("frameshape"), pub.URL())
		require.NoError(t, h.mgr.StartStream(spec))
		require.True(t, waitForFrames(h.collector, 3, firstFrameBudget), "frames should flow")

		frames := h.collector.snapshot()
		for i := range frames {
			f := &frames[i]
			assert.Equal(t, spec.SourceID, f.SourceID)
			assert.Equal(t, spec.SourceName, f.SourceName)
			assert.Equal(t, spec.SampleRate, f.SampleRate)
			assert.Equal(t, targetBitDepth, f.BitDepth)
			assert.Equal(t, monoChannels, f.Channels)
			assert.NotEmpty(t, f.Data)
			assert.Zero(t, len(f.Data)%s16BytesPerSample, "PCM length must be sample-aligned")
			assert.LessOrEqual(t, len(f.Data), maxFrameBytes)
			assert.True(t, f.HadRef, "frame should carry a pooled Ref when bufMgr is wired")
			assert.WithinDuration(t, f.ReceivedAt, f.Timestamp, time.Second)
		}
	})

	t.Run("nil Ref when no buffer manager", func(t *testing.T) {
		h := newHarness(t, cfg, nil, 0)
		spec := cfg.rtspSpec(uniquePath("frameshape-noref"), pub.URL())
		require.NoError(t, h.mgr.StartStream(spec))
		require.True(t, waitForFrames(h.collector, 3, firstFrameBudget), "frames should flow")
		frames := h.collector.snapshot()
		for i := range frames {
			assert.False(t, frames[i].HadRef, "frame should have a nil Ref when no bufMgr is wired")
		}
	})
}

// casePCMFidelity (contract case 2): the published 1 kHz tone survives ingest for
// every codec in the matrix, staying near 1000 Hz and clearly non-silent.
func casePCMFidelity(t *testing.T, cfg *ContractConfig) {
	t.Helper()
	for _, codec := range cfg.Codecs {
		t.Run(string(codec), func(t *testing.T) {
			rate := cfg.TargetSampleRate
			channels := monoChannels
			// G.711 is defined at 8 kHz mono; publish it that way and let the
			// producer resample to the analysis rate.
			if codec == CodecPCMU || codec == CodecPCMA {
				rate = g711SampleRate
			}
			pub := cfg.Fixture.Publish(t, PublishOptions{Codec: codec, SampleRate: rate, Channels: channels})
			t.Cleanup(func() { pub.Stop(t) })

			h := newHarness(t, cfg, bufferManagerFor(t), 0)
			spec := cfg.rtspSpec(uniquePath("fidelity-"+string(codec)), pub.URL())
			require.NoError(t, h.mgr.StartStream(spec))
			require.True(t, waitForFrames(h.collector, 1, firstFrameBudget), "frames should flow")

			// Collect a fidelity window of audio.
			h.collector.reset()
			time.Sleep(fidelityCollectWindow)
			pcm := h.collector.concatData()
			require.NotEmpty(t, pcm, "should have collected PCM")

			dominant := DominantFrequency(pcm, cfg.TargetSampleRate)
			rms := RMSDBFS(pcm)
			t.Logf("BASELINE %s PCMFidelity codec=%s dominantHz=%.1f rmsDBFS=%.2f", cfg.ProducerName, codec, dominant, rms)
			assert.InEpsilon(t, toneFrequencyHz, dominant, dominantFreqToleranceR,
				"dominant frequency should be ~1000 Hz for codec %s", codec)
			assert.Greater(t, rms, nonSilentFloorDBFS, "dispatched audio should be clearly non-silent for codec %s", codec)
		})
	}
}

// caseDataRate (contract case 3): the dispatched byte rate matches the analysis
// geometry and the health snapshot agrees.
func caseDataRate(t *testing.T, cfg *ContractConfig) {
	t.Helper()
	pub := cfg.Fixture.Publish(t, PublishOptions{Codec: CodecOpus, SampleRate: cfg.TargetSampleRate, Channels: monoChannels})
	t.Cleanup(func() { pub.Stop(t) })

	h := newHarness(t, cfg, bufferManagerFor(t), 0)
	spec := cfg.rtspSpec(uniquePath("datarate"), pub.URL())
	require.NoError(t, h.mgr.StartStream(spec))
	requireHealthy(t, h.mgr, spec.SourceID, healthyWithinBudget)

	h.collector.reset()
	start := time.Now()
	time.Sleep(dataRateWindow)
	elapsed := time.Since(start)
	bytesGot := len(h.collector.concatData())

	nominal := float64(cfg.TargetSampleRate * s16BytesPerSample)
	measured := float64(bytesGot) / elapsed.Seconds()
	health := pollHealth(t, h.mgr, spec.SourceID)
	t.Logf("BASELINE %s DataRate nominalBps=%.0f dispatchedBps=%.0f healthBps=%.0f",
		cfg.ProducerName, nominal, measured, health.BytesPerSecond())

	assert.InEpsilon(t, nominal, measured, dataRateTolerance, "dispatched byte rate should match analysis geometry")
	assert.InEpsilon(t, nominal, health.BytesPerSecond(), healthRateTolerance, "health BytesPerSecond should agree with dispatched rate")
}

// caseLifecycle (contract case 4): duplicate starts and unknown stops error,
// active tracking is accurate, and Shutdown is clean with no leaked goroutines.
func caseLifecycle(t *testing.T, cfg *ContractConfig) {
	t.Helper()
	leakOpt := goleak.IgnoreCurrent()

	pub := cfg.Fixture.Publish(t, PublishOptions{Codec: CodecOpus, SampleRate: cfg.TargetSampleRate, Channels: monoChannels})
	t.Cleanup(func() { pub.Stop(t) })

	coll := &frameCollector{}
	mgr := cfg.Factory(t, FactoryConfig{OnFrame: coll.onFrame, OnReset: coll.onReset, BufferManager: bufferManagerFor(t)})
	require.NotNil(t, mgr)
	// Safety net so a require failure before the explicit shutdown below cannot
	// leak the manager (and its FFmpeg child) into the sibling subtests that
	// share the fixture. The explicit ShutdownWithContext below still owns the
	// assertion; this best-effort call is a no-op once that has run.
	t.Cleanup(func() {
		//nolint:gocritic // t.Context() is already cancelled when Cleanup runs; shutdown needs a live context
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = mgr.ShutdownWithContext(ctx)
	})

	spec := cfg.rtspSpec(uniquePath("lifecycle"), pub.URL())
	require.NoError(t, mgr.StartStream(spec), "first start should succeed")
	require.Error(t, mgr.StartStream(spec), "duplicate start should error")
	require.Error(t, mgr.StopStream("does-not-exist"), "stopping an unknown stream should error")

	assert.Contains(t, mgr.GetActiveStreamIDs(), spec.SourceID)
	assert.Contains(t, mgr.AllStreamHealth(), spec.SourceID)

	require.True(t, waitForFrames(coll, 1, firstFrameBudget), "frames should flow before stop")
	require.NoError(t, mgr.StopStream(spec.SourceID))
	assert.NotContains(t, mgr.GetActiveStreamIDs(), spec.SourceID)
	assert.NotContains(t, mgr.AllStreamHealth(), spec.SourceID)

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	require.NoError(t, mgr.ShutdownWithContext(ctx), "shutdown should return within the timeout")

	// Stop the MediaMTX publisher before the leak check. pub.Stop is also
	// registered via t.Cleanup as a safety net, but t.Cleanup runs only after
	// this function body returns, i.e. after goleak.VerifyNone below. Without
	// this explicit stop, the publisher's still-running cmd.Wait() goroutine
	// (containers.startPublisher.func1) is caught as a false goroutine leak.
	// mediaPublication.Stop is idempotent, so the deferred cleanup call is a
	// no-op once this has run.
	pub.Stop(t)

	// os/exec.(*Cmd).watchCtx is the Go stdlib goroutine that watches a child
	// process's context; it is reaped by the runtime shortly after the child
	// exits and is not a producer goroutine, so it is ignored here. Any other
	// lingering goroutine is a real leak the producer must not have.
	goleak.VerifyNone(t, leakOpt, goleak.IgnoreAnyFunction("os/exec.(*Cmd).watchCtx"))
}

// caseOnResetFires (contract case 5): the reset callback fires exactly once for a
// fresh StartStream and stays stable while the stream runs healthy.
func caseOnResetFires(t *testing.T, cfg *ContractConfig) {
	t.Helper()
	pub := cfg.Fixture.Publish(t, PublishOptions{Codec: CodecOpus, SampleRate: cfg.TargetSampleRate, Channels: monoChannels})
	t.Cleanup(func() { pub.Stop(t) })

	h := newHarness(t, cfg, bufferManagerFor(t), 0)
	spec := cfg.rtspSpec(uniquePath("onreset"), pub.URL())
	require.NoError(t, h.mgr.StartStream(spec))
	require.Equal(t, 1, h.collector.resetCount(spec.SourceID), "reset should fire once on StartStream")

	requireHealthy(t, h.mgr, spec.SourceID, healthyWithinBudget)
	// While the stream stays healthy there must be no further resets.
	time.Sleep(healthyObserveWindow)
	assert.Equal(t, 1, h.collector.resetCount(spec.SourceID), "no extra resets while healthy")
}

// caseHealthTransitions (contract case 6): health flags track a stream going
// silent and recovering, and the legacy process_state string is always valid.
func caseHealthTransitions(t *testing.T, cfg *ContractConfig) {
	t.Helper()
	pub := cfg.Fixture.Publish(t, PublishOptions{Codec: CodecOpus, SampleRate: cfg.TargetSampleRate, Channels: monoChannels})
	t.Cleanup(func() { pub.Stop(t) })

	h := newHarness(t, cfg, bufferManagerFor(t), 0)
	spec := cfg.rtspSpec(uniquePath("health"), pub.URL())
	require.NoError(t, h.mgr.StartStream(spec))
	requireHealthy(t, h.mgr, spec.SourceID, healthyWithinBudget)

	assertProcessStateValid(t, cfg, pollHealth(t, h.mgr, spec.SourceID))

	// LastDataReceived advances while data flows.
	first := pollHealth(t, h.mgr, spec.SourceID).LastDataReceived()
	require.Eventually(t, func() bool {
		return pollHealth(t, h.mgr, spec.SourceID).LastDataReceived().After(first)
	}, healthyWithinBudget, pollInterval, "LastDataReceived should advance")

	// Stop the publisher (server stays up): receiving goes false quickly, healthy
	// goes false within the healthy threshold.
	pub.Stop(t)
	require.Eventually(t, func() bool {
		return !pollHealth(t, h.mgr, spec.SourceID).IsReceivingData()
	}, receivingFalseBudget, pollInterval, "IsReceivingData should go false after publisher stops")
	require.Eventually(t, func() bool {
		return !pollHealth(t, h.mgr, spec.SourceID).IsHealthy()
	}, defaultHealthyThreshold+healthyWithinBudget, pollInterval, "IsHealthy should go false after silence")

	// Recover.
	pub.Restart(t)
	requireHealthy(t, h.mgr, spec.SourceID, healthyWithinBudget)
	assertProcessStateValid(t, cfg, pollHealth(t, h.mgr, spec.SourceID))
}

func assertProcessStateValid(t *testing.T, cfg *ContractConfig, h Health) {
	t.Helper()
	state := h.ProcessState()
	assert.NotEmpty(t, state, "process_state must not be empty")
	if len(cfg.ValidProcessStates) > 0 {
		assert.Contains(t, cfg.ValidProcessStates, state, "process_state must be a value the frontend renders")
	}
}

// caseSilenceRestart (contract case 7): with a shortened silence timeout, a
// publisher that goes quiet while the server stays up triggers a restart, and the
// stream recovers when the publisher returns.
func caseSilenceRestart(t *testing.T, cfg *ContractConfig) {
	t.Helper()
	pub := cfg.Fixture.Publish(t, PublishOptions{Codec: CodecOpus, SampleRate: cfg.TargetSampleRate, Channels: monoChannels})
	t.Cleanup(func() { pub.Stop(t) })

	h := newHarness(t, cfg, bufferManagerFor(t), silenceTimeoutUnderTest)
	spec := cfg.rtspSpec(uniquePath("silence"), pub.URL())
	require.NoError(t, h.mgr.StartStream(spec))
	requireHealthy(t, h.mgr, spec.SourceID, healthyWithinBudget)

	before := pollHealth(t, h.mgr, spec.SourceID).RestartCount()
	pub.Stop(t)
	require.Eventually(t, func() bool {
		return pollHealth(t, h.mgr, spec.SourceID).RestartCount() > before
	}, silenceRestartBudget, pollInterval, "silence should trigger a restart")

	pub.Restart(t)
	requireHealthy(t, h.mgr, spec.SourceID, serverReconnectBudget)
}

// caseServerGoneReconnect (contract case 8): the whole server disappearing and
// returning is recovered from, with the restart counted and transitions recorded.
func caseServerGoneReconnect(t *testing.T, cfg *ContractConfig) {
	t.Helper()
	pub := cfg.Fixture.Publish(t, PublishOptions{Codec: CodecOpus, SampleRate: cfg.TargetSampleRate, Channels: monoChannels})
	t.Cleanup(func() { pub.Stop(t) })

	h := newHarness(t, cfg, bufferManagerFor(t), 0)
	spec := cfg.rtspSpec(uniquePath("servergone"), pub.URL())
	require.NoError(t, h.mgr.StartStream(spec))
	requireHealthy(t, h.mgr, spec.SourceID, healthyWithinBudget)

	restartsBefore := pollHealth(t, h.mgr, spec.SourceID).RestartCount()

	cfg.Fixture.StopServer(t)
	time.Sleep(serverGoneStopFor)
	cfg.Fixture.StartServer(t)
	pub.Restart(t)

	h.collector.reset()
	serverBack := time.Now()
	require.True(t, waitForFrames(h.collector, 1, serverReconnectBudget), "frames should resume after the server returns")
	reconnectTook := time.Since(serverBack)
	t.Logf("BASELINE %s ServerGoneReconnect reconnectSeconds=%.1f", cfg.ProducerName, reconnectTook.Seconds())

	after := pollHealth(t, h.mgr, spec.SourceID)
	assert.Greater(t, after.RestartCount(), restartsBefore, "the outage should have counted a restart")
	// StateHistory saturates at a small cap, so a count comparison is not
	// meaningful; assert only that lifecycle transitions are recorded at all,
	// which a producer that tracked none would fail.
	assert.Positive(t, after.StateHistoryLen(), "the producer should record lifecycle transitions")
}

// caseMediaModes (contract case 9): every RTSP media mode delivers audio from a
// video+audio publish.
func caseMediaModes(t *testing.T, cfg *ContractConfig) {
	t.Helper()
	if !cfg.Fixture.SupportsVideo() {
		t.Skip("fixture cannot publish video; media-mode selection not exercised")
	}
	pub := cfg.Fixture.Publish(t, PublishOptions{
		Codec: CodecOpus, SampleRate: cfg.TargetSampleRate, Channels: monoChannels, WithVideo: true,
	})
	t.Cleanup(func() { pub.Stop(t) })

	for _, mode := range []string{"auto", "audio-only", "full-stream"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, cfg, bufferManagerFor(t), 0)
			spec := cfg.rtspSpec(uniquePath("mediamode-"+mode), pub.URL())
			spec.MediaMode = mode
			require.NoError(t, h.mgr.StartStream(spec))
			require.True(t, waitForFrames(h.collector, 3, firstFrameBudget), "audio should flow in %s mode", mode)
		})
	}
}

// caseMultiStream (contract case 10): independent streams start, run, and stop
// without interfering with one another.
func caseMultiStream(t *testing.T, cfg *ContractConfig) {
	t.Helper()
	const streamCount = 3
	pubs := make([]Publication, streamCount)
	specs := make([]*StreamSpec, streamCount)

	h := newHarness(t, cfg, bufferManagerFor(t), 0)
	for i := range streamCount {
		pub := cfg.Fixture.Publish(t, PublishOptions{Codec: CodecOpus, SampleRate: cfg.TargetSampleRate, Channels: monoChannels})
		t.Cleanup(func() { pub.Stop(t) })
		pubs[i] = pub
		specs[i] = cfg.rtspSpec(uniquePath(fmt.Sprintf("multi%d", i)), pub.URL())
		require.NoError(t, h.mgr.StartStream(specs[i]))
	}

	for i := range streamCount {
		requireHealthy(t, h.mgr, specs[i].SourceID, healthyWithinBudget)
	}

	// Stop one stream; the others must keep delivering.
	require.NoError(t, h.mgr.StopStream(specs[0].SourceID))
	for i := 1; i < streamCount; i++ {
		before := pollHealth(t, h.mgr, specs[i].SourceID).TotalBytesReceived()
		require.Eventually(t, func() bool {
			return pollHealth(t, h.mgr, specs[i].SourceID).TotalBytesReceived() > before
		}, healthyWithinBudget, pollInterval, "surviving stream %d should keep delivering", i)
	}
	assert.NotContains(t, h.mgr.GetActiveStreamIDs(), specs[0].SourceID)
}

// caseHighRatePassthrough (contract case 11): a 96 kHz L16 source is dispatched at
// 96 kHz (the bat path) and the tone survives.
func caseHighRatePassthrough(t *testing.T, cfg *ContractConfig) {
	t.Helper()
	pub := cfg.Fixture.Publish(t, PublishOptions{Codec: CodecL16, SampleRate: highSampleRate, Channels: monoChannels})
	t.Cleanup(func() { pub.Stop(t) })

	h := newHarness(t, cfg, bufferManagerFor(t), 0)
	spec := cfg.rtspSpec(uniquePath("highrate"), pub.URL())
	spec.SampleRate = highSampleRate
	spec.SourceSampleRate = highSampleRate
	require.NoError(t, h.mgr.StartStream(spec))
	require.True(t, waitForFrames(h.collector, 1, firstFrameBudget), "frames should flow")

	h.collector.reset()
	time.Sleep(fidelityCollectWindow)
	frames := h.collector.snapshot()
	require.NotEmpty(t, frames)
	for i := range frames {
		assert.Equal(t, highSampleRate, frames[i].SampleRate, "frames should be dispatched at the high rate")
	}
	dominant := DominantFrequency(h.collector.concatData(), highSampleRate)
	t.Logf("BASELINE %s HighRatePassthrough dominantHz=%.1f", cfg.ProducerName, dominant)
	assert.InEpsilon(t, toneFrequencyHz, dominant, dominantFreqToleranceR, "tone should survive the high-rate path")
}

// caseErrorClarity (contract case 14): connection failures surface a specific
// error type where the producer classifies them; the measured types are recorded
// as the baseline. Fast failures (path not found, connection refused) are
// asserted to classify; the slow connect timeout is recorded only, because the
// FFmpeg producer's stderr classifier windows are shorter than its 10 s connect
// timeout so a timeout leaves the health error context empty (a documented gap
// the native producer is expected to close with typed errors).
func caseErrorClarity(t *testing.T, cfg *ContractConfig) {
	t.Helper()
	type errCase struct {
		name            string
		url             string
		wantSubstr      string // substring the classification should contain when present
		requireNonEmpty bool   // whether a classification must appear within the budget
	}
	cases := []errCase{
		{name: "path_not_found", url: cfg.Fixture.URLForPath(uniquePath("nopath")), wantSubstr: "404", requireNonEmpty: true},
		{name: "connection_refused", url: cfg.Fixture.RefusedPortURL(), wantSubstr: "refused", requireNonEmpty: true},
		{name: "unreachable_host", url: cfg.Fixture.UnreachableHostURL()},
	}
	if authURL := cfg.Fixture.BadAuthURL(t); authURL != "" {
		cases = append(cases, errCase{name: "auth_failed", url: authURL, wantSubstr: "auth", requireNonEmpty: true})
	}

	for _, ec := range cases {
		t.Run(ec.name, func(t *testing.T) {
			h := newHarness(t, cfg, bufferManagerFor(t), 0)
			spec := cfg.rtspSpec(uniquePath("err-"+ec.name), ec.url)
			require.NoError(t, h.mgr.StartStream(spec), "start returns nil; failures are async")

			var got string
			classified := waitForCondition(errorClarityBudget, func() bool {
				got = pollHealth(t, h.mgr, spec.SourceID).ErrorType()
				return got != ""
			})
			t.Logf("BASELINE %s ErrorClarity case=%s errorType=%q classified=%t", cfg.ProducerName, ec.name, got, classified)

			if ec.requireNonEmpty {
				require.True(t, classified, "an error type should be classified for %s", ec.name)
				assert.Contains(t, strings.ToLower(got), ec.wantSubstr, "error type should identify the failure")
			}
		})
	}
}

// waitForCondition polls fn until it returns true or the budget elapses. Unlike
// require.Eventually it does not fail the test, so a case can record whether a
// condition held without asserting it.
func waitForCondition(budget time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(pollInterval)
	}
	return fn()
}

// caseTimeToFirstFrame (contract case 15): the first frame arrives promptly from
// an already-publishing source; the measured latency is recorded as the baseline.
func caseTimeToFirstFrame(t *testing.T, cfg *ContractConfig) {
	t.Helper()
	pub := cfg.Fixture.Publish(t, PublishOptions{Codec: CodecOpus, SampleRate: cfg.TargetSampleRate, Channels: monoChannels})
	t.Cleanup(func() { pub.Stop(t) })
	// Let the publisher settle so the source is already live before we start.
	time.Sleep(streamStartSettle)

	h := newHarness(t, cfg, bufferManagerFor(t), 0)
	spec := cfg.rtspSpec(uniquePath("ttff"), pub.URL())
	start := time.Now()
	require.NoError(t, h.mgr.StartStream(spec))
	require.True(t, waitForFrames(h.collector, 1, firstFrameBudget), "a first frame should arrive")

	frames := h.collector.snapshot()
	require.NotEmpty(t, frames)
	// The require.True(waitForFrames(..., firstFrameBudget)) above already bounds
	// the latency; the value is captured here as the baseline the native producer
	// must match or beat in a later phase.
	ttff := frames[0].ReceivedAt.Sub(start)
	t.Logf("BASELINE %s TimeToFirstFrame seconds=%.2f", cfg.ProducerName, ttff.Seconds())
}
