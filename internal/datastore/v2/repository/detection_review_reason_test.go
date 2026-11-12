package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// markedReview stores a detection whose review is a false positive carrying the
// no animal reason and returns the detection ID.
func markedReview(t *testing.T, repo DetectionRepository, labelID uint) uint {
	t.Helper()
	id := seedClipDetection(t, repo, labelID, "2026/01/marked.flac")
	reason := entities.ReviewReasonNoAnimal
	require.NoError(t, repo.SaveReview(t.Context(), &entities.DetectionReview{
		DetectionID: id,
		Verified:    entities.VerificationFalsePositive,
		Reason:      &reason,
	}))
	return id
}

func storedReason(t *testing.T, repo DetectionRepository, id uint) *string {
	t.Helper()
	review, err := repo.GetReview(t.Context(), id)
	require.NoError(t, err)
	return review.Reason
}

// TestReviewReason covers the reason rule of both review writers: the reason
// survives a save that keeps false_positive and names none, and any other status
// clears it in the same statement.
func TestReviewReason(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	label := seedLabel(t, db, "Eptesicus nilssonii")

	t.Run("SaveReview to false_positive without a reason keeps it", func(t *testing.T) {
		id := markedReview(t, repo, label)
		require.NoError(t, repo.SaveReview(t.Context(), &entities.DetectionReview{
			DetectionID: id, Verified: entities.VerificationFalsePositive,
		}))
		got := storedReason(t, repo, id)
		require.NotNil(t, got)
		assert.Equal(t, entities.ReviewReasonNoAnimal, *got)
	})

	t.Run("SaveReview to correct clears it", func(t *testing.T) {
		id := markedReview(t, repo, label)
		require.NoError(t, repo.SaveReview(t.Context(), &entities.DetectionReview{
			DetectionID: id, Verified: entities.VerificationCorrect,
		}))
		assert.Nil(t, storedReason(t, repo, id))
		review, err := repo.GetReview(t.Context(), id)
		require.NoError(t, err)
		assert.Equal(t, entities.VerificationCorrect, review.Verified)
	})

	t.Run("SaveReview with a reason and status correct is refused", func(t *testing.T) {
		id := seedClipDetection(t, repo, label, "2026/01/refused.flac")
		reason := entities.ReviewReasonNoAnimal
		err := repo.SaveReview(t.Context(), &entities.DetectionReview{
			DetectionID: id, Verified: entities.VerificationCorrect, Reason: &reason,
		})
		require.ErrorIs(t, err, ErrReviewReasonNeedsFalsePositive)
		_, err = repo.GetReview(t.Context(), id)
		require.ErrorIs(t, err, ErrReviewNotFound, "a refused save writes nothing")
	})

	t.Run("a first save without a reason stores NULL", func(t *testing.T) {
		id := seedClipDetection(t, repo, label, "2026/01/first.flac")
		require.NoError(t, repo.SaveReview(t.Context(), &entities.DetectionReview{
			DetectionID: id, Verified: entities.VerificationFalsePositive,
		}))
		assert.Nil(t, storedReason(t, repo, id))
	})

	t.Run("UpdateReview to correct clears it and to false_positive keeps it", func(t *testing.T) {
		id := markedReview(t, repo, label)
		require.NoError(t, repo.UpdateReview(t.Context(), id, entities.VerificationFalsePositive))
		require.NotNil(t, storedReason(t, repo, id))
		require.NoError(t, repo.UpdateReview(t.Context(), id, entities.VerificationCorrect))
		assert.Nil(t, storedReason(t, repo, id))
	})
}
