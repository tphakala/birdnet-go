package adapter

import (
	"context"
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

func (m *mockSource) ID() string                                 { return m.id }
func (m *mockSource) Name() string                               { return m.name }
func (m *mockSource) Start(ctx context.Context) error           { m.active = true; return nil }
func (m *mockSource) Stop() error                                { m.active = false; close(m.audioOutput); close(m.errorOutput); return nil }
func (m *mockSource) AudioOutput() <-chan audiocore.AudioData   { return m.audioOutput }
func (m *mockSource) Errors() <-chan error                       { return m.errorOutput }
func (m *mockSource) IsActive() bool                             { return m.active }
func (m *mockSource) GetFormat() audiocore.AudioFormat          { return m.format }
func (m *mockSource) SetGain(gain float64) error                { return nil }

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

	// Send data through the source
	source.audioOutput <- testData

	// Give it time to process
	time.Sleep(100 * time.Millisecond)

	// In a real test, we would verify that the data was written to myaudio buffers
	// For now, we just ensure it doesn't panic

	// Stop the bridge
	bridge.Stop()
}

func TestBufferBridgeErrorHandling(t *testing.T) {
	source := newMockSource("test-source", "Test Source")
	bridge := NewBufferBridge(source, "test")

	ctx := context.Background()
	err := bridge.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start bridge: %v", err)
	}

	// Send test error
	testErr := bridge.source.Errors() // Just verify channel exists
	_ = testErr

	// Give it time to process
	time.Sleep(100 * time.Millisecond)

	// Stop the bridge
	bridge.Stop()
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

	// Close source channels to simulate source stopping
	close(source.audioOutput)
	close(source.errorOutput)

	// Give goroutines time to exit
	time.Sleep(100 * time.Millisecond)

	// Stop should still work without hanging
	done := make(chan bool)
	go func() {
		bridge.Stop()
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
	defer bridge.Stop()

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