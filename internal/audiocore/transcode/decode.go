package transcode

import (
	"context"
	"io"
	"math"
	"os"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// bytesPerSample is the width of one sample of the 16-bit PCM this package produces.
const bytesPerSample = 2

// maxZeroReads bounds a run of (0, nil) reads before a read loop gives up.
const maxZeroReads = 100

// maxWindowFrames bounds a window start in frames before it is converted to int64.
const maxWindowFrames = 1 << 50

// initialCapSlackBytes is added to the file-size based initial buffer estimate.
const initialCapSlackBytes = 64 << 10

// pcmBuffer is decoded audio: 16-bit little-endian interleaved samples.
type pcmBuffer struct {
	// Data holds whole frames of interleaved 16-bit little-endian samples.
	Data []byte
	// SampleRate is the sample rate in Hz.
	SampleRate int
	// Channels is the interleaved channel count.
	Channels int
	// wavRoom, when non-nil, is the buffer Data was sliced from, with exactly
	// wavHeaderBytes of unused space before Data. encodeWAV then builds the WAV in
	// place instead of copying the samples.
	wavRoom []byte
}

// Frames returns the number of whole frames in p.
func (p *pcmBuffer) Frames() int {
	if p.Channels <= 0 {
		return 0
	}
	return len(p.Data) / (p.Channels * bytesPerSample)
}

// window selects a time range of the source, in seconds.
type window struct {
	// Start is the offset of the first sample, at least 0.
	Start float64
	// Duration is the length of the range, greater than 0.
	Duration float64
}

// decode decodes the audio file at path to 16-bit PCM. A nil win decodes the
// whole file; otherwise only the requested window, shortened when the source
// ends inside it. maxBytes bounds the decoded size in output bytes (values
// outside (0, maxDecodedPCMBytes] select maxDecodedPCMBytes).
//
// The decoder is chosen by the file extension. Source and request problems wrap
// one of the package sentinels; context errors pass through, and file access
// errors carry no sentinel (except a failed read of M4A frame data, which
// go-m4a reports as corrupt data).
func decode(ctx context.Context, path string, win *window, maxBytes int) (*pcmBuffer, error) {
	return decodeAtLeast(ctx, path, win, maxBytes, 0)
}

// decodeAtLeast is decode that also rejects, from the header and before any audio
// is read, a source whose sample rate is below minRate (0 accepts any rate). The
// rejection wraps ErrBelowMinimumRate.
func decodeAtLeast(ctx context.Context, path string, win *window, maxBytes, minRate int) (*pcmBuffer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxBytes <= 0 || maxBytes > maxDecodedPCMBytes {
		maxBytes = maxDecodedPCMBytes
	}
	if win != nil {
		if err := validateWindow(win); err != nil {
			return nil, err
		}
	}

	f, size, err := openSourceFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	src, err := openSource(f, path)
	if err != nil {
		return nil, err
	}
	if src.sampleRate < minRate {
		return nil, validationError(ErrBelowMinimumRate, "decode_min_rate", "sample rate %d Hz is below the required %d Hz", src.sampleRate, minRate)
	}
	return decodeSource(ctx, src, win, size, maxBytes)
}

// validateWindow rejects values no clip request can legitimately carry.
func validateWindow(win *window) error {
	switch {
	case math.IsNaN(win.Start) || math.IsInf(win.Start, 0) || win.Start < 0:
		return validationError(ErrInvalidRequest, "decode_validate_window", "start %v must be finite and non-negative", win.Start)
	case math.IsNaN(win.Duration) || math.IsInf(win.Duration, 0) || win.Duration <= 0:
		return validationError(ErrInvalidRequest, "decode_validate_window", "duration %v must be finite and positive", win.Duration)
	}
	return nil
}

// openSourceFile opens path after checking it is a regular file within the size
// bound. It returns the open file and its size.
func openSourceFile(path string) (*os.File, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, wrapError(err, errors.CategoryFileIO, "decode_stat")
	}
	if !info.Mode().IsRegular() {
		return nil, 0, wrapSentinel(ErrUnsupportedSource, nil, "decode_stat", "not a regular file", errors.CategoryValidation)
	}
	if info.Size() > maxSourceFileBytes {
		return nil, 0, wrapSentinel(ErrAudioTooLong, nil, "decode_stat", "source file exceeds the size limit", errors.CategoryLimit)
	}
	f, err := os.Open(path) //nolint:gosec // G304: path is a stored clip resolved by the caller from the configured clip directory
	if err != nil {
		return nil, 0, wrapError(err, errors.CategoryFileIO, "decode_open")
	}
	return f, info.Size(), nil
}

