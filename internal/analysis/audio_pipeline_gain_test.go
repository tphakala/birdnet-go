package analysis

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dspgain "github.com/tphakala/go-audio-dsp/gain"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	enginepkg "github.com/tphakala/birdnet-go/internal/audiocore/engine"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

const (
	// gainTestConsumerID names the capture consumer on each test source.
	gainTestConsumerID = "gain-test-consumer"

	// gainTestRateHz is the rate of the registered test sources and consumer.
	gainTestRateHz = 48000

	// gainTestNewGainDB is the gain the settings change to in the tests.
	gainTestNewGainDB = 6.0

	// gainTestFrameTimeout bounds the wait for a routed frame.
	gainTestFrameTimeout = 5 * time.Second

	// gainTestStreamURL and gainTestDevice are the connection strings of the
	// test sources.
	gainTestStreamURL = "rtsp://gain-cam.invalid/stream"
	gainTestDevice    = "hw:gain-test"
)

// gainTestPCM holds four samples: 1000, -1000, 500, -500.
var gainTestPCM = []byte{0xe8, 0x03, 0x18, 0xfc, 0xf4, 0x01, 0x0c, 0xfe}

// gainCaptureConsumer is an AudioConsumer that forwards a copy of every frame
// and records whether it was closed.
type gainCaptureConsumer struct {
	frames chan []byte
	closed chan struct{}
}

func newGainCaptureConsumer() *gainCaptureConsumer {
	return &gainCaptureConsumer{frames: make(chan []byte, 8), closed: make(chan struct{})}
}

func (c *gainCaptureConsumer) ID() string      { return gainTestConsumerID }
func (c *gainCaptureConsumer) SampleRate() int { return gainTestRateHz }
func (c *gainCaptureConsumer) BitDepth() int   { return conf.BitDepth }
func (c *gainCaptureConsumer) Channels() int   { return 1 }
func (c *gainCaptureConsumer) Close() error    { close(c.closed); return nil }
func (c *gainCaptureConsumer) Write(f audiocore.AudioFrame) error { //nolint:gocritic // hugeParam: matches AudioConsumer.Write contract
	c.frames <- bytes.Clone(f.Data)
	return nil
}

func (c *gainCaptureConsumer) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

// gainFixture is one running source with a capture route and the settings that
// describe it. set changes the configured gain.
type gainFixture struct {
	engine   *enginepkg.AudioEngine
	consumer *gainCaptureConsumer
	sourceID string
	setGain  func(gainDB float64)
}

// newGainFixture registers the stream or sound card described by kind in a real
// engine at gain 0, adds a capture route, and publishes matching settings.
func newGainFixture(t *testing.T, kind audiocore.SourceType) *gainFixture {
	t.Helper()
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	settings := &conf.Settings{}
	cfg := &audiocore.SourceConfig{
		DisplayName: "gain-test",
		Type:        kind,
		SampleRate:  conf.SampleRate,
		BitDepth:    conf.BitDepth,
		Channels:    1,
	}
	var setGain func(gainDB float64)
	switch kind {
	case audiocore.SourceTypeAudioCard:
		cfg.ConnectionString = gainTestDevice
		settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{{Name: "gain-test", Device: gainTestDevice, Models: []string{"birdnet"}}}
		setGain = func(gainDB float64) {
			s := conf.CloneSettings(conf.GetSettings())
			s.Realtime.Audio.Sources[0].Gain = gainDB
			conftest.SetTestSettings(s)
		}
	default:
		cfg.ConnectionString = gainTestStreamURL
		settings.Realtime.RTSP.Streams = []conf.StreamConfig{{
			Name: "gain-test", URL: gainTestStreamURL, Enabled: true, Type: conf.StreamTypeRTSP, Models: []string{"birdnet"},
		}}
		cfg.Transport = settings.Realtime.RTSP.ResolveTransport("")
		setGain = func(gainDB float64) {
			s := conf.CloneSettings(conf.GetSettings())
			s.Realtime.RTSP.Streams[0].Gain = gainDB
			conftest.SetTestSettings(s)
		}
	}
	conftest.SetTestSettings(settings)

	eng := enginepkg.New(t.Context(), &enginepkg.Config{}, nil)
	t.Cleanup(eng.Stop)
	src, err := eng.Registry().Register(cfg)
	require.NoError(t, err)

	consumer := newGainCaptureConsumer()
	require.NoError(t, eng.Router().AddRoute(src.ID, consumer, gainTestRateHz, 0, nil))
	return &gainFixture{engine: eng, consumer: consumer, sourceID: src.ID, setGain: setGain}
}

