package privacy

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// ActivityResponse is the response for GET /api/v2/privacy-filter/activity.
// Counts are lower bounds: events dropped under load, events of unresolved
// sources and failed writes are not counted.
type ActivityResponse struct {
	// StartDate is the inclusive start date, station local, YYYY-MM-DD.
	StartDate string `json:"start_date"`
	// EndDate is the inclusive end date after the default is applied.
	EndDate string `json:"end_date"`
	// RangeStart is the UTC instant that starts the first local day of the range: its local midnight, or the transition instant when that midnight is skipped.
	RangeStart time.Time `json:"range_start"`
	// RangeEnd is the UTC instant that ends the range, exclusive.
	RangeEnd time.Time `json:"range_end"`
	// Rows are the hourly rollup rows, never null.
	Rows []ActivityRow `json:"rows"`
}

// ActivityRow is one hour of privacy filter activity for one source, detector
// model, trigger and threshold.
type ActivityRow struct {
	// HourStart is the start of the UTC hour.
	HourStart time.Time `json:"hour_start"`
	// Source is the audio source the activity belongs to.
	Source *SourceRef `json:"source"`
	// ModelID is the detector model; empty for unscanned saves.
	ModelID string `json:"model_id"`
	// TriggerType is "label" or "vad".
	TriggerType string `json:"trigger_type"`
	// Threshold is the label or VAD threshold in effect, by trigger type; 0 for
	// unscanned saves.
	Threshold float64 `json:"threshold"`

	// Hits counts speech hits that counted.
	Hits int64 `json:"hits"`
	// Held counts label hits held for confirmation.
	Held int64 `json:"held"`
	// Ignored counts held hits dropped without confirmation.
	Ignored int64 `json:"ignored"`
	// Confirmed counts held hits later confirmed.
	Confirmed int64 `json:"confirmed"`
	// Discards counts detections the filter dropped, attributed to the causing hit.
	Discards int64 `json:"discards"`
	// UnscannedSaves counts detections approved before a speech detector had
	// scanned the whole clip.
	UnscannedSaves int64 `json:"unscanned_saves"`

	// MaxScore is the highest score over counted and held hits.
	MaxScore float64 `json:"max_score"`
	// MarginLt50 to MarginGe200 bucket hits by how far the score is above the
	// threshold: below 0.05, 0.05 to 0.10, 0.10 to 0.20 and 0.20 or more.
	MarginLt50  int64 `json:"margin_lt_50"`
	MarginLt100 int64 `json:"margin_lt_100"`
	MarginLt200 int64 `json:"margin_lt_200"`
	MarginGe200 int64 `json:"margin_ge_200"`
}

// GetActivity returns the hourly rollup rows whose UTC hour overlaps the
// requested station-local date range for GET /api/v2/privacy-filter/activity
// ?start_date=&end_date=. It answers 409 unless the enhanced database is active.
func (c *Handler) GetActivity(ctx echo.Context) error {
	if !c.available() {
		return c.requireV2(ctx)
	}
	start, end, endDate, ok, err := c.parseRange(ctx)
	if !ok {
		return err
	}

	reqCtx, cancel := context.WithTimeout(ctx.Request().Context(), queryTimeout)
	defer cancel()

	rows, err := c.hourly.ListOverlapping(reqCtx, start.Unix(), end.Unix())
	if err != nil {
		return c.queryError(ctx, err)
	}

	seen := make(map[uint]struct{}, len(rows))
	for i := range rows {
		seen[rows[i].SourceID] = struct{}{}
	}
	loaded, err := c.loadSources(reqCtx, slices.Collect(maps.Keys(seen)))
	if err != nil {
		return c.queryError(ctx, err)
	}

	out := make([]ActivityRow, 0, len(rows))
	for i := range rows {
		out = append(out, activityRow(&rows[i], loaded))
	}
	return ctx.JSON(http.StatusOK, ActivityResponse{
		// The validated request value: for a date that does not exist in the zone the range
		// start is a later instant, whose formatted date would contradict EndDate.
		StartDate:  ctx.QueryParam(paramStartDate),
		EndDate:    endDate,
		RangeStart: start.UTC(),
		RangeEnd:   end.UTC(),
		Rows:       out,
	})
}

// activityRow maps a stored hourly row to its response form.
func activityRow(row *entities.PrivacyFilterHourly, loaded map[uint]*entities.AudioSource) ActivityRow {
	return ActivityRow{
		HourStart:      time.Unix(row.HourStart, 0).UTC(),
		Source:         sourceRef(row.SourceID, loaded),
		ModelID:        row.ModelID,
		TriggerType:    row.TriggerType,
		Threshold:      float64(row.ThresholdMilli) / milliPerUnit,
		Hits:           row.Hits,
		Held:           row.Held,
		Ignored:        row.Ignored,
		Confirmed:      row.Confirmed,
		Discards:       row.Discards,
		UnscannedSaves: row.UnscannedSaves,
		MaxScore:       row.MaxScore,
		MarginLt50:     row.MarginLt50,
		MarginLt100:    row.MarginLt100,
		MarginLt200:    row.MarginLt200,
		MarginGe200:    row.MarginGe200,
	}
}
