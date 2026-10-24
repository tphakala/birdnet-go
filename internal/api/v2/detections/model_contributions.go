package detections

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// modelContributionsReadBudget is the longest a detection response waits for its
// model contributions. The field is auxiliary and the v2 SQLite pool has a single
// connection, so a long write must not hold a list response. On a timeout the
// field is left out of that response.
const modelContributionsReadBudget = time.Second

// modelContributionReader loads stored model contributions. The v2 detection
// repository implements it.
type modelContributionReader interface {
	GetModelContributionsByDetectionIDs(ctx context.Context, detectionIDs []uint) (map[uint][]*entities.DetectionModelContribution, error)
}

// ModelContributionResponse describes one model's contribution to a detection.
// MinConfidence, MeanConfidence, FirstHitAt and LastHitAt are present together
// or not at all: they are absent when the row has no recorded statistics (rows
// recorded before they existed) or its stored values are unusable (non-positive
// hit count, minimum above maximum, first hit after last hit), never reported as
// zero.
type ModelContributionResponse struct {
	ModelName      string   `json:"modelName"`
	ModelVersion   string   `json:"modelVersion"`
	ModelVariant   string   `json:"modelVariant"`
	ModelType      string   `json:"modelType"`
	HitCount       int      `json:"hitCount"`
	MaxConfidence  float64  `json:"maxConfidence"`
	MinConfidence  *float64 `json:"minConfidence,omitempty"`
	MeanConfidence *float64 `json:"meanConfidence,omitempty"`
	FirstHitAt     *string  `json:"firstHitAt,omitempty"` // RFC3339 in the server's time zone
	LastHitAt      *string  `json:"lastHitAt,omitempty"`  // RFC3339 in the server's time zone
}

// formatHitTime renders a Unix millisecond hit time like the detection's beginTime.
func formatHitTime(ms int64) string {
	return time.UnixMilli(ms).In(time.Local).Format(time.RFC3339)
}

// newModelContributionResponse converts a stored row. The statistics are shown
// only when all four columns are set, the hit count is positive and the range is
// consistent (min <= max, first <= last); the mean is clamped to [min, max].
func newModelContributionResponse(row *entities.DetectionModelContribution) ModelContributionResponse {
	resp := ModelContributionResponse{
		HitCount:      row.HitCount,
		MaxConfidence: row.MaxConfidence,
	}
	if row.Model != nil {
		resp.ModelName = row.Model.Name
		resp.ModelVersion = row.Model.Version
		resp.ModelVariant = row.Model.Variant
		resp.ModelType = string(row.Model.ModelType)
	}

	if row.HitCount > 0 && row.FirstHitAt != nil && row.LastHitAt != nil &&
		row.MinConfidence != nil && row.ConfidenceSum != nil &&
		*row.MinConfidence <= row.MaxConfidence && *row.FirstHitAt <= *row.LastHitAt {
		mean := min(max(*row.ConfidenceSum/float64(row.HitCount), *row.MinConfidence), row.MaxConfidence)
		first := formatHitTime(*row.FirstHitAt)
		last := formatHitTime(*row.LastHitAt)
		resp.MinConfidence = new(*row.MinConfidence)
		resp.MeanConfidence = &mean
		resp.FirstHitAt = &first
		resp.LastHitAt = &last
	}
	return resp
}

// attachModelContributions fills ModelContributions on the given detections with
// one repository query. It does nothing when the reader is not set (legacy and
// migration mode). A failed read is logged and leaves the field out; it never
// fails the response.
func (c *Handler) attachModelContributions(ctx context.Context, detections []DetectionResponse) {
	if c.contributions == nil || len(detections) == 0 {
		return
	}

	ids := make([]uint, 0, len(detections))
	for i := range detections {
		if detections[i].ID != 0 {
			ids = append(ids, detections[i].ID)
		}
	}
	if len(ids) == 0 {
		return
	}

	readCtx, cancel := context.WithTimeout(ctx, modelContributionsReadBudget)
	defer cancel()

	byDetection, err := c.contributions.GetModelContributionsByDetectionIDs(readCtx, ids)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			c.LogDebugIfEnabled("Model contributions not loaded", logger.Error(err))
		} else {
			c.LogWarnIfEnabled("Failed to load model contributions", logger.Error(err), logger.Int("detections", len(ids)))
		}
		return
	}

	for i := range detections {
		rows := byDetection[detections[i].ID]
		if len(rows) == 0 {
			continue
		}
		out := make([]ModelContributionResponse, 0, len(rows))
		for _, row := range rows {
			out = append(out, newModelContributionResponse(row))
		}
		detections[i].ModelContributions = out
	}
}
