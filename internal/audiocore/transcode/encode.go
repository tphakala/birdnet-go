package transcode

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"

	flacpcm "github.com/tphakala/go-flac/pcm"
	wavpcm "github.com/tphakala/go-wav/pcm"

	"github.com/tphakala/birdnet-go/internal/audiocore/aac"
	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/audiocore/clipenc"
	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/audiocore/mp3"
	"github.com/tphakala/birdnet-go/internal/audiocore/opus"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// outputBitDepth is the bit depth of the PCM this package encodes.
const outputBitDepth = 16

// EncoderTag returns the encoder identity recorded in the export logs for format.
// An unknown format reports the WAV writer.
func EncoderTag(format string) string {
	if tag, ok := clipenc.ForFormat(format); ok {
		return tag
	}
	return clipenc.NativeWAV
}

// EncoderAcceptsRate reports whether the native encoder for format can carry audio
// of this shape. WAV carries any rate; FLAC carries rates up to flacpcm.MaxSampleRate;
// AAC, MP3 and Opus accept only the rates their encoders support. Clip export and on-demand transcoding share this
// rule to decide whether a clip must be resampled to conf.SampleRate first.
func EncoderAcceptsRate(format string, sampleRate, bitDepth, channels int) bool {
	switch format {
	case audioformat.AAC:
		return aac.Supports(sampleRate, bitDepth, channels) == nil
	case audioformat.MP3:
		return mp3.Supports(sampleRate, bitDepth, channels) == nil
	case audioformat.Opus:
		return opus.Supports(sampleRate, bitDepth, channels) == nil
	case audioformat.FLAC:
		return sampleRate <= flacpcm.MaxSampleRate
	default:
		return true
	}
}

// fileEncodeOptions is what a file-based encoder needs.
type fileEncodeOptions struct {
	data        []byte
	outputPath  string
	sampleRate  int
	channels    int
	bitrateKbps int
}

// fileEncoders maps each format that must be written to a seekable file to its
// native encoder. WAV is built in memory and is not listed.
var fileEncoders = map[string]func(ctx context.Context, o *fileEncodeOptions) error{
	audioformat.FLAC: func(ctx context.Context, o *fileEncodeOptions) error {
		return flac.EncodePCM(ctx, &flac.Options{
			PCMData: o.data, OutputPath: o.outputPath, SampleRate: o.sampleRate,
			Channels: o.channels, BitDepth: outputBitDepth,
		})
	},
	audioformat.MP3: func(ctx context.Context, o *fileEncodeOptions) error {
		return mp3.EncodePCM(ctx, &mp3.Options{
			PCMData: o.data, OutputPath: o.outputPath, SampleRate: o.sampleRate,
			Channels: o.channels, BitDepth: outputBitDepth, BitrateKbps: o.bitrateKbps,
		})
	},
	audioformat.AAC: func(ctx context.Context, o *fileEncodeOptions) error {
		return aac.EncodePCM(ctx, &aac.Options{
			PCMData: o.data, OutputPath: o.outputPath, SampleRate: o.sampleRate,
			Channels: o.channels, BitDepth: outputBitDepth, BitrateKbps: o.bitrateKbps,
		})
	},
	audioformat.Opus: func(ctx context.Context, o *fileEncodeOptions) error {
		return opus.EncodePCM(ctx, &opus.Options{
			PCMData: o.data, OutputPath: o.outputPath, SampleRate: o.sampleRate,
			Channels: o.channels, BitDepth: outputBitDepth, BitrateKbps: o.bitrateKbps,
		})
	},
}

// encode encodes p in format and returns the file bytes. The sample rate is kept
// unless the target encoder does not accept it, in which case the audio is
// resampled to conf.SampleRate, the same rule clip export applies. Audio too
// short to survive the resampling returns ErrEmptyRange. Formats that
// need a seekable sink are encoded into a private directory under tempDir that is
// removed before Encode returns.
func encode(ctx context.Context, p *pcmBuffer, format, tempDir string) ([]byte, error) {
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
	if p.Frames() == 0 {
		return nil, wrapSentinel(ErrEmptyRange, nil, "encode", "range too short to encode", errors.CategoryValidation)
	}
	if format == audioformat.WAV {
		return encodeWAV(p)
	}
	return encodeViaFile(ctx, p, format, tempDir)
}

