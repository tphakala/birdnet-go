package transcode

import (
	"context"
	"encoding/binary"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/go-audio-dsp/denoiser"
)

const (
	// referenceOddFrames is added to the multi-chunk clips so their length is not a
	// multiple of the denoiser hop.
	referenceOddFrames = 777
	// referenceShortFrames is shorter than one denoiser frame.
	referenceShortFrames = 100
	// referenceBatRate is the sample rate of the bat path (bench_test.go benchBatRate).
	referenceBatRate = 256000
	// referenceCDRate is a common rate that is not a multiple of the test rate.
	referenceCDRate = 44100
	// referenceBoundSeconds is the length of the clips at the sample rate bounds.
	referenceBoundSeconds = 0.5
)

// denoisePCMWholeChannel is the pre-streaming implementation of denoisePCM,
// frozen as the reference the streaming version must match byte for byte. It
// holds one whole channel as float32. Do not edit it: changing it changes what
// the equivalence test pins.
func denoisePCMWholeChannel(ctx context.Context, p *pcmBuffer, preset string) error {
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
			return denoiseError("denoise_init", err)
		}
		if need := max(d.MaxOutputLen(chunkFrames), d.FrameSize()); len(scratch) < need {
			scratch = make([]float32, need)
		}

		for i := range frames {
			off := (i*p.Channels + ch) * bytesPerSample
			x[i] = float32(int16(binary.LittleEndian.Uint16(p.Data[off:]))) / pcm16Scale //nolint:gosec // G115: intentional uint16 to int16 reinterpretation of PCM
		}

		if prof, perr := d.EstimateNoiseProfile(x); perr == nil {
			if err := d.SetNoiseProfile(prof); err != nil {
				return denoiseError("denoise_profile", err)
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

// interleave merges equal-length channels into one interleaved sample slice.
func interleave(channels ...[]int16) []int16 {
	out := make([]int16, len(channels)*len(channels[0]))
	for ch, samples := range channels {
		for i, s := range samples {
			out[i*len(channels)+ch] = s
		}
	}
	return out
}

// firstDifference returns the index of the first differing byte pair, or -1.
func firstDifference(a, b []byte) int {
	for i := 0; i+1 < min(len(a), len(b)); i += bytesPerSample {
		if a[i] != b[i] || a[i+1] != b[i+1] {
			return i / bytesPerSample
		}
	}
	return -1
}

// The streaming denoise must reproduce the frozen whole-channel implementation
// exactly: users compare clips, and chunk boundaries can change the output by one
// least significant bit.
func TestDenoisePCM_MatchesWholeChannelReference(t *testing.T) {
	t.Parallel()

	// One noisy clip, whose length is not a multiple of the chunk size, is sliced
	// into the cases; the sample rate only configures the denoiser.
	clip := noisyTone(testRate)
	longMono := append(slices.Clone(clip), clip[:referenceOddFrames]...)
	stereoSamples := interleave(clip, tone(testRate, 1, noiseOnlySeconds+noiseToneSeconds, testToneHz, testAmp))

	type testCase struct {
		name     string
		samples  []int16
		rate     int
		channels int
	}
	cases := []testCase{
		{name: "mono_48k", samples: longMono, rate: testRate, channels: 1},
		{name: "mono_44k", samples: longMono, rate: referenceCDRate, channels: 1},
		{name: "pure_tone_no_quiet_region", samples: tone(testRate, 1, 1, testToneHz, testAmp), rate: testRate, channels: 1},
		{name: "shorter_than_one_frame", samples: tone(testRate, 1, float64(referenceShortFrames)/testRate, testToneHz, testAmp), rate: testRate, channels: 1},
		{name: "exactly_one_chunk", samples: longMono[:chunkFrames], rate: testRate, channels: 1},
		{name: "one_chunk_plus_one_frame", samples: longMono[:chunkFrames+1], rate: testRate, channels: 1},
		{name: "min_source_rate", samples: tone(minSourceSampleRate, 1, referenceBoundSeconds, testToneHz, testAmp), rate: minSourceSampleRate, channels: 1},
		{name: "bat_rate", samples: tone(referenceBatRate, 1, referenceBoundSeconds, testToneHz, testAmp), rate: referenceBatRate, channels: 1},
		{name: "stereo_48k", samples: stereoSamples, rate: testRate, channels: 2},
	}

	for _, tc := range cases {
		for _, preset := range []string{"light", "medium", "heavy"} {
			t.Run(tc.name+"_"+preset, func(t *testing.T) {
				t.Parallel()

				got := newPCM(tc.samples, tc.rate, tc.channels)
				want := newPCM(tc.samples, tc.rate, tc.channels)
				require.NoError(t, denoisePCM(t.Context(), got, preset))
				require.NoError(t, denoisePCMWholeChannel(t.Context(), want, preset))

				require.Len(t, got.Data, len(want.Data))
				if i := firstDifference(got.Data, want.Data); i >= 0 {
					assert.Failf(t, "streaming denoise diverges from the whole-channel reference", "first differing sample index %d", i)
				}
			})
		}
	}
}
