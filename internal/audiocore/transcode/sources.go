package transcode

import (
	"bytes"
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	aacpcm "github.com/tphakala/go-aac/pcm"
	flac "github.com/tphakala/go-flac"
	flacpcm "github.com/tphakala/go-flac/pcm"
	m4a "github.com/tphakala/go-m4a"
	mp3 "github.com/tphakala/go-mp3"
	mp3pcm "github.com/tphakala/go-mp3/pcm"
	"github.com/tphakala/go-opus/oggopus"
	wav "github.com/tphakala/go-wav"
	wavpcm "github.com/tphakala/go-wav/pcm"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// unknownFrames marks a source whose length is not known from its header.
const unknownFrames = -1

// sniffLen is how many leading bytes identify the container.
const sniffLen = 16

// Expansion factors bound the initial decode buffer relative to the file size
// (see initialCapacity). They are hints; the buffer grows as data arrives.
const (
	// losslessExpansion covers PCM stored uncompressed or lightly compressed.
	losslessExpansion = 2
	// lossyExpansion is generous for lossy codecs, whose files are far smaller
	// than their decoded PCM.
	lossyExpansion = 32
)

// Container byte signatures.
const (
	ftypOffset      = 4
	mpegSyncMask    = 0xE0
	adtsSyncMask    = 0xF0
	mpegLayerMask   = 0x06
	mpegLayerShift  = 1
	oggHeaderWindow = 1024
)

// sniffed container kinds.
type container int

const (
	containerUnknown container = iota
	containerWAV
	containerFLAC
	containerOgg
	containerM4A
	containerMP3
	containerADTS
)

// source is an opened decoder that yields 16-bit little-endian interleaved PCM.
type source struct {
	reader     io.Reader
	sampleRate int
	channels   int
	// totalFrames is the playable length in frames, or unknownFrames.
	totalFrames int64
	// skipFrames counts leading frames the reader emits that are not audio (AAC
	// priming); they are discarded before any requested position.
	skipFrames int64
	// seek positions the reader at a frame index and returns the frame reached.
	// It is nil for decoders without a seek; those are positioned by discarding.
	seek func(frame int64) (int64, error)
	// expansion bounds the initial buffer relative to the file size.
	expansion int64
}

// openSource identifies the container of f and opens its decoder.
func openSource(f *os.File, path string) (*source, error) {
	kind, err := sniffContainer(f, path)
	if err != nil {
		return nil, err
	}
	var src *source
	switch kind {
	case containerWAV:
		src, err = openWAV(f)
	case containerFLAC:
		src, err = openFLAC(f)
	case containerOgg:
		src, err = openOpus(f)
	case containerM4A:
		src, err = openM4A(f)
	case containerMP3:
		src, err = openMP3(f)
	case containerADTS:
		src, err = openADTS(f)
	default:
		return nil, wrapSentinel(ErrUnsupportedSource, nil, "decode_sniff", "unrecognized audio container", errors.CategoryValidation)
	}
	if err != nil {
		return nil, err
	}
	if src.sampleRate < MinSourceSampleRate || src.sampleRate > MaxSourceSampleRate {
		return nil, validationError(ErrUnsupportedSource, "decode_bounds", "sample rate %d Hz outside %d..%d", src.sampleRate, MinSourceSampleRate, MaxSourceSampleRate)
	}
	if src.channels < 1 || src.channels > maxSourceChannels {
		return nil, validationError(ErrUnsupportedSource, "decode_bounds", "%d channels not supported", src.channels)
	}
	return src, nil
}

// sniffContainer identifies the container from the leading bytes; the file
// extension decides only when they are inconclusive.
func sniffContainer(f *os.File, path string) (container, error) {
	head := make([]byte, sniffLen)
	n, err := f.ReadAt(head, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return containerUnknown, errors.New(err).
			Component(componentName).
			Category(errors.CategoryFileIO).
			Context("operation", "decode_sniff").
			Build()
	}
	if kind := containerFromBytes(head[:n]); kind != containerUnknown {
		return kind, nil
	}
	return containerFromExtension(path), nil
}

// containerFromBytes maps leading file bytes to a container.
func containerFromBytes(head []byte) container {
	switch {
	case bytes.HasPrefix(head, []byte("RIFF")), bytes.HasPrefix(head, []byte("RF64")), bytes.HasPrefix(head, []byte("BW64")):
		return containerWAV
	case bytes.HasPrefix(head, []byte("fLaC")):
		return containerFLAC
	case bytes.HasPrefix(head, []byte("OggS")):
		return containerOgg
	case len(head) >= ftypOffset+4 && bytes.Equal(head[ftypOffset:ftypOffset+4], []byte("ftyp")):
		return containerM4A
	case bytes.HasPrefix(head, []byte("ID3")):
		return containerMP3
	case len(head) >= 2 && head[0] == 0xFF && head[1]&mpegSyncMask == mpegSyncMask:
		// MPEG audio shares its 11-bit sync with ADTS AAC; ADTS has layer bits 00
		// and a 12-bit sync, MP3 has a non-zero layer.
		if (head[1]&mpegLayerMask)>>mpegLayerShift == 0 {
			if head[1]&adtsSyncMask == adtsSyncMask {
				return containerADTS
			}
			return containerUnknown
		}
		return containerMP3
	}
	return containerUnknown
}

// containerFromExtension is the fallback when the leading bytes say nothing.
func containerFromExtension(path string) container {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wav":
		return containerWAV
	case ".flac":
		return containerFLAC
	case ".opus", ".ogg":
		return containerOgg
	case ".m4a", ".mp4":
		return containerM4A
	case ".mp3":
		return containerMP3
	case ".aac":
		return containerADTS
	}
	return containerUnknown
}

