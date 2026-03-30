package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

func TestCreateDetectionResult_UsesActualModelInfo(t *testing.T) {
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
		"Perch_V2",
	)

	assert.Equal(t, "Perch", result.Model.Name)
	assert.Equal(t, "V2", result.Model.Version)
	assert.Equal(t, "default", result.Model.Variant)
}

func TestCreateDetectionResult_DefaultModelForEmpty(t *testing.T) {
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
		"",
	)

	assert.Equal(t, "BirdNET", result.Model.Name, "empty modelID should default to BirdNET")
}

func TestCreateDetectionResult_DefaultModelForBirdNET(t *testing.T) {
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
		"BirdNET_V2.4",
	)

	assert.Equal(t, "BirdNET", result.Model.Name)
	assert.Equal(t, "2.4", result.Model.Version)
}
