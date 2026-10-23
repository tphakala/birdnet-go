package transcode

import (
	"context"
	"fmt"
	"os"

	"github.com/tphakala/go-audio-dsp/pcm"
	audioresampler "github.com/tphakala/go-audio-resampler"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// minStreamFrames is the shortest declared length OpenMono streams. A clip
// shorter than one chunk gains nothing from streaming, and keeping it buffered
// leaves the errors of a tiny clip (no audio, or a span that rounds to zero after
// resampling) as DecodeMono reports them.
const minStreamFrames = chunkFrames

// analysisResampleQuality is the resampler preset for analysis audio. Medium
// keeps High's passband (the engine passband end fraction is 0.91 against 0.912,
// go-audio-resampler v1.9.0 internal/engine/filter_params.go:73-74)
// with a 102 dB design stopband instead of 126 dB (filter_params.go:66-67), at a
// lower cost per input sample.
const analysisResampleQuality = audioresampler.QualityMedium

// MonoSource is decoded audio prepared for spectral analysis: the decoded
// samples are downmixed to mono and, when a target rate differs from the source
// rate, resampled while streaming. It is backed either by the decoded 16-bit
// samples held in memory (DecodeMono, MonoFromPCM16) or by the file itself
// (OpenMono), which Stream decodes as it delivers. Either way the samples are
// converted to float32 one chunk at a time, so the analysis never holds a float
// copy of the whole clip.
//
// Stream is not safe for concurrent use on one MonoSource.
type MonoSource struct {
	// data holds whole frames of interleaved 16-bit little-endian samples of a
	// buffered source. It is nil for a file-backed one.
	data []byte
	// path names the file of a file-backed source, and is empty for a buffered
	// one. frames is the frame count the header declared when the file was
	// opened, and exact is whether that count bounds the audio (see
	// source.exactLength).
	path   string
	frames int64
	exact  bool

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
// Opus) are not checked. The result holds the decoded clip in memory; OpenMono
// avoids that for a source it can stream.
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

// OpenMono prepares the file at path for analysis like DecodeMono, without
// holding the decoded audio. It reads the header now and decodes the audio on
// each Stream call, which reopens the file, so memory does not depend on the clip
// length. Errors that only a full decode finds (a WAV or FLAC cut short, a file
// with no audio) come from Stream, not from OpenMono. A source whose header
// declares no length, fewer frames than one chunk, or more frames than the
// decoded-size bound goes through DecodeMono instead, so it is decoded into
// memory and DecodeMono's errors come from OpenMono. Stream on a file-backed
// source can wrap ErrShorterThanDeclared; see Stream. toRate is as for
// DecodeMono.
func OpenMono(ctx context.Context, path string, toRate int) (*MonoSource, error) {
	return openMono(ctx, path, toRate, maxDecodedPCMBytes)
}

// openMono is OpenMono with the most decoded bytes a streamed clip may declare.
// OpenMono passes maxDecodedPCMBytes.
func openMono(ctx context.Context, path string, toRate, maxBytes int) (*MonoSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hdr, err := readMonoHeader(path)
	if err != nil {
		return nil, err
	}
	if hdr.frames < minStreamFrames || hdr.frames > int64(maxBytes/(hdr.channels*bytesPerSample)) {
		return DecodeMono(ctx, path, toRate)
	}
	dstRate, err := checkMonoRates(hdr.sampleRate, toRate)
	if err != nil {
		return nil, err
	}
	return &MonoSource{path: path, frames: hdr.frames, exact: hdr.exact, srcRate: hdr.sampleRate, channels: hdr.channels, dstRate: dstRate}, nil
}

// monoHeader is what the header of a clip declares.
type monoHeader struct {
	sampleRate int
	channels   int
	// frames is the declared length, or unknownFrames.
	frames int64
	exact  bool
}

// openMonoSource opens path and its decoder with the analysis channel bound. The
// caller closes the file.
func openMonoSource(path string) (*os.File, *source, error) {
	f, _, err := openSourceFile(path)
	if err != nil {
		return nil, nil, err
	}
	src, err := openSourceChannels(f, path, maxAnalysisChannels)
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, src, nil
}

// readMonoHeader reads the header of the clip at path and closes the file.
func readMonoHeader(path string) (monoHeader, error) {
	f, src, err := openMonoSource(path)
	if err != nil {
		return monoHeader{}, err
	}
	_ = f.Close()
	return monoHeader{sampleRate: src.sampleRate, channels: src.channels, frames: src.totalFrames, exact: src.exactLength}, nil
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

// checkMonoRates returns the delivered rate for a target (the source rate when
// toRate is zero or less), or an error when either rate is outside the supported
// bounds.
func checkMonoRates(srcRate, toRate int) (int, error) {
	if toRate <= 0 {
		toRate = srcRate
	}
	for _, rate := range [...]int{srcRate, toRate} {
		if rate < minSourceSampleRate || rate > maxSourceSampleRate {
			return 0, validationError(ErrUnsupportedSource, "mono_source_bounds", "sample rate %d Hz outside %d..%d", rate, minSourceSampleRate, maxSourceSampleRate)
		}
	}
	return toRate, nil
}

// newMonoSource validates the rates and builds a buffered source.
func newMonoSource(data []byte, srcRate, channels, toRate int) (*MonoSource, error) {
	dstRate, err := checkMonoRates(srcRate, toRate)
	if err != nil {
		return nil, err
	}
	return &MonoSource{data: data, srcRate: srcRate, channels: channels, dstRate: dstRate}, nil
}

// WithRate returns a source over the same decoded samples, or the same file,
// that delivers toRate, resampling while streaming as DecodeMono does; zero or
// less keeps the source rate. Decoded samples are shared, not copied, and m is
// unchanged. A rate outside the supported bounds wraps ErrUnsupportedSource.
func (m *MonoSource) WithRate(toRate int) (*MonoSource, error) {
	dstRate, err := checkMonoRates(m.srcRate, toRate)
	if err != nil {
		return nil, err
	}
	rated := *m
	rated.dstRate = dstRate
	return &rated, nil
}

// SampleRate returns the rate, in Hz, of the samples Stream delivers.
func (m *MonoSource) SampleRate() int { return m.dstRate }

// Streamed reports whether the source decodes its file during Stream (OpenMono)
// instead of holding the decoded samples.
func (m *MonoSource) Streamed() bool { return m.path != "" }

// SpanSamples returns the clip length in samples at SampleRate, rounded to the
// nearest sample. A clip shorter than half the ratio of the two rates, such as
// one frame resampled down by more than two, spans zero samples. For a
// file-backed source the length is the one the header declared.
func (m *MonoSource) SpanSamples() int64 {
	frames := m.frames
	if !m.Streamed() {
		frames = int64(len(m.data) / (bytesPerSample * m.channels))
	}
	return (frames*int64(m.dstRate) + int64(m.srcRate)/2) / int64(m.srcRate)
}

// Stream delivers the clip as mono float32 samples at SampleRate in successive
// chunks, scaled to +-1.0 (the int16 value divided by 32768). fn must not keep
// or modify the slice after it returns. Each call resamples with a fresh
// resampler, so repeated calls deliver identical samples. Stream returns the
// first error from fn, and the context error unchanged when ctx is done.
//
// A file-backed source reopens and decodes its file on every call, so repeated
// calls deliver identical samples while the file is unchanged. A file whose
// header differs from the one OpenMono read wraps ErrUndecodableSource. A WAV or
// FLAC that ends before its declared length wraps ErrTruncatedSource, and a file
// with no audio wraps ErrUndecodableSource. An M4A, MP3 or Opus file that
// delivers fewer frames than declared returns an error wrapping
// ErrShorterThanDeclared, since SpanSamples was then too long.
func (m *MonoSource) Stream(ctx context.Context, fn func([]float32) error) error {
	if m.Streamed() {
		return m.streamFile(ctx, fn)
	}
	frameBytes := m.channels * bytesPerSample
	frames := len(m.data) / frameBytes
	pump, err := newMonoPump(m.srcRate, m.dstRate, m.channels, min(chunkFrames, frames))
	if err != nil {
		return err
	}
	for off := 0; off < frames; off += chunkFrames {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(chunkFrames, frames-off)
		if err := pump.push(m.data[off*frameBytes:(off+n)*frameBytes], fn); err != nil {
			return err
		}
	}
	return pump.finish(ctx, fn)
}

// streamFile is Stream for a file-backed source. It reads whole chunks of
// chunkFrames frames, the cut the buffered path makes, so the resampler sees the
// same chunk boundaries in both modes.
func (m *MonoSource) streamFile(ctx context.Context, fn func([]float32) error) error {
	f, src, err := openMonoSource(m.path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if src.sampleRate != m.srcRate || src.channels != m.channels || src.totalFrames != m.frames || src.exactLength != m.exact {
		return wrapSentinel(ErrUndecodableSource, nil, "mono_stream_reopen", "source changed since it was opened", errors.CategoryAudio)
	}
	frameBytes := m.channels * bytesPerSample
	if err := positionSource(ctx, src, 0, frameBytes, false); err != nil {
		return err
	}
	chunk := int(min(chunkFrames, m.frames))
	pump, err := newMonoPump(m.srcRate, m.dstRate, m.channels, chunk)
	if err != nil {
		return err
	}

	buf := make([]byte, chunk*frameBytes)
	fr := &frameReader{r: src.reader, frameBytes: frameBytes, op: "mono_stream_read"}
	total := m.frames * int64(frameBytes)
	var delivered int64
	for delivered < total {
		n, eof, err := fr.fill(ctx, buf[:int(min(int64(len(buf)), total-delivered))])
		if err != nil {
			return err
		}
		if n > 0 {
			if err := pump.push(buf[:n], fn); err != nil {
				return err
			}
			delivered += int64(n)
		}
		if eof {
			break
		}
	}

	// Same order as the buffered path: no audio at all (decodeSource) before a
	// short count (DecodeMono).
	got := delivered / int64(frameBytes)
	switch {
	case got == 0:
		return wrapSentinel(ErrUndecodableSource, nil, "mono_stream_read", "source holds no audio", errors.CategoryAudio)
	case got < m.frames && m.exact:
		return truncatedError(nil, "mono_stream_length", fmt.Sprintf("read %d of %d declared frames", got, m.frames))
	case got < m.frames:
		// Not built with the errors builder: the caller redoes the decode, so this
		// must not reach telemetry.
		return fmt.Errorf("%w: read %d of %d declared frames", ErrShorterThanDeclared, got, m.frames)
	}
	return pump.finish(ctx, fn)
}

// monoPump turns chunks of interleaved 16-bit PCM into mono float32 at the
// delivered rate. Both backings of MonoSource feed it, so they share one
// downmix and resample path.
type monoPump struct {
	channels int
	// rs is nil when the delivered rate equals the source rate.
	rs   *audioresampler.SimpleResamplerFloat32
	mono []float32
	out  []float32
}

// newMonoPump builds a pump for chunks of at most maxFrames frames.
func newMonoPump(srcRate, dstRate, channels, maxFrames int) (*monoPump, error) {
	p := &monoPump{channels: channels, mono: make([]float32, maxFrames)}
	if dstRate != srcRate {
		rs, err := audioresampler.NewEngineFloat32(float64(srcRate), float64(dstRate), analysisResampleQuality)
		if err != nil {
			return nil, wrapError(err, errors.CategoryAudio, "mono_resample_init")
		}
		p.rs = rs
		p.out = make([]float32, rs.EstimateOutput(maxFrames))
	}
	return p, nil
}

// push downmixes and resamples one chunk of whole frames and hands the result to
// fn, unless it is empty.
func (p *monoPump) push(block []byte, fn func([]float32) error) error {
	n := len(block) / (p.channels * bytesPerSample)
	got, err := pcm.DownmixBytesToFloat32(p.mono[:n], block, p.channels)
	if err != nil {
		return wrapError(err, errors.CategoryAudio, "mono_downmix")
	}
	samples := p.mono[:got]
	if p.rs != nil {
		k, err := p.rs.ProcessInto(samples, p.out)
		if err != nil {
			return wrapError(err, errors.CategoryAudio, "mono_resample")
		}
		samples = p.out[:k]
	}
	if len(samples) == 0 {
		return nil
	}
	return fn(samples)
}

// finish flushes the resampler tail to fn. It returns the context error, not the
// tail, when ctx is done.
func (p *monoPump) finish(ctx context.Context, fn func([]float32) error) error {
	if p.rs == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tail, err := p.rs.Flush()
	if err != nil {
		return wrapError(err, errors.CategoryAudio, "mono_resample_flush")
	}
	if len(tail) > 0 {
		return fn(tail)
	}
	return nil
}
