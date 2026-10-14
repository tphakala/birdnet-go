package transcode

import (
	"context"
	"encoding/binary"
	"math"

	"github.com/tphakala/go-audio-dsp/denoiser"

	"github.com/tphakala/birdnet-go/internal/audiocore/audionorm"
	"github.com/tphakala/birdnet-go/internal/audiocore/pcmgain"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// pcm16Scale converts between int16 samples and the [-1, 1) floats the denoiser
// works in.
const pcm16Scale = 32768.0

// denoiseStrengths maps the request presets to denoiser strengths. The mapping is
// explicit and never defaults: the denoiser's Strength zero value is Medium, so
// an unknown preset must be rejected here rather than reach it (see Config in
// the denoiser package). The maximum
// reductions of Light, Medium and Heavy (6, 12 and 20 dB) equal the afftdn "nr"
// values the presets used to select.
var denoiseStrengths = map[string]denoiser.Strength{
	"light":  denoiser.Light,
	"medium": denoiser.Medium,
	"heavy":  denoiser.Heavy,
}

// Filters selects the optional processing steps. They run in a fixed order:
// denoise, then loudness normalization measured on the denoised samples, then
// the user gain.
type Filters struct {
	// Denoise is a preset name: "", "light", "medium" or "heavy". "" runs no
	// denoiser.
	Denoise string
	// Normalize enables loudness normalization to the package target.
	Normalize bool
	// GainDB is the volume adjustment in dB (0 = no change).
	GainDB float64
}

// Active reports whether any processing step is enabled.
func (f Filters) Active() bool {
	return f.Denoise != "" || f.Normalize || f.GainDB != 0
}

// ApplyFilters runs the enabled filters over p in place (p.Data may be replaced).
func ApplyFilters(ctx context.Context, p *PCM, f Filters) error {
	if f.Denoise != "" {
		if err := denoisePCM(ctx, p, f.Denoise); err != nil {
			return err
		}
	}

	totalGain := f.GainDB
	if f.Normalize {
		if err := ctx.Err(); err != nil {
			return err
		}
		plan, err := audionorm.PlanGainWithGateFallbackInt16Bytes(p.Data, audionorm.Options{
			SampleRate:   p.SampleRate,
			Channels:     p.Channels,
			TargetLUFS:   normalizeTargetLUFS,
			TruePeakDBTP: normalizeTruePeakDBTP,
		}, normalizeMaxGainDB)
		if err != nil {
			return errors.New(err).
				Component(componentName).
				Category(errors.CategoryAudio).
				Context("operation", "normalize_measure").
				Build()
		}
		totalGain += plan.GainDB
	}

	if totalGain != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		// One quantization for normalization and user gain together. Applied
		// returns a copy: ApplyInt16 forbids overlapping src and dst.
		p.Data = pcmgain.Applied(p.Data, totalGain)
	}
	return nil
}

// denoisePCM denoises p channel by channel. Each channel is copied into one
// reusable float32 buffer, so the working set is the PCM plus one channel of
// floats, and the result is written back into p.Data in place.
func denoisePCM(ctx context.Context, p *PCM, preset string) error {
	strength, ok := denoiseStrengths[preset]
	if !ok {
		return validationError(ErrInvalidRequest, "denoise_preset", "unknown denoise preset %q", preset)
	}
	frames := p.Frames()
	if frames == 0 {
		return nil
	}

	x := make([]float32, frames)
	// ProcessInto needs MaxOutputLen(chunk) samples; FlushInto needs FrameSize().
	var scratch []float32

	for ch := range p.Channels {
		d, err := denoiser.New(denoiser.Config{SampleRate: p.SampleRate, Strength: strength})
		if err != nil {
			return errors.New(err).
				Component(componentName).
				Category(errors.CategoryAudio).
				Context("operation", "denoise_init").
				Context("sample_rate", p.SampleRate).
				Build()
		}
		if need := max(d.MaxOutputLen(chunkFrames), d.FrameSize()); len(scratch) < need {
			scratch = make([]float32, need)
		}

		for i := range frames {
			off := (i*p.Channels + ch) * bytesPerSample
			x[i] = float32(int16(binary.LittleEndian.Uint16(p.Data[off:]))) / pcm16Scale //nolint:gosec // G115: intentional uint16 to int16 reinterpretation of PCM
		}

		// Mirror denoiser.Denoise: learn the noise floor from the quietest window
		// when the clip has one, otherwise let the adaptive tracker converge. A
		// failed estimate is not an error.
		if prof, perr := d.EstimateNoiseProfile(x); perr == nil {
			if err := d.SetNoiseProfile(prof); err != nil {
				return errors.New(err).
					Component(componentName).
					Category(errors.CategoryAudio).
					Context("operation", "denoise_profile").
					Build()
			}
		}

		written := 0
		for off := 0; off < frames; off += chunkFrames {
			if err := ctx.Err(); err != nil {
				return err
			}
			end := min(off+chunkFrames, frames)
			n, err := d.ProcessInto(x[off:end], scratch[:d.MaxOutputLen(end-off)])
			if err != nil {
				return denoiseError("denoise_process", err)
			}
			if written+n > frames {
				return denoiseLengthError(written+n, frames)
			}
			writeChannel(p, ch, written, scratch[:n])
			written += n
		}
		n, err := d.FlushInto(scratch[:d.FrameSize()])
		if err != nil {
			return denoiseError("denoise_flush", err)
		}
		if written+n != frames {
			return denoiseLengthError(written+n, frames)
		}
		writeChannel(p, ch, written, scratch[:n])
	}
	return nil
}

// denoiseLengthError reports a denoiser that broke its contract of returning
// exactly as many samples as it was given.
func denoiseLengthError(got, want int) error {
	return errors.Newf("denoiser returned %d samples for %d input samples", got, want).
		Component(componentName).
		Category(errors.CategoryAudio).
		Context("operation", "denoise_length").
		Build()
}

// denoiseError wraps a denoiser failure.
func denoiseError(operation string, err error) error {
	return errors.New(err).
		Component(componentName).
		Category(errors.CategoryAudio).
		Context("operation", operation).
		Build()
}

// writeChannel stores samples into channel ch of p starting at frame index start,
// rounding and saturating to int16.
func writeChannel(p *PCM, ch, start int, samples []float32) {
	for i, s := range samples {
		off := ((start+i)*p.Channels + ch) * bytesPerSample
		binary.LittleEndian.PutUint16(p.Data[off:], uint16(floatToInt16(s))) //nolint:gosec // G115: intentional int16 to uint16 reinterpretation of PCM
	}
}

// floatToInt16 converts a normalized sample to int16 with rounding and
// saturation. NaN maps to silence.
func floatToInt16(s float32) int16 {
	v := math.Round(float64(s) * pcm16Scale)
	switch {
	case math.IsNaN(v):
		return 0
	case v > math.MaxInt16:
		return math.MaxInt16
	case v < math.MinInt16:
		return math.MinInt16
	}
	return int16(v)
}
