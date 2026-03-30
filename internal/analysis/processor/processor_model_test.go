package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

			result := p.createDetectionResult(
				time.Now(),
				time.Now(), time.Now().Add(3*time.Second),
				"Parus major", "Great Tit", "gretit1",
				0.95,
				datastore.AudioSource{ID: "test", DisplayName: "Test"},
				"clip.wav",
				100*time.Millisecond, 0.5,
				tc.modelID,
			)

			assert.Equal(t, tc.expectedName, result.Model.Name)
			assert.Equal(t, tc.expectedVersion, result.Model.Version)
			assert.Equal(t, tc.expectedVariant, result.Model.Variant)
		})
	}
}
