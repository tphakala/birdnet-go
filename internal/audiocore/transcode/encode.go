package transcode

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	wavpcm "github.com/tphakala/go-wav/pcm"

	"github.com/tphakala/birdnet-go/internal/audiocore/aac"
	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/audiocore/clipenc"
	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/audiocore/mp3"
	"github.com/tphakala/birdnet-go/internal/audiocore/opus"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// outputBitDepth is the bit depth of the PCM this package encodes.
const outputBitDepth = 16

// EncoderTag returns the encoder identity recorded in the export logs for format.
func EncoderTag(format string) string {
	switch format {
	case audioformat.FLAC:
		return clipenc.NativeFLAC
	case audioformat.MP3:
		return clipenc.NativeMP3
	case audioformat.AAC:
		return clipenc.NativeAAC
	case audioformat.Opus:
		return clipenc.NativeOpus
	default:
		return clipenc.NativeWAV
	}
}

// fileExtension returns the file extension the native encoder writes for format.
func fileExtension(format string) string {
	if format == audioformat.AAC {
		return "m4a"
	}
	return format
}

// Encode encodes p in format and returns the file bytes. The sample rate is kept
// unless the target lossy encoder does not accept it, in which case the audio is
// resampled to conf.SampleRate, the same rule clip export applies. Formats that
// need a seekable sink are encoded into a private directory under tempDir that is
// removed before Encode returns.
func Encode(ctx context.Context, p *PCM, format, tempDir string) ([]byte, error) {
	if !IsSupportedClipFormat(format) {
		return nil, validationError(ErrInvalidRequest, "encode", "unsupported format %q", format)
	}
	if p.Frames() == 0 {
		return nil, wrapSentinel(ErrEmptyRange, nil, "encode", "no audio to encode", errors.CategoryValidation)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p, err := resampleForEncoder(ctx, p, format)
	if err != nil {
		return nil, err
	}
	if format == audioformat.WAV {
		return encodeWAV(p)
	}
	return encodeViaFile(ctx, p, format, tempDir)
}

// resampleForEncoder returns p at a rate the lossy encoder for format accepts.
func resampleForEncoder(ctx context.Context, p *PCM, format string) (*PCM, error) {
	var supports func(rate, bitDepth, channels int) error
	switch format {
	case audioformat.AAC:
		supports = aac.Supports
	case audioformat.MP3:
		supports = mp3.Supports
	case audioformat.Opus:
		supports = opus.Supports
	default:
		// WAV and FLAC carry any rate.
		return p, nil
	}
	if supports(p.SampleRate, outputBitDepth, p.Channels) == nil {
		return p, nil
	}
	return resampleInterleaved(ctx, p, conf.SampleRate, MaxDecodedPCMBytes)
}

// encodeWAV writes p as a WAV file in memory. The length is known, so the RIFF
// sizes are final.
func encodeWAV(p *PCM) ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(len(p.Data) + wavHeaderSlackBytes)
	err := wavpcm.EncodeInterleaved(&buf, wavpcm.Config{
		SampleRate: p.SampleRate,
		BitDepth:   outputBitDepth,
		Channels:   p.Channels,
	}, p.Data)
	if err != nil {
		return nil, errors.New(err).
			Component(componentName).
			Category(errors.CategoryAudio).
			Context("operation", "encode_wav").
			Build()
	}
	return buf.Bytes(), nil
}

// wavHeaderSlackBytes is spare buffer room for the WAV header.
const wavHeaderSlackBytes = 512

// encodeViaFile runs a file-based encoder in a private temp directory and reads
// the result back. FLAC goes through the file path rather than a buffer so the
// STREAMINFO MD5 and seek table are real.
func encodeViaFile(ctx context.Context, p *PCM, format, tempDir string) ([]byte, error) {
	dir, err := os.MkdirTemp(tempDir, "transcode-*")
	if err != nil {
		return nil, errors.New(err).
			Component(componentName).
			Category(errors.CategoryFileIO).
			Context("operation", "encode_tempdir").
			Build()
	}
	defer func() { _ = os.RemoveAll(dir) }()

	out := filepath.Join(dir, "clip."+fileExtension(format))
	bitrate := clipBitrateKbps[format]
	switch format {
	case audioformat.FLAC:
		err = flac.EncodePCM(ctx, &flac.Options{
			PCMData: p.Data, OutputPath: out, SampleRate: p.SampleRate,
			Channels: p.Channels, BitDepth: outputBitDepth,
		})
	case audioformat.MP3:
		err = mp3.EncodePCM(ctx, &mp3.Options{
			PCMData: p.Data, OutputPath: out, SampleRate: p.SampleRate,
			Channels: p.Channels, BitDepth: outputBitDepth, BitrateKbps: bitrate,
		})
	case audioformat.AAC:
		err = aac.EncodePCM(ctx, &aac.Options{
			PCMData: p.Data, OutputPath: out, SampleRate: p.SampleRate,
			Channels: p.Channels, BitDepth: outputBitDepth, BitrateKbps: bitrate,
		})
	case audioformat.Opus:
		err = opus.EncodePCM(ctx, &opus.Options{
			PCMData: p.Data, OutputPath: out, SampleRate: p.SampleRate,
			Channels: p.Channels, BitDepth: outputBitDepth, BitrateKbps: bitrate,
		})
	}
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(out) //nolint:gosec // G304: out is inside a directory this function just created
	if err != nil {
		return nil, errors.New(err).
			Component(componentName).
			Category(errors.CategoryFileIO).
			Context("operation", "encode_read_back").
			Build()
	}
	return data, nil
}
