package ultrasonic

import (
	"slices"

	"github.com/tphakala/go-audio-dsp/stft"

	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	// broadbandSubFrameDivisor divides FFTSize into the sub-frame length of the
	// broadband pass: 1024 samples at the 8192 default, short enough that a click
	// of 1 to 2 ms fills a sub-frame instead of being diluted in a 32 ms frame.
	broadbandSubFrameDivisor = 8
	// broadbandSubHopDivisor divides the sub-frame length into its hop (half a sub-frame).
	broadbandSubHopDivisor = 2
	// broadbandGuardLowRatio and broadbandGuardHighRatio bound the guard band as
	// fractions of FrequencySplitHz: 12 to 16 kHz at the 20 kHz default. A bat call
	// above the split has no energy there; a click or a rustle does.
	broadbandGuardLowRatio  = 0.6
	broadbandGuardHighRatio = 0.8
	// broadbandGuardThreshold is how many times its window median the guard band
	// power of a sub-frame must exceed to count as a broadband event. Ordinary
	// guard band fluctuation stays below it.
	broadbandGuardThreshold = 3.0
	// broadbandContinuityLowRatio and broadbandContinuityHighRatio bound the
	// continuity range as fractions of FrequencySplitHz: 14 to 24 kHz at the
	// 20 kHz default, so the range crosses the split. Rain ticks begin near
	// 14 kHz and run through the split, above most of the guard band, whose
	// median the rain itself keeps high. The guard band rule stays beside this
	// one: on recorded audio neither rule replaced the other.
	broadbandContinuityLowRatio  = 0.7
	broadbandContinuityHighRatio = 1.2
	// broadbandContinuityBands is how many equal bands the continuity range is
	// cut into (1 kHz each at the default). A tick raises all of them at once.
	// A call well above the range leaves most of them at their noise level; a
	// call that sweeps through the split fills the upper bands itself, so only
	// the bands below the split speak against it.
	broadbandContinuityBands = 10
	// broadbandContinuityThreshold is how many times its window median the
	// power of a continuity band must exceed to count as raised. Noise alone is
	// above its median half the time, and a faint real call can come with a
	// slight rise below the split; on recorded calls that rise stayed below
	// this factor in at least one band.
	broadbandContinuityThreshold = 1.25
)

// BroadbandAdjustedCV removes the share of broadband events from cv, the
// result of ComputeUSFrameCV for the same samples, and returns the lowered CV.
//
// A click or a rustle reaches above the split too, and in a 32 ms frame the
// ultrasonic part of a 1 to 2 ms click is enough to clear the CV threshold. The
// window is therefore re-examined in sub-frames of FFTSize/8. A sub-frame is
// broadband when its ultrasonic power is above the window median and either
// the power in the guard band exceeds broadbandGuardThreshold times its window
// median, or every band of the continuity range exceeds
// broadbandContinuityThreshold times its own window median. The second rule is
// left out when a continuity band would be narrower than one sub-frame bin or
// the range would end above the Nyquist bin. The ultrasonic power of a
// broadband sub-frame is replaced by the median, and the returned CV is cv
// scaled by the CV after that replacement over the CV before it.
//
// The step only lowers a CV. cv is returned unchanged when it is already below
// the threshold, when the settings or the window leave no usable sub-frame
// layout, when the guard band or the ultrasonic variability is empty (a guard
// median of zero, as in gated or zero filled audio), and, because the ratio is
// then exactly 1, when no sub-frame is broadband.
func BroadbandAdjustedCV(samples []float32, sampleRate int, cfg conf.UltrasonicFilterConfig, cv float64) float64 {
	if IsUnlikely(cv, cfg) || cfg.ValidateSTFT() != nil || !HasUltrasonicBand(sampleRate, cfg.FrequencySplitHz) {
		return cv
	}
	subSize := cfg.FFTSize / broadbandSubFrameDivisor
	subHop := subSize / broadbandSubHopDivisor
	if subHop < 1 || len(samples) < subSize+subHop { // no sub-frame hop, or fewer than two sub-frames
		return cv
	}
	binWidth := float64(sampleRate) / float64(subSize)
	split := float64(cfg.FrequencySplitHz)
	splitBin := int(split / binWidth)
	guardLo := int(broadbandGuardLowRatio * split / binWidth)
	guardHi := int(broadbandGuardHighRatio * split / binWidth)
	if guardLo < 1 || guardHi <= guardLo {
		return cv
	}
	contLo := int(broadbandContinuityLowRatio * split / binWidth)
	contHi := int(broadbandContinuityHighRatio * split / binWidth)
	contSpan := contHi - contLo
	plan, err := stft.New(stft.Config{FrameSize: subSize, HopSize: subHop, Window: stft.HannSymmetric})
	if err != nil {
		return cv
	}

	numSub := 1 + (len(samples)-subSize)/subHop
	us := make([]float64, numSub)
	guard := make([]float64, numSub)
	frame := make([]float32, subSize)
	power := make([]float32, plan.NumBins())
	var cont [][]float64 // stays empty, and the rule off, when its bands do not fit the spectrum
	if contSpan >= broadbandContinuityBands && contHi <= len(power) {
		cont = make([][]float64, broadbandContinuityBands)
		for k := range cont {
			cont[k] = make([]float64, numSub)
		}
	}
	for f := range numSub {
		offset := f * subHop
		removeMean(frame, samples[offset:offset+subSize])
		plan.PowerInto(power, frame, stft.NoPad)
		us[f] = bandPower(power, splitBin, len(power))
		guard[f] = bandPower(power, guardLo, guardHi)
		for k := range cont {
			cont[k][f] = bandPower(power, contLo+contSpan*k/broadbandContinuityBands, contLo+contSpan*(k+1)/broadbandContinuityBands)
		}
	}

	before := coefficientOfVariation(us)
	medianUS, medianGuard := median(us), median(guard)
	if before <= 0 || medianGuard <= 0 {
		return cv
	}
	contFloor := make([]float64, len(cont))
	for k := range cont {
		contFloor[k] = broadbandContinuityThreshold * median(cont[k])
	}
	for f := range us {
		if us[f] > medianUS && (guard[f] > broadbandGuardThreshold*medianGuard || allAbove(cont, contFloor, f)) {
			us[f] = medianUS
		}
	}
	// With no broadband sub-frame us is unchanged and the ratio is exactly 1.
	return cv * min(1, coefficientOfVariation(us)/before)
}

// allAbove reports whether every band exceeds its floor in sub-frame f; it is
// false when there are no bands.
func allAbove(bands [][]float64, floors []float64, f int) bool {
	for k, band := range bands {
		if band[f] <= floors[k] {
			return false
		}
	}
	return len(bands) > 0
}

// bandPower sums the one-sided power of bins [from, to), where from is above
// the DC bin: every bin but the last one of power (Nyquist) counts twice, the
// weighting ComputeUSFrameCV applies inline.
func bandPower(power []float32, from, to int) float64 {
	nyquistBin := len(power) - 1
	var sum float64
	for bin := from; bin < to; bin++ {
		p := float64(power[bin])
		if bin < nyquistBin {
			p *= 2
		}
		sum += p
	}
	return sum
}

// median returns the middle value of values, or the mean of the two middle
// values for an even count. values is not modified.
func median(values []float64) float64 {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
