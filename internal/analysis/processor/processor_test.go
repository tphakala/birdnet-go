package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

func TestConvertToAdditionalResults_DeduplicatesByScientificName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		input                 []datastore.Results
		primaryScientificName string
		expectedCount         int
		expectedFirst         string
		expectedFirstC        float64
	}{
		{
			name: "duplicate_species_keeps_highest_confidence",
			input: []datastore.Results{
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.99},
				{Species: "Parus major_talitiainen", Confidence: 0.50},
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.92},
			},
			primaryScientificName: "",
			expectedCount:         2,
			expectedFirst:         "Periparus ater",
			expectedFirstC:        0.99,
		},
		{
			name: "duplicate_lower_confidence_first",
			input: []datastore.Results{
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.50},
				{Species: "Parus major_talitiainen", Confidence: 0.30},
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.95},
			},
			primaryScientificName: "",
			expectedCount:         2,
			expectedFirst:         "Periparus ater",
			expectedFirstC:        0.95,
		},
		{
			name: "multiple_different_duplicates",
			input: []datastore.Results{
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.99},
				{Species: "Parus major_talitiainen", Confidence: 0.80},
				{Species: "Corvus corax_korppi", Confidence: 0.70},
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.92},
				{Species: "Parus major_talitiainen", Confidence: 0.85},
			},
			primaryScientificName: "",
			expectedCount:         3,
			expectedFirst:         "Periparus ater",
			expectedFirstC:        0.99,
		},
		{
			name: "no_duplicates_unchanged",
			input: []datastore.Results{
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.99},
				{Species: "Parus major_talitiainen", Confidence: 0.50},
				{Species: "Corvus corax_korppi", Confidence: 0.30},
			},
			primaryScientificName: "",
			expectedCount:         3,
			expectedFirst:         "Periparus ater",
			expectedFirstC:        0.99,
		},
		{
			name: "excludes_primary_species",
			input: []datastore.Results{
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.99},
				{Species: "Parus major_talitiainen", Confidence: 0.50},
				{Species: "Corvus corax_korppi", Confidence: 0.30},
			},
			primaryScientificName: "Periparus ater",
			expectedCount:         2,
			expectedFirst:         "Parus major",
			expectedFirstC:        0.50,
		},
		{
			name: "excludes_primary_with_duplicates",
			input: []datastore.Results{
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.99},
				{Species: "Parus major_talitiainen", Confidence: 0.50},
				{Species: "Periparus ater_kuusitiainen", Confidence: 0.92},
			},
			primaryScientificName: "Periparus ater",
			expectedCount:         1,
			expectedFirst:         "Parus major",
			expectedFirstC:        0.50,
		},
		{
			name:                  "empty_input",
			input:                 []datastore.Results{},
			primaryScientificName: "",
			expectedCount:         0,
		},
		{
			name:                  "nil_input",
			input:                 nil,
			primaryScientificName: "",
			expectedCount:         0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := convertToAdditionalResults(tt.input, tt.primaryScientificName)
			require.Len(t, result, tt.expectedCount)

			if tt.expectedCount > 0 {
				assert.Equal(t, tt.expectedFirst, result[0].Species.ScientificName)
				assert.InDelta(t, tt.expectedFirstC, result[0].Confidence, 0.001)
			}

			// Verify no duplicate scientific names in output
			seen := make(map[string]bool)
			for _, r := range result {
				assert.False(t, seen[r.Species.ScientificName],
					"duplicate scientific name in output: %s", r.Species.ScientificName)
				seen[r.Species.ScientificName] = true
			}
		})
	}
}

