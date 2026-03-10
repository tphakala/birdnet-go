package errors

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFastPathNoTelemetry(t *testing.T) {
	t.Parallel()

	// Ensure no telemetry or hooks
	SetTelemetryReporter(nil)
	ClearErrorHooks()

	// Create an error - should use fast path
	err := fmt.Errorf("test error")
	ee := New(err).Build()

	assert.Equal(t, "test error", ee.Err.Error(), "expected error message 'test error'")

	// Use production constant directly to ensure test stays in sync
	assert.Equal(t, ComponentUnknown, ee.GetComponent(), "expected component to be unknown in fast path")

	assert.Equal(t, CategoryGeneric, ee.Category, "expected category 'generic' in fast path")
}

func TestErrorOriginTag(t *testing.T) {
	tests := []struct {
		name     string
		category ErrorCategory
		expected string
	}{
		{"validation is code", CategoryValidation, "code"},
		{"not-found is code", CategoryNotFound, "code"},
		{"model-init is code", CategoryModelInit, "code"},
		{"image-cache is code", CategoryImageCache, "code"},
		{"sound-level is code", CategorySoundLevel, "code"},
		{"audio is code", CategoryAudio, "code"},
		{"audio-analysis is code", CategoryAudioAnalysis, "code"},
		{"buffer is code", CategoryBuffer, "code"},
		{"worker is code", CategoryWorker, "code"},
		{"job-queue is code", CategoryJobQueue, "code"},
		{"state is code", CategoryState, "code"},
		{"processing is code", CategoryProcessing, "code"},
		{"limit is code", CategoryLimit, "code"},
		{"threshold is code", CategoryThreshold, "code"},
		{"event-tracking is code", CategoryEventTracking, "code"},
		{"species-tracking is code", CategorySpeciesTracking, "code"},
		{"file-parsing is code", CategoryFileParsing, "code"},
		{"policy-config is code", CategoryPolicyConfig, "code"},
		{"conflict is code", CategoryConflict, "code"},
		{"broadcast is code", CategoryBroadcast, "code"},
		{"image-fetch is environment", CategoryImageFetch, "environment"},
		{"network is environment", CategoryNetwork, "environment"},
		{"database is environment", CategoryDatabase, "environment"},
		{"file-io is environment", CategoryFileIO, "environment"},
		{"configuration is environment", CategoryConfiguration, "environment"},
		{"rtsp is environment", CategoryRTSP, "environment"},
		{"mqtt-connection is environment", CategoryMQTTConnection, "environment"},
		{"mqtt-publish is environment", CategoryMQTTPublish, "environment"},
		{"resource is environment", CategoryResource, "environment"},
		{"system is environment", CategorySystem, "environment"},
		{"command-execution is environment", CategoryCommandExecution, "environment"},
		{"integration is external", CategoryIntegration, "external"},
		{"timeout is unknown", CategoryTimeout, "unknown"},
		{"cancellation is unknown", CategoryCancellation, "unknown"},
		{"retry is unknown", CategoryRetry, "unknown"},
		{"generic is unknown", CategoryGeneric, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := GetErrorOrigin(tt.category)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestFingerprintIncludesNormalizedErrorType(t *testing.T) {
	ee1 := Newf("database is locked").
		Component("datastore").
		Category(CategoryDatabase).
		Context("operation", "save_note").
		Build()

	ee2 := Newf("database or disk is full").
		Component("datastore").
		Category(CategoryDatabase).
		Context("operation", "save_note").
		Build()

	fp1 := buildFingerprint(ee1)
	fp2 := buildFingerprint(ee2)
	assert.NotEqual(t, fp1, fp2, "different root causes should have different fingerprints")
}

func TestMatchesPathSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		s       string
		pattern string
		want    bool
	}{
		{
			name:    "birdnet should not match birdnet-go module path",
			s:       "github.com/tphakala/birdnet-go/internal/datastore.Save",
			pattern: "birdnet",
			want:    false,
		},
		{
			name:    "birdnet should match birdnet package segment",
			s:       "github.com/tphakala/birdnet-go/internal/birdnet.Predict",
			pattern: "birdnet",
			want:    true,
		},
		{
			name:    "myaudio should match myaudio package",
			s:       "github.com/tphakala/birdnet-go/internal/myaudio.ProcessSoundLevelData",
			pattern: "myaudio",
			want:    true,
		},
		{
			name:    "soundlevel does not match camelCase SoundLevel",
			s:       "github.com/tphakala/birdnet-go/internal/analysis.registerSoundLevelProcessors",
			pattern: "soundlevel",
			want:    false, // case-sensitive: "soundlevel" != "SoundLevel"
		},
		{
			name:    "analysis matches as path segment",
			s:       "github.com/tphakala/birdnet-go/internal/analysis.registerSoundLevelProcessors",
			pattern: "analysis",
			want:    true,
		},
		{
			name:    "analysis/processor matches subpackage path",
			s:       "github.com/tphakala/birdnet-go/internal/analysis/processor.Process",
			pattern: "analysis/processor",
			want:    true,
		},
		{
			name:    "pattern at end of string",
			s:       "github.com/tphakala/birdnet-go/internal/datastore",
			pattern: "datastore",
			want:    true,
		},
		{
			name:    "ffmpeg-manager matches hyphenated component name",
			s:       "ffmpeg-manager",
			pattern: "ffmpeg-manager",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := matchesPathSegment(tt.s, tt.pattern)
			assert.Equal(t, tt.want, got, "matchesPathSegment(%q, %q)", tt.s, tt.pattern)
		})
	}
}

func TestLookupComponentAvoidsMisdetection(t *testing.T) {
	t.Parallel()

	// A function in the datastore package should not be detected as "birdnet"
	// just because "birdnet" appears in the module path "birdnet-go"
	result := lookupComponent("github.com/tphakala/birdnet-go/internal/datastore.Save")
	assert.Equal(t, "datastore", result, "should match datastore, not birdnet")

	// A function in the actual birdnet package should match birdnet
	result = lookupComponent("github.com/tphakala/birdnet-go/internal/birdnet.Predict")
	assert.Equal(t, "birdnet", result, "should match birdnet package")
}

func TestRegexPrecompilation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
		contains string
		excludes []string
	}{
		{
			name:     "URL parameter scrubbing",
			input:    "Error at https://api.example.com?api_key=secret123&token=abc",
			expected: "Error at https://api.example.com?[REDACTED]",
		},
		{
			name:     "API key scrubbing",
			input:    "Config error: api_key=secret123 is invalid",
			contains: "[API_KEY_REDACTED]",
		},
		{
			name:     "Multi-token scrubbing",
			input:    "Auth failed with token=abc123 and auth=xyz789",
			excludes: []string{"abc123", "xyz789"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scrubbed := basicURLScrub(tt.input)

			if tt.expected != "" {
				assert.Equal(t, tt.expected, scrubbed)
			}
			if tt.contains != "" {
				assert.Contains(t, scrubbed, tt.contains)
			}
			for _, exclude := range tt.excludes {
				assert.NotContains(t, scrubbed, exclude)
			}
		})
	}
}
