package detection

import (
	"math"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// DetectionTimeOffset is subtracted from the current time when creating detections.
// BirdNET analyzes 3-second audio chunks, and this offset places the detection
// timestamp closer to when the bird actually vocalized rather than when the
// analysis completed.
const DetectionTimeOffset = 2 * time.Second

// ResultParams holds the parameters for creating a new Result.
type ResultParams struct {
	Begin       time.Time
	End         time.Time
	Species     string  // BirdNET species string to be parsed
	Confidence  float64
	Source      AudioSource
	ClipName    string
	Elapsed     time.Duration
	Occurrence  float64
}

// NewResult creates a Result from the given parameters.
// This is the primary factory function for creating detection results.
func NewResult(settings *conf.Settings, p *ResultParams) *Result {
	// Parse the species string to get structured species info
	species := ParseSpeciesString(p.Species)

	// Detection time is adjusted to account for the 3-second analysis chunk
	detectionTime := time.Now().Add(-DetectionTimeOffset)

	// Round confidence to two decimal places
	roundedConfidence := math.Round(p.Confidence*100) / 100

	// Clamp occurrence to [0, 1] range
	occurrence := math.Max(0.0, math.Min(1.0, p.Occurrence))

	return &Result{
		Timestamp:      detectionTime,
		SourceNode:     settings.Main.Name,
		AudioSource:    p.Source,
		BeginTime:      p.Begin,
		EndTime:        p.End,
		Species:        species,
		Confidence:     roundedConfidence,
		Latitude:       settings.BirdNET.Latitude,
		Longitude:      settings.BirdNET.Longitude,
		Threshold:      settings.BirdNET.Threshold,
		Sensitivity:    settings.BirdNET.Sensitivity,
		ClipName:       p.ClipName,
		ProcessingTime: p.Elapsed,
		Occurrence:     occurrence,
		Model:          DefaultModelInfo(),
	}
}

// NewResultWithTimestamp creates a Result with an explicit timestamp.
// Use this when recreating results from database or for testing.
func NewResultWithTimestamp(timestamp time.Time, species Species, confidence float64) *Result {
	return &Result{
		Timestamp:  timestamp,
		Species:    species,
		Confidence: math.Round(confidence*100) / 100,
		Model:      DefaultModelInfo(),
	}
}
