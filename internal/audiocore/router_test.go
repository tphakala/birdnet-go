package audiocore

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	dspeq "github.com/tphakala/go-audio-dsp/equalizer"
)

// mockConsumer implements AudioConsumer for testing.
type mockConsumer struct {
	id         string
	sampleRate int
	bitDepth   int
	channels   int
	frames     chan AudioFrame
	closed     atomic.Bool
}

func newMockConsumer(id string) *mockConsumer {
	return &mockConsumer{
		id:         id,
		sampleRate: 48000,
		bitDepth:   16,
		channels:   1,
		frames:     make(chan AudioFrame, 64),
	}
}

func (m *mockConsumer) ID() string      { return m.id }
func (m *mockConsumer) SampleRate() int { return m.sampleRate }
func (m *mockConsumer) BitDepth() int   { return m.bitDepth }
func (m *mockConsumer) Channels() int   { return m.channels }
func (m *mockConsumer) Close() error    { m.closed.Store(true); return nil }

func (m *mockConsumer) Write(frame AudioFrame) error { //nolint:gocritic // hugeParam: signature required by AudioConsumer interface
	if m.closed.Load() {
		return ErrConsumerClosed
	}
	m.frames <- frame
	return nil
}

// blockingConsumer never reads from its frames channel, simulating a slow consumer.
// Tests that need to clean up promptly can call unblock() which closes the
// unblockCh, causing Write to return and the drainer to exit cleanly.
type blockingConsumer struct {
	mockConsumer
	unblockCh chan struct{}
}

func newBlockingConsumer(id string) *blockingConsumer {
	return &blockingConsumer{
		mockConsumer: mockConsumer{
			id:         id,
			sampleRate: 48000,
			bitDepth:   16,
			channels:   1,
			frames:     make(chan AudioFrame), // unbuffered; Write blocks until unblock
		},
		unblockCh: make(chan struct{}),
	}
}

func (b *blockingConsumer) Write(frame AudioFrame) error { //nolint:gocritic // hugeParam: signature required by AudioConsumer interface
	// Block until unblock() is called; simulates a permanently stalled
	// consumer for the duration of the test.
	<-b.unblockCh
	return nil
}

// unblock releases any in-flight Write calls. Safe to call at most once.
func (b *blockingConsumer) unblock() {
	close(b.unblockCh)
}

func testFrame(sourceID string) AudioFrame {
	return AudioFrame{
		SourceID:   sourceID,
		SourceName: "Test Source",
		Data:       []byte{0x01, 0x02, 0x03, 0x04},
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}
}

// TestRouter_AddAndRemoveRoute verifies that a route can be added and then
// removed, with HasConsumers reflecting the correct state at each step.
func TestRouter_AddAndRemoveRoute(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(func() { router.Close() })

	consumer := newMockConsumer("consumer-1")

	err := router.AddRoute("src-1", consumer, 48000, 0.0, nil)
	require.NoError(t, err)
	assert.True(t, router.HasConsumers("src-1"))

	router.RemoveRoute("src-1", "consumer-1")
	assert.False(t, router.HasConsumers("src-1"))
	assert.True(t, consumer.closed.Load(), "consumer should be closed after route removal")
}

// TestRouter_DispatchSingleConsumer verifies that a dispatched frame reaches
// a single registered consumer.
func TestRouter_DispatchSingleConsumer(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(func() { router.Close() })

	consumer := newMockConsumer("consumer-1")
	err := router.AddRoute("src-1", consumer, 48000, 0.0, nil)
	require.NoError(t, err)

	frame := testFrame("src-1")
	router.Dispatch(frame)

	select {
	case received := <-consumer.frames:
		assert.Equal(t, frame.SourceID, received.SourceID)
		assert.Equal(t, frame.Data, received.Data)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for frame delivery")
	}
}

// TestRouter_DispatchFanOut verifies that a dispatched frame reaches all
// consumers registered for the same source.
func TestRouter_DispatchFanOut(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(func() { router.Close() })

	c1 := newMockConsumer("consumer-1")
	c2 := newMockConsumer("consumer-2")

	require.NoError(t, router.AddRoute("src-1", c1, 48000, 0.0, nil))
	require.NoError(t, router.AddRoute("src-1", c2, 48000, 0.0, nil))

	frame := testFrame("src-1")
	router.Dispatch(frame)

	for _, c := range []*mockConsumer{c1, c2} {
		select {
		case received := <-c.frames:
			assert.Equal(t, frame.SourceID, received.SourceID)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for frame on consumer %s", c.ID())
		}
	}
}

// TestRouter_DispatchNoConsumers verifies that dispatching to a source with
// no registered routes does not panic.
func TestRouter_DispatchNoConsumers(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(func() { router.Close() })

	// Should not panic.
	assert.NotPanics(t, func() {
		router.Dispatch(testFrame("no-such-source"))
	})
}

// TestRouter_DropOnFullInbox verifies that when a consumer's inbox is full,
// the frame is dropped and the drop counter increments.
func TestRouter_DropOnFullInbox(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(func() { router.Close() })

	consumer := newBlockingConsumer("slow-consumer")
	require.NoError(t, router.AddRoute("src-1", consumer, 48000, 0.0, nil))
	t.Cleanup(consumer.unblock)

	// Fill the inbox buffer (capacity 64) plus extra to guarantee drops.
	const totalFrames = 128
	for range totalFrames {
		router.Dispatch(testFrame("src-1"))
	}

	// Check that the route's drop counter is > 0.
	routes := router.Routes("src-1")
	require.Len(t, routes, 1)
	assert.Positive(t, routes[0].Drops, "drop counter should be positive for overflowing consumer")
}

