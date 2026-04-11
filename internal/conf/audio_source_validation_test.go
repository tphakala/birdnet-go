package conf

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAudioSourceConfig_Validate_Valid(t *testing.T) {
	t.Parallel()

	src := &AudioSourceConfig{
		Name:   "Front Yard Mic",
		Device: "hw:0,0",
		Gain:   3.5,
		Model:  "birdnet",
	}
	assert.NoError(t, src.Validate())
}

func TestAudioSourceConfig_Validate_DefaultModel(t *testing.T) {
	t.Parallel()

	src := &AudioSourceConfig{
		Name:   "Mic",
		Device: "sysdefault",
		Model:  "", // Empty = default (birdnet)
	}
	assert.NoError(t, src.Validate())
}

func TestAudioSourceConfig_Validate_FutureModels(t *testing.T) {
	t.Parallel()

	for _, model := range []string{"perch_v2", "bat"} {
		src := &AudioSourceConfig{
			Name:   "Mic",
			Device: "sysdefault",
			Model:  model,
		}
		assert.NoError(t, src.Validate(), "model %q should be valid", model)
	}
}

func TestAudioSourceConfig_Validate_NameRequired(t *testing.T) {
	t.Parallel()

	src := &AudioSourceConfig{
		Name:   "",
		Device: "sysdefault",
	}
	err := src.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestAudioSourceConfig_Validate_NameTooLong(t *testing.T) {
	t.Parallel()

	src := &AudioSourceConfig{
		Name:   strings.Repeat("a", MaxAudioSourceNameLength+1),
		Device: "sysdefault",
	}
	err := src.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum length")
}

func TestAudioSourceConfig_Validate_DeviceRequired(t *testing.T) {
	t.Parallel()

	src := &AudioSourceConfig{
		Name:   "Mic",
		Device: "",
	}
	err := src.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "device is required")
}

// TestAudioSourceConfig_Validate_RejectsGPSCoordinates is a regression
// test for the case where a user pasted GPS coordinates (intended for
// birdnet.latitude/longitude) into the audio source device field.
// The device string parses at config load time but fails forever once
// the audio engine tries to open it, producing repeated Sentry events.
func TestAudioSourceConfig_Validate_RejectsGPSCoordinates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		device string
	}{
		{"positive lat/lon", ":45.5,-120.5"},
		{"negative lat", ":-45.5,120.5"},
		{"explicit signs", ":+45.5,-120.5"},
		{"integer coords", ":45,-120"},
		// Naked variant — same nonsense, no leading colon. Must also be
		// rejected even though it does not mimic the ALSA device-prefix
		// shape, since real ALSA/Pulse device strings always start with
		// a letter.
		{"naked decimal coords", "45.5,-120.5"},
		{"naked integer coords", "45,120"},
		// Whitespace variants — copy-pasted from map services or
		// spreadsheet exports often include a space after (or around)
		// the comma. The shape is still nonsense as an audio device
		// and must be rejected with the same clear error.
		{"colon space after comma", ":45.5, -120.5"},
		{"naked space after comma", "45.5, -120.5"},
		{"naked space before comma", "45.5 ,-120.5"},
		{"naked spaces both sides", ":45.5  ,  -120.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := &AudioSourceConfig{
				Name:   "Mic",
				Device: tt.device,
			}
			err := src.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GPS coordinates",
				"GPS-shaped device strings must fail with a clear error")
		})
	}
}

// TestAudioSourceConfig_Validate_AcceptsRealDeviceStrings ensures the
// GPS regex is narrow enough that real ALSA/Pulse/CoreAudio device
// shapes still validate. The plan scopes this to a narrow negative
// regex, so a permissive positive-shape check is out of scope.
func TestAudioSourceConfig_Validate_AcceptsRealDeviceStrings(t *testing.T) {
	t.Parallel()

	tests := []string{
		"default",
		"sysdefault",
		"hw:0,0",
		"hw:CARD0,DEV0",
		"plughw:0,0",
		"pulse:0",
		"pulse",
		"Built-in Microphone",
	}
	for _, device := range tests {
		t.Run(device, func(t *testing.T) {
			t.Parallel()
			src := &AudioSourceConfig{
				Name:   "Mic",
				Device: device,
			}
			assert.NoError(t, src.Validate(), "real device string %q must still validate", device)
		})
	}
}

func TestAudioSourceConfig_Validate_GainRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		gain    float64
		wantErr bool
	}{
		{"zero gain", 0, false},
		{"max gain", MaxAudioGain, false},
		{"min gain", MinAudioGain, false},
		{"over max", MaxAudioGain + 1, true},
		{"under min", MinAudioGain - 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := &AudioSourceConfig{
				Name:   "Mic",
				Device: "sysdefault",
				Gain:   tt.gain,
			}
			err := src.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "gain")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAudioSourceConfig_Validate_UnknownModel(t *testing.T) {
	t.Parallel()

	src := &AudioSourceConfig{
		Name:   "Mic",
		Device: "sysdefault",
		Model:  "unknown_model",
	}
	err := src.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown model")
}

func TestAudioSourceConfig_Validate_PerSourceEQ(t *testing.T) {
	t.Parallel()

	src := &AudioSourceConfig{
		Name:   "Mic",
		Device: "sysdefault",
		Equalizer: &EqualizerSettings{
			Enabled: true,
			Filters: []EqualizerFilter{
				{Type: "HighPass", Frequency: 100},
			},
		},
	}
	assert.NoError(t, src.Validate())
}

func TestAudioSourceConfig_Validate_PerSourceEQInvalidFrequency(t *testing.T) {
	t.Parallel()

	src := &AudioSourceConfig{
		Name:   "Mic",
		Device: "sysdefault",
		Equalizer: &EqualizerSettings{
			Enabled: true,
			Filters: []EqualizerFilter{
				{Type: "HighPass", Frequency: 0},
			},
		},
	}
	err := src.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid frequency")
}

func TestAudioSettings_ValidateSources_DuplicateNames(t *testing.T) {
	t.Parallel()

	audio := &AudioSettings{
		Sources: []AudioSourceConfig{
			{Name: "Mic 1", Device: "hw:0,0"},
			{Name: "mic 1", Device: "hw:1,0"}, // Case-insensitive duplicate
		},
	}
	err := audio.ValidateSources()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate audio source name")
}

func TestAudioSettings_ValidateSources_DuplicateDevices(t *testing.T) {
	t.Parallel()

	audio := &AudioSettings{
		Sources: []AudioSourceConfig{
			{Name: "Mic 1", Device: "sysdefault"},
			{Name: "Mic 2", Device: "sysdefault"},
		},
	}
	err := audio.ValidateSources()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate device")
}

func TestAudioSettings_ValidateSources_EmptyArray(t *testing.T) {
	t.Parallel()

	audio := &AudioSettings{
		Sources: []AudioSourceConfig{},
	}
	assert.NoError(t, audio.ValidateSources(), "empty sources array should be valid")
}

func TestAudioSettings_ValidateSources_MultipleSources(t *testing.T) {
	t.Parallel()

	audio := &AudioSettings{
		Sources: []AudioSourceConfig{
			{Name: "Front Yard", Device: "hw:0,0"},
			{Name: "Back Yard", Device: "hw:1,0", Gain: 6.0, Model: "birdnet"},
			{Name: "Bat Detector", Device: "hw:2,0", Model: "bat"},
		},
	}
	assert.NoError(t, audio.ValidateSources())
}
