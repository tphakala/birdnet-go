package audiocore

import (
	"bytes"
	"math"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dsppcm "github.com/tphakala/go-audio-dsp/pcm"

	dspeq "github.com/tphakala/go-audio-dsp/equalizer"

	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
)

const (
	// gainUpdateSource is the source ID used by the live gain update tests.
	gainUpdateSource = "gain-src"

	// gainUpdateSrcRate and gainUpdateResampledRate are the source rate and the
	// rate of the resampled consumer in the live gain update tests.
	gainUpdateSrcRate       = 48000
	gainUpdateResampledRate = 32000

	// gainUpdateFrameSamples is long enough that the resampler emits output on
	// every frame, not only after its filter primes.
	gainUpdateFrameSamples = 4800

	// gainUpdateWaitTimeout bounds each wait for a delivered frame.
	gainUpdateWaitTimeout = 5 * time.Second

	// gainUpdateFlips is how many times the concurrent test flips the gain.
	gainUpdateFlips = 1000

	// gainUpdateFinalDB is set after the concurrent flips and differs from every
	// gain flipped between.
	gainUpdateFinalDB = 3.0

	// gainUpdateOverflowDB is a finite gain whose linear factor overflows float32.
	gainUpdateOverflowDB = 1000.0

	// gainUpdateHighPassHz is the corner of the EQ chain in the sequence test.
	gainUpdateHighPassHz = 200.0
)

// gainRefPipeline reproduces what one route does to a frame, with its own
// persistent resampler and EQ chain state, so a live route can be compared
// byte for byte against a stage-by-stage reference that sees the same frames.
type gainRefPipeline struct {
	rs    *resample.Resampler
	chain *dspeq.FilterChain
}

func newGainRefPipeline(t *testing.T, dstRate int, withChain bool) *gainRefPipeline {
	t.Helper()
	p := &gainRefPipeline{}
	if dstRate != gainUpdateSrcRate {
		rs, err := resample.NewResampler(gainUpdateSrcRate, dstRate)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, rs.Close()) })
		p.rs = rs
	}
	if withChain {
		p.chain = gainUpdateChain(t, dstRate)
	}
	return p
}

func gainUpdateChain(t *testing.T, rate int) *dspeq.FilterChain {
	t.Helper()
	return testChain(t, rate, dspeq.Band{Type: dspeq.HighPass, Frequency: gainUpdateHighPassHz, Q: 0.707, Passes: 1})
}

// process runs in through resample, EQ and gain and returns the PCM16 bytes.
func (p *gainRefPipeline) process(t *testing.T, in []byte, gainDB float64) []byte {
	t.Helper()
	var x []float32
	if p.rs != nil {
		out, err := p.rs.ResampleFloat32Into(in)
		require.NoError(t, err)
		x = slices.Clone(out)
	} else {
		x = make([]float32, len(in)/bytesPerPCM16Sample)
		_, err := dsppcm.BytesToFloat32(x, in)
		require.NoError(t, err)
	}
	if p.chain != nil {
		_, err := p.chain.ProcessInto(x, x)
		require.NoError(t, err)
	}
	if g := testGain(t, gainDB); g != nil {
		_, err := g.ProcessInto(x, x)
		require.NoError(t, err)
	}
	out := make([]byte, len(x)*bytesPerPCM16Sample)
	_, err := dsppcm.Float32ToBytes(out, x)
	require.NoError(t, err)
	return out
}

func gainUpdateFrame(data []byte) AudioFrame {
	return AudioFrame{
		SourceID:   gainUpdateSource,
		SourceName: "Gain Test",
		Data:       data,
		SampleRate: gainUpdateSrcRate,
		BitDepth:   bitDepthPCM16,
		Channels:   1,
	}
}

func waitForGainFrame(t *testing.T, c *mockConsumer) AudioFrame {
	t.Helper()
	select {
	case f := <-c.frames:
		return f
	case <-time.After(gainUpdateWaitTimeout):
		require.FailNow(t, "timed out waiting for a frame")
		return AudioFrame{}
	}
}

