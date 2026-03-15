package myaudio

import (
	"fmt"
	"math"
	"strings"
)

// AudioFilters defines optional processing filters for clip extraction and preview.
type AudioFilters struct {
	Denoise       string         // "", "light", "medium", "heavy"
	Normalize     bool           // EBU R128 loudnorm
	LoudnessStats *LoudnessStats // Measured stats from analysis pass (nil = analysis mode)
	GainDB        float64        // Volume adjustment in dB (0 = no change)
}

// HasFilters returns true if any processing filter is active.
func (f AudioFilters) HasFilters() bool {
	return f.Denoise != "" || f.Normalize || f.GainDB != 0
}

// denoisePresets maps preset names to afftdn parameters (nr=noise reduction, nf=noise floor).
var denoisePresets = map[string][2]int{
	"light":  {6, -30},
	"medium": {12, -40},
	"heavy":  {20, -50},
}

// IsValidDenoisePreset returns true if the preset name is valid (including empty for "off").
func IsValidDenoisePreset(preset string) bool {
	if preset == "" {
		return true
	}
	_, ok := denoisePresets[preset]
	return ok
}

// Loudnorm default targets (EBU R128).
const (
	loudnormTargetI   = -23.0
	loudnormTargetTP  = -2.0
	loudnormTargetLRA = 7.0
)

// BuildProcessingFilterChain constructs an FFmpeg -af filter string from AudioFilters.
// Filter order: denoise -> normalize -> gain (per spec).
// Returns empty string if no filters are active.
func BuildProcessingFilterChain(f AudioFilters) string {
	var filters []string

	// 1. Denoise (afftdn)
	if params, ok := denoisePresets[f.Denoise]; ok {
		filters = append(filters, fmt.Sprintf("afftdn=nr=%d:nf=%d", params[0], params[1]))
	}

	// 2. Normalize (loudnorm)
	if f.Normalize {
		if f.LoudnessStats != nil {
			// Pass 2: apply with measured values
			filters = append(filters, fmt.Sprintf(
				"loudnorm=I=%.1f:LRA=%.1f:TP=%.1f:measured_I=%s:measured_LRA=%s:measured_TP=%s:measured_thresh=%s",
				loudnormTargetI, loudnormTargetLRA, loudnormTargetTP,
				f.LoudnessStats.InputI, f.LoudnessStats.InputLRA,
				f.LoudnessStats.InputTP, f.LoudnessStats.InputThresh,
			))
		} else {
			// Pass 1: analysis mode
			filters = append(filters, fmt.Sprintf(
				"loudnorm=I=%.1f:LRA=%.1f:TP=%.1f:print_format=json",
				loudnormTargetI, loudnormTargetLRA, loudnormTargetTP,
			))
		}
	}

	// 3. Gain (volume)
	if f.GainDB != 0 && !math.IsNaN(f.GainDB) {
		sign := "+"
		if f.GainDB < 0 {
			sign = ""
		}
		filters = append(filters, fmt.Sprintf("volume=%s%.1fdB", sign, f.GainDB))
	}

	return strings.Join(filters, ",")
}
