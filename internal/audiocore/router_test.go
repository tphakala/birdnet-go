package audiocore

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
type blockingConsumer struct {
	mockConsumer
}

func newBlockingConsumer(id string) *blockingConsumer {
	return &blockingConsumer{
		mockConsumer: mockConsumer{
			id:         id,
			sampleRate: 48000,
			bitDepth:   16,
			channels:   1,
			frames:     make(chan AudioFrame), // unbuffered — Write blocks forever
		},
	}
}

func (b *blockingConsumer) Write(frame AudioFrame) error { //nolint:gocritic // hugeParam: signature required by AudioConsumer interface
	// Block indefinitely to simulate a permanently stalled consumer.
	select {}
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
	router := NewAudioRouter(GetLogger())
	t.Cleanup(func() { router.Close() })

	consumer := newMockConsumer("consumer-1")

	err := router.AddRoute("src-1", consumer, 48000, 0.0)
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
	router := NewAudioRouter(GetLogger())
	t.Cleanup(func() { router.Close() })

	consumer := newMockConsumer("consumer-1")
	err := router.AddRoute("src-1", consumer, 48000, 0.0)
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
	router := NewAudioRouter(GetLogger())
	t.Cleanup(func() { router.Close() })

	c1 := newMockConsumer("consumer-1")
	c2 := newMockConsumer("consumer-2")

	require.NoError(t, router.AddRoute("src-1", c1, 48000, 0.0))
	require.NoError(t, router.AddRoute("src-1", c2, 48000, 0.0))

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
	router := NewAudioRouter(GetLogger())
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
	router := NewAudioRouter(GetLogger())
	t.Cleanup(func() { router.Close() })

	consumer := newBlockingConsumer("slow-consumer")
	require.NoError(t, router.AddRoute("src-1", consumer, 48000, 0.0))

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
	router := NewAudioRouter(GetLogger())
	t.Cleanup(func() { router.Close() })

	c1 := newMockConsumer("consumer-1")
	c2 := newMockConsumer("consumer-2")

	require.NoError(t, router.AddRoute("src-1", c1, 48000, 0.0))
	require.NoError(t, router.AddRoute("src-1", c2, 48000, 0.0))
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
	router := NewAudioRouter(GetLogger())
	t.Cleanup(func() { router.Close() })

	c1 := newMockConsumer("consumer-1")
	c2 := newMockConsumer("consumer-1") // same ID

	require.NoError(t, router.AddRoute("src-1", c1, 48000, 0.0))

	err := router.AddRoute("src-1", c2, 48000, 0.0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRouteExists)
}

// TestRouter_DispatchWithResampling verifies that when a consumer's sample rate
// differs from the source rate, the router resamples the frame before delivery.
// The consumer should receive a frame with the consumer's sample rate and
// resampled data (approximately 2/3 the length for a 48kHz→32kHz conversion).
func TestRouter_DispatchWithResampling(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger())
	t.Cleanup(func() { router.Close() })

	// Consumer expects 32kHz; source produces 48kHz.
	consumer := newMockConsumer("consumer-resampled")
	consumer.sampleRate = 32000

	// Add route with source at 48kHz.
	err := router.AddRoute("src-resample", consumer, 48000, 0.0)
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
func TestRouter_ConcurrentDispatch(t *testing.T) {
	t.Parallel()
	router := NewAudioRouter(GetLogger())
	t.Cleanup(func() { router.Close() })

	c1 := newMockConsumer("consumer-1")
	c2 := newMockConsumer("consumer-2")
	require.NoError(t, router.AddRoute("src-1", c1, 48000, 0.0))
	require.NoError(t, router.AddRoute("src-1", c2, 48000, 0.0))

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

	// Drain what we can — the point is no race detected.
	drained := 0
	for {
		select {
		case <-c1.frames:
			drained++
		default:
			goto done
		}
	}
done:
	assert.Positive(t, drained, "at least some frames should have been delivered")
}

func TestDrainRoutePanicRecovery(t *testing.T) {
	t.Parallel()

	router := NewAudioRouter(GetLogger())
	t.Cleanup(func() { router.Close() })

	panicConsumer := &panicOnWriteConsumer{
		id:         "panic-consumer",
		sampleRate: 48000,
	}

	err := router.AddRoute("src1", panicConsumer, 48000, 0.0)
	require.NoError(t, err)

	// Dispatch a frame — the consumer will panic on Write.
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

	// The drainer exited on panic — the route's stopped channel is closed,
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

			router := NewAudioRouter(GetLogger())
			defer router.Close()

			consumer := newMockConsumer("c1")
			err := router.AddRoute("src-1", consumer, 48000, tt.gainDB)
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

// TestRouter_GainClipping verifies that high gain correctly clips signals
// to the int16 range rather than producing corrupted output.
func TestRouter_GainClipping(t *testing.T) {
	t.Parallel()

	router := NewAudioRouter(GetLogger())
	defer router.Close()

	consumer := newMockConsumer("c1")
	// +40 dB is 100x linear — will clip a signal near max.
	err := router.AddRoute("src-1", consumer, 48000, 40.0)
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

	// Verify output is clipped to max int16 range, not corrupted.
	require.Len(t, received.Data, 4)
	sample0 := int16(received.Data[0]) | int16(received.Data[1])<<8
	sample1 := int16(received.Data[2]) | int16(received.Data[3])<<8

	// Float64ToBytesPCM16 clamps to [-1.0, 1.0] before conversion,
	// so output should be at or near ±32767.
	assert.InDelta(t, 32767, int(sample0), 1, "positive sample should clip to max")
	assert.InDelta(t, -32767, int(sample1), 1, "negative sample should clip to min")
}

// TestRouter_AddRouteWithGain verifies that AddRoute correctly converts
// a dB gain value to the corresponding linear multiplier and stores it
// on the Route.
func TestRouter_AddRouteWithGain(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		gainDB     float64
		wantLinear float64
	}{
		{"zero_dB_no_change", 0.0, 1.0},
		{"positive_6dB", 6.0, 1.9953},
		{"negative_6dB", -6.0, 0.5012},
		{"max_40dB", 40.0, 100.0},
		{"min_neg40dB", -40.0, 0.01},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := NewAudioRouter(GetLogger())
			t.Cleanup(func() { router.Close() })
			consumer := newMockConsumer("c1")
			err := router.AddRoute("src-1", consumer, 48000, tt.gainDB)
			require.NoError(t, err)
			router.mu.RLock()
			routes := router.routes["src-1"]
			require.Len(t, routes, 1)
			assert.InDelta(t, tt.wantLinear, routes[0].gainLinear, 0.01)
			router.mu.RUnlock()
		})
	}
}
