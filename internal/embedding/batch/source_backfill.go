package batch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// backfillPageSize bounds each datastore query. Pages keep memory flat on
// installs with very large detection histories.
const backfillPageSize = 500

// farFuture is a sentinel end date used when a Since-only date range is
// requested. applyDateRangeFilter in the datastore applies both Start and End
// unconditionally (date >= start AND date <= end), so an open upper bound
// must be represented with a date far enough in the future to include all
// realistic detections. Treated as a constant; never reassign.
var farFuture = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

// NoteSearcher is the slice of datastore.Interface backfill needs.
type NoteSearcher interface {
	SearchNotesAdvanced(filters *datastore.AdvancedSearchFilters) ([]datastore.Note, int64, error)
}

// BackfillFilter scopes a backfill run.
type BackfillFilter struct {
	// Species is the scientific name to restrict to; empty means all species.
	// SearchNotesAdvanced matches this against both species_code and
	// scientific_name columns, so it may also match legacy species-code values
	// that happen to equal the scientific name string.
	Species string
	// Since is the lower bound on detection date (inclusive); zero means no bound.
	Since time.Time
	// Limit is the max items returned; 0 means unbounded.
	Limit int
}

// BackfillItems enumerates detections that still have a clip on disk,
// newest first, as batch Items keyed by note id. Notes without a clip
// record or whose clip file was purged are skipped silently: they are
// simply not embeddable. ctx cancellation is checked at the start of each
// page iteration and returns promptly with the context error.
func BackfillItems(ctx context.Context, ds NoteSearcher, clipDir string, filter BackfillFilter) ([]Item, error) {
	var items []Item
	offset := 0
	// Accepted tradeoff: offset pagination over date_desc can skip or duplicate
	// notes if detections are inserted mid-run; fine for backfill because the
	// next run catches skips and duplicates re-embed harmlessly (Put is an upsert).
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f := &datastore.AdvancedSearchFilters{
			SortBy: "date_desc",
			Limit:  backfillPageSize,
			Offset: offset,
		}
		if filter.Species != "" {
			f.Species = []string{filter.Species}
		}
		if !filter.Since.IsZero() {
			// applyDateRangeFilter always applies both Start and End bounds, so
			// an open-ended Since filter must carry a far-future End sentinel to
			// avoid excluding all real records.
			f.DateRange = &datastore.DateRange{
				Start: filter.Since,
				End:   farFuture,
			}
		}
		notes, _, err := ds.SearchNotesAdvanced(f)
		if err != nil {
			return nil, fmt.Errorf("backfill: search notes page offset %d: %w", offset, err)
		}
		for i := range notes {
			n := &notes[i]
			if n.ClipName == "" {
				continue
			}
			path := filepath.Join(clipDir, n.ClipName)
			if _, statErr := os.Stat(path); statErr != nil {
				if errors.Is(statErr, os.ErrNotExist) {
					continue
				}
				return nil, fmt.Errorf("backfill: stat %s: %w", path, statErr)
			}
			items = append(items, Item{
				Path:        path,
				Key:         n.ClipName,
				DetectionID: strconv.FormatUint(uint64(n.ID), 10),
				Species:     n.ScientificName,
				CapturedAt:  n.BeginTime,
			})
			if filter.Limit > 0 && len(items) >= filter.Limit {
				return items, nil
			}
		}
		// A short page means the result set is exhausted; returning here skips
		// the terminal zero-row query and its count/preload cost.
		if len(notes) < backfillPageSize {
			return items, nil
		}
		offset += backfillPageSize
	}
}
