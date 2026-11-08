package detection

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// TestResolveModelType pins the model type each detection model name resolves to.
// The type is written once with the model row, so a bat model that resolved to
// bird would be exported as a lossy clip and drawn with the bird spectrogram range.
func TestResolveModelType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		model   string
		version string
		want    entities.ModelType
	}{
		{"battybirdnet is bat", "BattyBirdNET", "1.0", entities.ModelTypeBat},
		{"bsg-bat is bat", ModelNameBSGBat, "0.21", entities.ModelTypeBat},
		{"perch is multi", "Perch", "V2", entities.ModelTypeMulti},
		{"birdnet 3.0 is multi", "BirdNET", "3.0", entities.ModelTypeMulti},
		{"birdnet 2.4 is bird", "BirdNET", "2.4", entities.ModelTypeBird},
		{"bsg finland is bird", "BSG", "4.4", entities.ModelTypeBird},
		{"unknown is bird", "SomethingElse", "", entities.ModelTypeBird},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ResolveModelType(tt.model, tt.version))
		})
	}
}
