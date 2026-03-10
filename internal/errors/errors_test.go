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

func TestLookupComponentSegmentMatching(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		funcName string
		want     string
	}{
		{
			name:     "birdnet package matches birdnet component",
			funcName: "github.com/tphakala/birdnet-go/internal/birdnet.Predict",
			want:     "birdnet",
		},
		{
			name:     "module path birdnet-go does not match birdnet",
			funcName: "github.com/tphakala/birdnet-go/cmd.Execute",
			want:     "cmd", // Falls through to package name extraction fallback
		},
		{
			name:     "myaudio package matches",
			funcName: "github.com/tphakala/birdnet-go/internal/myaudio.ProcessSoundLevelData",
			want:     "myaudio",
		},
		{
			name:     "analysis/processor subpackage matches",
			funcName: "github.com/tphakala/birdnet-go/internal/analysis/processor.NewProcessor",
			want:     "analysis.processor",
		},
		{
			name:     "api package matches",
			funcName: "github.com/tphakala/birdnet-go/internal/api.New",
			want:     "api",
		},
		{
			name:     "api/v2 subpackage still matches api",
			funcName: "github.com/tphakala/birdnet-go/internal/api/v2.HandleRequest",
			want:     "api",
		},
		{
			name:     "datastore matches",
			funcName: "github.com/tphakala/birdnet-go/internal/datastore.Save",
			want:     "datastore",
		},
		{
			name:     "conf matches configuration",
			funcName: "github.com/tphakala/birdnet-go/internal/conf.Setting",
			want:     "configuration",
		},
		{
			name:     "completely unknown function uses package name fallback",
			funcName: "github.com/other/pkg.Foo",
			want:     "pkg", // Package name extracted from path
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := lookupComponent(tt.funcName)
			assert.Equal(t, tt.want, got)
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