// decodeSource reads the requested range from an opened source.
func decodeSource(ctx context.Context, src *source, win *window, fileSize int64, maxBytes int) (*pcmBuffer, error) {
	frameBytes := src.channels * bytesPerSample
	maxFrames := int64(maxBytes / frameBytes)

	var startFrame int64
	wantFrames := int64(-1) // negative reads to the end of the source
	if win != nil {
		start := math.Round(win.Start * float64(src.sampleRate))
		length := math.Round(win.Duration * float64(src.sampleRate))
		if start > maxWindowFrames {
			return nil, wrapSentinel(ErrEmptyRange, nil, "decode_window", "start is beyond the end of the audio", errors.CategoryValidation)
		}
		startFrame = int64(start)
		if src.totalFrames >= 0 {
			if startFrame >= src.totalFrames {
				return nil, wrapSentinel(ErrEmptyRange, nil, "decode_window", "start is beyond the end of the audio", errors.CategoryValidation)
			}
			// Clamp to the audio that exists before the cap check, so a window longer
			// than the cap that reaches past a short source is still served.
			length = min(length, float64(src.totalFrames-startFrame))
		}
		if length > float64(maxFrames) {
			return nil, wrapSentinel(ErrAudioTooLong, nil, "decode_window", "requested range exceeds the decoded size limit", errors.CategoryLimit)
		}
		wantFrames = max(int64(length), 1)
	} else if src.totalFrames >= 0 {
		// A header can declare more frames than any buffer holds; one frame past
		// the cap is enough to detect an overlong source and keeps the byte
		// products below from overflowing.
		wantFrames = min(src.totalFrames, maxFrames+1)
	}

	// A whole-file read is capped one frame past maxBytes so an overlong source is
	// detected rather than silently truncated.
	limit := int64(maxBytes) + int64(frameBytes)
	if wantFrames >= 0 {
		limit = min(limit, wantFrames*int64(frameBytes))
	}
	if err := positionSource(ctx, src, startFrame, frameBytes, win != nil); err != nil {
		return nil, err
	}
	data, err := readLimited(ctx, src.reader, int(limit), initialCapacity(src, fileSize, limit, frameBytes), frameBytes)
	if err != nil {
		return nil, err
	}
	if win == nil && len(data) > maxBytes {
		return nil, wrapSentinel(ErrAudioTooLong, nil, "decode_read", "decoded audio exceeds the size limit", errors.CategoryLimit)
	}
	if len(data) == 0 {
		if win != nil {
			return nil, wrapSentinel(ErrEmptyRange, nil, "decode_read", "no audio in the requested range", errors.CategoryValidation)
		}
		return nil, wrapSentinel(ErrUndecodableSource, nil, "decode_read", "source holds no audio", errors.CategoryAudio)
	}
	return &pcmBuffer{Data: data, SampleRate: src.sampleRate, Channels: src.channels}, nil
}

