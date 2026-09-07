//go:build integration

package engine

import (
	"context"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/audiocore/streamtest"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

// Engine-level contract timing (integration; real container and FFmpeg children).
const (
	engineHealthyBudget = 25 * time.Second
	enginePollInterval  = 250 * time.Millisecond
	engineSampleRate    = 48000
	engineBitDepth      = 16
	engineChannels      = 1

	// Shortened liveness thresholds so the chain runs in seconds.
	livenessCheckInterval = 1 * time.Second
	livenessSilence       = 3 * time.Second
	livenessMaxRetries    = 20
	livenessRetryBackoff  = 2 * time.Second
	livenessCooldown      = 2 * time.Second
	livenessEscalation    = 90 * time.Second
	livenessRestartBudget = 30 * time.Second
	livenessRecoverBudget = 60 * time.Second

	// managerShutdownCleanupTimeout bounds the manager shutdown in test cleanup.
	managerShutdownCleanupTimeout = 15 * time.Second
)

// TestEngineIngestContract runs the Phase 0 engine-level characterization cases
// (spec cases 12 and 13) against the live FFmpeg ingest path with a MediaMTX
// container. They pin behaviour the manager-seam contract cannot: engine
// reconfigure/stop/start round-trips, and the router + LivenessWatchdog + manager
// recovery chain.
func TestEngineIngestContract(t *testing.T) {
	containers.SkipIfContainerRuntimeUnavailable(t)

	ffmpegPath, err := exec.LookPath("ffmpeg")
	require.NoError(t, err, "ffmpeg must be on PATH")

	server, err := containers.NewMediaMTXContainer(t.Context(), nil)
	require.NoError(t, err, "MediaMTX container should start")
	t.Cleanup(func() {
		//nolint:gocritic // t.Context() is already cancelled when Cleanup runs; Terminate needs a live context
		assert.NoError(t, server.Terminate(context.Background()), "MediaMTX container should terminate cleanly")
	})

	fixture := streamtest.NewMediaMTXFixture(t, server)

	t.Run("EngineReconfigure", func(t *testing.T) {
		engineReconfigureCase(t, ffmpegPath, fixture)
	})
	t.Run("LivenessChain", func(t *testing.T) {
		livenessChainCase(t, ffmpegPath, fixture)
	})
}

// engineStreamHealthy reports whether the engine's FFmpeg stream for sourceID is
// healthy right now.
func engineStreamHealthy(eng *AudioEngine, sourceID string) bool {
	h, err := eng.StreamManager().StreamHealth(sourceID)
	return err == nil && h != nil && h.IsHealthy
}

// engineReconfigureCase (contract case 12): a reconfigure restarts the stream and
// frames keep flowing, and a stop/start round-trip (the quiet-hours path) works.
func engineReconfigureCase(t *testing.T, ffmpegPath string, fixture streamtest.Fixture) {
	t.Helper()
	pub := fixture.Publish(t, streamtest.PublishOptions{Codec: streamtest.CodecOpus, SampleRate: engineSampleRate, Channels: engineChannels})
	t.Cleanup(func() { pub.Stop(t) })

	eng := New(t.Context(), &Config{
		Logger:     audiocore.GetLogger(),
		FFmpegPath: ffmpegPath,
		Transport:  "tcp",
		LogLevel:   "error",
	}, nil)
	t.Cleanup(eng.Stop)
	eng.SetPrimaryModel(testModelID, testClipBytes, testOverlapBytes, testReadSize)

	const sourceID = "engine-reconf"
	baseCfg := func(mediaMode string) *audiocore.SourceConfig {
		return &audiocore.SourceConfig{
			ID:               sourceID,
			DisplayName:      "Engine Reconfigure",
			Type:             audiocore.SourceTypeRTSP,
			ConnectionString: pub.URL(),
			SampleRate:       engineSampleRate,
			BitDepth:         engineBitDepth,
			Channels:         engineChannels,
			Transport:        "tcp",
			MediaMode:        mediaMode,
		}
	}

	require.NoError(t, eng.AddSource(baseCfg("auto")))
	require.Eventually(t, func() bool { return engineStreamHealthy(eng, sourceID) },
		engineHealthyBudget, enginePollInterval, "stream should become healthy after AddSource")

	// Reconfigure with a changed field (MediaMode) keeping TCP transport, which
	// the TCP-only test server supports; the stream restarts and frames resume.
	require.NoError(t, eng.ReconfigureSource(sourceID, baseCfg("full-stream")))
	require.Eventually(t, func() bool { return engineStreamHealthy(eng, sourceID) },
		engineHealthyBudget, enginePollInterval, "stream should recover after reconfigure")

	// Quiet-hours round-trip: stop then start the same source.
	require.NoError(t, eng.StopStream(sourceID))
	require.Eventually(t, func() bool {
		_, err := eng.StreamManager().StreamHealth(sourceID)
		return err != nil
	}, engineHealthyBudget, enginePollInterval, "stream should be gone from the manager after StopStream")

	require.NoError(t, eng.StartStream(sourceID, pub.URL(), "tcp"))
	require.Eventually(t, func() bool { return engineStreamHealthy(eng, sourceID) },
		engineHealthyBudget, enginePollInterval, "stream should become healthy after StartStream")
}

// countingConsumer is a minimal AudioConsumer that registers a route so the
// LivenessWatchdog treats the source as active, and counts dispatched frames.
type countingConsumer struct {
	id string
	n  atomic.Int64
}

func (c *countingConsumer) ID() string      { return c.id }
func (c *countingConsumer) SampleRate() int { return engineSampleRate }
func (c *countingConsumer) BitDepth() int   { return engineBitDepth }
func (c *countingConsumer) Channels() int   { return engineChannels }
func (c *countingConsumer) Close() error    { return nil }
func (c *countingConsumer) Write(_ audiocore.AudioFrame) error { //nolint:gocritic // hugeParam: matches AudioConsumer.Write contract
	c.n.Add(1)
	return nil
}

// watchdogState returns the current liveness state string for sourceID, or "".
func watchdogState(w *audiocore.LivenessWatchdog, sourceID string) string {
	for _, s := range w.Snapshot() {
		if s.SourceID == sourceID {
			return s.State
		}
	}
	return ""
}

// livenessChainCase (contract case 13): router + LivenessWatchdog + manager;
// stopping the publisher drives the chain to invoke RestartSource, and the source
// returns to HEALTHY once the publisher comes back.
func livenessChainCase(t *testing.T, ffmpegPath string, fixture streamtest.Fixture) {
	t.Helper()
	pub := fixture.Publish(t, streamtest.PublishOptions{Codec: streamtest.CodecOpus, SampleRate: engineSampleRate, Channels: engineChannels})
	t.Cleanup(func() { pub.Stop(t) })

	log := audiocore.GetLogger()
	bufMgr := buffer.NewManager(log)
	router := audiocore.NewAudioRouter(log, bufMgr)

	const sourceID = "liveness-chain"
	consumer := &countingConsumer{id: "liveness-consumer"}
	require.NoError(t, router.AddRoute(sourceID, consumer, engineSampleRate, 0, nil))

	mgr := ffmpeg.NewManagerWithOptions(t.Context(), func(f audiocore.AudioFrame) { router.Dispatch(f) }, nil, log, bufMgr, ffmpeg.Options{FFmpegPath: ffmpegPath, LogLevel: "error"})
	t.Cleanup(func() {
		//nolint:gocritic // t.Context() is already cancelled when Cleanup runs; shutdown needs a live context
		ctx, cancel := context.WithTimeout(context.Background(), managerShutdownCleanupTimeout)
		defer cancel()
		assert.NoError(t, mgr.ShutdownWithContext(ctx), "manager should shut down cleanly")
	})

	streamCfg := &audiocore.StreamSpec{
		SourceID:   sourceID,
		SourceName: "Liveness Chain",
		URL:        pub.URL(),
		Type:       audiocore.SourceTypeRTSP,
		SampleRate: engineSampleRate,
		BitDepth:   engineBitDepth,
		Channels:   engineChannels,
		Transport:  "tcp",
	}

	var restartCalls atomic.Int64
	watchdog := audiocore.NewLivenessWatchdog(audiocore.LivenessConfig{
		CheckInterval:      livenessCheckInterval,
		SilenceThreshold:   livenessSilence,
		MaxRetries:         livenessMaxRetries,
		RetryBackoff:       livenessRetryBackoff,
		CooldownAfterRecov: livenessCooldown,
		EscalationTimeout:  livenessEscalation,
	}, router, audiocore.LivenessCallbacks{
		RestartSource: func(id string) error {
			restartCalls.Add(1)
			// Mirror the production restart shape (fresh stop then start) so a
			// recovered publisher can be picked up without waiting out the
			// producer's own backoff.
			_ = mgr.StopStream(id)
			return mgr.StartStream(streamCfg)
		},
	})

	require.NoError(t, mgr.StartStream(streamCfg))
	watchdog.Start()
	t.Cleanup(watchdog.Stop)

	// Establish healthy flow.
	require.Eventually(t, func() bool { return consumer.n.Load() > 0 },
		engineHealthyBudget, enginePollInterval, "frames should flow before the outage")
	require.Eventually(t, func() bool { return watchdogState(watchdog, sourceID) == audiocore.StateHealthy.String() },
		engineHealthyBudget, enginePollInterval, "watchdog should report HEALTHY while data flows")

	// Silence: the chain must reach a recovery state and invoke RestartSource.
	pub.Stop(t)
	require.Eventually(t, func() bool { return restartCalls.Load() > 0 },
		livenessRestartBudget, enginePollInterval, "silence should invoke RestartSource")

	// The publisher returns; the watchdog should drive the source back to HEALTHY.
	pub.Restart(t)
	require.Eventually(t, func() bool { return watchdogState(watchdog, sourceID) == audiocore.StateHealthy.String() },
		livenessRecoverBudget, enginePollInterval, "watchdog should return to HEALTHY after recovery")
}
