// Package soundlevel provides 1/3 octave band sound level analysis.
//
// It implements a standalone processor that filters audio through a bank of
// ISO 266 standard bandpass filters, accumulates 1-second RMS measurements,
// and aggregates them over a configurable interval into SoundLevelData.
package soundlevel

import "time"

// OctaveBandData represents sound level statistics for a single 1/3 octave band.
type OctaveBandData struct {
	CenterFreq  float64 `json:"center_frequency_hz"`
	Min         float64 `json:"min_db"`
	Max         float64 `json:"max_db"`
	Mean        float64 `json:"mean_db"`
	SampleCount int     `json:"-"` // Internal use only
}

// SoundLevelData represents complete sound level measurements for all octave bands
// over a configurable interval.
type SoundLevelData struct {
	Timestamp   time.Time                 `json:"timestamp"`
	Source      string                    `json:"source"`
	Name        string                    `json:"name"`
	Duration    int                       `json:"duration_seconds"`
	OctaveBands map[string]OctaveBandData `json:"octave_bands"`
}
