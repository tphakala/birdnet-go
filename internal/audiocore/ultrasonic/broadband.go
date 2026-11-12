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
	// broadbandContinuityLowRatio is where the continuity range starts, as a
	// fraction of FrequencySplitHz; it ends at the split: 14 to 20 kHz at the
	// 20 kHz default. Rain ticks begin near 14 kHz and run through the split,
	// above most of the guard band, whose median the rain itself keeps high.
	broadbandContinuityLowRatio = 0.7
	// broadbandContinuityBands is how many equal bands the continuity range is
	// cut into (1 kHz each at the default). A tick raises all of them at once; a
	// call above the split raises none, and one that reaches just below the
	// split raises only the upper ones.
	broadbandContinuityBands = 6
)

// BroadbandAdjustedCV removes the share of broadband events from cv, the
// result of ComputeUSFrameCV for the same samples, and returns the lowered CV.
//
// A click or a rustle reaches above the split too, and in a 32 ms frame the
// ultrasonic part of a 1 to 2 ms click is enough to clear the CV threshold. The
// window is therefore re-examined in sub-frames of FFTSize/8. A sub-frame is
// broadband when its ultrasonic power is above the window median and either
// the power in the guard band (0.6 to 0.8 of FrequencySplitHz) exceeds
// broadbandGuardThreshold times its window median, or every band of the
// continuity range (0.7 of FrequencySplitHz up to the split, in
// broadbandContinuityBands equal bands) is above its own window median. The
// second rule catches rain: its ticks start above most of the guard band, and
// the rain keeps the guard band median too high for the first rule. The
// ultrasonic power of a broadband sub-frame is replaced by the median, and the
// returned CV is cv scaled by the CV after that replacement over the CV before
// it.
//
// The step only lowers a CV. cv is returned unchanged when it is already below
// the threshold, when the settings or the window leave no usable sub-frame
// layout, when the guard band or the ultrasonic variability is empty (a guard
// median of zero, as in gated or zero filled audio), and, because the ratio is
// then exactly 1, when no sub-frame is broadband. The continuity rule is left
// out when its range is narrower than one bin per band.
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
	// Bin edges of the continuity bands; none when a band would be empty.
	contLo := int(broadbandContinuityLowRatio * split / binWidth)
	var contEdges [broadbandContinuityBands + 1]int
	numCont := 0
	if splitBin-contLo >= broadbandContinuityBands {
		numCont = broadbandContinuityBands
		for k := range contEdges {
			contEdges[k] = contLo + (splitBin-contLo)*k/broadbandContinuityBands
		}
	}
	plan, err := stft.New(stft.Config{FrameSize: subSize, HopSize: subHop, Window: stft.HannSymmetric})
	if err != nil {
		return cv
	}

	numSub := 1 + (len(samples)-subSize)/subHop
	us := make([]float64, numSub)
	guard := make([]float64, numSub)
	cont := make([][]float64, numCont)
	for k := range cont {
		cont[k] = make([]float64, numSub)
	}
	frame := make([]float32, subSize)
	power := make([]float32, plan.NumBins())
	for f := range numSub {
		offset := f * subHop
		removeMean(frame, samples[offset:offset+subSize])
		plan.PowerInto(power, frame, stft.NoPad)
		us[f] = bandPower(power, splitBin, len(power))
		guard[f] = bandPower(power, guardLo, guardHi)
		for k := range cont {
			cont[k][f] = bandPower(power, contEdges[k], contEdges[k+1])
		}
	}

	before := coefficientOfVariation(us)
	medianUS, medianGuard := median(us), median(guard)
	if before <= 0 || medianGuard <= 0 {
		return cv
	}
	medianCont := make([]float64, numCont)
	for k := range cont {
		medianCont[k] = median(cont[k])
	}
	for f := range us {
		if us[f] <= medianUS {
			continue
		}
		continuous := numCont > 0
		for k := range cont {
			if cont[k][f] <= medianCont[k] {
				continuous = false
				break
			}
		}
		if continuous || guard[f] > broadbandGuardThreshold*medianGuard {
			us[f] = medianUS
		}
	}
	// With no broadband sub-frame us is unchanged and the ratio is exactly 1.
	return cv * min(1, coefficientOfVariation(us)/before)
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
