package transcode

import (
	"context"
	"encoding/binary"

	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// resampleSlackFrames is extra output capacity, in frames, for the few frames by
// which a resampler's output can differ from the exact rate ratio.
const resampleSlackFrames = 4096

// resampleInterleaved converts p to toRate. The wrapper resampler is mono and
// keeps filter state across calls, so each channel gets its own and the output is
// interleaved from the per-channel results. The output size is computed and
// checked against maxBytes before it is allocated. Up to the resampler's latency
// of tail samples is dropped, as on the clip export path.
//
// When the rates are equal p is returned unchanged: resample.NewResampler
// returns a nil resampler for equal rates.
func resampleInterleaved(ctx context.Context, p *PCM, toRate, maxBytes int) (*PCM, error) {
	if p.SampleRate == toRate {
		return p, nil
	}
	frames := p.Frames()
	frameBytes := p.Channels * bytesPerSample
	expected := int64(frames) * int64(toRate) / int64(p.SampleRate)
	if expected*int64(frameBytes) > int64(maxBytes) {
		return nil, wrapSentinel(ErrAudioTooLong, nil, "resample_size", "resampled audio exceeds the size limit", errors.CategoryLimit)
	}
	capFrames := int(expected) + resampleSlackFrames
	out := make([]byte, capFrames*frameBytes)

	resamplers := make([]*resample.Resampler, p.Channels)
	defer func() {
		for _, r := range resamplers {
			if r != nil {
				_ = r.Close()
			}
		}
	}()
	for ch := range resamplers {
		r, err := resample.NewResampler(p.SampleRate, toRate)
		if err != nil {
			return nil, errors.New(err).
				Component(componentName).
				Category(errors.CategoryAudio).
				Context("operation", "resample_init").
				Build()
		}
		resamplers[ch] = r
	}

	pos := make([]int, p.Channels) // output frames written per channel
	mono := make([]byte, chunkFrames*bytesPerSample)
	for off := 0; off < frames; off += chunkFrames {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := min(chunkFrames, frames-off)
		for ch, r := range resamplers {
			for i := range n {
				src := ((off+i)*p.Channels + ch) * bytesPerSample
				copy(mono[i*bytesPerSample:], p.Data[src:src+bytesPerSample])
			}
			res, err := r.ResampleInto(mono[:n*bytesPerSample])
			if err != nil {
				return nil, errors.New(err).
					Component(componentName).
					Category(errors.CategoryAudio).
					Context("operation", "resample_process").
					Build()
			}
			got := len(res) / bytesPerSample
			if pos[ch]+got > capFrames {
				return nil, errors.Newf("resampler produced %d frames, more than the %d expected", pos[ch]+got, capFrames).
					Component(componentName).
					Category(errors.CategoryAudio).
					Context("operation", "resample_length").
					Build()
			}
			for i := range got {
				dst := ((pos[ch]+i)*p.Channels + ch) * bytesPerSample
				binary.LittleEndian.PutUint16(out[dst:], binary.LittleEndian.Uint16(res[i*bytesPerSample:]))
			}
			pos[ch] += got
		}
	}

	outFrames := pos[0]
	for _, n := range pos[1:] {
		outFrames = min(outFrames, n)
	}
	return &PCM{Data: out[:outFrames*frameBytes], SampleRate: toRate, Channels: p.Channels}, nil
}
