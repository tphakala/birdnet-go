package soundlevel

import (
	"fmt"
	"time"

	"github.com/tphakala/go-audio-dsp/octave"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// MinLevelDB and MaxLevelDB bound every band level the processor reports, in dB
// relative to a full scale amplitude of 1.0. MinLevelDB equals the floor of
// octave.DB (mean square 1e-20). MaxLevelDB is +20 dB: 16-bit PCM in [-1, 1)
// cannot reach it, and the MQTT validator rejects anything above it.
const (
	MinLevelDB = -200.0
	MaxLevelDB = 20.0
)

// Processor measures 1/3-octave band levels of one mono stream with a
// go-audio-dsp octave.Meter over one-second periods and aggregates them per
// interval. It is not safe for concurrent use; run one per stream.
type Processor struct {
	source, name string
	interval     int // periods per reported interval, >= 1
	meter        *octave.Meter
	keys         []string  // bandKey(Nominal), in meter band order
	centers      []float64 // Nominal Hz, in meter band order
	stats        levelStats
}

// NewProcessor builds a Processor for one source at sampleRate Hz. interval is
// the number of one-second periods per result; values below 1 become 1. It
// returns an error wrapping octave.ErrInvalidConfig when sampleRate is not
// positive or no 1/3-octave band from 25 Hz to 20 kHz fits below 0.95 of the
// Nyquist frequency (rates of 59 Hz and below).
func NewProcessor(source, name string, sampleRate, interval int) (*Processor, error) {
	// A period is one second of samples. Every other field keeps the library
	// default: bands from 25 Hz to 20 kHz, Nyquist margin 0.95, order 1.
	meter, err := octave.New(octave.Config{SampleRate: sampleRate, PeriodSamples: sampleRate})
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore/soundlevel").
			Category(errors.CategoryValidation).
			Context("operation", "create_processor").
			Context("sample_rate", sampleRate).
			Build()
	}

	bands := meter.Bands()
	p := &Processor{
		source:   source,
		name:     name,
		interval: max(interval, 1),
		meter:    meter,
		keys:     make([]string, len(bands)),
		centers:  make([]float64, len(bands)),
		stats:    newLevelStats(len(bands)),
	}
	for i, b := range bands {
		p.keys[i] = bandKey(b.Nominal)
		p.centers[i] = b.Nominal
	}
	return p, nil
}

// Process feeds mono float32 samples in [-1, 1] through the meter and calls emit
// once for every completed interval, in order. The band statistics do not
// depend on how the samples are split across calls; Timestamp is the wall clock
// at emission. A nil emit discards the results. Non-finite samples count as 0,
// and every level is finite, within [MinLevelDB, MaxLevelDB] and satisfies
// Min <= Mean <= Max. Process allocates only when an interval completes (the
// result's map).
func (p *Processor) Process(samples []float32, emit func(SoundLevelData)) {
	p.meter.Process(samples, func(meanSquare []float64) { p.addPeriod(meanSquare, emit) })
}

// addPeriod folds one second of band mean squares into the interval statistics
// and reports the interval when it is complete.
func (p *Processor) addPeriod(meanSquare []float64, emit func(SoundLevelData)) {
	p.stats.add(meanSquare)
	if p.stats.periods < p.interval {
		return
	}
	if emit != nil {
		bands := make(map[string]OctaveBandData, len(p.keys))
		for i, key := range p.keys {
			bands[key] = OctaveBandData{
				CenterFreq:  p.centers[i],
				Min:         p.stats.minDB[i],
				Max:         p.stats.maxDB[i],
				Mean:        p.stats.mean(i),
				SampleCount: p.stats.periods,
			}
		}
		emit(SoundLevelData{
			Timestamp:   time.Now(),
			Source:      p.source,
			Name:        p.name,
			Duration:    p.interval,
			OctaveBands: bands,
		})
	}
	p.stats.reset()
}

// bandKey returns the map key for a nominal centre frequency in Hz. Frequencies
// below 1000 Hz use Hz units; 1000 Hz and above use kHz, so 1250 Hz is
// "1.2_kHz" and 3150 Hz is "3.1_kHz".
func bandKey(centerFreq float64) string {
	if centerFreq < 1000 {
		return fmt.Sprintf("%.1f_Hz", centerFreq)
	}
	return fmt.Sprintf("%.1f_kHz", centerFreq/1000)
}