// TestRouter_RemoveAllRoutes verifies that RemoveAllRoutes removes every
// route for a given source.
func TestRouter_RemoveAllRoutes(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(func() { router.Close() })

	c1 := newMockConsumer("consumer-1")
	c2 := newMockConsumer("consumer-2")

	require.NoError(t, router.AddRoute("src-1", c1, 48000, 0.0, nil))
	require.NoError(t, router.AddRoute("src-1", c2, 48000, 0.0, nil))
	assert.True(t, router.HasConsumers("src-1"))

	router.RemoveAllRoutes("src-1")
	assert.False(t, router.HasConsumers("src-1"))
	assert.True(t, c1.closed.Load(), "consumer-1 should be closed")
	assert.True(t, c2.closed.Load(), "consumer-2 should be closed")
}

// TestRouter_DuplicateRouteError verifies that adding the same consumer ID
// twice for the same source returns ErrRouteExists.
func TestRouter_DuplicateRouteError(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(func() { router.Close() })

	c1 := newMockConsumer("consumer-1")
	c2 := newMockConsumer("consumer-1") // same ID

	require.NoError(t, router.AddRoute("src-1", c1, 48000, 0.0, nil))

	err := router.AddRoute("src-1", c2, 48000, 0.0, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRouteExists)
}

// TestRouter_DispatchWithResampling verifies that when a consumer's sample rate
// differs from the source rate, the router resamples the frame before delivery.
// The consumer should receive a frame with the consumer's sample rate and
// resampled data (approximately 2/3 the length for a 48kHz→32kHz conversion).
func TestRouter_DispatchWithResampling(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(func() { router.Close() })

	// Consumer expects 32kHz; source produces 48kHz.
	consumer := newMockConsumer("consumer-resampled")
	consumer.sampleRate = 32000

	// Add route with source at 48kHz.
	err := router.AddRoute("src-resample", consumer, 48000, 0.0, nil)
	require.NoError(t, err)

	// Build a 48kHz frame with 4800 samples (100 ms of 16-bit PCM, 9600 bytes).
	// This matches the minimum size used in the resample package tests to stay
	// within the ±5% resampler tolerance window.
	// Expected output ≈ 3200 samples (2/3 of 4800), allowing ±5% tolerance.
	const inputSamples = 4800
	const bytesPerSample = 2
	inputData := make([]byte, inputSamples*bytesPerSample)
	// Fill with a simple ramp pattern so the resampler has non-trivial input.
	for i := range inputSamples {
		v := int16((i % 65536) - 32768) //nolint:gosec // G115: intentional narrowing for test data
		inputData[i*bytesPerSample] = byte(v)
		inputData[i*bytesPerSample+1] = byte(v >> 8)
	}

	frame := AudioFrame{
		SourceID:   "src-resample",
		SourceName: "Resample Test Source",
		Data:       inputData,
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}
	router.Dispatch(frame)

	select {
	case received := <-consumer.frames:
		assert.Equal(t, "src-resample", received.SourceID, "SourceID should be preserved")
		assert.Equal(t, "Resample Test Source", received.SourceName, "SourceName should be preserved")
		assert.Equal(t, 32000, received.SampleRate, "consumer should receive frame at its own sample rate")
		// Resampled data should be approximately 2/3 of the input length.
		// Allow ±5% tolerance to match the resampler package's own tolerance.
		expectedSamples := inputSamples * 32000 / 48000
		tolerance := float64(expectedSamples) * 0.05
		outputSamples := len(received.Data) / bytesPerSample
		assert.InDelta(t, expectedSamples, outputSamples, tolerance,
			"resampled sample count should be approximately 2/3 of input")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for resampled frame delivery")
	}
}

// TestRouter_ConcurrentDispatch verifies that concurrent dispatch from
// multiple goroutines does not trigger data races (run with -race).
// Uses testing/synctest so the drain assertion does not race the per-route
// drainer goroutine on loaded CI runners.
func TestRouter_ConcurrentDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router := NewAudioRouter(GetLogger(), nil)
		// Close inside the bubble so the drainer goroutines exit before
		// synctest.Test waits for bubble goroutines to finish; t.Cleanup
		// runs after synctest.Test returns and would deadlock.
		defer router.Close()

		c1 := newMockConsumer("consumer-1")
		c2 := newMockConsumer("consumer-2")
		require.NoError(t, router.AddRoute("src-1", c1, 48000, 0.0, nil))
		require.NoError(t, router.AddRoute("src-1", c2, 48000, 0.0, nil))

		const goroutines = 8
		const framesPerGoroutine = 100

		var wg sync.WaitGroup
		for range goroutines {
			wg.Go(func() {
				for range framesPerGoroutine {
					router.Dispatch(testFrame("src-1"))
				}
			})
		}
		wg.Wait()

		// Drain both consumer channels until the drainer goroutines are
		// blocked on empty inboxes. synctest.Wait lets the drainer make
		// progress; each outer iteration that pulls zero frames proves
		// the drainers are parked on the select rather than on a full
		// consumer channel, so forwarding is complete.
		d1, d2 := 0, 0
		for {
			synctest.Wait()
			got := 0
		drain:
			for {
				select {
				case <-c1.frames:
					d1++
					got++
				case <-c2.frames:
					d2++
					got++
				default:
					break drain
				}
			}
			if got == 0 {
				break
			}
		}
		assert.Positive(t, d1, "c1 should receive at least one frame")
		assert.Positive(t, d2, "c2 should receive at least one frame")
	})
}

