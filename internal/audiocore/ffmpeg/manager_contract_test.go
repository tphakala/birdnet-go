//go:build integration

package ffmpeg_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/audiocore/streamtest"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

// ffmpegManagerAdapter adapts *ffmpeg.Manager to the producer-agnostic
// streamtest.Manager contract, translating the neutral streamtest.StreamSpec
// into an audiocore.StreamSpec and the neutral health snapshot into
// streamtest.Health. The FFmpeg binary path and log level are manager-level
// options passed to NewManagerWithOptions, not per-stream spec fields.
type ffmpegManagerAdapter struct {
	mgr *ffmpeg.Manager
}

func (a *ffmpegManagerAdapter) StartStream(spec *streamtest.StreamSpec) error {
	return a.mgr.StartStream(&audiocore.StreamSpec{
		URL:                  spec.URL,
		SourceID:             spec.SourceID,
		SourceName:           spec.SourceName,
		Type:                 audiocore.SourceType(spec.Type),
		SampleRate:           spec.SampleRate,
		SourceSampleRate:     spec.SourceSampleRate,
		BitDepth:             spec.BitDepth,
		Channels:             spec.Channels,
		SourceChannels:       spec.SourceChannels,
		ChannelMode:          spec.ChannelMode,
		MediaMode:            spec.MediaMode,
		Transport:            spec.Transport,
		HealthyDataThreshold: spec.HealthyDataThreshold,
		Debug:                spec.Debug,
	})
}

func (a *ffmpegManagerAdapter) StopStream(sourceID string) error { return a.mgr.StopStream(sourceID) }

func (a *ffmpegManagerAdapter) StreamHealth(sourceID string) (streamtest.Health, error) {
	h, err := a.mgr.StreamHealth(sourceID)
	if err != nil {
		return nil, err
	}
	return ffmpegHealth{h: h}, nil
}

func (a *ffmpegManagerAdapter) AllStreamHealth() map[string]streamtest.Health {
	src := a.mgr.AllStreamHealth()
	out := make(map[string]streamtest.Health, len(src))
	for id, h := range src {
		out[id] = ffmpegHealth{h: h}
	}
	return out
}

func (a *ffmpegManagerAdapter) GetActiveStreamIDs() []string { return a.mgr.GetActiveStreamIDs() }

func (a *ffmpegManagerAdapter) SetOnStreamReset(fn func(sourceID string)) { a.mgr.SetOnStreamReset(fn) }

func (a *ffmpegManagerAdapter) Shutdown() error { return a.mgr.Shutdown() }

func (a *ffmpegManagerAdapter) ShutdownWithContext(ctx context.Context) error {
	return a.mgr.ShutdownWithContext(ctx)
}

// ffmpegHealth adapts *audiocore.StreamHealth to streamtest.Health.
type ffmpegHealth struct{ h *audiocore.StreamHealth }

func (f ffmpegHealth) IsHealthy() bool             { return f.h.IsHealthy }
func (f ffmpegHealth) IsReceivingData() bool       { return f.h.IsReceivingData }
func (f ffmpegHealth) RestartCount() int           { return f.h.RestartCount }
func (f ffmpegHealth) TotalBytesReceived() int64   { return f.h.TotalBytesReceived }
func (f ffmpegHealth) BytesPerSecond() float64     { return f.h.BytesPerSecond }
func (f ffmpegHealth) LastDataReceived() time.Time { return f.h.LastDataReceived }
func (f ffmpegHealth) SourceChannels() int         { return f.h.SourceChannels }
func (f ffmpegHealth) ProcessState() string        { return f.h.ProcessStateName() }
func (f ffmpegHealth) StateHistoryLen() int        { return len(f.h.StateHistory) }

func (f ffmpegHealth) ErrorType() string {
	if f.h.LastErrorContext != nil {
		return f.h.LastErrorContext.ErrorType
	}
	return ""
}

// ffmpegProcessStates are the legacy process_state strings the FFmpeg producer
// can report; the frontend and health API must be able to render each.
var ffmpegProcessStates = []string{
	ffmpeg.ProcessStateIdle,
	ffmpeg.ProcessStateStarting,
	ffmpeg.ProcessStateRunning,
	ffmpeg.ProcessStateRestarting,
	ffmpeg.ProcessStateBackoff,
	ffmpeg.ProcessStateCircuitOpen,
	ffmpeg.ProcessStateStopped,
}

// TestFFmpegManagerContract runs the Phase 0 characterization suite against the
// live FFmpeg ingest path, using a MediaMTX container as the media server. It
// records the FFmpeg baseline numbers (grep BASELINE in the output) that the
// native producer must match or beat in later phases.
func TestFFmpegManagerContract(t *testing.T) {
	containers.SkipIfContainerRuntimeUnavailable(t)

	ffmpegPath, err := exec.LookPath("ffmpeg")
	require.NoError(t, err, "ffmpeg must be on PATH for the ingest contract")

	server, err := containers.NewMediaMTXContainer(t.Context(), nil)
	require.NoError(t, err, "MediaMTX container should start")
	t.Cleanup(func() {
		//nolint:gocritic // t.Context() is already cancelled when Cleanup runs; Terminate needs a live context
		assert.NoError(t, server.Terminate(context.Background()), "MediaMTX container should terminate cleanly")
	})

	fixture := streamtest.NewMediaMTXFixture(t, server)

	factory := func(t *testing.T, fc streamtest.FactoryConfig) streamtest.Manager {
		t.Helper()
		mgr := ffmpeg.NewManagerWithOptions(
			t.Context(),
			fc.OnFrame,
			fc.OnReset,
			nil,
			fc.BufferManager,
			ffmpeg.Options{SilenceTimeout: fc.SilenceTimeout, FFmpegPath: ffmpegPath, LogLevel: "error"},
		)
		return &ffmpegManagerAdapter{mgr: mgr}
	}

	streamtest.RunManagerContract(t, &streamtest.ContractConfig{
		Factory: factory,
		Fixture: fixture,
		Codecs: []streamtest.Codec{
			streamtest.CodecPCMU,
			streamtest.CodecPCMA,
			streamtest.CodecAAC,
			streamtest.CodecOpus,
			streamtest.CodecL16,
		},
		ValidProcessStates: ffmpegProcessStates,
		TargetSampleRate:   48000,
		ProducerName:       "ffmpeg",
	})
}

// compile-time guard: the adapter satisfies the contract manager interface.
var _ streamtest.Manager = (*ffmpegManagerAdapter)(nil)
