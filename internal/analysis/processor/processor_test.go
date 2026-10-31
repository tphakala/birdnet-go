package processor

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// keepAllAlternates is the keep predicate for tests that exercise
// convertToAdditionalResults without a range filter.
func keepAllAlternates(string) bool { return true }

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
			result := convertToAdditionalResults(tt.input, tt.primaryScientificName, keepAllAlternates)
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

// TestConvertToAdditionalResults_ExcludesAliasedPrimary verifies that the primary
// species is excluded from the additional results even when the model emitted it under
// a legacy/alias scientific name while primaryScientificName is the canonical name (as
// happens after parseAndValidateSpecies canonicalizes the primary). Otherwise the
// aliased primary would be stored a second time as an additional result.
func TestConvertToAdditionalResults_ExcludesAliasedPrimary(t *testing.T) {
	t.Parallel()

	input := []datastore.Results{
		// The model emitted the primary taxon under its legacy v2.4 name.
		{Species: "Streptopelia senegalensis_Laughing Dove", Confidence: 0.91},
		{Species: "Turdus merula_Eurasian Blackbird", Confidence: 0.40},
	}

	// primaryScientificName is the canonical name resolved by parseAndValidateSpecies.
	result := convertToAdditionalResults(input, "Spilopelia senegalensis", keepAllAlternates)

	require.Len(t, result, 1, "the aliased primary must be excluded from additional results")
	assert.Equal(t, "Turdus merula", result[0].Species.ScientificName)
}

func TestConvertToAdditionalResults_SetsRawLabelFromSpecies(t *testing.T) {
	t.Parallel()

	input := []datastore.Results{
		{Species: "male_speech_and_man_speaking", Confidence: 0.80},
		{Species: "Turdus merula_Common Blackbird", Confidence: 0.75},
	}

	result := convertToAdditionalResults(input, "", keepAllAlternates)

	require.Len(t, result, 2)
	assert.Equal(t, "male_speech_and_man_speaking", result[0].RawLabel, "raw label must equal the original r.Species string")
	assert.Equal(t, "Turdus merula_Common Blackbird", result[1].RawLabel, "raw label must equal the original r.Species string")
}

// TestConvertToAdditionalResults_SkipsNonFiniteConfidence verifies that a
// top-K entry carrying a NaN or Inf confidence is dropped from the additional
// results rather than persisted (NULL on SQLite, a rejected insert on MySQL).
func TestConvertToAdditionalResults_SkipsNonFiniteConfidence(t *testing.T) {
	t.Parallel()

	input := []datastore.Results{
		{Species: "Periparus ater_Coal Tit", Confidence: 0.80},
		{Species: "Parus major_Great Tit", Confidence: float32(math.NaN())},
		{Species: "Corvus corax_Common Raven", Confidence: float32(math.Inf(1))},
		{Species: "Turdus merula_Common Blackbird", Confidence: 0.40},
	}

	result := convertToAdditionalResults(input, "", keepAllAlternates)

	require.Len(t, result, 2, "non-finite entries must be skipped")
	assert.Equal(t, "Periparus ater", result[0].Species.ScientificName)
	assert.Equal(t, "Turdus merula", result[1].Species.ScientificName)
}

func TestConvertToAdditionalResults_PerchSoundClassesStayDistinct(t *testing.T) {
	t.Parallel()

	input := []datastore.Results{
		{Species: "Domestic_sounds_and_home_sounds", Confidence: 0.90},
		{Species: "Domestic_animals_and_pets", Confidence: 0.60},
	}

	result := convertToAdditionalResults(input, "Domestic_sounds_and_home_sounds", keepAllAlternates)

	require.Len(t, result, 1, "only the primary class is dropped")
	assert.Equal(t, "Domestic_animals_and_pets", result[0].Species.ScientificName)
}

// Raw labels and canonical names used by the alternates range-filter tests.
const (
	altBlackbirdLabel = "Turdus merula_Common Blackbird"
	altGreatTitLabel  = "Parus major_Great Tit"
	altKiskadeeLabel  = "Pitangus sulphuratus_Great Kiskadee"
	altAliasDoveLabel = "Streptopelia senegalensis_Laughing Dove"
	altGunLabel       = "Gun_Gun"
	altAliasDoveSci   = "Streptopelia senegalensis"
	altKiskadeeSci    = "Pitangus sulphuratus"
	altHumanLabel     = "Homo sapiens"
	altDogLabel       = "Canis familiaris"
	altAlternateConf  = 0.5
)

// altRangeSnapshot builds the per-chunk settings snapshot with the range filter
// active and the given canonical included species.
func altRangeSnapshot(included ...string) *conf.Settings {
	settings := &conf.Settings{}
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.IncludedScientificNames = make(map[string]struct{}, len(included))
	for _, name := range included {
		settings.BirdNET.RangeFilter.IncludedScientificNames[name] = struct{}{}
	}
	return settings
}