func TestDrainRoutePanicRecovery(t *testing.T) {
	t.Parallel()

	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(func() { router.Close() })

	panicConsumer := &panicOnWriteConsumer{
		id:         "panic-consumer",
		sampleRate: 48000,
	}

	err := router.AddRoute("src1", panicConsumer, 48000, 0.0, nil)
	require.NoError(t, err)

	// Dispatch a frame: the consumer will panic on Write.
	router.Dispatch(AudioFrame{
		SourceID:   "src1",
		Data:       make([]byte, 100),
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	})

	// Wait for drainer to process the frame, recover, and exit.
	time.Sleep(200 * time.Millisecond)

	// The drainer exited on panic: the route's stopped channel is closed,
	// and subsequent dispatches to its inbox are silently dropped.
	// Verify the process didn't crash (panic was recovered).
}

// panicOnWriteConsumer always panics on Write.
type panicOnWriteConsumer struct {
	id         string
	sampleRate int
}

func (c *panicOnWriteConsumer) ID() string      { return c.id }
func (c *panicOnWriteConsumer) SampleRate() int { return c.sampleRate }
func (c *panicOnWriteConsumer) BitDepth() int   { return 16 }
func (c *panicOnWriteConsumer) Channels() int   { return 1 }
func (c *panicOnWriteConsumer) Close() error    { return nil }
func (c *panicOnWriteConsumer) Write(_ AudioFrame) error { //nolint:gocritic // hugeParam: signature required by AudioConsumer interface
	panic("consumer exploded")
}

// panicOnWriteCloseTracker panics on Write and tracks whether Close was called.
type panicOnWriteCloseTracker struct {
	id         string
	sampleRate int
	closed     atomic.Bool
}

func (c *panicOnWriteCloseTracker) ID() string      { return c.id }
func (c *panicOnWriteCloseTracker) SampleRate() int { return c.sampleRate }
func (c *panicOnWriteCloseTracker) BitDepth() int   { return 16 }
func (c *panicOnWriteCloseTracker) Channels() int   { return 1 }
func (c *panicOnWriteCloseTracker) Close() error    { c.closed.Store(true); return nil }
func (c *panicOnWriteCloseTracker) Write(_ AudioFrame) error { //nolint:gocritic // hugeParam: signature required by AudioConsumer interface
	panic("consumer exploded")
}

// TestDrainRoutePanicCallsClose verifies that when a consumer panics during
// Write, the panic recovery handler still calls Close() on the consumer so
// that resources (file descriptors, CGO allocations) are released.
func TestDrainRoutePanicCallsClose(t *testing.T) {
	t.Parallel()

	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(func() { router.Close() })

	consumer := &panicOnWriteCloseTracker{
		id:         "panic-close-tracker",
		sampleRate: 48000,
	}

	err := router.AddRoute("src1", consumer, 48000, 0.0, nil)
	require.NoError(t, err)

	// Dispatch a frame; the consumer will panic on Write, triggering recovery.
	router.Dispatch(AudioFrame{
		SourceID:   "src1",
		Data:       make([]byte, 100),
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	})

	// The drainer should recover from the panic and call Close().
	require.Eventually(t, func() bool {
		return consumer.closed.Load()
	}, 2*time.Second, 10*time.Millisecond,
		"expected Close() to be called on consumer after panic recovery")
}

// TestRouter_DrainRouteAppliesGain verifies that per-route gain amplifies,
// attenuates, or leaves audio data unchanged depending on the dB value.
func TestRouter_DrainRouteAppliesGain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		gainDB     float64
		inputPCM   []int16
		wantScaled bool // true if output should differ from input
	}{
		{
			name:       "positive_gain_amplifies",
			gainDB:     6.0,
			inputPCM:   []int16{1000, -1000, 500, -500},
			wantScaled: true,
		},
		{
			name:       "negative_gain_attenuates",
			gainDB:     -6.0,
			inputPCM:   []int16{1000, -1000, 500, -500},
			wantScaled: true,
		},
		{
			name:       "zero_gain_no_change",
			gainDB:     0.0,
			inputPCM:   []int16{1000, -1000, 500, -500},
			wantScaled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			router := NewAudioRouter(GetLogger(), nil)
			defer router.Close()

			consumer := newMockConsumer("c1")
			err := router.AddRoute("src-1", consumer, 48000, tt.gainDB, nil)
			require.NoError(t, err)

			// Build PCM byte data from int16 samples.
			inputBytes := make([]byte, len(tt.inputPCM)*2)
			for i, s := range tt.inputPCM {
				inputBytes[i*2] = byte(s)
				inputBytes[i*2+1] = byte(s >> 8)
			}

			router.Dispatch(AudioFrame{
				SourceID:   "src-1",
				SourceName: "Test",
				Data:       inputBytes,
				SampleRate: 48000,
				BitDepth:   16,
				Channels:   1,
			})

			var received AudioFrame
			select {
			case received = <-consumer.frames:
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for frame")
			}

			if tt.wantScaled {
				assert.NotEqual(t, inputBytes, received.Data,
					"gain %.1f dB should change the audio data", tt.gainDB)
			} else {
				assert.Equal(t, inputBytes, received.Data,
					"0 dB gain should leave audio data unchanged")
			}
		})
	}
}

