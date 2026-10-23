// Derived from SoX src/spectrogram.c, (c) 2008-9 robs@users.sourceforge.net,
// licensed under the GNU Lesser General Public License version 2.1 or later.
// Changes made in October 2026: constants are named and
// exported, and only the single channel, untitled, non-raw layout is kept.

package soxchrome

import "math"

// Margins around the raster, in pixels (spectrogram.c:769-773, :782-783).
const (
	// MarginLeft is the width left of the raster (axis name and tick labels).
	MarginLeft = 58
	// MarginBelow is the height under the raster (tick labels, axis name and
	// the footer band).
	MarginBelow = 48
	// MarginAbove is the height over the raster (tick labels).
	MarginAbove = 30
	// GapBetween is the gap between the raster and the legend bar.
	GapBetween = 37
	// LegendWidth is the width of the legend bar.
	LegendWidth = 14
	// MarginRight is the width right of the legend bar (level labels).
	MarginRight = 35
)

// Text cell and limits (spectrogram.c:206, :708-710, :914-916).
const (
	// CellW is the horizontal advance of one character, SoX font_X.
	CellW = 6
	// CellH is the height of one text line, SoX font_y.
	CellH = 12
	// MinWidth is the smallest raster width SoX accepts (-x).
	MinWidth = 100
	// LegendMaxRows is the largest height of the legend bar.
	LegendMaxRows = 400
	// TickLen is the length of a tick mark.
	TickLen = 3
)

// Offsets measured in the text cell, as used by SoX (spectrogram.c:868-931).
const (
	timeTitleY      = 24      // baseline row of the time axis title
	timeLabelBelow  = 6       // tick label offset under the raster
	timeLabelAbove  = 14      // tick label offset over the raster
	timeLabelHalfW  = 3       // half a character, centres a tick label
	freqTitleX      = 10      // column of the rotated frequency title
	freqLabelGap    = 4       // gap between the raster and a left label
	freqLabelRight  = 6       // gap between the raster and a right label
	labelRowOffset  = 5       // rows from a tick down to the label top
	legendTitleGap  = 2       // columns from the legend bar to its title
	legendTitleRise = 13      // rows from the legend bar bottom to its title
	timeTickDensity = 9       // time ticks: one per CellW*9/2 pixels
	freqTickNumer   = 3       // frequency ticks: one per (CellH*3+1)/2 pixels
	tenthsPerUnit   = 10      // axis steps are counted in tenths
	maxAxisValue    = 1 << 30 // largest step or limit, in tenths, an axis scale may return
	axisStepFive    = 5       // a step of 5 labels only multiples of 10
	axisStepDivisor = 2       // divisor in the frequency tick density

	// log10SnapEpsilon is how close a log10 must be to an integer to snap to it.
	log10SnapEpsilon = 1e-9
	// tenthStep converts a step in tenths of a unit to the unit.
	tenthStep = 0.1
	// freqLabelChars is the width, in characters, of a left frequency label.
	freqLabelChars = 5
)

// Dims returns the image size for a raster of cols by rows pixels. A raw image is
// the raster alone.
func Dims(cols, rows int, raw bool) (w, h int) {
	if raw {
		return cols, rows
	}
	return MarginLeft + cols + GapBetween + LegendWidth + MarginRight, MarginBelow + rows + MarginAbove
}

// axisScale ports SoX's axis(): it picks a tick step for the range [0, to] with
// at most maxSteps ticks. Step and limit are in tenths of the displayed unit,
// after the SI prefix is applied; prefix is empty or one of p n u m k M G T P E.
func axisScale(to float64, maxSteps int) (step int, limit float64, prefix string) {
	// A length that is not finite, or whose tenths overflow, has no ticks to scale;
	// treat it as zero so the limit stays finite and the callers' tick loops end.
	if scaled := to * tenthsPerUnit; math.IsInf(scaled, 0) || math.IsNaN(scaled) {
		to = 0
	}
	scale := 1.0
	fstep := math.Max(1, tenthsPerUnit*to)
	prefixNum := 0
	if maxSteps > 0 && to > 0 {
		log10v := math.Inf(1)
		to *= tenthsPerUnit
		minStep := to / float64(maxSteps)
		for i := axisStepFive; i > 0; i >>= 1 {
			// Snap near-integer log10 values to the integer before ceil, as the C
			// library does (log10(0.1) is exactly -1 there but a hair above in Go).
			l := math.Log10(minStep * float64(i))
			if r := math.Round(l); math.Abs(l-r) < log10SnapEpsilon {
				l = r
			}
			if try := math.Ceil(l); try <= log10v {
				log10v = try
				fstep = math.Pow(10, log10v) / float64(i)
				if i > 1 {
					log10v--
				}
			}
		}
		prefixNum = int(math.Floor(log10v / 3))
		scale = math.Pow(10, -3*float64(prefixNum))
	}
	// SoX indexes "pnum-kMGTPE" with prefix_num + 4, or the terminator for none.
	const prefixes = "pnum-kMGTPE"
	if prefixNum != 0 {
		if idx := prefixNum + 4; idx >= 0 && idx < len(prefixes) {
			prefix = prefixes[idx : idx+1]
		}
	}
	// A step or limit the tick loops cannot finish has no usable scale: a
	// subnormal length scales to an infinite limit, and an enormous one overflows
	// int. Every real axis is far below the bound.
	scaledStep, scaledLimit := fstep*scale, to*scale
	if !(scaledStep < maxAxisValue && scaledLimit < maxAxisValue) {
		return 1, 0, ""
	}
	return max(int(scaledStep+.5), 1), scaledLimit, prefix
}
