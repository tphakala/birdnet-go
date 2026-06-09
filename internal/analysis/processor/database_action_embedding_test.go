package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/embedding"
)

// spyCapturer records the Record handed to it.
type spyCapturer struct {
	calls []embedding.Record
}

func (s *spyCapturer) Capture(rec embedding.Record) { s.calls = append(s.calls, rec) }

func newEmbeddingTestAction(t *testing.T, capturer embeddingCapturer, embeddings []float32) (*DatabaseAction, *MockDetectionRepository) {
	t.Helper()
	settings := &conf.Settings{}
	settings.Embeddings.Storage.Format = "fp16"
	repo := NewMockDetectionRepository()
	action := &DatabaseAction{
		Settings:     settings,
		Repo:         repo,
		EventTracker: NewEventTracker(0), // 0 interval => never throttled
		Result: detection.Result{
			Species:     detection.Species{CommonName: "Test Bird", ScientificName: "Testus birdus"},
			Model:       detection.ModelInfo{Name: "BirdNET", Version: "2.4"},
			AudioSource: detection.AudioSource{ID: "test-source"},
		},
		Embeddings:       embeddings,
		ModelID:          "birdnet",
		EmbeddingCapture: capturer,
	}
	return action, repo
}

func TestDatabaseAction_CapturesEmbeddingAfterSave(t *testing.T) {
	t.Parallel()
	spy := &spyCapturer{}
	action, repo := newEmbeddingTestAction(t, spy, []float32{1, 2, 3, 4})

	require.NoError(t, action.ExecuteContext(t.Context(), nil))
	require.Equal(t, 1, repo.GetSavedCount())
	require.Len(t, spy.calls, 1)

	rec := spy.calls[0]
	assert.Equal(t, "1", rec.DetectionID) // mock assigns ID 1
	assert.Equal(t, "birdnet", rec.Model)
	assert.Equal(t, "test-source", rec.Source)
	assert.Equal(t, "2.4", rec.Version)
	assert.Equal(t, embedding.FormatFP16, rec.Format)
	assert.Equal(t, 4, rec.Dim)
	assert.Equal(t, []float32{1, 2, 3, 4}, rec.Vector)
}

func TestDatabaseAction_NoCaptureWhenEmbeddingEmpty(t *testing.T) {
	t.Parallel()
	spy := &spyCapturer{}
	action, _ := newEmbeddingTestAction(t, spy, nil) // no embedding

	require.NoError(t, action.ExecuteContext(t.Context(), nil))
	assert.Empty(t, spy.calls, "must not capture when no embedding present")
}

func TestDatabaseAction_NoCaptureWhenCapturerNil(t *testing.T) {
	t.Parallel()
	action, _ := newEmbeddingTestAction(t, nil, []float32{1, 2})
	action.EmbeddingCapture = nil

	require.NoError(t, action.ExecuteContext(t.Context(), nil)) // must not panic
}
