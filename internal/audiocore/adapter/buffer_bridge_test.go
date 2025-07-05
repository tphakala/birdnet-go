package adapter

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// Mock audio source for testing
type mockSource struct {
	id          string
	name        string
	audioOutput chan audiocore.AudioData
	errorOutput chan error
	active      bool
	format      audiocore.AudioFormat
	closeOnce   sync.Once
	mu          sync.Mutex
}

func newMockSource(id, name string) *mockSource {
	return &mockSource{
		id:          id,
		name:        name,
		audioOutput: make(chan audiocore.AudioData, 10),
		errorOutput: make(chan error, 10),
		format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
	}
}

func (m *mockSource) ID() string   { return m.id }
func (m *mockSource) Name() string { return m.name }
func (m *mockSource) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = true
	return nil
}
func (m *mockSource) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = false

	// Close channels only once to prevent panic
	m.closeOnce.Do(func() {
		close(m.audioOutput)
		close(m.errorOutput)
	})
	return nil
}
func (m *mockSource) AudioOutput() <-chan audiocore.AudioData { return m.audioOutput }
func (m *mockSource) Errors() <-chan error                    { return m.errorOutput }
func (m *mockSource) IsActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}
func (m *mockSource) GetFormat() audiocore.AudioFormat { return m.format }
func (m *mockSource) SetGain(gain float64) error       { return nil }

func TestNewBufferBridge(t *testing.T) {
	source := newMockSource("test-source", "Test Source")
	bridge := NewBufferBridge(source, "test")

	if bridge.source != source {
		t.Error("Bridge source not set correctly")
	}
	if bridge.sourceID != "test" {
		t.Errorf("Expected sourceID 'test', got '%s'", bridge.sourceID)
	}
	if bridge.running {
		t.Error("New bridge should not be running")
	}
}

func TestBufferBridgeStartStop(t *testing.T) {
	source := newMockSource("test-source", "Test Source")
	bridge := NewBufferBridge(source, "test")

	ctx := context.Background()

	// Start the bridge
	err := bridge.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start bridge: %v", err)
	}

	if !bridge.IsRunning() {
		t.Error("Bridge should be running after start")
	}

	if !source.IsActive() {
		t.Error("Source should be active after bridge start")
	}

	// Test double start
	err = bridge.Start(ctx)
	if err == nil {
		t.Error("Expected error on double start")
	}

	// Stop the bridge
	err = bridge.Stop()
	if err != nil {
		t.Fatalf("Failed to stop bridge: %v", err)
	}

	if bridge.IsRunning() {
		t.Error("Bridge should not be running after stop")
	}

	// Test double stop
	err = bridge.Stop()
	if err != nil {
		t.Error("Double stop should not return error")
	}
}

func TestBufferBridgeAudioProcessing(t *testing.T) {
	source := newMockSource("test-source", "Test Source")
	bridge := NewBufferBridge(source, "test")

	ctx := context.Background()
	err := bridge.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start bridge: %v", err)
	}

	// Send test audio data
	testData := audiocore.AudioData{
		Buffer:    []byte{0x00, 0x01, 0x02, 0x03},
		Format:    source.GetFormat(),
		Timestamp: time.Now(),
		Duration:  time.Millisecond * 10,
		SourceID:  "test-source",
	}

	// Create a channel to signal when data is processed
	processed := make(chan struct{})

	// Send data and wait for processing in separate goroutine
	go func() {
		// Wait a moment for bridge to start processing
		time.Sleep(10 * time.Millisecond)
		source.audioOutput <- testData
		close(processed)
	}()

	// Wait for processing signal
	select {
	case <-processed:
		// Data was sent
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for data processing")
	}

	// In a real test, we would verify that the data was written to myaudio buffers
	// For now, we just ensure it doesn't panic

	// Stop the bridge
	_ = bridge.Stop()
}

func TestBufferBridgeErrorHandling(t *testing.T) {
	source := newMockSource("test-source", "Test Source")
	bridge := NewBufferBridge(source, "test")

	ctx := context.Background()
	err := bridge.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start bridge: %v", err)
	}

	// Create a channel to signal when error handler is ready
	ready := make(chan struct{})

	// Send a test error to verify error handling
	go func() {
		// Wait a moment for bridge error handler to start
		time.Sleep(10 * time.Millisecond)
		testErr := errors.New("test error")
		select {
		case source.errorOutput <- testErr:
			close(ready)
		case <-time.After(100 * time.Millisecond):
			// Error channel might be full or not ready
			close(ready)
		}
	}()

	// Wait for error handling
	<-ready

	// Stop the bridge
	_ = bridge.Stop()
}

func TestGetSource(t *testing.T) {
	source := newMockSource("test-source", "Test Source")
	bridge := NewBufferBridge(source, "test")

	if bridge.GetSource() != source {
		t.Error("GetSource() should return the wrapped source")
	}
}

func TestBufferBridgeChannelClosure(t *testing.T) {
	source := newMockSource("test-source", "Test Source")
	bridge := NewBufferBridge(source, "test")

	ctx := context.Background()
	err := bridge.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start bridge: %v", err)
	}

	// Create done channel to signal when channels are closed
	closed := make(chan struct{})

	// Close source channels to simulate source stopping
	go func() {
		close(source.audioOutput)
		close(source.errorOutput)
		close(closed)
	}()

	// Wait for channels to be closed
	<-closed

	// Stop should still work without hanging
	done := make(chan bool)
	go func() {
		_ = bridge.Stop()
		done <- true
	}()

	select {
	case <-done:
		// Good, Stop() completed
	case <-time.After(1 * time.Second):
		t.Error("Stop() timed out after source channels closed")
	}
}

func BenchmarkBufferBridgeProcessing(b *testing.B) {
	source := newMockSource("bench-source", "Bench Source")
	bridge := NewBufferBridge(source, "bench")

	ctx := context.Background()
	if err := bridge.Start(ctx); err != nil {
		b.Fatal(err)
	}
	defer func() { _ = bridge.Stop() }()

	// Create test data
	testData := audiocore.AudioData{
		Buffer:    make([]byte, 4096),
		Format:    source.GetFormat(),
		Timestamp: time.Now(),
		Duration:  time.Millisecond * 10,
		SourceID:  "bench-source",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		select {
		case source.audioOutput <- testData:
		default:
			// Channel full, skip
		}
	}
}