// TestRouter_UpdateGainAppliesToLiveRoutes steps one source's gain through
// 0, +6, 0, -6, an invalid NaN and +40 dB with an EQ chain on a passthrough and
// a resampled consumer. After every step each consumer's frame must equal an
// independent stage-by-stage reference at that gain; after the NaN the previous
// gain keeps applying.
func TestRouter_UpdateGainAppliesToLiveRoutes(t *testing.T) {
	t.Parallel()

	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(router.Close)

	pass := newMockConsumer("pass")
	res := newMockConsumer("res")
	res.sampleRate = gainUpdateResampledRate
	require.NoError(t, router.AddRoute(gainUpdateSource, pass, gainUpdateSrcRate, 0, gainUpdateChain(t, gainUpdateSrcRate)))
	require.NoError(t, router.AddRoute(gainUpdateSource, res, gainUpdateSrcRate, 0, gainUpdateChain(t, gainUpdateResampledRate)))

	refPass := newGainRefPipeline(t, gainUpdateSrcRate, true)
	refRes := newGainRefPipeline(t, gainUpdateResampledRate, true)
	input := int16sToBytes(noiseAndTone(gainUpdateFrameSamples, gainUpdateSrcRate))

	steps := []struct {
		name     string
		set      float64
		wantErr  bool
		wantGain float64
	}{
		{name: "initial 0 dB", set: 0, wantGain: 0},
		{name: "plus 6 dB", set: 6, wantGain: 6},
		{name: "back to 0 dB", set: 0, wantGain: 0},
		{name: "minus 6 dB", set: -6, wantGain: -6},
		{name: "NaN is rejected and keeps minus 6 dB", set: math.NaN(), wantErr: true, wantGain: -6},
		{name: "plus 40 dB", set: 40, wantGain: 40},
	}
	for _, st := range steps {
		err := router.UpdateGain(gainUpdateSource, st.set)
		if st.wantErr {
			require.Error(t, err, st.name)
		} else {
			require.NoError(t, err, st.name)
		}

		router.Dispatch(gainUpdateFrame(input))

		gotPass := waitForGainFrame(t, pass)
		gotRes := waitForGainFrame(t, res)
		assert.True(t, bytes.Equal(refPass.process(t, input, st.wantGain), gotPass.Data),
			"%s: passthrough consumer differs from the reference", st.name)
		assert.True(t, bytes.Equal(refRes.process(t, input, st.wantGain), gotRes.Data),
			"%s: resampled consumer differs from the reference", st.name)
	}
}

// TestRouter_UpdateGainZeroDBWithoutChainIsPassthrough pins invariant 8: with no
// EQ chain, 0 dB removes the gain stage and the bytes arrive unchanged, also
// after a non-zero gain was set and cleared.
func TestRouter_UpdateGainZeroDBWithoutChainIsPassthrough(t *testing.T) {
	t.Parallel()

	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(router.Close)

	consumer := newMockConsumer("c1")
	require.NoError(t, router.AddRoute(gainUpdateSource, consumer, gainUpdateSrcRate, 0, nil))
	input := int16sToBytes(noiseAndTone(gainUpdateFrameSamples, gainUpdateSrcRate))

	require.NoError(t, router.UpdateGain(gainUpdateSource, 6))
	router.Dispatch(gainUpdateFrame(input))
	boosted := waitForGainFrame(t, consumer)
	assert.False(t, bytes.Equal(input, boosted.Data), "+6 dB must change the audio")

	require.NoError(t, router.UpdateGain(gainUpdateSource, 0))
	router.Dispatch(gainUpdateFrame(input))
	restored := waitForGainFrame(t, consumer)
	assert.True(t, bytes.Equal(input, restored.Data), "0 dB without a chain must be a byte-exact passthrough")
}

// TestRouter_UpdateGainInvalidGainWithoutRoutesIsRejected pins that an unusable
// gain is an error even when the source has no route to reject it.
func TestRouter_UpdateGainInvalidGainWithoutRoutesIsRejected(t *testing.T) {
	t.Parallel()

	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(router.Close)

	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), gainUpdateOverflowDB} {
		require.Error(t, router.UpdateGain("no-such-source", bad), "gain %v", bad)
	}
}

// TestRouter_UpdateGainUnknownSourceIsNoOp checks that a source without routes
// is not an error and creates nothing.
func TestRouter_UpdateGainUnknownSourceIsNoOp(t *testing.T) {
	t.Parallel()

	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(router.Close)

	require.NoError(t, router.UpdateGain("no-such-source", 6))
	assert.False(t, router.HasConsumers("no-such-source"))
}

// TestRouter_UpdateGainKeepsRoutesAndConsumersOpen checks the point of the live
// update: the routes and their consumers survive, unclosed.
func TestRouter_UpdateGainKeepsRoutesAndConsumersOpen(t *testing.T) {
	t.Parallel()

	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(router.Close)

	a := newMockConsumer("a")
	b := newMockConsumer("b")
	require.NoError(t, router.AddRoute(gainUpdateSource, a, gainUpdateSrcRate, 0, nil))
	require.NoError(t, router.AddRoute(gainUpdateSource, b, gainUpdateSrcRate, 3, nil))

	idsBefore := routeConsumerIDs(router.Routes(gainUpdateSource))

	require.NoError(t, router.UpdateGain(gainUpdateSource, 6))
	require.NoError(t, router.UpdateGain(gainUpdateSource, 0))
	require.NoError(t, router.UpdateGain(gainUpdateSource, -6))

	assert.Equal(t, idsBefore, routeConsumerIDs(router.Routes(gainUpdateSource)), "routes must not be rebuilt")
	assert.False(t, a.closed.Load(), "consumer a must stay open")
	assert.False(t, b.closed.Load(), "consumer b must stay open")
}