// positionSource moves the reader to startFrame, past any hidden leading frames:
// by seeking where the decoder can for a start after frame 0, otherwise by
// decoding and discarding skipFrames plus the start.
func positionSource(ctx context.Context, src *source, startFrame int64, frameBytes int, windowed bool) error {
	if src.seek != nil && startFrame != 0 {
		landed, err := src.seek(startFrame)
		if err != nil {
			return decodeError("decode_seek", err)
		}
		if landed < startFrame {
			return wrapSentinel(ErrEmptyRange, nil, "decode_seek", "start is beyond the end of the audio", errors.CategoryValidation)
		}
		return nil
	}
	skip := src.skipFrames + startFrame
	if skip == 0 {
		return nil
	}
	skipped, err := discardFrames(ctx, src.reader, skip, frameBytes)
	if err != nil {
		return err
	}
	if skipped < skip {
		if windowed {
			return wrapSentinel(ErrEmptyRange, nil, "decode_discard", "start is beyond the end of the audio", errors.CategoryValidation)
		}
		return wrapSentinel(ErrUndecodableSource, nil, "decode_discard", "source holds no audio", errors.CategoryAudio)
	}
	return nil
}

// discardFrames reads and drops up to n frames, checking ctx per chunk. It
// returns the number of whole frames dropped, fewer than n when the source ends.
func discardFrames(ctx context.Context, r io.Reader, n int64, frameBytes int) (int64, error) {
	chunk := make([]byte, chunkBytes(frameBytes))
	frame := int64(frameBytes)
	remaining := n * frame
	var dropped int64
	zeroReads := 0
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return dropped / frame, err
		}
		m, err := r.Read(chunk[:min(int64(len(chunk)), remaining)])
		dropped += int64(m)
		remaining -= int64(m)
		if m == 0 && err == nil {
			zeroReads++
			if zeroReads >= maxZeroReads {
				return dropped / frame, wrapSentinel(ErrUndecodableSource, io.ErrNoProgress, "decode_discard", "decoder made no progress", errors.CategoryAudio)
			}
		} else {
			zeroReads = 0
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return dropped / frame, decodeError("decode_discard", err)
		}
	}
	return dropped / frame, nil
}

// chunkBytes returns the read chunk size, a whole number of frames.
func chunkBytes(frameBytes int) int {
	return chunkFrames * frameBytes
}

// readLimited reads r until EOF or until limit bytes are held, whichever comes
// first. The buffer starts at initialCap and doubles, never beyond limit, so its
// capacity is bounded by what the caller allowed. A trailing partial frame is
// dropped.
func readLimited(ctx context.Context, r io.Reader, limit, initialCap, frameBytes int) ([]byte, error) {
	out := make([]byte, 0, min(max(initialCap, 0), limit))
	chunk := chunkBytes(frameBytes)
	zeroReads := 0
	for len(out) < limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := min(chunk, limit-len(out))
		if cap(out)-len(out) < n {
			out = growTo(out, min(max(2*cap(out), len(out)+n), limit))
		}
		m, err := r.Read(out[len(out) : len(out)+n])
		out = out[:len(out)+m]
		if m == 0 && err == nil {
			zeroReads++
			if zeroReads >= maxZeroReads {
				return nil, wrapSentinel(ErrUndecodableSource, io.ErrNoProgress, "decode_read", "decoder made no progress", errors.CategoryAudio)
			}
		} else {
			zeroReads = 0
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, decodeError("decode_read", err)
		}
	}
	return out[:len(out)-len(out)%frameBytes], nil
}

// growTo returns s with capacity of at least newCap, keeping its contents.
func growTo(s []byte, newCap int) []byte {
	grown := make([]byte, len(s), newCap)
	copy(grown, s)
	return grown
}

// initialCapacity picks the first buffer size. It never trusts a header length:
// the estimate is bounded by what the file size makes plausible for the codec.
func initialCapacity(src *source, fileSize, limit int64, frameBytes int) int {
	plausible := fileSize*src.expansion + initialCapSlackBytes
	hint := limit
	if src.totalFrames >= 0 {
		// Clamp before multiplying: a declared count can overflow the product.
		hint = min(hint, min(src.totalFrames, hint/int64(frameBytes)+1)*int64(frameBytes))
	}
	return int(min(hint, plausible, int64(maxDecodedPCMBytes)))
}
