package detections

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// readBudgetTolerance is how far the deadline the reader sees may be from now plus
// modelContributionsReadBudget; it is far below the budget so a changed budget fails.
const readBudgetTolerance = 250 * time.Millisecond

// fakeContributionReader is a modelContributionReader with a canned result.
type fakeContributionReader struct {
	rows     map[uint][]*entities.DetectionModelContribution
	err      error
	calls    int
	gotIDs   []uint
	deadline time.Time
	hasDL    bool
}

func (f *fakeContributionReader) GetModelContributionsByDetectionIDs(ctx context.Context, ids []uint) (map[uint][]*entities.DetectionModelContribution, error) {
	f.calls++
	f.gotIDs = ids
	f.deadline, f.hasDL = ctx.Deadline()
	return f.rows, f.err
}

func TestNewModelContributionResponse_Statistics(t *testing.T) {
	const (
		first = int64(1_000)
		last  = int64(2_000)
	)
	stats := func(hits int, maxC, minC, sum float64, firstAt, lastAt int64) *entities.DetectionModelContribution {
		return &entities.DetectionModelContribution{
			HitCount: hits, MaxConfidence: maxC,
			FirstHitAt: &firstAt, LastHitAt: &lastAt, MinConfidence: &minC, ConfidenceSum: &sum,
		}
	}

	// without clears one column of an otherwise complete row.
	without := func(r *entities.DetectionModelContribution, drop func(*entities.DetectionModelContribution)) *entities.DetectionModelContribution {
		drop(r)
		return r
	}

	tests := []struct {
		name     string
		row      *entities.DetectionModelContribution
		wantMean *float64
	}{
		{"mean inside range is kept", stats(2, 0.9, 0.5, 1.4, first, last), new(0.7)},
		{"mean above max is clamped to max", stats(2, 0.8, 0.6, 1.7, first, last), new(0.8)},
		{"mean below min is clamped to min", stats(2, 0.9, 0.6, 0.2, first, last), new(0.6)},
		{"min above max shows no statistics", stats(2, 0.5, 0.6, 1.1, first, last), nil},
		{"first after last shows no statistics", stats(2, 0.9, 0.5, 1.4, last, first), nil},
		{"zero hit count shows no statistics", stats(0, 0.9, 0.5, 0, first, last), nil},
		{"negative hit count shows no statistics", stats(-1, 0.9, 0.5, 1.4, first, last), nil},
		{"single hit with equal bounds keeps its statistics", stats(1, 0.9, 0.9, 0.9, first, first), new(0.9)},
		{"missing first hit time shows no statistics", without(stats(2, 0.9, 0.5, 1.4, first, last), func(r *entities.DetectionModelContribution) { r.FirstHitAt = nil }), nil},
		{"missing last hit time shows no statistics", without(stats(2, 0.9, 0.5, 1.4, first, last), func(r *entities.DetectionModelContribution) { r.LastHitAt = nil }), nil},
		{"missing minimum shows no statistics", without(stats(2, 0.9, 0.5, 1.4, first, last), func(r *entities.DetectionModelContribution) { r.MinConfidence = nil }), nil},
		{"missing sum shows no statistics", without(stats(2, 0.9, 0.5, 1.4, first, last), func(r *entities.DetectionModelContribution) { r.ConfidenceSum = nil }), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := newModelContributionResponse(tc.row)
			assert.Equal(t, tc.row.HitCount, got.HitCount)
			assert.InDelta(t, tc.row.MaxConfidence, got.MaxConfidence, 1e-9)
			if tc.wantMean == nil {
				assert.Nil(t, got.MinConfidence)
				assert.Nil(t, got.MeanConfidence)
				assert.Nil(t, got.FirstHitAt)
				assert.Nil(t, got.LastHitAt)
				return
			}
			require.NotNil(t, got.MeanConfidence)
			require.NotNil(t, got.MinConfidence)
			require.NotNil(t, got.FirstHitAt)
			require.NotNil(t, got.LastHitAt)
			assert.InDelta(t, *tc.wantMean, *got.MeanConfidence, 1e-9)
			assert.LessOrEqual(t, *got.MinConfidence, *got.MeanConfidence)
			assert.LessOrEqual(t, *got.MeanConfidence, got.MaxConfidence)
		})
	}
}

func TestFormatHitTime_UsesServerLocalZone(t *testing.T) {
	// time.Local is process-global: no t.Parallel, and the original is restored.
	orig := time.Local
	t.Cleanup(func() { time.Local = orig })
	time.Local = time.FixedZone("test", 3*60*60)

	instant := time.Date(2026, 10, 24, 3, 30, 15, 999_000_000, time.UTC)
	assert.Equal(t, "2026-10-24T06:30:15+03:00", formatHitTime(instant.UnixMilli()),
		"rendered in the server zone, with seconds precision")
}

func TestAttachModelContributions_ReadFailureLeavesFieldOut(t *testing.T) {
	_, _, h := setupTestEnvironment(t)

	row := &entities.DetectionModelContribution{DetectionID: 5, HitCount: 1, MaxConfidence: 0.5, Model: &entities.AIModel{Name: "BirdNET"}}
	newDetections := func() []DetectionResponse { return []DetectionResponse{{ID: 5}, {ID: 0}, {ID: 6}} }

	t.Run("success fills by id under a bounded context", func(t *testing.T) {
		f := &fakeContributionReader{rows: map[uint][]*entities.DetectionModelContribution{5: {row}}}
		h.contributions = f
		dets := newDetections()
		h.attachModelContributions(t.Context(), dets)

		assert.Equal(t, 1, f.calls)
		assert.Equal(t, []uint{5, 6}, f.gotIDs, "zero ids are not queried")
		require.True(t, f.hasDL, "the read must run under its own budget")
		assert.WithinDuration(t, time.Now().Add(modelContributionsReadBudget), f.deadline, readBudgetTolerance)
		require.Len(t, dets[0].ModelContributions, 1)
		assert.Equal(t, "BirdNET", dets[0].ModelContributions[0].ModelName)
		assert.Nil(t, dets[1].ModelContributions)
		assert.Nil(t, dets[2].ModelContributions)
	})

	for name, err := range map[string]error{
		"database error":    errors.NewStd("boom"),
		"deadline exceeded": context.DeadlineExceeded,
		"canceled":          context.Canceled,
	} {
		t.Run(name+" leaves the field out", func(t *testing.T) {
			h.contributions = &fakeContributionReader{rows: map[uint][]*entities.DetectionModelContribution{5: {row}}, err: err}
			dets := newDetections()
			h.attachModelContributions(t.Context(), dets)
			for i := range dets {
				assert.Nil(t, dets[i].ModelContributions)
			}
		})
	}

	t.Run("no usable ids skips the read", func(t *testing.T) {
		f := &fakeContributionReader{}
		h.contributions = f
		h.attachModelContributions(t.Context(), []DetectionResponse{{ID: 0}})
		assert.Zero(t, f.calls)
	})
}