func routeConsumerIDs(infos []RouteInfo) []string {
	ids := make([]string, 0, len(infos))
	for _, info := range infos {
		ids = append(ids, info.ConsumerID)
	}
	return ids
}

// TestRouter_UpdateGainConcurrentWithDispatch flips the gain between 0, +6 and
// -6 dB while frames are dispatched. Every delivered frame must equal exactly
// one of the three references, so a frame never mixes two gains. Run with -race.
func TestRouter_UpdateGainConcurrentWithDispatch(t *testing.T) {
	t.Parallel()

	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(router.Close)

	consumer := newMockConsumer("c1")
	require.NoError(t, router.AddRoute(gainUpdateSource, consumer, gainUpdateSrcRate, 0, nil))

	input := int16sToBytes(noiseAndTone(gainUpdateFrameSamples, gainUpdateSrcRate))
	gains := []float64{0, 6, -6}
	refs := make([][]byte, len(gains))
	for i, g := range gains {
		refs[i] = newGainRefPipeline(t, gainUpdateSrcRate, false).process(t, input, g)
	}

	stop := make(chan struct{})
	flipErr := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range gainUpdateFlips {
			if err := router.UpdateGain(gainUpdateSource, gains[i%len(gains)]); err != nil {
				flipErr <- err
				break
			}
		}
		close(stop)
	})
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				router.Dispatch(gainUpdateFrame(input))
			}
		}
	})

	delivered := 0
	for done := false; !done; {
		select {
		case f := <-consumer.frames:
			delivered++
			matched := false
			for _, ref := range refs {
				if bytes.Equal(ref, f.Data) {
					matched = true
					break
				}
			}
			assert.True(t, matched, "delivered frame %d equals none of the three gain references", delivered)
		case <-stop:
			done = true
		case <-time.After(gainUpdateWaitTimeout):
			require.FailNow(t, "timed out waiting for frames")
		}
	}
	wg.Wait()
	select {
	case err := <-flipErr:
		require.NoError(t, err, "UpdateGain must accept every gain in the flip sequence")
	default:
	}
	assert.Positive(t, delivered, "at least one frame must be delivered during the flips")

	// A frame path that read the gain twice could dereference a nil gain while
	// the flips switch to 0 dB; the drainer recovers from that panic and retires
	// the route. This is a best-effort check for that: the route must still be
	// registered and still deliver frames, now at a gain set after the flips.
	require.True(t, router.HasConsumers(gainUpdateSource), "the route must survive the gain flips")
	assert.False(t, consumer.closed.Load(), "the consumer must not have been closed by a drainer panic")
	// Set one more gain, distinct from the three flipped between, so no frame
	// queued during the flips can match it. Dispatch inside the wait: a frame can
	// be dropped while the inbox is still full of queued frames.
	require.NoError(t, router.UpdateGain(gainUpdateSource, gainUpdateFinalDB))
	want := newGainRefPipeline(t, gainUpdateSrcRate, false).process(t, input, gainUpdateFinalDB)
	require.Eventually(t, func() bool {
		router.Dispatch(gainUpdateFrame(input))
		for {
			select {
			case f := <-consumer.frames:
				if bytes.Equal(want, f.Data) {
					return true
				}
			default:
				return false
			}
		}
	}, gainUpdateWaitTimeout, time.Millisecond, "a frame carrying the gain set after the flips must arrive")
}

// TestRouter_UpdateGainOnResampledRouteWithoutChain covers the common production
// route: resampled, no EQ. Moving it between 0 dB (the byte resample path) and a
// non-zero gain (the float handoff path) must follow each step.
func TestRouter_UpdateGainOnResampledRouteWithoutChain(t *testing.T) {
	t.Parallel()

	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(router.Close)

	res := newMockConsumer("res")
	res.sampleRate = gainUpdateResampledRate
	require.NoError(t, router.AddRoute(gainUpdateSource, res, gainUpdateSrcRate, 0, nil))

	ref := newGainRefPipeline(t, gainUpdateResampledRate, false)
	input := int16sToBytes(noiseAndTone(gainUpdateFrameSamples, gainUpdateSrcRate))

	for _, db := range []float64{0, 6, 0, -6, 0} {
		require.NoError(t, router.UpdateGain(gainUpdateSource, db))
		router.Dispatch(gainUpdateFrame(input))
		got := waitForGainFrame(t, res)
		assert.True(t, bytes.Equal(ref.process(t, input, db), got.Data), "gain %g dB: resampled route differs from the reference", db)
	}
}
