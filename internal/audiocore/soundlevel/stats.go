package soundlevel

import "github.com/tphakala/go-audio-dsp/octave"

// levelStats folds one-second band levels into per-band min, max and the sum
// for an arithmetic mean of dB values (not an energy mean), the published
// interval statistics.
type levelStats struct {
	minDB, maxDB, sumDB []float64
	periods             int
}

// newLevelStats returns statistics for the given number of bands.
func newLevelStats(bands int) levelStats {
	return levelStats{
		minDB: make([]float64, bands),
		maxDB: make([]float64, bands),
		sumDB: make([]float64, bands),
	}
}

// add folds one period's band mean squares into the statistics. The first
// period after a reset overwrites the previous interval; later ones fold in.
func (s *levelStats) add(meanSquare []float64) {
	first := s.periods == 0
	for i, ms := range meanSquare {
		db := levelDB(ms)
		if first {
			s.minDB[i], s.maxDB[i], s.sumDB[i] = db, db, db
			continue
		}
		s.minDB[i] = min(s.minDB[i], db)
		s.maxDB[i] = max(s.maxDB[i], db)
		s.sumDB[i] += db
	}
	s.periods++
}

// reset starts a new interval.
func (s *levelStats) reset() { s.periods = 0 }

// mean returns the arithmetic mean of band i's dB values. It is clamped into
// [min, max] because floating point summation can place the mean of a constant
// signal a rounding error outside its own minimum.
func (s *levelStats) mean(i int) float64 {
	return min(max(s.sumDB[i]/float64(s.periods), s.minDB[i]), s.maxDB[i])
}

// levelDB converts a band mean square to the published level, clamped to
// [MinLevelDB, MaxLevelDB]. octave.DB is already finite and floors at -200 dB;
// it caps at +200 dB, which the upper clamp brings down to MaxLevelDB. The lower
// clamp is a no-op today and keeps the bound defined in one place.
func levelDB(meanSquare float64) float64 {
	return min(max(octave.DB(meanSquare), MinLevelDB), MaxLevelDB)
}
