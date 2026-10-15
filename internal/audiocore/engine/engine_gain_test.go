package engine

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dspgain "github.com/tphakala/go-audio-dsp/gain"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

const (
	// gainTestSourceID names the source registered by the gain tests.
	gainTestSourceID = "gain_test_source"

	// gainTestRate is the sample rate of the gain test source and consumer.
	gainTestRate = 48000

	// gainTestFrameWait bounds the wait for a routed frame.
	gainTestFrameWait = 5 * time.Second

	// gainTestOverflowDB is a finite gain whose linear factor overflows float32,
	// so go-audio-dsp rejects it.
	gainTestOverflowDB = 1000.0

	// gainTestBoostDB is a gain that visibly changes the test samples.
	gainTestBoostDB = 6.0
)

// frameCaptureConsumer is an AudioConsumer that forwards a copy of every frame.
type frameCaptureConsumer struct {
	frames chan []byte
}

func (c *frameCaptureConsumer) ID() string      { return "gain-capture" }
func (c *frameCaptureConsumer) SampleRate() int { return gainTestRate }
func (c *frameCaptureConsumer) BitDepth() int   { return 16 }
func (c *frameCaptureConsumer) Channels() int   { return 1 }
func (c *frameCaptureConsumer) Close() error    { return nil }
func (c *frameCaptureConsumer) Write(f audiocore.AudioFrame) error { //nolint:gocritic // hugeParam: matches AudioConsumer.Write contract
	c.frames <- bytes.Clone(f.Data)
	return nil
}

// newGainTestEngine registers a sound card source at gain 0 with one capture
// route on the engine's router.
func newGainTestEngine(t *testing.T) (*AudioEngine, *frameCaptureConsumer) {
	t.Helper()
	eng := newTestEngine(t)
	_, err := eng.Registry().Register(&audiocore.SourceConfig{
		ID:               gainTestSourceID,
		DisplayName:      "Gain Test",
		Type:             audiocore.SourceTypeAudioCard,
		ConnectionString: "gain-test-device",
		SampleRate:       gainTestRate,
		BitDepth:         16,
		Channels:         1,
	})
	require.NoError(t, err)

	consumer := &frameCaptureConsumer{frames: make(chan []byte, 8)}
	require.NoError(t, eng.Router().AddRoute(gainTestSourceID, consumer, gainTestRate, 0, nil))
	return eng, consumer
}

func dispatchGainTestFrame(t *testing.T, eng *AudioEngine, consumer *frameCaptureConsumer, data []byte) []byte {
	t.Helper()
	eng.Router().Dispatch(audiocore.AudioFrame{
		SourceID:   gainTestSourceID,
		SourceName: "Gain Test",
		Data:       data,
		SampleRate: gainTestRate,
		BitDepth:   16,
		Channels:   1,
	})
	select {
	case got := <-consumer.frames:
		return got
	case <-time.After(gainTestFrameWait):
		require.FailNow(t, "timed out waiting for a routed frame")
		return nil
	}
}

// TestUpdateSourceGain_UpdatesRegistryAndRoutes checks that one call moves the
// registry gain and the live route gain together.
func TestUpdateSourceGain_UpdatesRegistryAndRoutes(t *testing.T) {
	t.Parallel()
	eng, consumer := newGainTestEngine(t)

	input := []byte{0xe8, 0x03, 0x18, 0xfc, 0xf4, 0x01, 0x0c, 0xfe} // 1000, -1000, 500, -500

	require.NoError(t, eng.UpdateSourceGain(gainTestSourceID, gainTestBoostDB))

	gain, ok := eng.Registry().GetGain(gainTestSourceID)
	require.True(t, ok)
	assert.InDelta(t, gainTestBoostDB, gain, 0, "registry gain must follow the update")

	g, err := dspgain.New(gainTestBoostDB)
	require.NoError(t, err)
	want := make([]byte, len(input))
	require.NoError(t, g.ApplyBytesTo(want, input))
	got := dispatchGainTestFrame(t, eng, consumer, input)
	assert.Equal(t, want, got, "the live route must apply the new gain")

	require.NoError(t, eng.UpdateSourceGain(gainTestSourceID, 0))
	gain, _ = eng.Registry().GetGain(gainTestSourceID)
	assert.InDelta(t, 0.0, gain, 0)
	assert.Equal(t, input, dispatchGainTestFrame(t, eng, consumer, input), "0 dB must restore the original bytes")
}

// TestUpdateSourceGain_RejectedGainLeavesRegistryUnchanged checks that an unusable
// gain on a source with a live route is rejected and leaves both the registry and
// the route on the previous gain.
func TestUpdateSourceGain_RejectedGainLeavesRegistryUnchanged(t *testing.T) {
	t.Parallel()
	eng, consumer := newGainTestEngine(t)

	require.NoError(t, eng.UpdateSourceGain(gainTestSourceID, gainTestBoostDB))
	require.Error(t, eng.UpdateSourceGain(gainTestSourceID, math.NaN()))

	gain, ok := eng.Registry().GetGain(gainTestSourceID)
	require.True(t, ok)
	assert.InDelta(t, gainTestBoostDB, gain, 0, "a rejected gain must not change the registry")

	input := []byte{0xe8, 0x03, 0x18, 0xfc}
	g, err := dspgain.New(gainTestBoostDB)
	require.NoError(t, err)
	want := make([]byte, len(input))
	require.NoError(t, g.ApplyBytesTo(want, input))
	assert.Equal(t, want, dispatchGainTestFrame(t, eng, consumer, input), "the route keeps the previous gain")
}

// TestUpdateSourceGain_UnknownSource checks that an unregistered source is an error.
func TestUpdateSourceGain_UnknownSource(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)

	err := eng.UpdateSourceGain("missing", gainTestBoostDB)
	require.ErrorIs(t, err, audiocore.ErrSourceNotFound)
}

// TestUpdateSourceGain_InvalidGainWithoutRoutesIsRejected pins that an unusable
// gain is rejected, and never reaches the registry, even when the source has no
// route to reject it: a route built later would read the registry value and
// fail to start.
func TestUpdateSourceGain_InvalidGainWithoutRoutesIsRejected(t *testing.T) {
	t.Parallel()
	eng := newTestEngine(t)
	_, err := eng.Registry().Register(&audiocore.SourceConfig{
		ID:               gainTestSourceID,
		DisplayName:      "Gain Test",
		Type:             audiocore.SourceTypeAudioCard,
		ConnectionString: "gain-test-device",
		SampleRate:       gainTestRate,
		BitDepth:         16,
		Channels:         1,
	})
	require.NoError(t, err)

	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), gainTestOverflowDB} {
		require.Error(t, eng.UpdateSourceGain(gainTestSourceID, bad), "gain %v", bad)
		gain, ok := eng.Registry().GetGain(gainTestSourceID)
		require.True(t, ok)
		assert.InDelta(t, 0.0, gain, 0, "an unusable gain must not reach the registry (gain %v)", bad)
	}
}
