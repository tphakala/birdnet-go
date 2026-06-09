package batch

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// backfillPageSize bounds each datastore query. Pages keep memory flat on
// installs with very large detection histories.
const backfillPageSize = 500

// farFuture is a sentinel end date used when a Since-only date range is
// requested. applyDateRangeFilter in the datastore applies both Start and End
// unconditionally (date >= start AND date <= end), so an open upper bound
// must be represented with a date far enough in the future to include all
// realistic detections.
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
// simply not embeddable.
func BackfillItems(ds NoteSearcher, clipDir string, filter BackfillFilter) ([]Item, error) {
	var items []Item
	offset := 0
	for {
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
			return nil, err
		}
		if len(notes) == 0 {
			return items, nil
		}
		for i := range notes {
			n := &notes[i]
			if n.ClipName == "" {
				continue
			}
			path := filepath.Join(clipDir, n.ClipName)
			if _, statErr := os.Stat(path); statErr != nil {
				continue
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
		offset += backfillPageSize
	}
}
