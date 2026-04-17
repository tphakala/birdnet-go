package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// TestDetectionToRecord_Verified is a regression test for GitHub issue #2769:
// /api/v2/search must emit the same verification vocabulary that /api/v2/detections
// and the frontend speak, namely "correct" / "false_positive" / "unverified".
// Previously the search path returned the literal "verified", causing the Search
// view to render every detection as "Unverified" regardless of its real status.
func TestDetectionToRecord_Verified(t *testing.T) {
	t.Parallel()

	ds := &Datastore{timezone: time.UTC}

	tests := []struct {
		name   string
		review *entities.DetectionReview
		want   string
	}{
		{
			name:   "unreviewed detection reports unverified",
			review: nil,
			want:   "unverified",
		},
		{
			name:   "correctly reviewed detection reports correct",
			review: &entities.DetectionReview{Verified: entities.VerificationCorrect},
			want:   string(entities.VerificationCorrect),
		},
		{
			name:   "false-positive review reports false_positive",
			review: &entities.DetectionReview{Verified: entities.VerificationFalsePositive},
			want:   string(entities.VerificationFalsePositive),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			det := &entities.Detection{
				ID:         42,
				Confidence: 0.9,
				DetectedAt: time.Date(2026, 4, 14, 20, 11, 29, 0, time.UTC).Unix(),
				Review:     tc.review,
			}

			got := ds.detectionToRecord(det)

			assert.Equal(t, tc.want, got.Verified,
				"DetectionRecord.Verified must use the shared 'correct/false_positive/unverified' vocabulary")
		})
	}
}
