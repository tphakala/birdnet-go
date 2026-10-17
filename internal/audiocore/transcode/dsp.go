package transcode

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/tphakala/go-audio-dsp/denoiser"
	"github.com/tphakala/go-audio-dsp/gain"
	"github.com/tphakala/go-audio-dsp/loudnorm"

	"github.com/tphakala/birdnet-go/internal/audiocore/loudness"
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

// applyFilters runs the enabled filters over p in place; p.Data is modified.
func applyFilters(ctx context.Context, p *pcmBuffer, f Filters) error {
	if f.Denoise != "" {
		if err := denoisePCM(ctx, p, f.Denoise); err != nil {
			return err
		}
	}

	totalGain := f.GainDB
	if f.Normalize {
		plan, err := loudnorm.PlanBytes(ctx, p.Data,
			loudness.ClipPlanOptions(p.SampleRate, p.Channels, normalizeTargetLUFS, normalizeTruePeakDBTP))
		if err != nil {
			// A cancelled request is reported as cancellation, not as a failed measurement.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return wrapError(err, errors.CategoryAudio, "normalize_measure")
		}
		totalGain += plan.GainDB
	}

	if totalGain != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		g, err := gain.New(totalGain)
		if err != nil {
			return wrapError(err, errors.CategoryValidation, "gain_init")
		}
		// One quantization for normalization and user gain together, applied in
		// place: p.Data is owned by this request, so it may be overwritten.
		if err := g.ApplyBytes(p.Data); err != nil {
			return wrapError(err, errors.CategoryAudio, "apply_gain")
		}
	}
	return nil
}

// denoisePCM denoises p channel by channel and writes the result back into
// p.Data in place.
//
// Each channel takes two passes over one reusable chunk of float32 samples, so
// the working set is the PCM plus one chunk of floats and the profile
// estimator's bounded window, not a whole channel of floats. Pass 1 feeds the
// chunks to the noise profile estimator; pass 2 denoises them. Pass 2 runs
// ProcessInto at exactly chunkFrames boundaries: chunked and one-shot denoising
// can differ by one least significant bit, so the boundaries are part of the
// output.
func denoisePCM(ctx context.Context, p *pcmBuffer, preset string) error {
	strength, ok := denoiseStrengths[preset]
	if !ok {
		return validationError(ErrInvalidRequest, "denoise_preset", "unknown denoise preset %q", preset)
	}
	frames := p.Frames()
	if frames == 0 {
		return nil
	}

	x := make([]float32, min(frames, chunkFrames))
	// ProcessInto needs MaxOutputLen(chunk) samples; FlushInto needs FrameSize().
	var scratch []float32

	for ch := range p.Channels {
		d, err := denoiser.New(denoiser.Config{SampleRate: p.SampleRate, Strength: strength})
		if err != nil {
			return denoiseError("denoise_init", err)
		}
		if need := max(d.MaxOutputLen(chunkFrames), d.FrameSize()); len(scratch) < need {
			scratch = make([]float32, need)
		}

		// Pass 1, mirroring denoiser.Denoise: learn the noise floor from the
		// quietest window when the clip has one, otherwise let the adaptive
		// tracker converge. A failed estimate is not an error.
		est := d.NewNoiseProfileEstimator()
		for off := 0; off < frames; off += chunkFrames {
			if err := ctx.Err(); err != nil {
				return err
			}
			end := min(off+chunkFrames, frames)
			readChannel(p, ch, off, x[:end-off])
			est.AddFloat32(x[:end-off])
		}
		if prof, perr := est.Profile(); perr == nil {
			if err := d.SetNoiseProfile(prof); err != nil {
				return denoiseError("denoise_profile", err)
			}
		}

		// Pass 2.
		written := 0
		for off := 0; off < frames; off += chunkFrames {
			if err := ctx.Err(); err != nil {
				return err
			}
			end := min(off+chunkFrames, frames)
			// The chunk is converted to float before anything is written back.
			readChannel(p, ch, off, x[:end-off])
			n, err := d.ProcessInto(x[:end-off], scratch[:d.MaxOutputLen(end-off)])
			if err != nil {
				return denoiseError("denoise_process", err)
			}
			// Output never exceeds the input consumed so far, so the write-back
			// stays below the next chunk's unread samples; this guard enforces
			// that rather than assuming it.
			if written+n > end {
				return denoiseLengthError(written+n, end)
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
	return denoiseError("denoise_length", fmt.Errorf("denoiser returned %d samples for %d input samples", got, want))
}

// denoiseError wraps a denoiser failure.
func denoiseError(operation string, err error) error {
	return wrapError(err, errors.CategoryAudio, operation)
}

// readChannel fills dst with the samples of channel ch of p starting at frame
// index start, as floats in [-1, 1).
func readChannel(p *pcmBuffer, ch, start int, dst []float32) {
	for i := range dst {
		off := ((start+i)*p.Channels + ch) * bytesPerSample
		dst[i] = float32(int16(binary.LittleEndian.Uint16(p.Data[off:]))) / pcm16Scale //nolint:gosec // G115: intentional uint16 to int16 reinterpretation of PCM
	}
}

// writeChannel stores samples into channel ch of p starting at frame index start,
// rounding and saturating to int16.
func writeChannel(p *pcmBuffer, ch, start int, samples []float32) {
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
