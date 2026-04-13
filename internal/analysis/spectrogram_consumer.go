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
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"gonum.org/v1/gonum/dsp/fourier"
)

const (
	spectrogramChanSize = 16
	// spectrogramDropLogInterval controls how often drop warnings are emitted
	// at each fan-out stage. One log line per this many consecutive drops,
	// plus a single line on the very first drop so the operator is alerted
	// as soon as loss starts.
	spectrogramDropLogInterval int64 = 100
)

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
	// sendMu serializes the non-blocking send in flush with the channel
	// close in Close. Without it, a Close racing with an in-flight flush
	// could panic on "send on closed channel". In production the router
	// drains the consumer before calling Close, so contention is effectively
	// zero — this guard protects tests and future callers.
	sendMu sync.Mutex
	outCh  chan apiv2.LiveSpectrogramBatch
	drops  atomic.Int64

	fft          *fourier.FFT
	windowCoeffs []float64
	rolling      []float64
	// streamEpoch is the wall-clock timestamp of sample index 0 (the first
	// sample of the first frame). startSample is the absolute sample index
	// of rolling[0] relative to streamEpoch. Using integer sample counts
	// eliminates floating-point drift across long streams — centerTime is
	// computed absolutely from the epoch rather than incremented per hop.
	streamEpoch time.Time
	startSample int64
	batch       []apiv2.LiveSpectrogramColumn
	lastFlush   time.Time
	// scratchPCM is reused across decodeFrame calls to avoid reallocating
	// the PCM buffer every frame. Mono downmix happens in place at the
	// front of the same buffer.
	scratchPCM []float64
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
	if c.streamEpoch.IsZero() {
		c.streamEpoch = frame.Timestamp
		c.startSample = 0
	}
	c.rolling = append(c.rolling, samples...)

	for len(c.rolling) >= c.fftSize {
		c.batch = append(c.batch, c.makeColumn())
		c.rolling = c.rolling[c.hopSize:]
		c.startSample += int64(c.hopSize)
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
	c.closeOnce.Do(func() {
		c.sendMu.Lock()
		defer c.sendMu.Unlock()
		c.closed.Store(true)
		close(c.outCh)
	})
	return nil
}

// decodeFrame converts frame.Data from 16-bit little-endian PCM into float64
// samples in c.scratchPCM and returns a slice referencing it. The caller
// (Write) immediately appends the result into c.rolling, which copies, so
// returning the scratch buffer directly is safe — the next Write call may
// overwrite it. Multi-channel input is downmixed in place at the front of
// the same buffer.
func (c *SpectrogramConsumer) decodeFrame(frame audiocore.AudioFrame) []float64 {
	evenLen := len(frame.Data) &^ 1
	if evenLen == 0 {
		return nil
	}
	sampleCount := evenLen / 2
	if cap(c.scratchPCM) < sampleCount {
		c.scratchPCM = make([]float64, sampleCount)
	} else {
		c.scratchPCM = c.scratchPCM[:sampleCount]
	}
	convert.BytesToFloat64PCM16Into(c.scratchPCM, frame.Data[:evenLen])

	if frame.Channels <= 1 {
		return c.scratchPCM
	}

	// In-place downmix: write position i always lags the read positions
	// [i*channels, i*channels+channels), so overwrites never corrupt an
	// unread sample. Requires channels >= 2, which is guaranteed here.
	monoCount := sampleCount / frame.Channels
	invChannels := 1.0 / float64(frame.Channels)
	for i := 0; i < monoCount; i++ {
		sum := 0.0
		base := i * frame.Channels
		for ch := 0; ch < frame.Channels; ch++ {
			sum += c.scratchPCM[base+ch]
		}
		c.scratchPCM[i] = sum * invChannels
	}
	return c.scratchPCM[:monoCount]
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

	// Center time is computed absolutely from the stream epoch plus the
	// integer sample index of the FFT window's midpoint. This keeps
	// timestamps stable over multi-hour streams — no per-hop float drift.
	centerSample := c.startSample + int64(c.fftSize/2)
	centerTime := c.streamEpoch.Add(sampleOffset(centerSample, c.rate))
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
	c.batch = c.batch[:0]

	// Serialize the send with Close so a concurrent Close cannot cause a
	// "send on closed channel" panic. Both the lock window and the select
	// are O(nanoseconds).
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed.Load() {
		return
	}
	select {
	case c.outCh <- batch:
	default:
		drops := c.drops.Add(1)
		if drops == 1 || drops%spectrogramDropLogInterval == 0 {
			GetLogger().Warn("live spectrogram batch dropped at consumer",
				logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
				logger.String("consumer_id", c.id),
				logger.Int64("total_drops", drops),
				logger.Int("columns", len(batch.Columns)))
		}
	}
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

// sampleOffset returns the time.Duration that corresponds to the given
// sample index at the given sample rate, using integer math. It splits
// samples into whole seconds + remainder to keep the intermediate product
// inside int64 even for very long streams (hundreds of years at 48 kHz).
func sampleOffset(samples int64, sampleRate int) time.Duration {
	if sampleRate <= 0 {
		return 0
	}
	r := int64(sampleRate)
	sec := samples / r
	rem := samples % r
	return time.Duration(sec)*time.Second + time.Duration(rem)*time.Second/time.Duration(r)
}

var _ audiocore.AudioConsumer = (*SpectrogramConsumer)(nil)
