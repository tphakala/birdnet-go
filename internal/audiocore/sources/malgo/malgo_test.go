package malgo

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/malgo"
)

// Mock buffer pool for testing
type mockBufferPool struct{}

func (m *mockBufferPool) Get(size int) audiocore.AudioBuffer {
	return &mockBuffer{data: make([]byte, size)}
}

func (m *mockBufferPool) Put(buffer audiocore.AudioBuffer) {}

func (m *mockBufferPool) Stats() audiocore.BufferPoolStats {
	return audiocore.BufferPoolStats{}
}

func (m *mockBufferPool) TierStats(tier string) (audiocore.BufferPoolStats, bool) {
	return audiocore.BufferPoolStats{}, false
}

func (m *mockBufferPool) ReportMetrics() {}

type mockBuffer struct {
	data []byte
}

func (b *mockBuffer) Data() []byte                   { return b.data }
func (b *mockBuffer) Len() int                       { return len(b.data) }
func (b *mockBuffer) Cap() int                       { return cap(b.data) }
func (b *mockBuffer) Reset()                         { b.data = b.data[:0] }
func (b *mockBuffer) Resize(newSize int) error       { b.data = make([]byte, newSize); return nil }
func (b *mockBuffer) Slice(s, e int) ([]byte, error) { return b.data[s:e], nil }
func (b *mockBuffer) Acquire()                       {}
func (b *mockBuffer) Release()                       {}

func TestNewMalgoSource(t *testing.T) {
	sourceConfig := audiocore.SourceConfig{
		ID:     "test-source",
		Name:   "Test Source",
		Type:   "malgo",
		Device: "test-device",
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
		BufferSize: 4096,
		Gain:       1.0,
		ExtraConfig: map[string]any{
			"buffer_frames": uint32(512),
		},
	}

	pool := &mockBufferPool{}
	source, err := NewMalgoSource(&sourceConfig, pool)
	if err != nil {
		t.Fatalf("Failed to create malgo source: %v", err)
	}

	if source.ID() != "test-source" {
		t.Errorf("Expected ID 'test-source', got '%s'", source.ID())
	}

	if source.Name() != "Test Source" {
		t.Errorf("Expected name 'Test Source', got '%s'", source.Name())
	}

	format := source.GetFormat()
	if format.SampleRate != 48000 {
		t.Errorf("Expected sample rate 48000, got %d", format.SampleRate)
	}
	if format.Channels != 1 {
		t.Errorf("Expected 1 channel, got %d", format.Channels)
	}
	if format.BitDepth != 16 {
		t.Errorf("Expected bit depth 16, got %d", format.BitDepth)
	}
	if format.Encoding != "pcm_s16le" {
		t.Errorf("Expected encoding 'pcm_s16le', got '%s'", format.Encoding)
	}
}

func TestMalgoSourceGain(t *testing.T) {
	sourceConfig := audiocore.SourceConfig{
		ID:         "test-source",
		Name:       "Test Source",
		Type:       "malgo",
		Device:     "test-device",
		Format:     audiocore.AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16, Encoding: "pcm_s16le"},
		BufferSize: 4096,
		Gain:       1.0,
	}

	pool := &mockBufferPool{}
	source, _ := NewMalgoSource(&sourceConfig, pool)

	// Test valid gain values
	testCases := []struct {
		gain    float64
		wantErr bool
	}{
		{0.0, false},
		{1.0, false},
		{1.5, false},
		{2.0, false},
		{-0.1, true},
		{2.1, true},
	}

	for _, tc := range testCases {
		err := source.SetGain(tc.gain)
		if (err != nil) != tc.wantErr {
			t.Errorf("SetGain(%f) error = %v, wantErr %v", tc.gain, err, tc.wantErr)
		}
	}
}

