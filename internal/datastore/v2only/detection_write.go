package v2only

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// predictionWrite is one secondary prediction of a detection write.
type predictionWrite struct {
	// label is the species name as the caller holds it; it may carry the legacy
	// "ScientificName_CommonName" form, so the scientific name is extracted before storage.
	label string
	// rawLabel is the full un-truncated classifier label used to classify non-bird classes.
	rawLabel   string
	confidence float64
}

// detectionWrite is everything one detection save stores, independent of the
// caller's input model. saveDetection is the single implementation that persists it.
type detectionWrite struct {
	// detectedAt is the detection instant as Unix seconds.
	detectedAt int64

	model          detection.ModelInfo
	rawLabel       string
	scientificName string
	confidence     float64
	unlikely       bool

	latitude       float64
	longitude      float64
	clipName       string
	clipSampleRate int

	beginTime      time.Time
	endTime        time.Time
	processingTime time.Duration

	sourceSafeString  string
	sourceDisplayName string
	sourceNode        string

	modelContributions map[string]detection.ResultModelContrib
	predictions        []predictionWrite
}

// writeFromNote maps a legacy Note and its results to a detectionWrite. The
// detection instant is parsed from the Note's wall-clock Date and Time strings in tz,
// so it is ambiguous during a repeated DST hour.
func writeFromNote(note *datastore.Note, results []datastore.Results, tz *time.Location) *detectionWrite {
	w := &detectionWrite{
		detectedAt:         parseDetectionTimestamp(note.Date, note.Time, tz),
		model:              note.Model,
		rawLabel:           note.RawLabel,
		scientificName:     note.ScientificName,
		confidence:         note.Confidence,
		unlikely:           note.Unlikely,
		latitude:           note.Latitude,
		longitude:          note.Longitude,
		clipName:           note.ClipName,
		clipSampleRate:     note.ClipSampleRate,
		beginTime:          note.BeginTime,
		endTime:            note.EndTime,
		processingTime:     note.ProcessingTime,
		sourceSafeString:   note.Source.SafeString,
		sourceDisplayName:  note.Source.DisplayName,
		sourceNode:         note.SourceNode,
		modelContributions: note.ModelContributions,
	}
	if len(results) > 0 {
		w.predictions = make([]predictionWrite, len(results))
		for i, r := range results {
			w.predictions[i] = predictionWrite{
				label:      r.Species,
				rawLabel:   r.RawLabel,
				confidence: float64(r.Confidence),
			}
		}
	}
	return w
}