// resampleForEncoder returns p at a rate the encoder for format accepts.
func resampleForEncoder(ctx context.Context, p *pcmBuffer, format string) (*pcmBuffer, error) {
	if EncoderAcceptsRate(format, p.SampleRate, outputBitDepth, p.Channels) {
		return p, nil
	}
	return resampleInterleaved(ctx, p, conf.SampleRate, maxDecodedPCMBytes)
}

// wavHeaderBytes is the size of the canonical RIFF/WAVE header go-wav writes for
// 16-bit PCM with one or two channels.
const wavHeaderBytes = 44

// RIFF header field offsets within the canonical header.
const (
	wavRIFFSizeOffset = 4
	wavDataSizeOffset = 40
	// wavRIFFSizeExtra is what the RIFF size adds to the data size: the header
	// minus the "RIFF" tag and the size field itself.
	wavRIFFSizeExtra = wavHeaderBytes - 8
)

// encodeWAV writes p as a WAV file in memory. The length is known, so the RIFF
// sizes are final. When p carries header room (see pcmBuffer.wavRoom) the header
// is written in front of the samples and no sample is copied.
func encodeWAV(p *pcmBuffer) ([]byte, error) {
	cfg := wavpcm.Config{SampleRate: p.SampleRate, BitDepth: outputBitDepth, Channels: p.Channels}
	if wav, ok := encodeWAVInPlace(p, &cfg); ok {
		return wav, nil
	}
	var buf bytes.Buffer
	buf.Grow(len(p.Data) + wavHeaderSlackBytes)
	if err := wavpcm.EncodeInterleaved(&buf, cfg, p.Data); err != nil {
		return nil, wrapError(err, errors.CategoryAudio, "encode_wav")
	}
	return buf.Bytes(), nil
}

// encodeWAVInPlace builds the WAV inside p.wavRoom. It reports false, leaving the
// copying path to run, when p has no header room, when the room does not directly
// precede p.Data, or when the encoder's header is not the canonical 44 bytes.
func encodeWAVInPlace(p *pcmBuffer, cfg *wavpcm.Config) ([]byte, bool) {
	if len(p.wavRoom) < wavHeaderBytes+len(p.Data) || len(p.Data) == 0 || &p.wavRoom[wavHeaderBytes] != &p.Data[0] {
		return nil, false
	}
	var head bytes.Buffer
	if err := wavpcm.EncodeInterleaved(&head, *cfg, nil); err != nil || head.Len() != wavHeaderBytes {
		return nil, false
	}
	wav := p.wavRoom[:wavHeaderBytes+len(p.Data)]
	copy(wav, head.Bytes())
	binary.LittleEndian.PutUint32(wav[wavRIFFSizeOffset:], uint32(len(p.Data)+wavRIFFSizeExtra)) //nolint:gosec // G115: bounded by maxDecodedPCMBytes
	binary.LittleEndian.PutUint32(wav[wavDataSizeOffset:], uint32(len(p.Data)))                  //nolint:gosec // G115: bounded by maxDecodedPCMBytes
	return wav, true
}

// wavHeaderSlackBytes is spare buffer room for the WAV header.
const wavHeaderSlackBytes = 512

// encodeViaFile runs a file-based encoder in a private temp directory and reads
// the result back. FLAC goes through the file path rather than a buffer so the
// STREAMINFO MD5 and seek table are real.
func encodeViaFile(ctx context.Context, p *pcmBuffer, format, tempDir string) ([]byte, error) {
	enc, ok := fileEncoders[format]
	if !ok {
		return nil, wrapError(errors.NewStd("no file encoder for format "+format), errors.CategoryProcessing, "encode_dispatch")
	}
	dir, err := os.MkdirTemp(tempDir, "transcode-*")
	if err != nil {
		return nil, wrapError(err, errors.CategoryFileIO, "encode_tempdir")
	}
	defer func() { _ = os.RemoveAll(dir) }()

	out := filepath.Join(dir, "clip."+convert.GetFileExtension(format))
	if err := enc(ctx, &fileEncodeOptions{
		data: p.Data, outputPath: out, sampleRate: p.SampleRate,
		channels: p.Channels, bitrateKbps: clipBitrateKbps[format],
	}); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(out) //nolint:gosec // G304: out is inside a directory this function just created
	if err != nil {
		return nil, wrapError(err, errors.CategoryFileIO, "encode_read_back")
	}
	return data, nil
}
