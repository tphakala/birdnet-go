// Derived from SoX src/spectrogram.c, (c) 2008-9 robs@users.sourceforge.net,
// licensed under the GNU Lesser General Public License version 2.1 or later.
// Changes made in October 2026: drawing goes through the Surface interface so the caller
// supplies glyph shapes, the footer comment and title are not drawn, and gain and
// autogain are fixed at zero.

package soxchrome

import (
	"fmt"
	"math"
)

// Surface is what DrawChrome draws on. Coordinates are bottom-up: y = 0 is the
// bottom row. PrintAt and PrintUp place a text cell grid whose top-left cell is
// at (x, y) and advance by CellW per character.
type Surface interface {
	// Set writes one pixel.
	Set(x, y int, idx uint8)
	// PrintAt draws s left to right; cell row r is at y-r.
	PrintAt(x, y int, idx uint8, s string)
	// PrintUp draws s rotated to read upward; cell row r is at column x+r and
	// successive characters step up by CellW.
	PrintUp(x, y int, idx uint8, s string)
}

// ChromeParams describes the raster DrawChrome frames.
type ChromeParams struct {
	// Cols and Rows are the raster size in pixels.
	Cols, Rows int
	// SampleRate is the rate in Hz the frequency axis is drawn for: it runs from
	// DC to half of it. It can be lower than the rate of the analysed signal.
	SampleRate int
	// Seconds is the time the raster spans.
	Seconds float64
	// RangeDB is the dynamic range of the legend, in dB.
	RangeDB float64
}

// DrawChrome draws the frame, tick marks, tick labels, axis titles and dBFS
// legend of a non-raw, single channel image of Dims(p.Cols, p.Rows, false),
// in SoX's order. It never writes inside the raster.
func DrawChrome(s Surface, p ChromeParams) {
	w, _ := Dims(p.Cols, p.Rows, false)
	drawFrame(s, p)
	drawTimeAxis(s, p)
	drawFrequencyAxis(s, p)
	drawLegend(s, p, w)
}

// drawFrame draws the border around the raster (spectrogram.c:847-853).
func drawFrame(s Surface, p ChromeParams) {
	for j := range p.Rows {
		s.Set(MarginLeft-1, MarginBelow+j, IndexGrid)
		s.Set(MarginLeft+p.Cols, MarginBelow+j, IndexGrid)
	}
	for i := -1; i <= p.Cols; i++ {
		s.Set(MarginLeft+i, MarginBelow-1, IndexGrid)
		s.Set(MarginLeft+i, MarginBelow+p.Rows, IndexGrid)
	}
}

// drawTimeAxis draws the time title, ticks and labels (spectrogram.c:868-886).
func drawTimeAxis(s Surface, p ChromeParams) {
	step, limit, prefix := axisScale(p.Seconds, p.Cols/(CellW*timeTickDensity/2))
	title := fmt.Sprintf("Time (%ss)", prefix)
	s.PrintAt(MarginLeft+(p.Cols-CellW*len(title))/2, timeTitleY, IndexText, title)
	for i := 0; float64(i) <= limit; i += step {
		x := 0
		if limit != 0 {
			x = int(float64(i)/limit*float64(p.Cols) + .5)
		}
		for t := range TickLen {
			s.Set(MarginLeft-1+x, MarginBelow-1-t, IndexGrid)
			s.Set(MarginLeft-1+x, MarginBelow+p.Rows+t, IndexGrid)
		}
		if step == axisStepFive && i%tenthsPerUnit != 0 {
			continue
		}
		text := fmt.Sprintf("%.6g", tenthStep*float64(i))
		lx := MarginLeft + x - timeLabelHalfW*len(text)
		s.PrintAt(lx, MarginBelow-timeLabelBelow, IndexLabels, text)
		s.PrintAt(lx, MarginBelow+p.Rows+timeLabelAbove, IndexLabels, text)
	}
}

// drawFrequencyAxis draws the frequency title, ticks and labels on both sides
// (spectrogram.c:889-911).
func drawFrequencyAxis(s Surface, p ChromeParams) {
	maxSteps := (p.Rows - 1) / ((CellH*freqTickNumer + 1) / axisStepDivisor)
	step, limit, prefix := axisScale(float64(p.SampleRate)/2, maxSteps)
	title := fmt.Sprintf("Frequency (%sHz)", prefix)
	s.PrintUp(freqTitleX, MarginBelow+(p.Rows-CellW*len(title))/2, IndexText, title)
	for i := 0; float64(i) <= limit; i += step {
		y := 0
		if limit != 0 {
			y = int(float64(i)/limit*float64(p.Rows-1) + .5)
		}
		for t := range TickLen {
			s.Set(MarginLeft-1-t, MarginBelow+y, IndexGrid)
			s.Set(MarginLeft+p.Cols+t, MarginBelow+y, IndexGrid)
		}
		if step == axisStepFive && i%tenthsPerUnit != 0 {
			continue
		}
		left, right := fmt.Sprintf("%*s", freqLabelChars, "DC"), "DC"
		if i != 0 {
			left = fmt.Sprintf("%*.6g", freqLabelChars, tenthStep*float64(i))
			right = fmt.Sprintf("%.6g", tenthStep*float64(i))
		}
		s.PrintAt(MarginLeft-freqLabelGap-CellW*freqLabelChars, MarginBelow+y+labelRowOffset, IndexLabels, left)
		s.PrintAt(MarginLeft+p.Cols+freqLabelRight, MarginBelow+y+labelRowOffset, IndexLabels, right)
	}
}

// maxLegendRangeDB is the largest level range that gets labels. Real ranges are
// 20 to 180 dB; the bound keeps the integer label loop from wrapping.
const maxLegendRangeDB = 1e6

// drawLegend draws the dBFS bar, its title and level labels (spectrogram.c:914-931).
func drawLegend(s Surface, p ChromeParams, width int) {
	k := min(LegendMaxRows, p.Rows)
	if k < 2 {
		return
	}
	base := MarginBelow + (p.Rows-k)/2
	s.PrintAt(width-MarginRight-legendTitleGap-CellW, base-legendTitleRise, IndexText, "dBFS")
	for j := range k {
		idx := colourIndex(p.RangeDB*(float64(j)/float64(k-1)-1), p.RangeDB)
		for i := range LegendWidth {
			s.Set(width-MarginRight-1-i, base+j, idx)
		}
	}
	if !(p.RangeDB > 0) || p.RangeDB > maxLegendRangeDB {
		return // only a positive range within the bound has level labels
	}
	rangeDB := int(math.Round(p.RangeDB))
	step := tenthsPerUnit * int(math.Ceil(p.RangeDB/tenthsPerUnit*(CellH+2)/float64(k-1)))
	for i := 0; i <= rangeDB; i += step {
		y := int(float64(i)/p.RangeDB*float64(k-1) + .5)
		s.PrintAt(width-MarginRight+1, base+y+labelRowOffset, IndexLabels, fmt.Sprintf("%+d", i-rangeDB))
	}
}
