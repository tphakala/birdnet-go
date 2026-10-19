package mapper

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/detection"
)

func TestAdditionalResultEntityRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		label       string
		wantStored  string
		wantSciName string
	}{
		{"Perch multi-word class", "Domestic_animals_and_pets", "Domestic_animals_and_pets", "Domestic_animals_and_pets"},
		{"BirdNET label", "Turdus merula_Common Blackbird", "Turdus merula_Common Blackbird", "Turdus merula"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ar := &detection.AdditionalResult{Species: detection.ParseSpeciesString(tt.label), Confidence: 0.5}

			entity := AdditionalResultToEntity(ar, 1)
			assert.Equal(t, tt.wantStored, entity.Species)

			back := EntityToAdditionalResult(entity)
			assert.Equal(t, tt.wantSciName, back.Species.ScientificName)
		})
	}
}
