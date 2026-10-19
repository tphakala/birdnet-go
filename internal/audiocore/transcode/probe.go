package transcode

import (
	"context"
	"os"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// ProbeInfo is the stream configuration a clip's headers declare.
type ProbeInfo struct {
	SampleRate int // Hz, as DecodeMono reads it (48000 for Opus)
	Channels   int
	// DecodedBytes is the most bytes DecodeMono can hold for this file, from the
	// header alone: the declared length at 16 bits per sample, or one frame past
	// the decoded-size cap when the length is unknown or above it.
	DecodedBytes int64
}

// MaxDecodedPCMBytes is the most decoded audio, in 16-bit output bytes, that
// DecodeMono holds for one file; a longer source wraps ErrAudioTooLong.
const MaxDecodedPCMBytes = maxDecodedPCMBytes

// decodedBytesBound returns the most bytes decodeSource holds for a whole-file
// read of a source declaring totalFrames frames (unknownFrames when none). The
// count is capped before it is multiplied, so a corrupt header cannot overflow.
func decodedBytesBound(totalFrames int64, frameBytes int) int64 {
	maxFrames := int64(maxDecodedPCMBytes / frameBytes)
	if totalFrames < 0 {
		totalFrames = maxFrames + 1
	}
	return min(totalFrames, maxFrames+1) * int64(frameBytes)
}

// ProbeFile reports whether DecodeMono can open path and read audio from it. It
// stats path, runs the file checks ProbeOpenFile runs (regular file, size
// bound) before the open, then opens the same decoder as DecodeMono with the
// same sample-rate and channel bounds, returning the same errors except those
// only a full decode can find (the decoded-size bound). It
// decodes one frame, so a clip whose headers parse but holds no audio wraps
// ErrUndecodableSource. A nil error does not mean the clip is complete: a
// truncated data section is found only by decoding it all. The open itself does
// not observe ctx. DecodedBytes bounds the memory a following DecodeMono needs.
func ProbeFile(ctx context.Context, path string) (ProbeInfo, error) {
	if err := ctx.Err(); err != nil {
		return ProbeInfo{}, err
	}
	f, _, err := openSourceFile(path)
	if err != nil {
		return ProbeInfo{}, err
	}
	defer func() { _ = f.Close() }()
	return probeSource(ctx, f, path)
}

// ProbeOpenFile is ProbeFile on a file the caller already opened: the same
// checks (regular file, size bound) run on f itself, so no path is resolved
// again, and the same decoder bounds and errors apply. name selects the decoder
// by its extension only. It reads f from its current offset, so pass a freshly
// opened file, and it does not close f.
func ProbeOpenFile(ctx context.Context, f *os.File, name string) (ProbeInfo, error) {
	if err := ctx.Err(); err != nil {
		return ProbeInfo{}, err
	}
	info, err := f.Stat()
	if err != nil {
		return ProbeInfo{}, wrapError(err, errors.CategoryFileIO, "decode_stat")
	}
	if err := checkSourceInfo(info); err != nil {
		return ProbeInfo{}, err
	}
	return probeSource(ctx, f, name)
}

// probeSource opens the decoder for f by the extension of name and decodes one
// frame.
func probeSource(ctx context.Context, f *os.File, name string) (ProbeInfo, error) {
	src, err := openSourceChannels(f, name, maxAnalysisChannels)
	if err != nil {
		return ProbeInfo{}, err
	}
	frameBytes := src.channels * bytesPerSample
	data, err := readLimited(ctx, src.reader, frameBytes, frameBytes, frameBytes)
	if err != nil {
		return ProbeInfo{}, err
	}
	if len(data) == 0 {
		return ProbeInfo{}, wrapSentinel(ErrUndecodableSource, nil, "probe_read", "source holds no audio", errors.CategoryAudio)
	}
	return ProbeInfo{SampleRate: src.sampleRate, Channels: src.channels, DecodedBytes: decodedBytesBound(src.totalFrames, frameBytes)}, nil
}
