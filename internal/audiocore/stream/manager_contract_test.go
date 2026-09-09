//go:build integration

package stream_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/stream"
	"github.com/tphakala/birdnet-go/internal/audiocore/streamtest"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

// nativeManagerAdapter adapts *stream.Manager to the producer-agnostic
// streamtest.Manager contract, so the same Phase-0 characterization suite that
// pins the FFmpeg producer's behaviour also runs against the native producer.
type nativeManagerAdapter struct{ mgr *stream.Manager }

func (a *nativeManagerAdapter) StartStream(spec *streamtest.StreamSpec) error {
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

func (a *nativeManagerAdapter) StopStream(sourceID string) error { return a.mgr.StopStream(sourceID) }

func (a *nativeManagerAdapter) StreamHealth(sourceID string) (streamtest.Health, error) {
	h, err := a.mgr.StreamHealth(sourceID)
	if err != nil {
		return nil, err
	}
	return nativeHealth{h: h}, nil
}

func (a *nativeManagerAdapter) AllStreamHealth() map[string]streamtest.Health {
	src := a.mgr.AllStreamHealth()
	out := make(map[string]streamtest.Health, len(src))
	for id, h := range src {
		out[id] = nativeHealth{h: h}
	}
	return out
}

func (a *nativeManagerAdapter) GetActiveStreamIDs() []string { return a.mgr.GetActiveStreamIDs() }

func (a *nativeManagerAdapter) SetOnStreamReset(fn func(sourceID string)) {
	a.mgr.SetOnStreamReset(fn)
}

func (a *nativeManagerAdapter) Shutdown() error { return a.mgr.Shutdown() }

func (a *nativeManagerAdapter) ShutdownWithContext(ctx context.Context) error {
	return a.mgr.ShutdownWithContext(ctx)
}

// nativeHealth adapts *audiocore.StreamHealth to streamtest.Health.
type nativeHealth struct{ h *audiocore.StreamHealth }

func (f nativeHealth) IsHealthy() bool           { return f.h.IsHealthy }
func (f nativeHealth) IsReceivingData() bool     { return f.h.IsReceivingData }
func (f nativeHealth) RestartCount() int         { return f.h.RestartCount }
func (f nativeHealth) TotalBytesReceived() int64 { return f.h.TotalBytesReceived }
func (f nativeHealth) BytesPerSecond() float64   { return f.h.BytesPerSecond }
func (f nativeHealth) LastDataReceived() time.Time {
	return f.h.LastDataReceived
}
func (f nativeHealth) SourceChannels() int  { return f.h.SourceChannels }
func (f nativeHealth) ProcessState() string { return f.h.ProcessStateName() }
func (f nativeHealth) StateHistoryLen() int { return len(f.h.StateHistory) }

func (f nativeHealth) ErrorType() string {
	if f.h.LastErrorContext != nil {
		return f.h.LastErrorContext.ErrorType
	}
	return ""
}

// nativeProcessStates are the legacy process_state strings the native producer
// can report (a subset of the FFmpeg set: it has no idle/circuit_open because it
// runs no subprocess and no circuit breaker). The frontend must render each.
var nativeProcessStates = []string{
	"starting", "running", "restarting", "backoff", "stopped", "failed",
}

// TestNativeManagerContract runs the Phase-0 characterization suite against the
// native go-audio-stream ingest path, using a MediaMTX container as the media
// server, proving the native producer matches the FFmpeg baseline at the manager
// seam (frame shape, PCM fidelity across codecs, data rate, lifecycle, health
// transitions, silence restart, reconnect, media modes, multi-stream, error
// clarity, time to first frame).
func TestNativeManagerContract(t *testing.T) {
	containers.SkipIfContainerRuntimeUnavailable(t)

	server, err := containers.NewMediaMTXContainer(t.Context(), nil)
	require.NoError(t, err, "MediaMTX container should start")
	t.Cleanup(func() {
		//nolint:gocritic // t.Context() is already cancelled when Cleanup runs; Terminate needs a live context
		assert.NoError(t, server.Terminate(context.Background()), "MediaMTX container should terminate cleanly")
	})

	fixture := streamtest.NewMediaMTXFixture(t, server)

	factory := func(t *testing.T, fc streamtest.FactoryConfig) streamtest.Manager {
		t.Helper()
		opts := &stream.Options{}
		if fc.SilenceTimeout > 0 {
			// The suite drives the silence watchdog through SilenceTimeout; on the
			// native path the supervisor's read-idle window is the equivalent knob.
			opts.ReadIdle = fc.SilenceTimeout
		}
		mgr := stream.NewManager(t.Context(), fc.OnFrame, fc.OnReset, nil, fc.BufferManager, opts)
		return &nativeManagerAdapter{mgr: mgr}
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
		ValidProcessStates: nativeProcessStates,
		TargetSampleRate:   48000,
		ProducerName:       "native",
	})
}

// compile-time guard: the adapter satisfies the contract manager interface.
var _ streamtest.Manager = (*nativeManagerAdapter)(nil)
