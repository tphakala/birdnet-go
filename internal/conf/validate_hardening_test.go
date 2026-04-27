package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// -----------------------------------------------------------------------
// Issue #498: Zero interval accepted
// -----------------------------------------------------------------------

func TestValidateRealtimeSettings_IntervalMustBePositive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		interval int
		wantErr  bool
		errType  string
	}{
		{"zero interval rejected", 0, true, "realtime-interval"},
		{"negative interval rejected", -5, true, "realtime-interval"},
		{"positive interval accepted", 15, false, ""},
		{"boundary: 1 second accepted", 1, false, ""},
		{"max interval accepted", MaxRealtimeInterval, false, ""},
		{"exceeds max interval", MaxRealtimeInterval + 1, true, "realtime-interval"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &RealtimeSettings{
				Interval: tt.interval,
				Audio: AudioSettings{
					Sources: []AudioSourceConfig{
						{Name: "test", Device: testAudioDeviceSysdefault, Model: "birdnet"},
					},
					Export: ExportSettings{Type: AudioExportTypeWAV},
				},
			}

			err := validateRealtimeSettings(settings)

			if tt.wantErr {
				assertValidationError(t, err, tt.errType)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// -----------------------------------------------------------------------
// Issue #499: HTML/XSS in site name
// -----------------------------------------------------------------------

func TestSanitizeStringField(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain text unchanged", "My Bird Station", "My Bird Station"},
		{"script tag stripped", "<script>alert(1)</script>", "alert(1)"},
		{"HTML tags stripped", "<b>Bold</b> <i>italic</i>", "Bold italic"},
		{"empty string unchanged", "", ""},
		{"self-closing tag stripped", "test<br/>text", "testtext"},
		{"nested tags stripped", "<div><span>nested</span></div>", "nested"},
		{"angle brackets without tag shape", "5 < 10", "5 < 10"},
		{"attributes stripped with tag", `<img src="x" onerror="alert(1)">`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := sanitizeStringField(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateSettings_MainNameSanitized(t *testing.T) {
	settings := createMinimalValidSettings()
	settings.Main.Name = "<script>alert('xss')</script>My Station"

	// ValidateSettings should sanitize the name in place
	_ = ValidateSettings(settings)

	assert.Equal(t, "alert('xss')My Station", settings.Main.Name)
	assert.NotContains(t, settings.Main.Name, "<script>")
}

// -----------------------------------------------------------------------
// Issue #500: Path traversal in export path
// -----------------------------------------------------------------------

func TestValidateExportPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		path    string
		wantErr bool
		errMsg  string
	}{
		{"empty path allowed", "", false, ""},
		{"simple relative path", "clips", false, ""},
		{"nested relative path", "data/clips/birds", false, ""},
		{"parent traversal rejected", "../../../etc/passwd", true, "path traversal"},
		{"hidden traversal rejected", "foo/../../../etc/passwd", true, "path traversal"},
		{"double dot in middle rejected", "data/../secret", true, "path traversal"},
		{"absolute path rejected", "/var/data/clips", true, "must be relative"},
		{"windows-style path treated as relative on unix", "C:\\data\\clips", false, ""},
		{"dot-only rejected", "..", true, "path traversal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateExportPath(tt.path)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateAudioSettings_ExportPathTraversal(t *testing.T) {
	settings := &AudioSettings{
		Sources: []AudioSourceConfig{
			{Name: "test", Device: testAudioDeviceSysdefault, Model: "birdnet"},
		},
		Export: ExportSettings{
			Enabled: true,
			Path:    "../../../etc/passwd",
			Type:    AudioExportTypeWAV,
			Length:  15,
		},
		Equalizer: EqualizerSettings{Enabled: false},
	}

	err := validateAudioSettings(settings)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

// -----------------------------------------------------------------------
// Issue #501: EQ filter negative frequency and zero Q
// -----------------------------------------------------------------------

func TestValidateEQFilters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		filters []EqualizerFilter
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid filters",
			filters: []EqualizerFilter{{Frequency: 1000, Q: 1.0, Type: "Peaking"}},
			wantErr: false,
		},
		{
			name:    "empty filters",
			filters: []EqualizerFilter{},
			wantErr: false,
		},
		{
			name:    "zero frequency rejected",
			filters: []EqualizerFilter{{Frequency: 0, Q: 1.0}},
			wantErr: true,
			errMsg:  "invalid frequency",
		},
		{
			name:    "negative frequency rejected",
			filters: []EqualizerFilter{{Frequency: -100, Q: 1.0}},
			wantErr: true,
			errMsg:  "invalid frequency",
		},
		{
			name:    "frequency exceeds max rejected",
			filters: []EqualizerFilter{{Frequency: MaxEQFrequency + 1, Q: 1.0}},
			wantErr: true,
			errMsg:  "exceeds maximum",
		},
		{
			name:    "boundary: max frequency accepted",
			filters: []EqualizerFilter{{Frequency: MaxEQFrequency, Q: 1.0}},
			wantErr: false,
		},
		{
			name:    "zero Q rejected",
			filters: []EqualizerFilter{{Frequency: 1000, Q: 0}},
			wantErr: true,
			errMsg:  "invalid Q factor",
		},
		{
			name:    "negative Q rejected",
			filters: []EqualizerFilter{{Frequency: 1000, Q: -0.5}},
			wantErr: true,
			errMsg:  "invalid Q factor",
		},
		{
			name:    "Q exceeds max rejected",
			filters: []EqualizerFilter{{Frequency: 1000, Q: MaxEQQ + 1}},
			wantErr: true,
			errMsg:  "exceeds maximum",
		},
		{
			name:    "boundary: max Q accepted",
			filters: []EqualizerFilter{{Frequency: 1000, Q: MaxEQQ}},
			wantErr: false,
		},
		{
			name: "second filter invalid",
			filters: []EqualizerFilter{
				{Frequency: 1000, Q: 1.0},
				{Frequency: -500, Q: 2.0},
			},
			wantErr: true,
			errMsg:  "filter 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateEQFilters(tt.filters, "test")

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateAudioSettings_GlobalEQFilters(t *testing.T) {
	settings := &AudioSettings{
		Sources: []AudioSourceConfig{
			{Name: "test", Device: testAudioDeviceSysdefault, Model: "birdnet"},
		},
		Export: ExportSettings{Type: AudioExportTypeWAV},
		Equalizer: EqualizerSettings{
			Enabled: true,
			Filters: []EqualizerFilter{
				{Frequency: 0, Q: 1.0, Type: "LowPass"},
			},
		},
	}

	err := validateAudioSettings(settings)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid frequency")
}

// -----------------------------------------------------------------------
// Issue #502: Dynamic threshold cross-field validation
// -----------------------------------------------------------------------

func TestValidateDynamicThresholdSettings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings DynamicThresholdSettings
		wantErr  bool
		errType  string
	}{
		{
			name:     "disabled skips validation",
			settings: DynamicThresholdSettings{Enabled: false, Trigger: 0, Min: 0, ValidHours: 0},
			wantErr:  false,
		},
		{
			name:     "valid enabled settings",
			settings: DynamicThresholdSettings{Enabled: true, Trigger: 0.8, Min: 0.5, ValidHours: 24},
			wantErr:  false,
		},
		{
			name:     "trigger equals min accepted",
			settings: DynamicThresholdSettings{Enabled: true, Trigger: 0.5, Min: 0.5, ValidHours: 12},
			wantErr:  false,
		},
		{
			name:     "trigger out of range high",
			settings: DynamicThresholdSettings{Enabled: true, Trigger: 1.5, Min: 0.5, ValidHours: 24},
			wantErr:  true,
			errType:  "dynamic-threshold-trigger",
		},
		{
			name:     "trigger out of range negative",
			settings: DynamicThresholdSettings{Enabled: true, Trigger: -0.1, Min: 0.0, ValidHours: 24},
			wantErr:  true,
			errType:  "dynamic-threshold-trigger",
		},
		{
			name:     "min out of range high",
			settings: DynamicThresholdSettings{Enabled: true, Trigger: 0.8, Min: 1.1, ValidHours: 24},
			wantErr:  true,
			errType:  "dynamic-threshold-min",
		},
		{
			name:     "min out of range negative",
			settings: DynamicThresholdSettings{Enabled: true, Trigger: 0.8, Min: -0.1, ValidHours: 24},
			wantErr:  true,
			errType:  "dynamic-threshold-min",
		},
		{
			name:     "min exceeds trigger rejected",
			settings: DynamicThresholdSettings{Enabled: true, Trigger: 0.5, Min: 0.8, ValidHours: 24},
			wantErr:  true,
			errType:  "dynamic-threshold-cross-field",
		},
		{
			name:     "zero valid hours rejected",
			settings: DynamicThresholdSettings{Enabled: true, Trigger: 0.8, Min: 0.5, ValidHours: 0},
			wantErr:  true,
			errType:  "dynamic-threshold-valid-hours",
		},
		{
			name:     "negative valid hours rejected",
			settings: DynamicThresholdSettings{Enabled: true, Trigger: 0.8, Min: 0.5, ValidHours: -1},
			wantErr:  true,
			errType:  "dynamic-threshold-valid-hours",
		},
		{
			name:     "boundary: trigger and min both zero",
			settings: DynamicThresholdSettings{Enabled: true, Trigger: 0, Min: 0, ValidHours: 1},
			wantErr:  false,
		},
		{
			name:     "boundary: trigger and min both one",
			settings: DynamicThresholdSettings{Enabled: true, Trigger: 1.0, Min: 1.0, ValidHours: 1},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateDynamicThresholdSettings(&tt.settings)

			if tt.wantErr {
				assertValidationError(t, err, tt.errType)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// -----------------------------------------------------------------------
// Issue #503: Empty strings for required fields
// -----------------------------------------------------------------------

func TestValidateSettings_EmptyBirdNETLocaleDefaulted(t *testing.T) {
	settings := createMinimalValidSettings()
	settings.BirdNET.Locale = ""

	_ = ValidateSettings(settings)

	// The default "en" is normalized by validateBirdNETSettings to "en-uk"
	assert.NotEmpty(t, settings.BirdNET.Locale,
		"empty locale should be defaulted, not left empty")
	assert.Contains(t, settings.BirdNET.Locale, "en",
		"defaulted locale should be an English variant")
}

func TestValidateWeatherSettings_InvalidProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		provider string
		wantErr  bool
	}{
		{"empty provider allowed", "", false},
		{"none provider allowed", "none", false},
		{"yrno provider allowed", "yrno", false},
		{"openweather provider allowed", "openweather", false},
		{"wunderground provider allowed", "wunderground", false},
		{"unknown provider rejected", "invalid_provider", true},
		{"whitespace-only rejected", "  ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &WeatherSettings{
				Provider:     tt.provider,
				PollInterval: 30,
				Wunderground: WundergroundSettings{
					APIKey:    "testkey",
					StationID: "KTEST1",
				},
			}

			err := validateWeatherSettings(settings)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "weather provider")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// -----------------------------------------------------------------------
// Issue #504: Retention maxAge/minClips
// -----------------------------------------------------------------------

func TestValidateRetentionSettings_MaxAgeMustBePositive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		maxAge  string
		wantErr bool
		errMsg  string
	}{
		{"valid age: 24h", "24h", false, ""},
		{"valid age: 7d", "7d", false, ""},
		{"valid age: 1w", "1w", false, ""},
		{"invalid: abc", "abc", true, "invalid"},
		{"zero hours rejected", "0h", true, "must be positive"},
		{"zero days rejected", "0d", true, "must be positive"},
		{"negative value rejected", "-5h", true, "must be positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &RetentionSettings{
				Policy: RetentionPolicyAge,
				MaxAge: tt.maxAge,
			}

			err := validateRetentionSettings(settings)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateRetentionSettings_MinClipsNonNegative(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		minClips int
		wantErr  bool
	}{
		{"zero accepted", 0, false},
		{"positive accepted", 5, false},
		{"negative rejected", -1, true},
		{"large negative rejected", -100, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &RetentionSettings{
				Policy:   RetentionPolicyAge,
				MaxAge:   "24h",
				MinClips: tt.minClips,
			}

			err := validateRetentionSettings(settings)

			if tt.wantErr {
				assertValidationError(t, err, "retention-min-clips")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateRetentionSettings_MinClipsValidatedRegardlessOfPolicy(t *testing.T) {
	// MinClips should be validated even with "none" or "usage" policy
	settings := &RetentionSettings{
		Policy:   RetentionPolicyUsage,
		MaxUsage: "80%",
		MinClips: -5,
	}

	err := validateRetentionSettings(settings)
	require.Error(t, err)

	enhanced := requireEnhancedError(t, err)
	assert.Equal(t, errors.CategoryValidation, enhanced.Category)
}

// -----------------------------------------------------------------------
// Helper: createMinimalValidSettings creates a Settings struct that passes
// all validation, so tests can modify one field at a time.
// -----------------------------------------------------------------------

func createMinimalValidSettings() *Settings {
	s := &Settings{}

	// Main
	s.Main.Name = "Test Station"

	// BirdNET
	s.BirdNET.Sensitivity = 1.0
	s.BirdNET.Threshold = 0.7
	s.BirdNET.Overlap = 1.5
	s.BirdNET.Locale = "en"

	// WebServer
	s.WebServer.Enabled = true
	s.WebServer.Port = "8080"
	s.WebServer.LiveStream.BitRate = 128
	s.WebServer.LiveStream.SegmentLength = 5

	// Realtime
	s.Realtime.Interval = 15
	s.Realtime.Audio.Sources = []AudioSourceConfig{
		{Name: "test", Device: testAudioDeviceSysdefault, Model: "birdnet"},
	}
	s.Realtime.Audio.Export.Type = AudioExportTypeWAV
	s.Realtime.Audio.Export.Length = 15

	// Weather
	s.Realtime.Weather.PollInterval = 30

	// Dynamic threshold (disabled by default)
	s.Realtime.DynamicThreshold.Enabled = false

	return s
}
