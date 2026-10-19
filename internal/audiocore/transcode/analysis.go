package transcode

import (
	"context"
	"fmt"

	"github.com/tphakala/go-audio-dsp/pcm"
	audioresampler "github.com/tphakala/go-audio-resampler"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// MonoSource is decoded audio prepared for spectral analysis: the decoded
// samples are downmixed to mono and, when a target rate differs from the source
// rate, resampled while streaming. It holds the decoded 16-bit samples and
// converts them to float32 one chunk at a time, so the analysis never holds a
// float copy of the whole clip.
//
// Stream is not safe for concurrent use on one MonoSource.
type MonoSource struct {
	// data holds whole frames of interleaved 16-bit little-endian samples.
	data     []byte
	srcRate  int
	channels int
	dstRate  int
}

// DecodeMono decodes the whole file at path for analysis. Mono and stereo
// sources are accepted; stereo is averaged to mono. toRate is the rate Stream
// delivers; a value of zero or less keeps the source rate. The decoded size is
// bounded like every other decode in this package. A file with no audio wraps
// ErrUndecodableSource. A WAV or FLAC that delivers fewer frames than its
// header declares wraps ErrTruncatedSource (and so ErrUndecodableSource), since
// a spectrogram of the part that exists would be a short picture of a clip that
// is cut or still being written. Formats without an exact length (MP3, M4A,
// Opus) are not checked.
func DecodeMono(ctx context.Context, path string, toRate int) (*MonoSource, error) {
	buf, err := decodeChannels(ctx, path, nil, maxDecodedPCMBytes, 0, maxAnalysisChannels)
	if err != nil {
		return nil, err
	}
	if buf.declaredFrames >= 0 && int64(buf.Frames()) < buf.declaredFrames {
		return nil, truncatedError(nil, "decode_mono_length", fmt.Sprintf("read %d of %d declared frames", buf.Frames(), buf.declaredFrames))
	}
	return newMonoSource(buf.Data, buf.SampleRate, buf.Channels, toRate)
}

// MonoFromPCM16 wraps mono 16-bit little-endian PCM captured at fromRate,
// without copying it, as a MonoSource delivering toRate (zero or less keeps
// fromRate). A trailing odd byte is dropped, as the file decoders drop a partial
// frame. Empty data wraps ErrEmptyRange; a rate outside the supported bounds
// wraps ErrUnsupportedSource. The caller must not modify data while the source
// is in use.
func MonoFromPCM16(data []byte, fromRate, toRate int) (*MonoSource, error) {
	data = data[:len(data)-len(data)%bytesPerSample]
	if len(data) == 0 {
		return nil, validationError(ErrEmptyRange, "mono_from_pcm", "no audio samples")
	}
	return newMonoSource(data, fromRate, 1, toRate)
}

// newMonoSource validates the rates and builds the source.
func newMonoSource(data []byte, srcRate, channels, toRate int) (*MonoSource, error) {
	if toRate <= 0 {
		toRate = srcRate
	}
	for _, rate := range [...]int{srcRate, toRate} {
		if rate < minSourceSampleRate || rate > maxSourceSampleRate {
			return nil, validationError(ErrUnsupportedSource, "mono_source_bounds", "sample rate %d Hz outside %d..%d", rate, minSourceSampleRate, maxSourceSampleRate)
		}
	}
	return &MonoSource{data: data, srcRate: srcRate, channels: channels, dstRate: toRate}, nil
}

// WithRate returns a source over the same decoded samples that delivers toRate,
// resampling while streaming as DecodeMono does; zero or less keeps the source
// rate. The samples are shared, not copied, and m is unchanged. A rate outside
// the supported bounds wraps ErrUnsupportedSource.
func (m *MonoSource) WithRate(toRate int) (*MonoSource, error) {
	return newMonoSource(m.data, m.srcRate, m.channels, toRate)
}

// SampleRate returns the rate, in Hz, of the samples Stream delivers.
func (m *MonoSource) SampleRate() int { return m.dstRate }

// SpanSamples returns the clip length in samples at SampleRate, rounded to the
// nearest sample. A clip shorter than half the ratio of the two rates, such as
// one frame resampled down by more than two, spans zero samples.
func (m *MonoSource) SpanSamples() int64 {
	frames := int64(len(m.data) / (bytesPerSample * m.channels))
	return (frames*int64(m.dstRate) + int64(m.srcRate)/2) / int64(m.srcRate)
}

// Stream delivers the clip as mono float32 samples at SampleRate in successive
// chunks, scaled to +-1.0 (the int16 value divided by 32768). fn must not keep
// or modify the slice after it returns. Each call resamples with a fresh
// resampler, so repeated calls deliver identical samples. Stream returns the
// first error from fn, and the context error unchanged when ctx is done.
func (m *MonoSource) Stream(ctx context.Context, fn func([]float32) error) error {
	frameBytes := m.channels * bytesPerSample
	frames := len(m.data) / frameBytes

	var rs *audioresampler.SimpleResamplerFloat32
	var out []float32
	if m.dstRate != m.srcRate {
		var err error
		rs, err = audioresampler.NewEngineFloat32(float64(m.srcRate), float64(m.dstRate), audioresampler.QualityHigh)
		if err != nil {
			return wrapError(err, errors.CategoryAudio, "mono_resample_init")
		}
		out = make([]float32, rs.EstimateOutput(min(chunkFrames, frames)))
	}

	mono := make([]float32, min(chunkFrames, frames))
	for off := 0; off < frames; off += chunkFrames {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(chunkFrames, frames-off)
		got, err := pcm.DownmixBytesToFloat32(mono[:n], m.data[off*frameBytes:(off+n)*frameBytes], m.channels)
		if err != nil {
			return wrapError(err, errors.CategoryAudio, "mono_downmix")
		}
		block := mono[:got]
		if rs != nil {
			k, err := rs.ProcessInto(block, out)
			if err != nil {
				return wrapError(err, errors.CategoryAudio, "mono_resample")
			}
			block = out[:k]
		}
		if len(block) == 0 {
			continue
		}
		if err := fn(block); err != nil {
			return err
		}
	}
	if rs == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tail, err := rs.Flush()
	if err != nil {
		return wrapError(err, errors.CategoryAudio, "mono_resample_flush")
	}
	if len(tail) > 0 {
		return fn(tail)
	}
	return nil
}
