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
