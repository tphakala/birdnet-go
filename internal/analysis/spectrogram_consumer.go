package analysis

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
	"github.com/tphakala/birdnet-go/internal/errors"
	"gonum.org/v1/gonum/dsp/fourier"
)

const spectrogramChanSize = 16

type SpectrogramConsumer struct {
	id              string
	rate            int
	depth           int
	channels        int
	fftSize         int
	hopSize         int
	window          string
	batchIntervalMs int
	closed          atomic.Bool
	closeOnce       sync.Once
	outCh           chan apiv2.LiveSpectrogramBatch

	fft          *fourier.FFT
	windowCoeffs []float64
	rolling      []float64
	rollingStart time.Time
	batch        []apiv2.LiveSpectrogramColumn
	lastFlush    time.Time
	scratchPCM   []float64
	scratchMono  []float64
}

func NewSpectrogramConsumer(id string, sampleRate, bitDepth, channels, fftSize, hopSize int, window string, batchIntervalMs int) (*SpectrogramConsumer, chan apiv2.LiveSpectrogramBatch, error) {
	if sampleRate <= 0 || fftSize <= 0 || hopSize <= 0 {
		return nil, nil, errors.Newf("invalid live spectrogram configuration").
			Component("analysis.spectrogram_consumer").
			Category(errors.CategoryValidation).
			Context("operation", "new_spectrogram_consumer").
			Build()
	}
	if fftSize&(fftSize-1) != 0 {
		return nil, nil, errors.Newf("fft size must be power of 2: %d", fftSize).
			Component("analysis.spectrogram_consumer").
			Category(errors.CategoryValidation).
			Context("operation", "new_spectrogram_consumer").
			Build()
	}
	if hopSize > fftSize {
		return nil, nil, errors.Newf("hop size must be <= fft size: hop=%d fft=%d", hopSize, fftSize).
			Component("analysis.spectrogram_consumer").
			Category(errors.CategoryValidation).
			Context("operation", "new_spectrogram_consumer").
			Build()
	}
	if batchIntervalMs <= 0 {
		batchIntervalMs = 16
	}

	ch := make(chan apiv2.LiveSpectrogramBatch, spectrogramChanSize)
	return &SpectrogramConsumer{
		id:              id,
		rate:            sampleRate,
		depth:           bitDepth,
		channels:        channels,
		fftSize:         fftSize,
		hopSize:         hopSize,
		window:          window,
		batchIntervalMs: batchIntervalMs,
		outCh:           ch,
		fft:             fourier.NewFFT(fftSize),
		windowCoeffs:    buildWindow(window, fftSize),
	}, ch, nil
}

func (c *SpectrogramConsumer) ID() string      { return c.id }
func (c *SpectrogramConsumer) SampleRate() int { return c.rate }
func (c *SpectrogramConsumer) BitDepth() int   { return c.depth }
func (c *SpectrogramConsumer) Channels() int   { return c.channels }

func (c *SpectrogramConsumer) Write(frame audiocore.AudioFrame) error { //nolint:gocritic
	if c.closed.Load() {
		return audiocore.ErrConsumerClosed
	}
	samples := c.decodeFrame(frame)
	if len(samples) == 0 {
		return nil
	}
	if c.rollingStart.IsZero() {
		c.rollingStart = frame.Timestamp
	}
	c.rolling = append(c.rolling, samples...)

	for len(c.rolling) >= c.fftSize {
		c.batch = append(c.batch, c.makeColumn())
		c.rolling = c.rolling[c.hopSize:]
		c.rollingStart = c.rollingStart.Add(samplesDuration(c.hopSize, c.rate))
	}

	if len(c.batch) == 0 {
		return nil
	}
	now := time.Now()
	if c.lastFlush.IsZero() || now.Sub(c.lastFlush) >= time.Duration(c.batchIntervalMs)*time.Millisecond {
		c.flush(frame.SourceID)
		c.lastFlush = now
	}
	return nil
}

func (c *SpectrogramConsumer) Close() error {
	c.closed.Store(true)
	c.closeOnce.Do(func() {
		close(c.outCh)
	})
	return nil
}

func (c *SpectrogramConsumer) decodeFrame(frame audiocore.AudioFrame) []float64 {
	evenLen := len(frame.Data) &^ 1
	if evenLen == 0 {
		return nil
	}
	sampleCount := evenLen / 2
	if cap(c.scratchPCM) < sampleCount {
		c.scratchPCM = make([]float64, sampleCount)
	}
	pcm := c.scratchPCM[:sampleCount]
	convert.BytesToFloat64PCM16Into(pcm, frame.Data[:evenLen])

	if frame.Channels <= 1 {
		out := make([]float64, len(pcm))
		copy(out, pcm)
		return out
	}

	monoCount := sampleCount / frame.Channels
	if cap(c.scratchMono) < monoCount {
		c.scratchMono = make([]float64, monoCount)
	}
	mono := c.scratchMono[:monoCount]
	for i := 0; i < monoCount; i++ {
		sum := 0.0
		base := i * frame.Channels
		for ch := 0; ch < frame.Channels; ch++ {
			sum += pcm[base+ch]
		}
		mono[i] = sum / float64(frame.Channels)
	}
	out := make([]float64, monoCount)
	copy(out, mono)
	return out
}

func (c *SpectrogramConsumer) makeColumn() apiv2.LiveSpectrogramColumn {
	windowed := make([]float64, c.fftSize)
	for i := 0; i < c.fftSize; i++ {
		windowed[i] = c.rolling[i] * c.windowCoeffs[i]
	}

	coeffs := c.fft.Coefficients(nil, windowed)
	bins := make([]uint8, c.fftSize/2)
	for i := 0; i < len(bins); i++ {
		mag := math.Hypot(real(coeffs[i]), imag(coeffs[i]))
		db := 20 * math.Log10(mag+1e-12)
		scaled := ((db + 100) / 100) * 255
		switch {
		case scaled < 0:
			scaled = 0
		case scaled > 255:
			scaled = 255
		}
		bins[i] = uint8(scaled)
	}

	centerTime := c.rollingStart.Add(samplesDuration(c.fftSize/2, c.rate))
	return apiv2.LiveSpectrogramColumn{
		TUnixMs: centerTime.UnixMilli(),
		Bins:    apiv2.SpectrogramBins(bins),
	}
}

func (c *SpectrogramConsumer) flush(sourceID string) {
	if len(c.batch) == 0 {
		return
	}
	batch := apiv2.LiveSpectrogramBatch{
		SourceID:        sourceID,
		SampleRate:      c.rate,
		FFTSize:         c.fftSize,
		HopSize:         c.hopSize,
		Window:          c.window,
		BatchIntervalMs: c.batchIntervalMs,
		Columns:         append([]apiv2.LiveSpectrogramColumn(nil), c.batch...),
	}
	select {
	case c.outCh <- batch:
	default:
	}
	c.batch = c.batch[:0]
}

func buildWindow(window string, size int) []float64 {
	coeffs := make([]float64, size)
	switch window {
	case "hann":
		for i := 0; i < size; i++ {
			coeffs[i] = 0.5 - 0.5*math.Cos((2*math.Pi*float64(i))/float64(size-1))
		}
	default:
		for i := range coeffs {
			coeffs[i] = 1
		}
	}
	return coeffs
}

func samplesDuration(sampleCount, sampleRate int) time.Duration {
	return time.Duration(float64(sampleCount) / float64(sampleRate) * float64(time.Second))
}

var _ audiocore.AudioConsumer = (*SpectrogramConsumer)(nil)
