package capture

import (
	"bytes"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

func TestNewCircularBuffer(t *testing.T) {
	format := audiocore.AudioFormat{
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   16,
	}

	tests := []struct {
		name      string
		duration  time.Duration
		wantError bool
	}{
		{
			name:      "valid duration",
			duration:  10 * time.Second,
			wantError: false,
		},
		{
			name:      "zero duration",
			duration:  0,
			wantError: true,
		},
		{
			name:      "negative duration",
			duration:  -1 * time.Second,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf, err := NewCircularBuffer(tt.duration, format, nil)
			if (err != nil) != tt.wantError {
				t.Errorf("NewCircularBuffer() error = %v, wantError %v", err, tt.wantError)
			}
			if err == nil && buf == nil {
				t.Error("Expected non-nil buffer")
			}
		})
	}
}

func TestCircularBuffer_Write(t *testing.T) {
	format := audiocore.AudioFormat{
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   16,
	}

	buf, err := NewCircularBuffer(5*time.Second, format, nil)
	if err != nil {
		t.Fatalf("Failed to create buffer: %v", err)
	}

	// Test writing data
	testData := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	err = buf.Write(testData)
	if err != nil {
		t.Errorf("Write failed: %v", err)
	}

	// Test writing empty data
	err = buf.Write([]byte{})
	if err != nil {
		t.Errorf("Write empty data failed: %v", err)
	}

	// Test buffer wraparound
	// Buffer size is 5 seconds * 48000 Hz * 1 channel * 2 bytes = 480,000 bytes
	largeData := make([]byte, 500000) // Larger than buffer
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	err = buf.Write(largeData)
	if err != nil {
		t.Errorf("Write large data failed: %v", err)
	}
}

