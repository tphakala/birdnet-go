package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Compile-time interface compliance check.
var _ app.Service = (*AudioPipelineService)(nil)

func TestAudioPipelineService_Name(t *testing.T) {
	t.Parallel()

	svc := NewAudioPipelineService(&conf.Settings{}, nil, nil, nil, nil)
	assert.Equal(t, "audio-pipeline", svc.Name())
}

func TestAudioPipelineService_Stop_NilSafe(t *testing.T) {
	t.Parallel()

	svc := NewAudioPipelineService(&conf.Settings{}, nil, nil, nil, nil)
	// Stop before Start should not panic and should return nil.
	assert.NotPanics(t, func() {
		err := svc.Stop(t.Context())
		assert.NoError(t, err)
	})
}