// TestRouter_GainSaturatesToInt16Range verifies that a gain large enough to
// overflow the int16 range saturates at exactly 32767 and -32768 (the library
// encoder scales by 32768 and saturates) rather than wrapping or losing the
// negative extreme.
func TestRouter_GainSaturatesToInt16Range(t *testing.T) {
	t.Parallel()

	router := NewAudioRouter(GetLogger(), nil)
	defer router.Close()

	consumer := newMockConsumer("c1")
	// +40 dB is 100x linear: will clip a signal near max.
	err := router.AddRoute("src-1", consumer, 48000, 40.0, nil)
	require.NoError(t, err)

	// Input: near-max signal (30000 out of 32767).
	inputPCM := []int16{30000, -30000}
	inputBytes := make([]byte, len(inputPCM)*2)
	for i, s := range inputPCM {
		inputBytes[i*2] = byte(s)
		inputBytes[i*2+1] = byte(s >> 8)
	}

	router.Dispatch(AudioFrame{
		SourceID:   "src-1",
		SourceName: "Test",
		Data:       inputBytes,
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
	})

	var received AudioFrame
	select {
	case received = <-consumer.frames:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for frame")
	}

	// Verify output saturates at the int16 range, not corrupted.
	require.Len(t, received.Data, 4)
	sample0 := int16(received.Data[0]) | int16(received.Data[1])<<8
	sample1 := int16(received.Data[2]) | int16(received.Data[3])<<8

	assert.Equal(t, math.MaxInt16, int(sample0), "positive sample should saturate to 32767")
	assert.Equal(t, math.MinInt16, int(sample1), "negative sample should saturate to -32768")
}

// TestRouter_AddRouteWithGain verifies that AddRoute stores a go-audio-dsp gain
// on the Route for a non-zero dB value and no gain stage for 0 dB.
func TestRouter_AddRouteWithGain(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		gainDB float64
	}{
		{"zero_dB_no_change", 0.0},
		{"positive_6dB", 6.0},
		{"negative_6dB", -6.0},
		{"max_40dB", 40.0},
		{"min_neg40dB", -40.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := NewAudioRouter(GetLogger(), nil)
			t.Cleanup(func() { router.Close() })
			consumer := newMockConsumer("c1")
			err := router.AddRoute("src-1", consumer, 48000, tt.gainDB, nil)
			require.NoError(t, err)
			router.mu.RLock()
			routes := router.routes["src-1"]
			require.Len(t, routes, 1)
			if tt.gainDB == 0 {
				assert.Nil(t, routes[0].gain.Load(), "0 dB must add no gain stage")
			} else if assert.NotNil(t, routes[0].gain.Load()) {
				assert.InDelta(t, tt.gainDB, routes[0].gain.Load().GainDB(), 0)
			}
			router.mu.RUnlock()
		})
	}
}

// TestRouter_AddRouteRejectsInvalidGain verifies that AddRoute refuses a gain
// go-audio-dsp cannot represent (NaN, infinities, a factor that overflows
// float32) and registers no route for it.
func TestRouter_AddRouteRejectsInvalidGain(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		gainDB float64
	}{
		{"nan", math.NaN()},
		{"positive_inf", math.Inf(1)},
		{"negative_inf", math.Inf(-1)},
		{"factor_overflows_float32", 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := NewAudioRouter(GetLogger(), nil)
			t.Cleanup(router.Close)
			err := router.AddRoute("src-1", newMockConsumer("c1"), 48000, tt.gainDB, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid gain value")
			assert.False(t, router.HasConsumers("src-1"), "a rejected gain must not register a route")
		})
	}
}

func TestRouter_UpdateFilterChain(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(func() { router.Close() })

	consumer := newMockConsumer("c1")
	require.NoError(t, router.AddRoute("src-1", consumer, 48000, 0.0, nil))

	// Initially nil.
	router.mu.RLock()
	routes := router.routes["src-1"]
	require.Len(t, routes, 1)
	assert.Nil(t, routes[0].filterChain.Load(), "initial chain should be nil")
	router.mu.RUnlock()

	// Build a chain via builder and update.
	router.UpdateFilterChain("src-1", func(sampleRate int) *dspeq.FilterChain {
		chain := dspeq.NewFilterChain()
		hp, hpErr := dspeq.NewFilter(dspeq.Band{Type: dspeq.HighPass, Frequency: 100, Q: 0.707, Passes: 1}, sampleRate)
		require.NoError(t, hpErr)
		require.NoError(t, chain.AddFilter(hp))
		return chain
	})

	router.mu.RLock()
	routes = router.routes["src-1"]
	loaded := routes[0].filterChain.Load()
	assert.NotNil(t, loaded, "chain should be set after update")
	assert.Equal(t, 1, loaded.Len())
	router.mu.RUnlock()

	// Update to nil (disable EQ).
	router.UpdateFilterChain("src-1", func(_ int) *dspeq.FilterChain {
		return nil
	})

	router.mu.RLock()
	routes = router.routes["src-1"]
	assert.Nil(t, routes[0].filterChain.Load(), "chain should be nil after disable")
	router.mu.RUnlock()
}