func TestConvertToS16(t *testing.T) {
	testCases := []struct {
		name     string
		format   malgo.FormatType
		input    []byte
		expected []byte
	}{
		{
			name:     "S16 passthrough",
			format:   malgo.FormatS16,
			input:    []byte{0x00, 0x10, 0x00, 0x20},
			expected: []byte{0x00, 0x10, 0x00, 0x20},
		},
		{
			name:     "U8 to S16",
			format:   malgo.FormatU8,
			input:    []byte{0x80, 0xFF},
			expected: []byte{0x00, 0x00, 0x00, 0x7F},
		},
		{
			name:     "S24 to S16",
			format:   malgo.FormatS24,
			input:    []byte{0x00, 0x00, 0x7F, 0x00, 0x00, 0x80}, // Two 24-bit samples
			// S24: 0x7F0000 (8323072) -> S16: 0x7F00 (32512)
			// S24: 0x800000 (-8388608) -> S16: 0x8000 (-32768)
			expected: []byte{0x00, 0x7F, 0x00, 0x80},
		},
		{
			name:     "S32 to S16",
			format:   malgo.FormatS32,
			input:    []byte{0x00, 0x00, 0xFF, 0x7F, 0x00, 0x00, 0x00, 0x80}, // Two 32-bit samples
			// S32: 0x7FFF0000 (2147418112) -> S16: 0x7FFF (32767)
			// S32: 0x80000000 (-2147483648) -> S16: 0x8000 (-32768)
			expected: []byte{0xFF, 0x7F, 0x00, 0x80},
		},
		{
			name:     "F32 to S16",
			format:   malgo.FormatF32,
			// F32: 0.5 = 0x3F000000, -0.5 = 0xBF000000 in IEEE 754
			input:    []byte{0x00, 0x00, 0x00, 0x3F, 0x00, 0x00, 0x00, 0xBF},
			// F32: 0.5 * 32767 = 16383.5 -> S16: 16383 (0x3FFF)
			// F32: -0.5 * 32767 = -16383.5 -> S16: -16383 (0xC001)
			expected: []byte{0xFF, 0x3F, 0x01, 0xC0},
		},
		{
			name:     "Empty input",
			format:   malgo.FormatS16,
			input:    []byte{},
			expected: []byte{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			output, err := ConvertToS16(tc.input, tc.format, nil)
			if err != nil {
				t.Fatalf("ConvertToS16 failed: %v", err)
			}

			if len(output) != len(tc.expected) {
				t.Errorf("Output length mismatch: got %d, expected %d", len(output), len(tc.expected))
			}

			for i := range output {
				if output[i] != tc.expected[i] {
					t.Errorf("Output mismatch at index %d: got 0x%02X, expected 0x%02X", i, output[i], tc.expected[i])
				}
			}
		})
	}
}

func TestGetFormatInfo(t *testing.T) {
	testCases := []struct {
		format        malgo.FormatType
		expectedBytes int
		expectedName  string
	}{
		{malgo.FormatU8, 1, "U8"},
		{malgo.FormatS16, 2, "S16"},
		{malgo.FormatS24, 3, "S24"},
		{malgo.FormatS32, 4, "S32"},
		{malgo.FormatF32, 4, "F32"},
		{malgo.FormatUnknown, 0, "Unknown"},
	}

	for _, tc := range testCases {
		bytes, name := GetFormatInfo(tc.format)
		if bytes != tc.expectedBytes {
			t.Errorf("GetFormatInfo(%v) bytes = %d, expected %d", tc.format, bytes, tc.expectedBytes)
		}
		if name != tc.expectedName {
			t.Errorf("GetFormatInfo(%v) name = %s, expected %s", tc.format, name, tc.expectedName)
		}
	}
}

func TestCalculateBufferSize(t *testing.T) {
	size := CalculateBufferSize(malgo.FormatS16, 2, 1024)
	expected := 2 * 2 * 1024 // 2 bytes per sample * 2 channels * 1024 frames
	if size != expected {
		t.Errorf("CalculateBufferSize = %d, expected %d", size, expected)
	}
}