// dispatch sends gainTestPCM through the router and returns what the consumer got.
func (f *gainFixture) dispatch(t *testing.T) []byte {
	t.Helper()
	f.engine.Router().Dispatch(audiocore.AudioFrame{
		SourceID:   f.sourceID,
		SourceName: "gain-test",
		Data:       gainTestPCM,
		SampleRate: gainTestRateHz,
		BitDepth:   conf.BitDepth,
		Channels:   1,
	})
	select {
	case got := <-f.consumer.frames:
		return got
	case <-time.After(gainTestFrameTimeout):
		require.FailNow(t, "timed out waiting for a routed frame")
		return nil
	}
}

// boosted returns gainTestPCM scaled by gainTestNewGainDB.
func boosted(t *testing.T) []byte {
	t.Helper()
	g, err := dspgain.New(gainTestNewGainDB)
	require.NoError(t, err)
	out := make([]byte, len(gainTestPCM))
	require.NoError(t, g.ApplyBytesTo(out, gainTestPCM))
	return out
}

// TestApplySourceGains_UpdatesLiveRoutesWithoutProbing applies a changed
// configured gain to the running source's registry entry and its live route, for
// a stream and a sound card. The stream URL does not resolve, so a probe would
// show up as a log line and a delay, and no route may be rebuilt or closed.
func TestApplySourceGains_UpdatesLiveRoutesWithoutProbing(t *testing.T) {
	// Not parallel: the settings snapshot is global.
	for _, kind := range []audiocore.SourceType{audiocore.SourceTypeRTSP, audiocore.SourceTypeAudioCard} {
		t.Run(string(kind), func(t *testing.T) {
			f := newGainFixture(t, kind)
			p := &AudioPipelineService{engine: f.engine}

			require.Equal(t, gainTestPCM, f.dispatch(t), "setup: gain 0 must pass the frame through")

			f.setGain(gainTestNewGainDB)
			start := time.Now()
			p.applySourceGains()
			assert.Less(t, time.Since(start), gainTestFrameTimeout, "applying gains must not wait on a stream probe")

			gain, ok := f.engine.Registry().GetGain(f.sourceID)
			require.True(t, ok)
			assert.InDelta(t, gainTestNewGainDB, gain, 0, "registry gain must follow the settings")
			assert.True(t, f.engine.Router().HasConsumers(f.sourceID), "the route must stay registered")
			assert.False(t, f.consumer.isClosed(), "the consumer must not be closed")
			assert.Equal(t, boosted(t), f.dispatch(t), "the live route must apply the new gain")

			// A second call finds nothing to change.
			p.applySourceGains()
			assert.Equal(t, boosted(t), f.dispatch(t), "an unchanged gain must leave the route alone")
			assert.False(t, f.consumer.isClosed())
		})
	}
}

// TestApplySourceGains_RejectedGainKeepsPreviousGain pins that a configured gain
// the router rejects (NaN) leaves the registry and the live route on the gain
// they had.
func TestApplySourceGains_RejectedGainKeepsPreviousGain(t *testing.T) {
	f := newGainFixture(t, audiocore.SourceTypeAudioCard)
	p := &AudioPipelineService{engine: f.engine}

	f.setGain(gainTestNewGainDB)
	p.applySourceGains()
	require.Equal(t, boosted(t), f.dispatch(t), "setup: the valid gain must apply")

	f.setGain(math.NaN())
	p.applySourceGains()

	gain, ok := f.engine.Registry().GetGain(f.sourceID)
	require.True(t, ok)
	assert.InDelta(t, gainTestNewGainDB, gain, 0, "a rejected gain must not change the registry")
	assert.Equal(t, boosted(t), f.dispatch(t), "a rejected gain must leave the route on the previous gain")
}

// TestReconfigureChangedSources_GainOnlyChangeKeepsRoutes covers the diff
// branch: a reconfigure that runs for another reason and finds only a gain change
// must apply it live instead of rebuilding the routes.
func TestReconfigureChangedSources_GainOnlyChangeKeepsRoutes(t *testing.T) {
	for _, kind := range []audiocore.SourceType{audiocore.SourceTypeRTSP, audiocore.SourceTypeAudioCard} {
		t.Run(string(kind), func(t *testing.T) {
			f := newGainFixture(t, kind)
			p := &AudioPipelineService{engine: f.engine}

			f.setGain(gainTestNewGainDB)
			p.reconfigureChangedSources(make(chan audiocore.AudioLevelData))

			gain, ok := f.engine.Registry().GetGain(f.sourceID)
			require.True(t, ok)
			assert.InDelta(t, gainTestNewGainDB, gain, 0)
			require.True(t, f.engine.Router().HasConsumers(f.sourceID), "a gain-only change must keep the routes")
			assert.False(t, f.consumer.isClosed(), "a gain-only change must not close the consumers")
			assert.Equal(t, boosted(t), f.dispatch(t), "the kept route must apply the new gain")
		})
	}
}
