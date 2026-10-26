package privacy

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/api/v2/dto"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

const (
	// defaultPageSize, maxPageSize and maxOffset match the detections list.
	defaultPageSize = 100
	maxPageSize     = 1000
	maxOffset       = 1_000_000

	paramLimit  = "limit"
	paramOffset = "offset"

	msgInvalidLimit  = "Invalid limit parameter. Must be between 1 and 1000"
	msgInvalidOffset = "Invalid offset parameter. Must be between 0 and 1000000"
)

// DiscardItem is one detection the privacy filter discarded.
type DiscardItem struct {
	// ID is the discard row id.
	ID uint `json:"id"`
	// DiscardedAt is the UTC wall-clock time of the flush that dropped the
	// detection.
	DiscardedAt time.Time `json:"discarded_at"`
	// Source is the audio source; null when it could not be resolved.
	Source *SourceRef `json:"source"`
	// ScientificName and CommonName name the discarded species.
	ScientificName string `json:"scientific_name"`
	CommonName     string `json:"common_name"`
	// Confidence is the confidence of the discarded detection.
	Confidence float64 `json:"confidence"`
	// ModelID is the model that made the discarded detection.
	ModelID string `json:"model_id"`
	// ClipBegin and ClipEnd are the span that would have been saved, UTC.
	ClipBegin time.Time `json:"clip_begin"`
	ClipEnd   time.Time `json:"clip_end"`
	// Settings are the filter settings in effect at the discard.
	Settings DiscardSettings `json:"settings"`
	// HitCountsLowerBound is true when the hit counts are lower bounds.
	HitCountsLowerBound bool `json:"hit_counts_lower_bound"`
	// Hits are the per-detector totals of the hits behind the discard, never null.
	Hits []DiscardHitItem `json:"hits"`
}

// DiscardSettings are the privacy filter settings in effect at a discard.
type DiscardSettings struct {
	LabelThreshold float64 `json:"label_threshold"`
	VADEnabled     bool    `json:"vad_enabled"`
	VADThreshold   float64 `json:"vad_threshold"`
}

// DiscardHitItem totals the hits one detector contributed to a discard.
type DiscardHitItem struct {
	// Detector is a model id, silero_vad, or empty when a hit was recorded without
	// a model.
	Detector    string  `json:"detector"`
	TriggerType string  `json:"trigger_type"`
	HitCount    int     `json:"hit_count"`
	MinScore    float64 `json:"min_score"`
	MaxScore    float64 `json:"max_score"`
}

// ListDiscards returns the discarded detections in the requested station-local
// date range, newest first, for GET /api/v2/privacy-filter/discards
// ?start_date=&end_date=&limit=&offset=. It answers 409 unless the enhanced
// database is active.
func (c *Handler) ListDiscards(ctx echo.Context) error {
	if !c.available() {
		return c.requireV2(ctx)
	}
	start, end, _, ok, err := c.parseRange(ctx)
	if !ok {
		return err
	}
	limit, ok := parseBounded(ctx.QueryParam(paramLimit), defaultPageSize, 1, maxPageSize)
	if !ok {
		return c.HandleError(ctx, nil, msgInvalidLimit, http.StatusBadRequest)
	}
	offset, ok := parseBounded(ctx.QueryParam(paramOffset), 0, 0, maxOffset)
	if !ok {
		return c.HandleError(ctx, nil, msgInvalidOffset, http.StatusBadRequest)
	}

	reqCtx, cancel := context.WithTimeout(ctx.Request().Context(), queryTimeout)
	defer cancel()

	rows, total, err := c.discards.ListPage(reqCtx, start.UnixMilli(), end.UnixMilli(), limit, offset)
	if err != nil {
		return c.queryError(ctx, err)
	}

	seen := make(map[uint]struct{}, len(rows))
	for i := range rows {
		if rows[i].SourceID != nil {
			seen[*rows[i].SourceID] = struct{}{}
		}
	}
	loaded, err := c.loadSources(reqCtx, slices.Collect(maps.Keys(seen)))
	if err != nil {
		return c.queryError(ctx, err)
	}

	items := make([]DiscardItem, 0, len(rows))
	for i := range rows {
		items = append(items, discardItem(&rows[i], loaded))
	}
	return ctx.JSON(http.StatusOK, dto.PaginatedResponse{
		Data:        items,
		Total:       total,
		Limit:       limit,
		Offset:      offset,
		CurrentPage: offset/limit + 1,
		TotalPages:  int((total + int64(limit) - 1) / int64(limit)),
	})
}

// parseBounded parses an integer query value in [minValue, maxValue]. An empty
// value yields def. ok is false for a non-numeric or out-of-range value.
func parseBounded(raw string, def, minValue, maxValue int) (value int, ok bool) {
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < minValue || n > maxValue {
		return 0, false
	}
	return n, true
}

// discardItem maps a stored discard to its response form.
func discardItem(row *entities.PrivacyFilterDiscard, loaded map[uint]*entities.AudioSource) DiscardItem {
	var source *SourceRef
	if row.SourceID != nil {
		source = sourceRef(*row.SourceID, loaded)
	}
	hits := make([]DiscardHitItem, 0, len(row.Hits))
	for i := range row.Hits {
		hits = append(hits, DiscardHitItem{
			Detector:    row.Hits[i].Detector,
			TriggerType: row.Hits[i].TriggerType,
			HitCount:    row.Hits[i].HitCount,
			MinScore:    row.Hits[i].MinScore,
			MaxScore:    row.Hits[i].MaxScore,
		})
	}
	return DiscardItem{
		ID:             row.ID,
		DiscardedAt:    time.UnixMilli(row.DiscardedAt).UTC(),
		Source:         source,
		ScientificName: row.ScientificName,
		CommonName:     row.CommonName,
		Confidence:     row.Confidence,
		ModelID:        row.ModelID,
		ClipBegin:      time.UnixMilli(row.ClipBegin).UTC(),
		ClipEnd:        time.UnixMilli(row.ClipEnd).UTC(),
		Settings: DiscardSettings{
			LabelThreshold: float64(row.LabelThresholdMilli) / milliPerUnit,
			VADEnabled:     row.VADEnabled,
			VADThreshold:   float64(row.VADThresholdMilli) / milliPerUnit,
		},
		HitCountsLowerBound: row.HitCountsLowerBound,
		Hits:                hits,
	}
}
