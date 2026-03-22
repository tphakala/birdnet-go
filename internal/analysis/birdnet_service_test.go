package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Compile-time interface compliance check.
var _ app.Analyzer = (*BirdNETAnalyzer)(nil)

func TestBirdNETAnalyzer_Name(t *testing.T) {
	t.Parallel()

	a := NewBirdNETAnalyzer(&conf.Settings{})
	assert.Equal(t, "birdnet-analyzer", a.Name())
}

func TestBirdNETAnalyzer_Compatible(t *testing.T) {
	t.Parallel()

	a := NewBirdNETAnalyzer(&conf.Settings{})

	tests := []struct {
		name       string
		sourceType app.SourceType
		want       bool
	}{
		{"audio card is compatible", app.SourceTypeAudioCard, true},
		{"RTSP is compatible", app.SourceTypeRTSP, true},
		{"ultrasonic is not compatible", app.SourceTypeUltrasonic, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := app.AudioSource{Type: tt.sourceType}
			assert.Equal(t, tt.want, a.Compatible(src))
		})
	}
}

func TestBirdNETAnalyzer_BirdNET_NilBeforeStart(t *testing.T) {
	t.Parallel()

	a := NewBirdNETAnalyzer(&conf.Settings{})
	assert.Nil(t, a.BirdNET(), "BirdNET() should return nil before Start()")
}

func TestBirdNETAnalyzer_Stop_NilSafe(t *testing.T) {
	t.Parallel()

	a := NewBirdNETAnalyzer(&conf.Settings{})
	// Stop before Start should not panic and should return nil.
	assert.NotPanics(t, func() {
		err := a.Stop(t.Context())
		assert.NoError(t, err)
	})
}
