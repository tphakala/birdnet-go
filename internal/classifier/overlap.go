package classifier

import (
	"math"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// analysisBaseClipLength is the reference clip length that the user-configured
// birdnet.overlap value is defined against. Overlap is expressed as seconds of
// overlap on the 3-second BirdNET v2.4 window (validated range 0..2.99s), and
// is scaled proportionally for models with a different clip length so the
// overlap FRACTION is preserved across models. See effectiveOverlap.
const analysisBaseClipLength = 3 * time.Second

// minAnalysisStep is the smallest analysis step, in the DURATION domain, that a
// resolved overlap may leave (ClipLength - overlap). It floors BufferInterval and
// caps ResolveModelOverlap so the derived read size cannot reach zero and stall
// the buffer manager. The separate one-sample-frame floor on the byte read size
// is enforced in BufferDimensions.
const minAnalysisStep = time.Millisecond

// effectiveOverlap scales a user-configured overlap to a model's clip length,
// preserving the overlap ratio relative to the base clip.
// Example: 2.0s overlap for a 3s base -> for a 5s model: (2.0 * 5) / 3 = 3.33s.
//
// The scaling is done in float64 for simplicity and to sidestep int64-overflow
// concerns: a naive int64-nanosecond userOverlap*modelClipLength reaches ~90% of
// math.MaxInt64 at the validated overlap ceiling and would overflow for a larger
// base clip. float64 has ample precision here: the result is rounded to whole
// nanoseconds and then aligned to a sample frame downstream.
func effectiveOverlap(userOverlap, baseClipLength, modelClipLength time.Duration) time.Duration {
	if baseClipLength <= 0 {
		return 0
	}
	scaled := float64(userOverlap) * float64(modelClipLength) / float64(baseClipLength)
	return time.Duration(math.Round(scaled))
}

// overlapToBytes converts an overlap duration to a byte count aligned to a whole
// PCM sample frame. sampleRate is in Hz; the frame size is
// conf.NumChannels * conf.BytesPerSample. Returns 0 for non-positive inputs.
func overlapToBytes(overlap time.Duration, sampleRate int) int {
	if overlap <= 0 || sampleRate <= 0 {
		return 0
	}
	frame := conf.NumChannels * conf.BytesPerSample
	if frame <= 0 {
		return 0
	}
	samples := int((overlap * time.Duration(sampleRate)) / time.Second) //nolint:durationcheck // intentional: converts Hz rate to sample count via duration arithmetic
	return samples * frame
}

// ResolveModelOverlap returns the effective analysis-window overlap for a model
// given the current settings. The bat model always uses a fixed 50% overlap
// (ClipLength/2), matching its historically fixed buffer cadence; every other
// model honors the user-configured birdnet.overlap, ratio-scaled to the model's
// clip length. The result is clamped to [0, ClipLength - minAnalysisStep] so the
// derived read size is always strictly positive.
func ResolveModelOverlap(modelID string, spec ModelSpec, s *conf.Settings) time.Duration {
	if modelID == RegistryIDBat {
		return spec.ClipLength / 2
	}

	var overlap time.Duration
	if s != nil {
		overlap = effectiveOverlap(
			time.Duration(s.BirdNET.Overlap*float64(time.Second)),
			analysisBaseClipLength,
			spec.ClipLength,
		)
	}

	// Clamp into [0, max(0, ClipLength-minAnalysisStep)] so the derived read size
	// stays strictly positive. maxOverlap is floored at 0 to stay well-defined for
	// a zero/degenerate clip length (e.g. an unset spec), where it must not push
	// the overlap negative.
	maxOverlap := max(spec.ClipLength-minAnalysisStep, 0)
	return min(max(overlap, 0), maxOverlap)
}