// TestRouter_ApplyProcessing_EQOnly verifies that a frame is filtered by the
// EQ chain when gain is unity (1.0) and a filter chain is set.
func TestRouter_ApplyProcessing_EQOnly(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(func() { router.Close() })

	consumer := newMockConsumer("c1")

	// Build a HighPass at 8000 Hz: should strongly attenuate a 100 Hz tone.
	chain := dspeq.NewFilterChain()
	hp, err := dspeq.NewFilter(dspeq.Band{Type: dspeq.HighPass, Frequency: 8000, Q: 0.707, Passes: 2}, 48000)
	require.NoError(t, err)
	require.NoError(t, chain.AddFilter(hp))

	require.NoError(t, router.AddRoute("src-eq", consumer, 48000, 0.0, chain))

	// Generate a 100 Hz sine wave as 16-bit PCM (480 samples = 10ms at 48kHz).
	const numSamples = 480
	input := make([]byte, numSamples*2)
	for i := range numSamples {
		// 100 Hz sine at ~50% amplitude.
		val := int16(16000 * math.Sin(2*math.Pi*100*float64(i)/48000))
		input[i*2] = byte(val)
		input[i*2+1] = byte(val >> 8)
	}

	frame := AudioFrame{
		SourceID:   "src-eq",
		SourceName: "test",
		Data:       input,
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
	}
	router.Dispatch(frame)

	// Wait for the consumer to receive the filtered frame.
	var received AudioFrame
	select {
	case received = <-consumer.frames:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for filtered frame")
	}

	// The 100 Hz signal should be heavily attenuated by the 8 kHz highpass.
	inputRMS := rmsOfPCM16(input)
	outputRMS := rmsOfPCM16(received.Data)
	assert.Less(t, outputRMS, inputRMS*0.1,
		"8 kHz highpass should attenuate 100 Hz signal by >90%%")
}

// TestRouter_ApplyProcessing_EQAndGain verifies that EQ and gain are both
// applied in a single conversion pass.
func TestRouter_ApplyProcessing_EQAndGain(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(func() { router.Close() })

	consumer := newMockConsumer("c1")

	// A -12 dB low shelf at 1 kHz cuts a 100 Hz tone to 0.25x amplitude; the
	// +6 dB route gain then raises it to ~0.5x. Without the EQ the output would
	// be ~2x the input and without the gain ~0.25x, so both stages are pinned.
	chain := dspeq.NewFilterChain()
	shelf, err := dspeq.NewFilter(dspeq.Band{Type: dspeq.LowShelf, Frequency: 1000, Q: 0.707, GainDB: -12}, 48000)
	require.NoError(t, err)
	require.NoError(t, chain.AddFilter(shelf))

	require.NoError(t, router.AddRoute("src-both", consumer, 48000, 6.0, chain))

	// 100 Hz sine, well below the 1 kHz shelf corner: receives the full -12 dB.
	const numSamples = 480
	input := make([]byte, numSamples*2)
	for i := range numSamples {
		val := int16(8000 * math.Sin(2*math.Pi*100*float64(i)/48000))
		input[i*2] = byte(val)
		input[i*2+1] = byte(val >> 8)
	}

	frame := AudioFrame{
		SourceID:   "src-both",
		SourceName: "test",
		Data:       input,
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
	}
	router.Dispatch(frame)

	var received AudioFrame
	select {
	case received = <-consumer.frames:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for processed frame")
	}

	// -12 dB shelf (x0.25) then +6 dB gain (x2) gives x0.5.
	inputRMS := rmsOfPCM16(input)
	outputRMS := rmsOfPCM16(received.Data)
	assert.InDelta(t, inputRMS*0.5, outputRMS, inputRMS*0.05,
		"output should be ~0.5x input (-12 dB shelf then +6 dB gain)")
}

// processingBenchCase is one steady-state processing configuration shared by
// the applyProcessing benchmarks and the allocation regression table.
type processingBenchCase struct {
	name   string
	bands  []dspeq.Band
	gainDB float64
}

// processingBenchCases are the applyProcessing configurations: gain only (the
// byte fast path), EQ only, and EQ plus gain (the float32 path).
var processingBenchCases = []processingBenchCase{
	{name: "gain_only", gainDB: 6.020599913279624},
	{name: "eq_only", bands: benchEQBands},
	{name: "eq_and_gain", bands: benchEQBands, gainDB: 6.020599913279624},
}

// benchEQBands is a three-band EQ at 48 kHz: high-pass, low shelf and peaking.
var benchEQBands = []dspeq.Band{
	{Type: dspeq.HighPass, Frequency: 100, Q: 0.707, Passes: 2},
	{Type: dspeq.LowShelf, Frequency: 200, Q: 0.707, GainDB: -6},
	{Type: dspeq.Peaking, Frequency: 3000, WidthHz: 1500, GainDB: 4},
}

// benchProcessingFrame is 2880 bytes = 1440 samples = 30 ms of 48 kHz mono
// 16-bit PCM, the size of the live pipeline's frames.
func benchProcessingFrame() AudioFrame {
	return AudioFrame{
		SourceID:   "bench",
		SourceName: "bench",
		Data:       make([]byte, refFrameSamples*bytesPerPCM16Sample),
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
	}
}

// BenchmarkApplyProcessing_PoolWarm measures steady-state per-frame allocations
// on the gain-only applyProcessing hot path with a fully wired buffer.Manager.
// The pools are warmed before the timed loop so the reported allocs/op reflects
// recycling behaviour, not first-call pool misses. The EQ variants follow in
// BenchmarkApplyProcessing_EQ and BenchmarkApplyProcessing_EQAndGain.
func BenchmarkApplyProcessing_PoolWarm(b *testing.B) {
	benchApplyProcessing(b, processingBenchCases[0])
}

// BenchmarkApplyProcessing_EQ is the EQ-only (float32 path) counterpart of
// BenchmarkApplyProcessing_PoolWarm.
func BenchmarkApplyProcessing_EQ(b *testing.B) {
	benchApplyProcessing(b, processingBenchCases[1])
}

// BenchmarkApplyProcessing_EQAndGain is the EQ plus gain counterpart of
// BenchmarkApplyProcessing_PoolWarm.
func BenchmarkApplyProcessing_EQAndGain(b *testing.B) {
	benchApplyProcessing(b, processingBenchCases[2])
}

