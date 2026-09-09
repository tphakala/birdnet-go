package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

func TestCreateDetectionResult_ModelInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		modelID         string
		expectedName    string
		expectedVersion string
		expectedVariant string
	}{
		{
			name:            "Perch_V2 resolves to Perch model",
			modelID:         "Perch_V2",
			expectedName:    "Perch",
			expectedVersion: "V2",
			expectedVariant: "default",
		},
		{
			name:            "empty modelID defaults to BirdNET",
			modelID:         "",
			expectedName:    "BirdNET",
			expectedVersion: "2.4",
			expectedVariant: "default",
		},
		{
			name:            "BirdNET_V2.4 resolves to BirdNET model",
			modelID:         "BirdNET_V2.4",
			expectedName:    "BirdNET",
			expectedVersion: "2.4",
			expectedVariant: "default",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := &Processor{Settings: &conf.Settings{}}

			result := p.createDetectionResult(p.Settings,
				time.Now(),
				time.Now(), time.Now().Add(3*time.Second),
				"Parus major", "Great Tit", "gretit1", "",
				0.95,
				datastore.AudioSource{ID: "test", DisplayName: "Test"},
				"clip.wav",
				100*time.Millisecond, 0.5,
				tc.modelID,
				"Parus major_Great Tit_gretit1",
			)

			assert.Equal(t, tc.expectedName, result.Model.Name)
			assert.Equal(t, tc.expectedVersion, result.Model.Version)
			assert.Equal(t, tc.expectedVariant, result.Model.Variant)
			assert.Equal(t, "Parus major_Great Tit_gretit1", result.RawLabel,
				"createDetectionResult must set RawLabel from the passed raw label")
		})
	}
}

// TestCreateDetectionResult_ThresholdModelAware verifies the stored detection
// Threshold reflects the model-aware gating threshold: Bat records Bat.Threshold,
// Perch v2 with its override records the Perch threshold, and every other model
// (including Perch with the override off) records the BirdNET threshold.
func TestCreateDetectionResult_ThresholdModelAware(t *testing.T) {
	t.Parallel()

	newSettings := func() *conf.Settings {
		s := &conf.Settings{}
		s.BirdNET.Threshold = 0.80
		s.Bat.Threshold = 0.30
		s.Perch.Threshold = 0.50
		return s
	}

	tests := []struct {
		name          string
		modelID       string
		perchOverride bool
		want          float64
	}{
		{"birdnet records birdnet threshold", "BirdNET_V2.4", false, 0.80},
		{"bat records bat threshold", classifier.RegistryIDBat, false, 0.30},
		{"perch override off records birdnet threshold", classifier.RegistryIDPerchV2, false, 0.80},
		{"perch override on records perch threshold", classifier.RegistryIDPerchV2, true, 0.50},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			settings := newSettings()
			settings.Perch.OverrideThreshold = tc.perchOverride
			p := &Processor{Settings: settings}

			result := p.createDetectionResult(settings,
				time.Now(),
				time.Now(), time.Now().Add(3*time.Second),
				"Parus major", "Great Tit", "gretit1", "",
				0.95,
				datastore.AudioSource{ID: "test", DisplayName: "Test"},
				"clip.wav",
				100*time.Millisecond, 0.5,
				tc.modelID,
				"Parus major_Great Tit_gretit1",
			)

			assert.InDelta(t, tc.want, result.Threshold, 0.001,
				"stored detection Threshold should be the model-aware gating threshold")
		})
	}
}