// openWAV opens a WAV or RF64 source, converting its samples to 16-bit.
func openWAV(f *os.File) (*source, error) {
	dec, err := wavpcm.NewDecoder(f, wavpcm.WithConvertTo(16))
	if err != nil {
		return nil, classifyDecodeError("decode_open_wav", err)
	}
	info := dec.Info()
	total := int64(unknownFrames)
	if info.DataSizeKnown && info.TotalFrames <= math.MaxInt64 {
		total = int64(info.TotalFrames) //nolint:gosec // G115: bounded by the check above
	}
	return &source{
		reader:      dec,
		sampleRate:  info.SampleRate,
		channels:    info.Channels,
		totalFrames: total,
		seek:        dec.SeekToFrame,
		expansion:   losslessExpansion,
	}, nil
}

// openFLAC opens a FLAC source. Samples wider or narrower than 16 bits are
// converted as they are read.
func openFLAC(f *os.File) (*source, error) {
	dec, err := flacpcm.NewDecoder(f, flacpcm.SkipMD5Verification())
	if err != nil {
		return nil, classifyDecodeError("decode_open_flac", err)
	}
	info := dec.Info()
	total := int64(unknownFrames)
	if info.TotalSamples > 0 && info.TotalSamples <= math.MaxInt64 {
		total = int64(info.TotalSamples) //nolint:gosec // G115: bounded by the check above
	}
	var reader io.Reader = dec
	if info.BitDepth != 16 {
		reader = newWidthConverter(dec, info.BitDepth)
	}
	return &source{
		reader:      reader,
		sampleRate:  info.SampleRate,
		channels:    info.Channels,
		totalFrames: total,
		seek:        dec.SeekToSample,
		expansion:   losslessExpansion,
	}, nil
}

// openOpus opens an Ogg Opus source. It decodes at 48 kHz. go-opus v1.1.0's
// oggopus.Decoder has no seek, so windows are positioned by discarding.
func openOpus(f *os.File) (*source, error) {
	// An Ogg stream that is not Opus (Vorbis, FLAC-in-Ogg) is unsupported rather
	// than corrupt: look for the Opus identification header in the first page.
	head := make([]byte, oggHeaderWindow)
	n, _ := f.ReadAt(head, 0)
	if !bytes.Contains(head[:n], []byte("OpusHead")) {
		return nil, wrapSentinel(ErrUnsupportedSource, nil, "decode_open_opus", "Ogg stream is not Opus", errors.CategoryValidation)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, classifyDecodeError("decode_open_opus", err)
	}
	dec, err := oggopus.NewDecoder(f)
	if err != nil {
		return nil, classifyDecodeError("decode_open_opus", err)
	}
	info := dec.Info()
	return &source{
		reader:      dec,
		sampleRate:  int(info.OutputSampleRate),
		channels:    info.Channels,
		totalFrames: unknownFrames,
		expansion:   lossyExpansion,
	}, nil
}