func benchApplyProcessing(b *testing.B, tc processingBenchCase) {
	b.Helper()
	mgr := buffer.NewManager(GetLogger())
	r := NewAudioRouter(GetLogger(), mgr)
	defer r.Close()

	route := &Route{}
	route.gain.Store(testGain(b, tc.gainDB))
	var chain *dspeq.FilterChain
	if tc.bands != nil {
		chain = testChain(b, 48000, tc.bands...)
	}
	frame := benchProcessingFrame()

	// Warm the pools so steady-state pool reuse is established before timing.
	for range 4 {
		res, err := r.applyProcessing(frame, route, chain, route.gain.Load())
		if err != nil {
			b.Fatalf("warm-up applyProcessing failed: %v", err)
		}
		res.release()
	}

	b.ReportAllocs()

	for b.Loop() {
		res, err := r.applyProcessing(frame, route, chain, route.gain.Load())
		if err != nil {
			b.Fatal(err)
		}
		res.release()
	}
}

// TestApplyProcessing_AllocationRegression guards against allocation regressions
// on the processing hot paths. It measures steady-state allocs/op after pool
// warm-up and asserts they stay within each case's cap.
//
// Do NOT add t.Parallel() here: sync.Pool is stateful and parallel execution
// could pollute pool state during the alloc-sensitive measurement window.
func TestApplyProcessing_AllocationRegression(t *testing.T) {
	// The baseline is not zero because sync.Pool.Put boxes the slice header into
	// an interface{} value (see the SA6002 nolint comment in
	// internal/audiocore/buffer/pool.go), costing one small allocation per Put.
	// Under -race with parallel tests, the GC may collect pooled items causing
	// up to 2 additional pool-miss allocs per call; every cap is the measured
	// baseline plus 2 so that noise is tolerated while a real regression (an
	// extra make(), a lost pool) still trips it.
	//
	// Measured baselines (benchmark, warm, isolated, -count=3):
	//   gain_only            1 alloc/op (the output BytePool Put)
	//   eq_only              2 allocs/op (float32 and output Puts)
	//   eq_and_gain          2 allocs/op
	//   resample_then_eq     2 allocs/op via handleRouteFrame (float32 and output Puts)
	// Increase a cap only after investigating WHY allocations grew.
	const allocNoiseAllowance = 2

	for i, tc := range processingBenchCases {
		baselines := []int{1, 2, 2}
		t.Run(tc.name, func(t *testing.T) {
			mgr := buffer.NewManager(GetLogger())
			r := NewAudioRouter(GetLogger(), mgr)
			defer r.Close()

			route := &Route{}
			route.gain.Store(testGain(t, tc.gainDB))
			var chain *dspeq.FilterChain
			if tc.bands != nil {
				chain = testChain(t, 48000, tc.bands...)
			}
			frame := benchProcessingFrame()

			for range 4 {
				res, err := r.applyProcessing(frame, route, chain, route.gain.Load())
				require.NoError(t, err)
				res.release()
			}

			allocs := testing.AllocsPerRun(100, func() {
				res, err := r.applyProcessing(frame, route, chain, route.gain.Load())
				if err != nil {
					panic(err)
				}
				res.release()
			})

			maxAllowed := baselines[i] + allocNoiseAllowance
			assert.LessOrEqual(t, int(allocs), maxAllowed,
				"applyProcessing allocations per call exceed the regression cap of %d (got %.0f)",
				maxAllowed, allocs)
		})
	}

	t.Run("resample_then_eq", func(t *testing.T) {
		const (
			srcRate  = 256000
			dstRate  = 48000
			baseline = 2
		)
		mgr := buffer.NewManager(GetLogger())
		r := NewAudioRouter(GetLogger(), mgr)
		defer r.Close()

		consumer := newMockConsumer("alloc-resample")
		consumer.sampleRate = dstRate
		route := newAlignTestRoute(t, consumer, alignRunOpts{srcRate: srcRate, dstRate: dstRate, bitDepth: 16})
		route.filterChain.Store(testChain(t, dstRate, benchEQBands...))
		frame := AudioFrame{
			SourceID: "bench", SourceName: "bench",
			Data: int16sToBytes(noiseAndTone(srcRate/10, srcRate)), SampleRate: srcRate,
			BitDepth: 16, Channels: 1,
		}
		run := func() {
			r.handleRouteFrame(frame, route)
			<-consumer.frames
		}
		for range 4 {
			run()
		}

		allocs := testing.AllocsPerRun(100, run)

		maxAllowed := baseline + allocNoiseAllowance
		assert.LessOrEqual(t, int(allocs), maxAllowed,
			"handleRouteFrame allocations per call exceed the regression cap of %d (got %.0f)",
			maxAllowed, allocs)
	})
}

// rmsOfPCM16 computes the RMS of 16-bit little-endian PCM samples.
func rmsOfPCM16(data []byte) float64 {
	n := len(data) / 2
	if n == 0 {
		return 0
	}
	var sumSq float64
	for i := range n {
		sample := float64(int16(data[i*2]) | int16(data[i*2+1])<<8)
		sumSq += sample * sample
	}
	return math.Sqrt(sumSq / float64(n))
}

// TestRouter_Dispatch_RefZeroRoutes verifies that when a frame has no
// registered routes, the producer's own release is sufficient to fire the
// closure (no retain/release imbalance on the no-route path).
func TestRouter_Dispatch_RefZeroRoutes(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(router.Close)

	var released atomic.Int32
	ref := NewFrameRef(func() { released.Add(1) })
	frame := AudioFrame{
		SourceID:   "no-routes",
		Data:       []byte{1},
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Ref:        ref,
	}

	router.Dispatch(frame)
	ref.Release()

	assert.EqualValues(t, 1, released.Load(), "no routes: producer's release alone must drop to zero")
}