func TestMalgoSourceStartStop(t *testing.T) {
	// Skip this test if we can't initialize malgo (e.g., in CI without audio devices)
	sourceConfig := audiocore.SourceConfig{
		ID:         "test-source",
		Name:       "default",
		Type:       "malgo",
		Device:     "default",
		Format:     audiocore.AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16, Encoding: "pcm_s16le"},
		BufferSize: 4096,
		Gain:       1.0,
	}

	pool := &mockBufferPool{}
	source, _ := NewMalgoSource(&sourceConfig, pool)

	// Test that Stop fails when not started
	err := source.Stop()
	if err == nil {
		t.Error("Expected error when stopping non-started source")
	}

	// Test double start
	// Note: This test may fail if no audio devices are available
	// In production, we'd want to mock the malgo interface
}

func TestEnumerateDevices(t *testing.T) {
	// This test may fail if no audio devices are available
	// It's mainly to ensure the function doesn't panic
	devices, err := EnumerateDevices()
	if err != nil {
		// It's OK if this fails in CI environment
		t.Logf("EnumerateDevices failed (expected in CI): %v", err)
		return
	}

	t.Logf("Found %d audio devices", len(devices))
	for _, device := range devices {
		t.Logf("Device %d: %s (ID: %s)", device.Index, device.Name, device.ID)
	}
}

func TestAudioDataPipeline(t *testing.T) {
	// Test the audio data pipeline with mock data
	sourceConfig := audiocore.SourceConfig{
		ID:         "test-source",
		Name:       "test",
		Type:       "malgo",
		Device:     "test-device",
		Format:     audiocore.AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16, Encoding: "pcm_s16le"},
		BufferSize: 4096,
		Gain:       1.5,
		ExtraConfig: map[string]any{"buffer_frames": uint32(512)},
	}

	pool := &mockBufferPool{}
	source, _ := NewMalgoSource(&sourceConfig, pool)

	// Test gain application
	buffer := []byte{0x00, 0x10, 0x00, 0x20} // Two 16-bit samples
	source.applyGain(buffer, 1.5)

	// First sample: 0x1000 = 4096, * 1.5 = 6144 = 0x1800
	if buffer[0] != 0x00 || buffer[1] != 0x18 {
		t.Errorf("First sample after gain: got 0x%02X%02X, expected 0x0018", buffer[1], buffer[0])
	}
}

func TestIsActive(t *testing.T) {
	sourceConfig := audiocore.SourceConfig{
		ID:         "test-source",
		Name:       "test",
		Type:       "malgo",
		Device:     "test",
		Format:     audiocore.AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16, Encoding: "pcm_s16le"},
		BufferSize: 4096,
		Gain:       1.0,
	}

	pool := &mockBufferPool{}
	source, _ := NewMalgoSource(&sourceConfig, pool)

	if source.IsActive() {
		t.Error("New source should not be active")
	}

	// After start, it should be active (if we could start it)
	// This would require mocking malgo
}

func BenchmarkConvertToS16(b *testing.B) {
	// Create test data
	input := make([]byte, 4096) // 1024 F32 samples
	for i := range input {
		input[i] = byte(i & 0xFF)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err := ConvertToS16(input, malgo.FormatF32, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkApplyGain(b *testing.B) {
	sourceConfig := audiocore.SourceConfig{
		ID:         "bench",
		Name:       "bench",
		Type:       "malgo",
		Device:     "default",
		Format:     audiocore.AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16, Encoding: "pcm_s16le"},
		BufferSize: 4096,
		Gain:       1.0,
	}
	pool := &mockBufferPool{}
	source, _ := NewMalgoSource(&sourceConfig, pool)

	buffer := make([]byte, 4096) // 2048 16-bit samples
	gain := 1.5

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		source.applyGain(buffer, gain)
	}
}
