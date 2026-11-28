package v2only

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clipNameSetter mirrors the narrow capability the imports engine type-asserts
// on its datastore.DetectionRepository to re-attach clips to detections a
// db-only import already stored. The interface is declared here so this test
// builds against the pin too, where the adapter has no such method yet and the
// assertion below fails.
type clipNameSetter interface {
	SetClipName(ctx context.Context, id uint, clipName string) (bool, error)
}

// TestDetectionRepository_ExposesSetClipName verifies that the production
// v2-only detection adapter — the repository a v2 install passes to
// imports.NewEngine — forwards the narrow SetClipName capability to the v2
// detection repository, so a db-audio re-import can attach clips to existing
// clip-less detections. At the pin the adapter has no such method, so the type
// assertion fails there.
func TestDetectionRepository_ExposesSetClipName(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	repo := NewDetectionRepository(ds)
	setter, ok := repo.(clipNameSetter)
	require.True(t, ok, "v2only.NewDetectionRepository must expose SetClipName so the imports engine can re-attach clips")

	det := seedDetection(t, ds, "Parus major", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	const clipName = "2026/01/parus_major_90p_20260102T030405Z.mp3"
	changed, err := setter.SetClipName(t.Context(), det.ID, clipName)
	require.NoError(t, err)
	assert.True(t, changed)

	stored, err := ds.detection.Get(t.Context(), det.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.ClipName)
	assert.Equal(t, clipName, *stored.ClipName)
}