// TestRouter_Dispatch_RefFullInboxDrops verifies that when Dispatch cannot
// enqueue a frame (inbox full), the drop path releases the retain so the pool
// slice is not leaked. The producer's own release then completes the cycle.
func TestRouter_Dispatch_RefFullInboxDrops(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(router.Close)

	// Blocking consumer so the inbox fills once its capacity is exceeded.
	blocker := newBlockingConsumer("blocker-ref-drop")
	require.NoError(t, router.AddRoute("src-full", blocker, 48000, 0.0, nil))
	t.Cleanup(blocker.unblock)

	var released atomic.Int32

	// Dispatch enough frames to guarantee drops: the drainer consumes at most
	// one frame before blocking in Write, so 2*cap + 1 leaves no room for
	// timing-related flakiness.
	totalFrames := 2*RouteInboxCapacity + 1
	for range totalFrames {
		ref := NewFrameRef(func() { released.Add(1) })
		router.Dispatch(AudioFrame{
			SourceID:   "src-full",
			Data:       []byte{0},
			SampleRate: 48000,
			BitDepth:   16,
			Channels:   1,
			Ref:        ref,
		})
		ref.Release()
	}

	// Every dropped frame should have released via the drop path. Without the
	// drop-path Release, the refcount would stay at one and the closure would
	// never fire. Expect at least one drop given the controlled overflow.
	assert.Positive(t, released.Load(), "dropped frames must still release")
}

// TestRouter_Dispatch_RefHappyPath verifies that when a frame carries a
// FrameRef and the router fans it out to multiple routes, the release closure
// fires exactly once after every drainer has processed the frame and the
// producer has released its own reference.
func TestRouter_Dispatch_RefHappyPath(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(router.Close)

	c1 := newMockConsumer("c1-ref-happy")
	c2 := newMockConsumer("c2-ref-happy")
	require.NoError(t, router.AddRoute("src-ref", c1, 48000, 0.0, nil))
	require.NoError(t, router.AddRoute("src-ref", c2, 48000, 0.0, nil))

	var released atomic.Int32
	ref := NewFrameRef(func() { released.Add(1) })
	frame := AudioFrame{
		SourceID:   "src-ref",
		Data:       []byte{1, 2},
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Ref:        ref,
	}

	router.Dispatch(frame)
	ref.Release() // producer's own reference

	// Wait for both drainers to process and release.
	require.Eventually(t, func() bool { return released.Load() == 1 }, time.Second, 10*time.Millisecond)
}

// TestRouter_Dispatch_RefReleasesOnConsumerPanic verifies that the drainer's
// defer runs even when Consumer.Write panics, so the pool slice is always
// returned to its pool.
func TestRouter_Dispatch_RefReleasesOnConsumerPanic(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(router.Close)

	// Consumer whose Write panics.
	panicer := &panicOnWriteConsumer{id: "panicer-ref", sampleRate: 48000}
	require.NoError(t, router.AddRoute("src-panic", panicer, 48000, 0.0, nil))

	var released atomic.Int32
	ref := NewFrameRef(func() { released.Add(1) })
	router.Dispatch(AudioFrame{
		SourceID:   "src-panic",
		Data:       []byte{1},
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Ref:        ref,
	})
	ref.Release()

	require.Eventually(t, func() bool { return released.Load() == 1 }, time.Second, 10*time.Millisecond,
		"FrameRef must release even when consumer.Write panics")
}

// TestRouter_Dispatch_RefReleasesOnShutdown verifies that closing the router
// while frames sit in an inbox does not cause a double-release panic. It is
// acceptable for frames stuck in the inbox during shutdown to leak from the
// pool's perspective (GC reclaims the underlying slice); what must not happen
// is a panic or a negative refcount that would corrupt pool accounting.
func TestRouter_Dispatch_RefReleasesOnShutdown(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger(), nil)

	blocker := newBlockingConsumer("blocker-ref-shutdown")
	require.NoError(t, router.AddRoute("src-shut", blocker, 48000, 0.0, nil))

	var released atomic.Int32
	ref := NewFrameRef(func() { released.Add(1) })
	router.Dispatch(AudioFrame{
		SourceID:   "src-shut",
		Data:       []byte{1},
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Ref:        ref,
	})
	ref.Release()

	// Closing the router while the frame sits in the inbox: the drainer exits
	// without processing, so Release is NOT called for that enqueue. The pool
	// slice leaks from the pool's perspective but GC will reclaim it. Verify
	// that the producer's own Release (already called above) plus the drop
	// path (if any) do not cause a double-release panic.
	blocker.unblock() // allow the drainer (if running handleRouteFrame) to exit cleanly
	router.Close()

	// released is at most 1: either the drainer processed the frame before
	// Close (count hits zero, fires) or the drainer exited without processing
	// (count stays at 1 and never fires). Either outcome is acceptable; a
	// double-release would push the counter negative and must not fire twice.
	assert.LessOrEqual(t, released.Load(), int32(1), "must not double-release during shutdown")
}

// slowConsumer adds a configurable delay per Write call to simulate a consumer
// that is close to the frame-rate limit.
type slowConsumer struct {
	mockConsumer
	delay time.Duration
}

