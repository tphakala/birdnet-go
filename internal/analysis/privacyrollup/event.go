// Package privacyrollup aggregates privacy filter activity into hourly rows of
// the v2 database. The processor reports small events through a Recorder; a
// Writer folds them in memory and writes additive deltas once a minute.
package privacyrollup

import (
	"math"
	"time"
)

// Kind is what happened to the privacy filter, which decides the counters an
// Event adds to.
type Kind int

const (
	// KindCountedHit is a speech hit that counted at once: hits+1, bucketed.
	KindCountedHit Kind = iota + 1
	// KindHeldHit is a label hit held for confirmation: held+1, bucketed.
	KindHeldHit
	// KindConfirmedHeld is a held hit that a second hit confirmed. The event
	// carries the held hit's own stamp, model and threshold: hits+1, confirmed+1,
	// not bucketed (the hit was bucketed when it was held).
	KindConfirmedHeld
	// KindIgnoredHeld is a held hit dropped without counting, because no second hit
	// came inside the window or a hit arrived with label confirmation off, keyed on
	// the dropped hit: ignored+1.
	KindIgnoredHeld
	// KindDiscard is a pending detection the privacy filter dropped, keyed on the
	// hit that caused the drop: discards+1.
	KindDiscard
	// KindUnscannedSave is a detection approved after the coverage wait expired,
	// keyed on the detector the gate waited for: unscanned_saves+1.
	KindUnscannedSave
)

const (
	// NoModelID is the model of rows that have no hit (unscanned saves).
	NoModelID = ""
	// NoThresholdMilli is the threshold of rows that have no hit.
	NoThresholdMilli = 0

	// millisPerUnit converts a score or threshold in 0..1 to thousandths.
	millisPerUnit = 1000

	// Bucket edges, as margins of the score above its threshold in thousandths.
	// Their meaning is frozen once shipped: a change needs new columns, or old and
	// new rows would mix.
	scoreMarginNearMilli = 50
	scoreMarginMidMilli  = 100
	scoreMarginFarMilli  = 200
)

// Event is one privacy filter occurrence. The source identity is captured when
// the event happens; the writer resolves it to an audio_sources id at flush time.
type Event struct {
	Kind Kind
	// At is the stamp that decides the hour of the row: the hit's own audio-end
	// stamp (for a discard, the causing hit's; for an unscanned save, the clip's
	// start).
	At time.Time
	// SourceURI, NodeName and DisplayName identify the audio source as the
	// detection save path does. An empty SourceURI marks an unresolvable source.
	SourceURI, NodeName, DisplayName string
	// ModelID is the model that scored the hit, or NoModelID.
	ModelID string
	// Trigger is "label" or "vad".
	Trigger string
	// ThresholdMilli is the threshold in effect when the hit was scored, or
	// NoThresholdMilli.
	ThresholdMilli int
	// Score is the label confidence or VAD probability, used by bucketed kinds.
	Score float64
}

// Recorder receives events from the processor. Implementations must not block.
type Recorder interface {
	Record(Event)
}

// ThresholdMilli converts a threshold or score in 0..1 to thousandths, rounded
// so a float32 widened to float64 (0.69999999) maps to 700, not 699.
func ThresholdMilli(v float64) int {
	return int(math.Round(v * millisPerUnit))
}

// HourStart returns the Unix seconds of the start of the UTC hour containing t.
func HourStart(t time.Time) int64 {
	return t.Truncate(time.Hour).Unix()
}