// TestProcessor_resolveAudioSource_EnrichesFromRegistry guards the enrichment
// chain that links a configured SourceConfig.DisplayName through the
// audiocore source registry to the detection.AudioSource.DisplayName stored
// with every detection.
//
// The contract this test protects: resolveAudioSource must use the registry
// (when set) to fill in the human-readable DisplayName and Type for the
// detection, preferring a match by connection string (datastore.AudioSource.ID
// == SourceInfo.ConnectionString) and falling back to a match by SourceInfo.ID.
// When the registry is unset or has no match, the input must pass through
// unchanged with Type derived from the SafeString.
func TestProcessor_resolveAudioSource_EnrichesFromRegistry(t *testing.T) {
	t.Parallel()
	t.Attr("component", "processor")
	t.Attr("feature", "source-enrichment")

	// Connection strings used throughout the cases. Declared as named
	// constants so the intent of each fixture is obvious and the project
	// rule against magic strings is respected.
	const (
		alsaConnection = "hw:1,0"
		alsaDisplay    = "Front Yard"
		rtspID         = "rtsp_abc123"
		rtspConnection = "rtsp://camera.local/stream"
		rtspDisplay    = "Back Porch"
		otherID        = "rtsp_other"
		otherConn      = "rtsp://other.local/stream"
	)

	// registrySetup describes how to seed the registry before the test runs.
	// A nil setup func means the processor is constructed without a registry,
	// exercising the "registry not set" branch of resolveAudioSource.
	type registrySetup func(t *testing.T, r *audiocore.SourceRegistry)

	tests := []struct {
		name            string
		input           datastore.AudioSource
		setup           registrySetup
		wantID          string
		wantSafeString  string
		wantDisplayName string
		wantType        string
	}{
		{
			name: "sound_card_match_by_connection_string",
			input: datastore.AudioSource{
				ID:          alsaConnection,
				SafeString:  alsaConnection,
				DisplayName: alsaConnection,
			},
			setup: func(t *testing.T, r *audiocore.SourceRegistry) {
				t.Helper()
				_, err := r.Register(&audiocore.SourceConfig{
					ID:               "audio_card_xyz",
					ConnectionString: alsaConnection,
					DisplayName:      alsaDisplay,
					Type:             audiocore.SourceTypeAudioCard,
				})
				require.NoError(t, err)
			},
			wantID:          "audio_card_xyz",
			wantSafeString:  alsaConnection,
			wantDisplayName: alsaDisplay,
			wantType:        "alsa",
		},
		{
			name: "rtsp_match_by_id_fallback",
			input: datastore.AudioSource{
				ID:          rtspID,
				SafeString:  rtspConnection,
				DisplayName: rtspID,
			},
			setup: func(t *testing.T, r *audiocore.SourceRegistry) {
				t.Helper()
				_, err := r.Register(&audiocore.SourceConfig{
					ID:               rtspID,
					ConnectionString: rtspConnection,
					DisplayName:      rtspDisplay,
					Type:             audiocore.SourceTypeRTSP,
				})
				require.NoError(t, err)
			},
			wantID:          rtspID,
			wantSafeString:  rtspConnection,
			wantDisplayName: rtspDisplay,
			wantType:        "rtsp",
		},
		{
			name: "registry_not_set_pass_through",
			input: datastore.AudioSource{
				ID:          rtspID,
				SafeString:  rtspConnection,
				DisplayName: rtspID,
			},
			setup:           nil,
			wantID:          rtspID,
			wantSafeString:  rtspConnection,
			wantDisplayName: rtspID,
			wantType:        "rtsp",
		},
		{
			name: "registry_has_no_match_pass_through",
			input: datastore.AudioSource{
				ID:          alsaConnection,
				SafeString:  alsaConnection,
				DisplayName: alsaConnection,
			},
			setup: func(t *testing.T, r *audiocore.SourceRegistry) {
				t.Helper()
				_, err := r.Register(&audiocore.SourceConfig{
					ID:               otherID,
					ConnectionString: otherConn,
					DisplayName:      "Unrelated Source",
					Type:             audiocore.SourceTypeRTSP,
				})
				require.NoError(t, err)
			},
			wantID:          alsaConnection,
			wantSafeString:  alsaConnection,
			wantDisplayName: alsaConnection,
			wantType:        "alsa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := &Processor{}
			if tt.setup != nil {
				registry := audiocore.NewSourceRegistry(audiocore.GetLogger())
				tt.setup(t, registry)
				p.SetRegistry(registry)
			}

			got := p.resolveAudioSource(tt.input)
			assert.Equal(t, tt.wantID, got.ID, "resolved ID")
			assert.Equal(t, tt.wantSafeString, got.SafeString, "resolved SafeString")
			assert.Equal(t, tt.wantDisplayName, got.DisplayName,
				"resolved DisplayName must flow from registry when available")
			assert.Equal(t, tt.wantType, got.Type, "resolved Type")
		})
	}
}
