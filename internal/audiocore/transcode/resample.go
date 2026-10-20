package transcode

import (
	"context"
	"fmt"

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
func resampleInterleaved(ctx context.Context, p *pcmBuffer, toRate, maxBytes int) (*pcmBuffer, error) {
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
	// Leave room in front of the samples so a WAV can be built in place.
	room := make([]byte, wavHeaderBytes+capFrames*frameBytes)
	out := room[wavHeaderBytes:]

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
			return nil, wrapError(err, errors.CategoryAudio, "resample_init")
		}
		resamplers[ch] = r
	}

	var mono []byte // per-channel input scratch, only needed to deinterleave
	if p.Channels > 1 {
		mono = make([]byte, chunkFrames*bytesPerSample)
	}
	written := 0 // output frames produced so far; every channel produces the same count
	for off := 0; off < frames; off += chunkFrames {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := min(chunkFrames, frames-off)
		got := -1
		for ch, r := range resamplers {
			in := p.Data[off*frameBytes : (off+n)*frameBytes]
			if p.Channels > 1 {
				for i := range n {
					src := (i*p.Channels + ch) * bytesPerSample
					copy(mono[i*bytesPerSample:], in[src:src+bytesPerSample])
				}
				in = mono[:n*bytesPerSample]
			}
			res, err := r.ResampleInto(in)
			if err != nil {
				return nil, wrapError(err, errors.CategoryAudio, "resample_process")
			}
			chGot := len(res) / bytesPerSample
			if got >= 0 && chGot != got {
				return nil, wrapError(fmt.Errorf("channel %d produced %d frames, channel 0 produced %d", ch, chGot, got), errors.CategoryAudio, "resample_length")
			}
			got = chGot
			if written+got > capFrames {
				return nil, wrapError(fmt.Errorf("resampler produced %d frames, more than the %d expected", written+got, capFrames), errors.CategoryAudio, "resample_length")
			}
			if p.Channels == 1 {
				copy(out[written*bytesPerSample:], res[:got*bytesPerSample])
				continue
			}
			for i := range got {
				dst := ((written+i)*p.Channels + ch) * bytesPerSample
				copy(out[dst:dst+bytesPerSample], res[i*bytesPerSample:(i+1)*bytesPerSample])
			}
		}
		written += max(got, 0)
	}

	return &pcmBuffer{Data: out[:written*frameBytes], SampleRate: toRate, Channels: p.Channels, wavRoom: room}, nil
}