func TestCircularBuffer_ReadSegment(t *testing.T) {
	format := audiocore.AudioFormat{
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   16,
	}

	buf, err := NewCircularBuffer(5*time.Second, format, nil)
	if err != nil {
		t.Fatalf("Failed to create buffer: %v", err)
	}

	// Write some test data
	testData := make([]byte, 48000*2) // 1 second of audio
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	err = buf.Write(testData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Wait a bit to ensure timing
	time.Sleep(10 * time.Millisecond)

	// Read segment
	startTime := buf.startTime
	endTime := startTime.Add(500 * time.Millisecond)

	segment, err := buf.ReadSegment(startTime, endTime)
	if err != nil {
		t.Errorf("ReadSegment failed: %v", err)
	}

	expectedSize := 48000 // 0.5 seconds at 48kHz, 16-bit mono
	if len(segment) != expectedSize {
		t.Errorf("Expected segment size %d, got %d", expectedSize, len(segment))
	}

	// Verify data integrity
	if !bytes.Equal(segment[:100], testData[:100]) {
		t.Error("Segment data doesn't match written data")
	}
}

func TestCircularBuffer_ReadSegment_Errors(t *testing.T) {
	format := audiocore.AudioFormat{
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   16,
	}

	buf, err := NewCircularBuffer(5*time.Second, format, nil)
	if err != nil {
		t.Fatalf("Failed to create buffer: %v", err)
	}

	// Test reading from uninitialized buffer
	_, err = buf.ReadSegment(time.Now(), time.Now().Add(1*time.Second))
	if err == nil {
		t.Error("Expected error reading from uninitialized buffer")
	}

	// Initialize buffer
	_ = buf.Write([]byte{1, 2, 3, 4})

	// Test invalid time ranges
	now := time.Now()
	tests := []struct {
		name      string
		startTime time.Time
		endTime   time.Time
		wantError bool
	}{
		{
			name:      "start before buffer",
			startTime: now.Add(-10 * time.Second),
			endTime:   now,
			wantError: true,
		},
		{
			name:      "end after buffer",
			startTime: now,
			endTime:   now.Add(10 * time.Second),
			wantError: true,
		},
		{
			name:      "end before start",
			startTime: now,
			endTime:   now.Add(-1 * time.Second),
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buf.ReadSegment(tt.startTime, tt.endTime)
			if (err != nil) != tt.wantError {
				t.Errorf("ReadSegment() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestCircularBuffer_Wraparound(t *testing.T) {
	format := audiocore.AudioFormat{
		SampleRate: 1000, // Low sample rate for easier testing
		Channels:   1,
		BitDepth:   16,
	}

	// Small buffer for testing wraparound
	buf, err := NewCircularBuffer(1*time.Second, format, nil)
	if err != nil {
		t.Fatalf("Failed to create buffer: %v", err)
	}

	// Buffer size is 1000 Hz * 1 channel * 2 bytes = 2000 bytes
	// Write data that will wrap around
	data1 := make([]byte, 1500)
	for i := range data1 {
		data1[i] = byte(i % 100) // Pattern 0-99
	}

	err = buf.Write(data1)
	if err != nil {
		t.Fatalf("Write 1 failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	// Write more data to cause wraparound
	data2 := make([]byte, 1000)
	for i := range data2 {
		data2[i] = byte(100 + i%100) // Pattern 100-199
	}

	err = buf.Write(data2)
	if err != nil {
		t.Fatalf("Write 2 failed: %v", err)
	}

	// Read recent data
	endTime := time.Now()
	startTime := endTime.Add(-300 * time.Millisecond)

	segment, err := buf.ReadSegment(startTime, endTime)
	if err != nil {
		t.Errorf("ReadSegment after wraparound failed: %v", err)
	}

	// Should get approximately 300ms of data = 600 bytes
	if len(segment) < 500 || len(segment) > 700 {
		t.Errorf("Unexpected segment size: %d", len(segment))
	}
}

func TestCircularBuffer_GettersAndReset(t *testing.T) {
	format := audiocore.AudioFormat{
		SampleRate: 48000,
		Channels:   2,
		BitDepth:   24,
	}
	duration := 10 * time.Second

	buf, err := NewCircularBuffer(duration, format, nil)
	if err != nil {
		t.Fatalf("Failed to create buffer: %v", err)
	}

	// Test getters
	if buf.GetDuration() != duration {
		t.Errorf("GetDuration() = %v, want %v", buf.GetDuration(), duration)
	}

	gotFormat := buf.GetFormat()
	if gotFormat.SampleRate != format.SampleRate ||
		gotFormat.Channels != format.Channels ||
		gotFormat.BitDepth != format.BitDepth {
		t.Errorf("GetFormat() = %+v, want %+v", gotFormat, format)
	}

	// Write some data
	_ = buf.Write([]byte{1, 2, 3, 4, 5, 6})

	// Reset
	buf.Reset()

	// Verify buffer is cleared
	if buf.initialized {
		t.Error("Buffer should not be initialized after reset")
	}

	if buf.writeIndex != 0 {
		t.Error("Write index should be 0 after reset")
	}
}

func TestCircularBuffer_Close(t *testing.T) {
	format := audiocore.AudioFormat{
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   16,
	}

	buf, err := NewCircularBuffer(5*time.Second, format, nil)
	if err != nil {
		t.Fatalf("Failed to create buffer: %v", err)
	}

	err = buf.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Verify buffer is reset after close
	if buf.initialized {
		t.Error("Buffer should not be initialized after close")
	}
}

func TestCircularBuffer_ConcurrentAccess(t *testing.T) {
	format := audiocore.AudioFormat{
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   16,
	}

	buf, err := NewCircularBuffer(5*time.Second, format, nil)
	if err != nil {
		t.Fatalf("Failed to create buffer: %v", err)
	}

	// Start multiple writers
	done := make(chan bool)
	for i := 0; i < 5; i++ {
		go func(id int) {
			data := make([]byte, 1000)
			for j := range data {
				data[j] = byte(id)
			}
			for k := 0; k < 10; k++ {
				_ = buf.Write(data)
				time.Sleep(time.Millisecond)
			}
			done <- true
		}(i)
	}

	// Start readers
	for i := 0; i < 3; i++ {
		go func() {
			time.Sleep(5 * time.Millisecond)
			for k := 0; k < 5; k++ {
				now := time.Now()
				_, _ = buf.ReadSegment(now.Add(-100*time.Millisecond), now)
				time.Sleep(2 * time.Millisecond)
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 8; i++ {
		<-done
	}
}