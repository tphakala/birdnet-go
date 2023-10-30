package output

import (
	"fmt"
	"sort"

	"github.com/tphakala/go-birdnet/pkg/observation"
)

// PrintNotesWithThreshold displays a list of detected species with their corresponding
// time intervals and confidence percentages. Only detections with confidence above the given
// threshold (e.g., 0.1 or 10%) are displayed.
func PrintNotesWithThreshold(notes []observation.Note, threshold float64) {
	// Sort notes based on time for display
	sort.Slice(notes, func(i, j int) bool {
		return notes[i].Time < notes[j].Time
	})

	for _, note := range notes {
		if note.Confidence >= threshold {
			fmt.Printf("Time Interval: %s\t%-30s %.1f%%\n", note.Time, note.CommonName, note.Confidence*100)
		}
	}
}
