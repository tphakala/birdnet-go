package processor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/embedding"
)

// Detections must carry the window embedding and model id so they ride through
// the pending-detection merge to DatabaseAction.
func TestDetections_CarriesEmbeddingFields(t *testing.T) {
	t.Parallel()
	vec := []float32{1, 2, 3}
	d := Detections{
		CorrelationID: "abc",
		Embeddings:    vec,
		ModelID:       "birdnet",
	}
	assert.Equal(t, vec, d.Embeddings)
	assert.Equal(t, "birdnet", d.ModelID)
}

// The Processor owns a non-nil Capture, opens it lazily, and closing the
// Processor flushes + closes it (a fresh store on the same path reopens cleanly).
func TestProcessor_EmbeddingCaptureLifecycle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "embeddings.db")

	p := &Processor{}
	p.Settings = &conf.Settings{}
	p.Settings.Embeddings.Storage.Path = dbPath
	p.Settings.Embeddings.Storage.MaxRows = 50000
	p.embeddingCapture = embedding.NewCapture(p.resolveEmbeddingStore)

	// Lazily open by capturing one record.
	p.embeddingCapture.Capture(embedding.Record{
		DetectionID: "7", Model: "birdnet", Dim: 3, Format: embedding.FormatFP16,
		Vector: []float32{1, 2, 3},
	})
	require.NoError(t, p.embeddingCapture.Close(context.Background()))

	store, err := embedding.NewStore(dbPath)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	rec, err := store.Get(context.Background(), "7")
	require.NoError(t, err)
	assert.Equal(t, "7", rec.DetectionID)
}
