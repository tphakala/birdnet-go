package transcode

import (
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"time"

	aaccodec "github.com/tphakala/go-aac"
	flacpcm "github.com/tphakala/go-flac/pcm"
	m4a "github.com/tphakala/go-m4a"
	aacm4a "github.com/tphakala/go-m4a/aacm4a"
	mp3pcm "github.com/tphakala/go-mp3/pcm"
	"github.com/tphakala/go-opus/oggopus"
	wavpcm "github.com/tphakala/go-wav/pcm"

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// unknownFrames marks a source whose length is not known from its header.
const unknownFrames = -1

// Expansion factors bound the initial decode buffer relative to the file size
// (see initialCapacity). They are hints; the buffer grows as data arrives.
const (
	// losslessExpansion covers PCM stored uncompressed or lightly compressed.
	losslessExpansion = 2
	// lossyExpansion is generous for lossy codecs, whose files are far smaller
	// than their decoded PCM.
	lossyExpansion = 32
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
	// seek positions the reader at a frame index of the presented audio and
	// returns the frame reached. It accounts for skipFrames itself and is called
	// only for a start other than frame 0. A start past the end is reported by
	// returning a frame below the target. It is nil for a decoder without a usable
	// seek (Opus, see openOpus); that source is positioned by discarding.
	seek func(frame int64) (int64, error)
	// expansion bounds the initial buffer relative to the file size.
	expansion int64
}

// sourceOpeners maps the file extension of each clip format to its decoder. The
// extensions come from the function clip export names its files with, so a format
// exported without a decoder here fails the dispatch test.
var sourceOpeners = map[string]func(*os.File) (*source, error){
	"." + audioformat.FileExtension(audioformat.WAV):  openWAV,
	"." + audioformat.FileExtension(audioformat.FLAC): openFLAC,
	"." + audioformat.FileExtension(audioformat.MP3):  openMP3,
	"." + audioformat.FileExtension(audioformat.AAC):  openM4A,
	"." + audioformat.FileExtension(audioformat.Opus): openOpus,
}

// openSource opens the decoder that the extension of path names.
func openSource(f *os.File, path string) (*source, error) {
	open, ok := sourceOpeners[filepath.Ext(path)]
	if !ok {
		return nil, wrapSentinel(ErrUnsupportedSource, nil, "decode_open", "file extension is not a clip format", errors.CategoryValidation)
	}
	src, err := open(f)
	if err != nil {
		return nil, err
	}
	if src.sampleRate < minSourceSampleRate || src.sampleRate > maxSourceSampleRate {
		return nil, validationError(ErrUnsupportedSource, "decode_bounds", "sample rate %d Hz outside %d..%d", src.sampleRate, minSourceSampleRate, maxSourceSampleRate)
	}
	if src.channels < 1 || src.channels > maxSourceChannels {
		return nil, validationError(ErrUnsupportedSource, "decode_bounds", "%d channels not supported", src.channels)
	}
	return src, nil
}

// declaredFrames returns the frame count a header declares, or unknownFrames when
// the header declares none or a count above the int64 range.
func declaredFrames(n uint64, known bool) int64 {
	if !known || n > math.MaxInt64 {
		return unknownFrames
	}
	return int64(n) //nolint:gosec // G115: bounded by the check above
}

// openWAV opens a WAV or RF64 source, converting its samples to 16-bit.
func openWAV(f *os.File) (*source, error) {
	dec, err := wavpcm.NewDecoder(f, wavpcm.WithConvertTo(outputBitDepth))
	if err != nil {
		return nil, decodeError("decode_open_wav", err)
	}
	info := dec.Info()
	return &source{
		reader:      dec,
		sampleRate:  info.SampleRate,
		channels:    info.Channels,
		totalFrames: declaredFrames(info.TotalFrames, info.DataSizeKnown),
		seek:        dec.SeekToFrame,
		expansion:   losslessExpansion,
	}, nil
}

// openFLAC opens a FLAC source. The decoder converts every bit depth to 16-bit as
// it reads; Info().BitDepth stays the native depth of the file.
func openFLAC(f *os.File) (*source, error) {
	dec, err := flacpcm.NewDecoder(f, flacpcm.SkipMD5Verification(), flacpcm.WithConvertTo(outputBitDepth))
	if err != nil {
		return nil, decodeError("decode_open_flac", err)
	}
	info := dec.Info()
	return &source{
		reader:      dec,
		sampleRate:  info.SampleRate,
		channels:    info.Channels,
		totalFrames: declaredFrames(info.TotalSamples, info.TotalSamples > 0),
		seek:        dec.SeekToSample,
		expansion:   losslessExpansion,
	}, nil
}

// openOpus opens an Ogg Opus source. It decodes at 48 kHz. The decoder drops the
// pre-skip and trims the end itself, so skipFrames is 0 and Length is in the
// index space the forward path discards in. Length gives the total; a stream
// go-opus cannot index keeps an unknown length. The source has no seek:
// go-opus's SeekToSample re-primes the decoder with only 80 ms of pre-roll, which
// is not bit-exact on CELT, so windows are positioned by discarding to stay equal
// to the linear decode.
func openOpus(f *os.File) (*source, error) {
	dec, err := oggopus.NewDecoder(f)
	if err != nil {
		return nil, decodeError("decode_open_opus", err)
	}
	info := dec.Info()
	src := &source{
		reader:      dec,
		sampleRate:  int(info.OutputSampleRate),
		channels:    info.Channels,
		totalFrames: unknownFrames,
		expansion:   lossyExpansion,
	}
	if length, err := dec.Length(); err == nil {
		src.totalFrames = length
	}
	return src, nil
}

// openM4A opens an AAC-LC source in an MP4 container through go-m4a's seekable
// decoder, which rejects a non-AAC or HE-AAC track. The decoder is not edit-list
// aware, so the priming samples are skipped through skipFrames and the padding
// after the declared duration is cut by totalFrames. A window that starts after
// frame 0 seeks by time, which handles the AAC pre-roll.
func openM4A(f *os.File) (*source, error) {
	dec, info, err := aacm4a.NewSeekableDecoder(f, m4a.WithMaxBoxBuffer(maxM4ABoxBytes))
	if err != nil {
		return nil, decodeError("decode_open_m4a", err)
	}
	total := int64(unknownFrames)
	if info.Duration > 0 {
		frames := math.Round(info.Duration.Seconds() * float64(info.SampleRate))
		if frames <= maxWindowFrames {
			total = int64(frames)
		}
	}
	// A corrupt edit list can declare an EncoderDelay near MaxInt64; the track
	// cannot decode to more than FrameCount access units, so a larger skip is
	// clamped to that.
	frameCount := int64(info.FrameCount)
	skip := min(max(info.EncoderDelay, 0), frameCount*aaccodec.FrameSize)
	seek := func(frame int64) (int64, error) {
		// The decoder output is not edit-list aware: SeekTime puts the next byte at
		// the decoded sample EncoderDelay + frame, the index the forward path
		// discards to. A time beyond the end is past the end of the audio. frame is
		// at most maxWindowFrames, so the time fits a time.Duration.
		ns := math.Round(float64(frame) / float64(info.SampleRate) * float64(time.Second))
		if err := dec.SeekTime(time.Duration(ns)); err != nil {
			if errors.Is(err, m4a.ErrOutOfRange) {
				return 0, nil
			}
			return 0, err
		}
		return frame, nil
	}
	return &source{
		reader:      dec,
		sampleRate:  info.SampleRate,
		channels:    info.Channels,
		totalFrames: total,
		skipFrames:  skip,
		seek:        seek,
		expansion:   lossyExpansion,
	}, nil
}

// openMP3 opens an MP3 source; its decoder seeks sample-exactly.
func openMP3(f *os.File) (*source, error) {
	dec, err := mp3pcm.NewDecoder(f)
	if err != nil {
		return nil, decodeError("decode_open_mp3", err)
	}
	info := dec.Info()
	return &source{
		reader:      dec,
		sampleRate:  info.SampleRate,
		channels:    info.Channels,
		totalFrames: declaredFrames(info.TotalSamples, info.TotalSamples > 0),
		seek:        dec.SeekToSample,
		expansion:   lossyExpansion,
	}, nil
}

// decodeError classifies an error from a decoder library. A failed read of the
// file itself (an *fs.PathError in the chain) is a server fault and carries no
// sentinel; anything else means the clip BirdNET-Go wrote is damaged and wraps
// ErrUndecodableSource. go-m4a reports a failed read of M4A frame data as
// corrupt data with the cause as text only, so that one still lands here as
// undecodable.
func decodeError(operation string, err error) error {
	if _, ok := errors.AsType[*fs.PathError](err); ok {
		return wrapError(err, errors.CategoryFileIO, operation)
	}
	return wrapSentinel(ErrUndecodableSource, err, operation, "decode failed", errors.CategoryAudio)
}