func altResults(labels ...string) []datastore.Results {
	results := make([]datastore.Results, len(labels))
	for i, label := range labels {
		results[i] = datastore.Results{Species: label, Confidence: altAlternateConf}
	}
	return results
}

func TestCreateDetection_StoresOnlyAlternatesInRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		modelID  string
		included []string
		noRange  bool // snapshot without a configured location
		labels   []string
		want     []string
	}{
		{
			name:     "BirdNET V2.4 keeps in-range, aliased and sound-event alternates and drops out-of-range ones",
			modelID:  classifier.RegistryIDBirdNETV24,
			included: []string{"turdus merula", "parus major", "spilopelia senegalensis"},
			labels:   []string{altBlackbirdLabel, altGreatTitLabel, altKiskadeeLabel, altAliasDoveLabel, altGunLabel},
			want:     []string{"Parus major", altAliasDoveSci, "Gun"},
		},
		{
			name:     "Perch keeps the human and dog taxa",
			modelID:  classifier.RegistryIDPerchV2,
			included: []string{"turdus merula"},
			labels:   []string{"Turdus merula", altKiskadeeSci, altHumanLabel, altDogLabel},
			want:     []string{altHumanLabel, altDogLabel},
		},
		{
			name:     "BirdNET V2.4 keeps out-of-range alternates when no location is configured",
			modelID:  classifier.RegistryIDBirdNETV24,
			included: []string{"turdus merula"},
			noRange:  true,
			labels:   []string{altBlackbirdLabel, altKiskadeeLabel},
			want:     []string{altKiskadeeSci},
		},
		{
			name:     "BSG keeps out-of-range alternates",
			modelID:  classifier.RegistryIDBSG,
			included: []string{"turdus merula", "parus major", "spilopelia senegalensis"},
			labels:   []string{altBlackbirdLabel, altGreatTitLabel, altKiskadeeLabel},
			want:     []string{"Parus major", altKiskadeeSci},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// p.Settings has no location, so reading it instead of the passed
			// snapshot would keep every alternate.
			p := &Processor{Settings: &conf.Settings{}, Bn: &classifier.Orchestrator{}}
			item := classifier.Results{
				StartTime: time.Now(),
				ModelID:   tt.modelID,
				Results:   altResults(tt.labels...),
			}
			primary := item.Results[0]
			primarySci := detection.ParseSpeciesString(primary.Species).ScientificName

			settings := altRangeSnapshot(tt.included...)
			settings.BirdNET.LocationConfigured = !tt.noRange
			det := p.createDetection(settings, item, primary, primarySci, "Primary", "", primary.Species)

			got := make([]string, len(det.Results))
			for i, r := range det.Results {
				got[i] = r.Species.ScientificName
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestProcessResults_RangeRuleSharedByFeedPrimaryAndAlternates drives one chunk
// through processResults. It is not parallel because mergeTestProcessor resets
// the global settings.
func TestProcessResults_RangeRuleSharedByFeedPrimaryAndAlternates(t *testing.T) {
	const (
		threshold      = 0.5
		primaryConf    = 0.9
		outOfRangeConf = 0.8
		blackbirdSci   = "Turdus merula"
		chunkSource    = "range-src"
	)
	p := mergeTestProcessor(t)
	p.Bn = &classifier.Orchestrator{}
	settings := altRangeSnapshot("turdus merula")
	settings.BirdNET.Threshold = threshold
	end := time.Now()
	item := classifier.Results{
		Source:    datastore.AudioSource{ID: chunkSource},
		ModelID:   classifier.RegistryIDBirdNETV24,
		StartTime: end.Add(-mergeTestClip),
		Audio:     classifier.AudioTiming{EndAt: end},
		Results: []datastore.Results{
			{Species: altBlackbirdLabel, Confidence: primaryConf},
			{Species: altKiskadeeLabel, Confidence: outOfRangeConf},
		},
	}

	detections := p.processResults(settings, item)

	require.Len(t, detections, 1, "the out-of-range species fails the primary gate")
	assert.Equal(t, blackbirdSci, detections[0].Result.Species.ScientificName)
	for _, r := range detections[0].Results {
		assert.NotEqual(t, altKiskadeeSci, r.Species.ScientificName, "out-of-range alternate must not be stored")
	}

	inRange := map[string]bool{}
	for _, d := range p.GetRecentDetections(classifier.RegistryIDBirdNETV24) {
		inRange[d.ScientificName] = d.InRange
	}
	assert.Equal(t, map[string]bool{blackbirdSci: true, altKiskadeeSci: false}, inRange)
}