// openM4A opens an AAC-LC source in an MP4 container. The decoder is assembled
// by hand (as go-m4a's aacm4a bridge does) so the box buffer can be capped. The
// decoder is not edit-list aware, so the priming samples are skipped and the
// padding after the declared duration is left unread.
func openM4A(f *os.File) (*source, error) {
	rd, err := m4a.NewReader(f, m4a.WithMaxBoxBuffer(maxM4ABoxBytes))
	if err != nil {
		return nil, classifyDecodeError("decode_open_m4a", err)
	}
	info := rd.Info()
	if info.Codec != m4a.CodecAACLC {
		return nil, validationError(ErrUnsupportedSource, "decode_open_m4a", "MP4 audio codec %v is not AAC", info.Codec)
	}
	dec, err := aacpcm.NewDecoder(rd.RawStream(), aacpcm.WithRawStream(info.ASC))
	if err != nil {
		return nil, classifyDecodeError("decode_open_m4a", err)
	}
	pcmInfo := dec.Info()
	total := int64(unknownFrames)
	if info.Duration > 0 {
		frames := math.Round(info.Duration.Seconds() * float64(pcmInfo.SampleRate))
		if frames <= maxWindowFrames {
			total = int64(frames)
		}
	}
	return &source{
		reader:      dec,
		sampleRate:  pcmInfo.SampleRate,
		channels:    pcmInfo.Channels,
		totalFrames: total,
		skipFrames:  max(info.EncoderDelay, 0),
		expansion:   lossyExpansion,
	}, nil
}

// openMP3 opens an MP3 source; its decoder seeks sample-exactly.
func openMP3(f *os.File) (*source, error) {
	dec, err := mp3pcm.NewDecoder(f)
	if err != nil {
		return nil, classifyDecodeError("decode_open_mp3", err)
	}
	info := dec.Info()
	total := int64(unknownFrames)
	if info.TotalSamples > 0 && info.TotalSamples <= math.MaxInt64 {
		total = int64(info.TotalSamples) //nolint:gosec // G115: bounded by the check above
	}
	return &source{
		reader:      dec,
		sampleRate:  info.SampleRate,
		channels:    info.Channels,
		totalFrames: total,
		seek:        dec.SeekToSample,
		expansion:   lossyExpansion,
	}, nil
}

// openADTS opens a raw ADTS AAC source. go-aac's pcm.Decoder has no seek.
func openADTS(f *os.File) (*source, error) {
	dec, err := aacpcm.NewDecoder(f)
	if err != nil {
		return nil, classifyDecodeError("decode_open_adts", err)
	}
	info := dec.Info()
	return &source{
		reader:      dec,
		sampleRate:  info.SampleRate,
		channels:    info.Channels,
		totalFrames: unknownFrames,
		expansion:   lossyExpansion,
	}, nil
}

// classifyDecodeError maps a decoder error to a package sentinel. Context errors
// and errors that already carry a sentinel pass through unchanged.
func classifyDecodeError(operation string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, ErrUnsupportedSource), errors.Is(err, ErrUndecodableSource),
		errors.Is(err, ErrAudioTooLong), errors.Is(err, ErrEmptyRange):
		return err
	case errors.Is(err, aacpcm.ErrUnsupportedSBR):
		return wrapSentinel(ErrHEAACSource, err, operation, "HE-AAC source", errors.CategoryValidation)
	case errors.Is(err, aacpcm.ErrUnsupported), errors.Is(err, m4a.ErrUnsupported),
		errors.Is(err, m4a.ErrBoxTooLarge), errors.Is(err, wav.ErrUnsupported),
		errors.Is(err, wav.ErrNotRIFF), errors.Is(err, flac.ErrUnsupported),
		errors.Is(err, mp3.ErrUnsupported):
		return wrapSentinel(ErrUnsupportedSource, err, operation, "unsupported stream", errors.CategoryValidation)
	}
	return wrapSentinel(ErrUndecodableSource, err, operation, "decode failed", errors.CategoryAudio)
}
