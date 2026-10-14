//go:build integration

package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// TestV2OnlyDatastore_GetSpeciesFirstAndLastDetectionTimeBefore_MySQL runs the
// first/last query against a real MySQL backend. The query selects two scalar
// subqueries with no FROM clause, so its syntax and NULL handling are checked
// on MySQL as well as on the SQLite test path.
func TestV2OnlyDatastore_GetSpeciesFirstAndLastDetectionTimeBefore_MySQL(t *testing.T) {
	ds := setupMySQLDatastore(t)
	ctx := t.Context()

	before := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return time.Date(2024, 6, d, 8, 30, 0, 0, time.UTC) }

	t.Run("each bound skips false positives but keeps reviewed-correct detections", func(t *testing.T) {
		review := func(ts time.Time, status entities.VerificationStatus) {
			t.Helper()
			det := seedDetection(t, ds, "Sylvia atricapilla", ts)
			require.NoError(t, ds.manager.DB().Create(&entities.DetectionReview{
				DetectionID: det.ID,
				Verified:    status,
			}).Error)
		}
		review(day(9), entities.VerificationFalsePositive)
		seedDetection(t, ds, "Sylvia atricapilla", day(10))
		seedDetection(t, ds, "Sylvia atricapilla", day(12))
		review(day(13), entities.VerificationCorrect)
		review(day(14), entities.VerificationFalsePositive)
		seedDetection(t, ds, "Sylvia atricapilla", day(20)) // after before: excluded

		first, last, err := ds.GetSpeciesFirstAndLastDetectionTimeBefore(ctx, "Sylvia atricapilla", before)
		require.NoError(t, err)
		require.NotNil(t, first)
		require.NotNil(t, last)
		assert.Equal(t, day(10), *first)
		assert.Equal(t, day(13), *last)
	})

	t.Run("returns nils for an unknown species", func(t *testing.T) {
		first, last, err := ds.GetSpeciesFirstAndLastDetectionTimeBefore(ctx, "Nonexistent species", before)
		require.NoError(t, err)
		assert.Nil(t, first)
		assert.Nil(t, last)
	})
}
