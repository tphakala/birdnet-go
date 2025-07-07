package analysis

import (
	"testing"
)

func TestSanitizeAudioCoreDeviceName(t *testing.T) {
	t.Parallel()
	
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple device name",
			input:    "Microphone",
			expected: "microphone",
		},
		{
			name:     "device name with spaces",
			input:    "Built-in Microphone",
			expected: "built_in_microphone",
		},
		{
			name:     "device name with special characters",
			input:    "USB Audio Device (2.0)",
			expected: "usb_audio_device_2_0",
		},
		{
			name:     "device name with multiple consecutive spaces",
			input:    "Multiple   Spaces",
			expected: "multiple_spaces",
		},
		{
			name:     "device name with leading/trailing spaces",
			input:    "  Trimmed Device  ",
			expected: "trimmed_device",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "device",
		},
		{
			name:     "very short name",
			input:    "AB",
			expected: "device",
		},
		{
			name:     "long device name",
			input:    "This is a very long device name that should be truncated to reasonable length",
			expected: "this_is_a_very_long_device_nam",
		},
		{
			name:     "mixed case with numbers",
			input:    "Audio Device 123",
			expected: "audio_device_123",
		},
		{
			name:     "only special characters",
			input:    "!@#$%^&*()",
			expected: "device",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeAudioCoreDeviceName(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeAudioCoreDeviceName(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGenerateAudioCoreSourceID(t *testing.T) {
	t.Parallel()
	
	tests := []struct {
		name         string
		sourceType   string
		deviceName   string
		index        int
		expected     string
	}{
		{
			name:       "soundcard with simple name",
			sourceType: "soundcard",
			deviceName: "Microphone",
			index:      0,
			expected:   "soundcard_microphone_0",
		},
		{
			name:       "soundcard with complex name",
			sourceType: "soundcard",
			deviceName: "Built-in Microphone (USB)",
			index:      1,
			expected:   "soundcard_built_in_microphone_usb_1",
		},
		{
			name:       "rtsp source",
			sourceType: "rtsp",
			deviceName: "IP Camera",
			index:      2,
			expected:   "rtsp_ip_camera_2",
		},
		{
			name:       "file source",
			sourceType: "file",
			deviceName: "test_file.wav",
			index:      0,
			expected:   "file_test_file_wav_0",
		},
		{
			name:       "device name with empty string",
			sourceType: "soundcard",
			deviceName: "",
			index:      0,
			expected:   "soundcard_device_0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateAudioCoreSourceID(tt.sourceType, tt.deviceName, tt.index)
			if result != tt.expected {
				t.Errorf("generateAudioCoreSourceID(%q, %q, %d) = %q, expected %q",
					tt.sourceType, tt.deviceName, tt.index, result, tt.expected)
			}
		})
	}
}

func TestGenerateAudioCoreSourceIDConsistency(t *testing.T) {
	t.Parallel()
	
	// Test that the same input always produces the same output
	sourceType := "soundcard"
	deviceName := "Test Device"
	index := 0
	
	first := generateAudioCoreSourceID(sourceType, deviceName, index)
	second := generateAudioCoreSourceID(sourceType, deviceName, index)
	
	if first != second {
		t.Errorf("generateAudioCoreSourceID should be consistent: %q != %q", first, second)
	}
}

func TestGenerateAudioCoreSourceIDUniqueness(t *testing.T) {
	t.Parallel()
	
	// Test that different inputs produce different outputs
	results := make(map[string]bool)
	
	inputs := []struct {
		sourceType string
		deviceName string
		index      int
	}{
		{"soundcard", "Device1", 0},
		{"soundcard", "Device2", 0},
		{"soundcard", "Device1", 1},
		{"rtsp", "Device1", 0},
		{"file", "Device1", 0},
	}
	
	for _, input := range inputs {
		result := generateAudioCoreSourceID(input.sourceType, input.deviceName, input.index)
		if results[result] {
			t.Errorf("generateAudioCoreSourceID produced duplicate ID: %q", result)
		}
		results[result] = true
	}
}

// Benchmark tests
func BenchmarkSanitizeAudioCoreDeviceName(b *testing.B) {
	deviceName := "Built-in Microphone (USB Audio Device 2.0)"
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		sanitizeAudioCoreDeviceName(deviceName)
	}
}

func BenchmarkGenerateAudioCoreSourceID(b *testing.B) {
	sourceType := "soundcard"
	deviceName := "Built-in Microphone (USB Audio Device 2.0)"
	index := 0
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		generateAudioCoreSourceID(sourceType, deviceName, index)
	}
}