func newSlowConsumer(id string, delay time.Duration) *slowConsumer {
	return &slowConsumer{
		id:         id,
		sampleRate: 48000,
		bitDepth:   16,
		channels:   1,
		frames:     make(chan AudioFrame, 256),
		delay:      delay,
	}
}

func (s *slowConsumer) Write(frame AudioFrame) error { //nolint:gocritic // hugeParam: signature required by AudioConsumer interface
	if s.closed.Load() {
		return ErrConsumerClosed
	}
	time.Sleep(s.delay)
	select {
	case s.frames <- frame:
	default:
	}
	return nil
}

// TestRouter_MultiRouteContention verifies that the router can sustain
// frame dispatch to multiple consumers doing non-trivial work (gain
// processing) without accumulating drops, provided the consumers are
// individually fast enough.
func TestRouter_MultiRouteContention(t *testing.T) {
	mgr := buffer.NewManager(GetLogger())
	router := NewAudioRouter(GetLogger(), mgr)
	t.Cleanup(func() { router.Close() })

	const routeCount = 4
	const frameCount = 500
	consumers := make([]*slowConsumer, routeCount)
	for i := range routeCount {
		consumers[i] = newSlowConsumer(
			fmt.Sprintf("contention-%d", i),
			1*time.Millisecond,
		)
		require.NoError(t, router.AddRoute("src-1", consumers[i], 48000, 3.0, nil))
	}

	for range frameCount {
		frame := testFrame("src-1")
		router.Dispatch(frame)
		frame.Ref.Release()
		time.Sleep(2 * time.Millisecond)
	}

	// Allow drainers to finish processing.
	time.Sleep(100 * time.Millisecond)

	routes := router.Routes("src-1")
	require.Len(t, routes, routeCount)
	for _, ri := range routes {
		assert.Zero(t, ri.Drops,
			"route %s should have zero drops with pool-cached routing", ri.ConsumerID)
	}
}

// TestRouter_PoolCacheHit verifies that routes with a buffer manager cache
// pool pointers and use them in handleRouteFrame without calling
// BytePoolFor/Float32PoolFor on each frame. A gain-only route takes no float
// buffer, so it caches only the output byte pool; an EQ route caches both.
func TestRouter_PoolCacheHit(t *testing.T) {
	t.Parallel()

	t.Run("gain_only", func(t *testing.T) {
		t.Parallel()
		route := poolCacheRoute(t, 3.0, nil)
		assert.Nil(t, route.float32Pool.Load(), "a gain-only route takes no float32 buffer")
		assert.NotNil(t, route.byteOutPool.Load(), "byteOutPool should be cached after frames")
	})

	t.Run("eq", func(t *testing.T) {
		t.Parallel()
		chain := dspeq.NewFilterChain()
		hp, err := dspeq.NewFilter(dspeq.Band{Type: dspeq.HighPass, Frequency: 100, Q: 0.707, Passes: 1}, 48000)
		require.NoError(t, err)
		require.NoError(t, chain.AddFilter(hp))
		route := poolCacheRoute(t, 0, chain)
		assert.NotNil(t, route.float32Pool.Load(), "float32Pool should be cached after frames")
		assert.NotNil(t, route.byteOutPool.Load(), "byteOutPool should be cached after frames")
	})
}

// poolCacheRoute adds a route with the given gain and chain to a router with a
// buffer manager, pushes ten frames through it, and returns the route.
func poolCacheRoute(t *testing.T, gainDB float64, chain *dspeq.FilterChain) *Route {
	t.Helper()

	mgr := buffer.NewManager(GetLogger())
	router := NewAudioRouter(GetLogger(), mgr)
	t.Cleanup(func() { router.Close() })

	consumer := newMockConsumer("pool-test")
	consumer.sampleRate = 48000
	require.NoError(t, router.AddRoute("src-1", consumer, 48000, gainDB, chain))

	const frameCount = 10
	for range frameCount {
		frame := testFrame("src-1")
		router.Dispatch(frame)
		frame.Ref.Release()
	}

	// Drain consumer frames.
	for range frameCount {
		select {
		case <-consumer.frames:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for consumer frame")
		}
	}

	router.mu.RLock()
	defer router.mu.RUnlock()
	routes := router.routes["src-1"]
	require.Len(t, routes, 1)
	return routes[0]
}

// TestRouter_QueueDepth verifies that Routes() returns the current inbox
// occupancy in RouteInfo.QueueDepth for routes with queued frames.
func TestRouter_QueueDepth(t *testing.T) {
	t.Parallel()

	router := NewAudioRouter(GetLogger(), nil)
	t.Cleanup(func() { router.Close() })

	const consumerID = "analysis-consumer"
	consumer := newBlockingConsumer(consumerID)
	t.Cleanup(consumer.unblock)

	require.NoError(t, router.AddRoute("src-1", consumer, 48000, 0.0, nil))

	// Push N frames without draining. The drainer goroutine will take at most
	// one frame (blocking in Write), so inbox occupancy should be N-1 or N.
	// We overfill slightly so at least RouteInboxCapacity frames sit in the
	// inbox after the drainer goroutine takes one.
	const pushFrames = RouteInboxCapacity
	for range pushFrames {
		router.Dispatch(testFrame("src-1"))
	}

	// QueueDepth must be positive: the inbox has frames queued.
	routes := router.Routes("src-1")
	require.Len(t, routes, 1)
	assert.Positive(t, routes[0].QueueDepth,
		"QueueDepth must be positive when frames are queued in the inbox")
	assert.LessOrEqual(t, routes[0].QueueDepth, RouteInboxCapacity,
		"QueueDepth must not exceed inbox capacity")
}
