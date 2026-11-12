// Package detection provides the core domain model for bird detection events.
// This package defines Result as the single source of truth for detection data
// used throughout the application. External serialization (API, MQTT, database)
// is handled by boundary-specific DTOs and entities.
package detection

import (
	"strings"
	"time"
)

// UnlikelySource is a bit set naming the validators that tagged a detection
// unlikely. On one audio window a bit means that validator judged the window. On a
// detection built from several hit windows a bit means that validator judged at
// least one of them, never that it judged every one: the detection stays unlikely
// only while every hit window was judged by some validator.
type UnlikelySource uint8

const (
	// UnlikelySourceUltrasonicCV is the ultrasonic validation filter, which judges a
	// window by the variation of its energy above the split frequency.
	UnlikelySourceUltrasonicCV UnlikelySource = 1 << iota
	// UnlikelySourceNoiseExamples is the comparison with the noise examples the user
	// marked, which judges a window by the similarity of its embedding to them.
	UnlikelySourceNoiseExamples
)

// Has reports whether every bit of source is set.
func (s UnlikelySource) Has(source UnlikelySource) bool {
	return s&source == source
}

// unlikelySourceNames lists the log name of every source bit, in bit order.
var unlikelySourceNames = []struct {
	source UnlikelySource
	name   string
}{
	{UnlikelySourceUltrasonicCV, "ultrasonic_cv"},
	{UnlikelySourceNoiseExamples, "noise_examples"},
}

// String names the set bits for logs, joined by "+"; the empty set is "none".
func (s UnlikelySource) String() string {
	var names []string
	for _, n := range unlikelySourceNames {
		if s.Has(n.source) {
			names = append(names, n.name)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, "+")
}

// Result represents a single bird detection event.
// This is the core domain model used throughout the application.
// External serialization is handled by boundary-specific DTOs.
type Result struct {
	// Identity (set after database save)
	ID uint

	// Timestamp with timezone - single source of truth for when detection occurred
	// Replaces separate Date/Time strings, removes ambiguity
	Timestamp time.Time

	// Source information
	SourceNode  string      // Node name (for multi-node setups)
	AudioSource AudioSource // Rich audio source metadata

	// Audio clip timing (within the source stream)
	BeginTime time.Time
	EndTime   time.Time

	// Species identification
	Species    Species
	Confidence float64

	// Location where detection occurred
	Latitude  float64
	Longitude float64

	// Analysis parameters used
	Threshold   float64
	Sensitivity float64

	// AI Model information
	Model ModelInfo

	// Output
	ClipName       string        // Saved audio clip filename
	ClipSampleRate int           // Sample rate in Hz of the saved clip file; 0 when unknown or without a clip
	ProcessingTime time.Duration // How long analysis took

	// SpectrogramClipName is the clip name a detection kept for its spectrogram image after
	// retention removed its audio. Repository reads set it; Save ignores it.
	SpectrogramClipName string

	// ClipDenoised is true only for a clip the export stage denoised. Repository reads
	// set it; Save ignores it. A clip saved before the marker existed, or written as
	// captured, reads false.
	ClipDenoised bool

	// Validation flags
	Unlikely bool // Tagged by a validator when every hit window was judged not to hold a call of the detected species; UnlikelySources says which validators judged

	// Runtime-only data (not persisted)
	Occurrence            float64                       // Probability 0-1 based on location/time/season
	ModelContributions    map[string]ResultModelContrib // Per-model data keyed by model ID: the models with hits (cross-model consensus) and the active models without one
	UltrasonicCV          float64                       // US frame CV from validation filter, highest among the hit windows (for comment generation)
	UltrasonicCVThreshold float64                       // CV threshold that judged the window behind UltrasonicCV (for comment generation)
	UnlikelySources       UnlikelySource                // Validators that tagged a hit window unlikely; zero unless Unlikely is set
	NoiseSimilarity       float64                       // Cosine similarity to the closest noise example, lowest among the windows the noise examples judged (for comment generation)
	// RawLabel is the full un-truncated classifier label (e.g. "power_tool"); runtime-only,
	// used by the datastore to classify sound-event classes correctly.
	RawLabel string

	// Review status (populated from DB relations when loaded)
	Verified string
	Locked   bool
	Comments []Comment
}

// IsSpectrogramOnly reports whether retention removed the detection's audio but a
// spectrogram image was kept.
func (r *Result) IsSpectrogramOnly() bool {
	return r.ClipName == "" && r.SpectrogramClipName != ""
}

// ResultModelContrib records a single AI model's contribution to a detection.
//
// FirstHitAt, LastHitAt, MinConfidence and ConfidenceSum form one unit of extended
// statistics, present only when HasHitStats reports true; otherwise the datastore
// stores NULL for all four. A HitCount of 0 marks a model that was active on the
// source when the detection was approved but recorded no hit.
type ResultModelContrib struct {
	Model         ModelInfo
	HitCount      int
	MaxConfidence float64
	FirstHitAt    time.Time // Capture time of the end of the model's first hit window (processing time when the source gave none)
	LastHitAt     time.Time // Capture time of the end of the model's last hit window (processing time when the source gave none)
	MinConfidence float64   // Lowest confidence among the hits
	ConfidenceSum float64   // Sum of the hit confidences; the mean is ConfidenceSum / HitCount
}

// HasHitStats reports whether the extended statistics are present: at least one hit
// and both hit times set. Both times are checked because the zero time.Time has a
// year-0001 Unix millisecond value that would break FirstHitAt <= LastHitAt.
func (c *ResultModelContrib) HasHitStats() bool {
	return c.HitCount > 0 && !c.FirstHitAt.IsZero() && !c.LastHitAt.IsZero()
}

// Comment represents a user comment on a detection.
type Comment struct {
	ID        uint
	Entry     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AdditionalResult represents a secondary species prediction from the same audio chunk.
// BirdNET may return multiple species predictions for a single 3-second analysis window.
// The primary (highest confidence) result is in Result.Species/Confidence.
// Additional predictions are stored separately for reference.
type AdditionalResult struct {
	Species    Species
	Confidence float64
	// RawLabel is the full un-truncated classifier label (e.g. "power_tool"); runtime-only,
	// used by the datastore to classify sound-event classes correctly.
	RawLabel string
	// OutOfRange marks an alternate that failed the range filter when the detection
	// was created; runtime-only, never persisted. The save step drops tagged rows.
	OutOfRange bool
}

// Date returns the detection date in YYYY-MM-DD format.
// This is a convenience method for APIs that need the legacy date format.
func (r *Result) Date() string {
	return r.Timestamp.Format(time.DateOnly)
}

// Time returns the detection time in HH:MM:SS format.
// This is a convenience method for APIs that need the legacy time format.
func (r *Result) Time() string {
	return r.Timestamp.Format(time.TimeOnly)
}
