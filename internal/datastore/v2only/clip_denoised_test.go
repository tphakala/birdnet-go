package v2only

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectionRepository_GetReadsClipDenoised(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	repo := NewDetectionRepository(ds)

	tests := []struct {
		name   string
		mark   func(ctx context.Context, id uint) error
		wanted bool
	}{
		{"stored true", func(ctx context.Context, id uint) error {
			return ds.detection.RecordClipDenoised(ctx, id, testClipName, true)
		}, true},
		{"stored false", func(ctx context.Context, id uint) error {
			return ds.detection.RecordClipDenoised(ctx, id, testClipName, false)
		}, false},
		{"NULL", func(context.Context, uint) error { return nil }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := saveRateNote(t, ds, testClipName, testBatClipRate)
			require.NoError(t, tt.mark(t.Context(), id))

			got, err := repo.Get(t.Context(), strconv.FormatUint(uint64(id), 10))
			require.NoError(t, err)
			assert.Equal(t, tt.wanted, got.ClipDenoised)
		})
	}
}
