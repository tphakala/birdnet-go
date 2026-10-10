package apicore

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// Typical string lengths of one stored detection, in bytes. They are
// deliberately on the generous side of what the datastore returns so the
// estimate is a worst case rather than an average.
const (
	sampleNameLen     = 24  // common and scientific names
	sampleClipPathLen = 60  // relative clip path
	sampleSourceLen   = 48  // source id, safe string and display name
	sampleCommentLen  = 160 // one user comment
	sampleCopies      = 4   // pages built per measurement, to average out noise
)

// sampleStr returns a distinct heap string of n bytes, so no two samples share
// backing memory the way compile-time constants would.
func sampleStr(prefix string, i, n int) string {
	s := fmt.Sprintf("%s-%d-", prefix, i)
	for len(s) < n {
		s += "x"
	}
	return s[:n]
}

// sampleNote builds a fully populated note (review, lock, one comment, source
// and model metadata), the shape the datastore preloads for list pages.
func sampleNote(i int) datastore.Note {
	classifier := sampleStr("classifier", i, sampleClipPathLen)
	return datastore.Note{
		ID:                  uint(i + 1),
		SourceNode:          sampleStr("node", i, sampleNameLen),
		Date:                "2026-01-02",
		Time:                "03:04:05",
		Source:              datastore.AudioSource{ID: sampleStr("id", i, sampleSourceLen), SafeString: sampleStr("safe", i, sampleSourceLen), DisplayName: sampleStr("name", i, sampleSourceLen)},
		Model:               detection.ModelInfo{Name: sampleStr("m", i, sampleNameLen), Version: "2.4", Variant: sampleStr("v", i, sampleNameLen), ClassifierPath: &classifier, ModelType: "bird"},
		BeginTime:           time.Now(),
		EndTime:             time.Now(),
		SpeciesCode:         sampleStr("sp", i, 6),
		ScientificName:      sampleStr("sci", i, sampleNameLen),
		CommonName:          sampleStr("com", i, sampleNameLen),
		ClipName:            sampleStr("clip", i, sampleClipPathLen),
		SpectrogramClipName: sampleStr("spec", i, sampleClipPathLen),
		Confidence:          0.9,
		Review:              &datastore.NoteReview{ID: uint(i), NoteID: uint(i), Verified: "correct", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Lock:                &datastore.NoteLock{ID: uint(i), NoteID: uint(i), LockedAt: time.Now()},
		Comments:            []datastore.NoteComment{{ID: uint(i), NoteID: uint(i), Entry: sampleStr("comment", i, sampleCommentLen), CreatedAt: time.Now(), UpdatedAt: time.Now()}},
		Verified:            "correct",
		Locked:              true,
	}
}

func samplePage(size int) DetectionPage {
	notes := make([]datastore.Note, size)
	for i := range notes {
		notes[i] = sampleNote(i)
	}
	return DetectionPage{Notes: notes, Total: int64(size)}
}

// retainedHeapBytes returns the average live heap bytes retained by one page of
// the given size, measured as the heap growth while sampleCopies pages are held.
func retainedHeapBytes(size int) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	pages := make([]DetectionPage, sampleCopies)
	for i := range pages {
		pages[i] = samplePage(size)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(pages)
	return (after.HeapAlloc - before.HeapAlloc) / sampleCopies
}

// TestDetectionCacheMaxEntries_WorstCaseStaysWithinBudget measures the heap
// retained by one cached detection page of 25, 100 and 1000 fully populated
// notes and checks that detectionCacheMaxEntries pages of the largest
// cacheable size (detectionCacheMaxPageNotes) stay within
// detectionCacheMaxBytes.
func TestDetectionCacheMaxEntries_WorstCaseStaysWithinBudget(t *testing.T) {
	// Not parallel: heap measurements are skewed by concurrently running tests.
	for _, size := range []int{25, 100, 1000} {
		perPage := retainedHeapBytes(size)
		t.Logf("%4d notes per page: %9d bytes per page, %5d bytes per note", size, perPage, perPage/uint64(size))
	}

	worstPage := retainedHeapBytes(detectionCacheMaxPageNotes)
	worstCase := worstPage * detectionCacheMaxEntries
	t.Logf("worst case: %d entries x %d notes = %d bytes (budget %d)", detectionCacheMaxEntries, detectionCacheMaxPageNotes, worstCase, detectionCacheMaxBytes)

	require.Positive(t, worstPage)
	assert.LessOrEqual(t, worstCase, uint64(detectionCacheMaxBytes),
		"lower detectionCacheMaxEntries or revisit its derivation")
}
