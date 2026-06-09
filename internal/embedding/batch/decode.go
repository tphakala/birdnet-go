// Package batch implements offline embedding extraction for stored audio:
// decode files to model-rate windows, run the embedding-capable model, and
// persist tagged vectors through the embedding store. It is the offline
// counterpart of the live capture path and shares its store contract.
package batch

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// minTailFraction is the minimum fill ratio for a trailing partial window to
// be emitted. A tail below this fraction is silently dropped.
const minTailFraction = 0.25

// pcm16Divisor scales int16 PCM samples to float32 so that -32768 maps
// exactly to -1.0. This matches the live analysis path (see
// internal/analysis/process.go and internal/audiocore/convert/pcm.go) so
// batch embeddings see identically scaled input.
const pcm16Divisor = float32(32768.0)

// pcm16ToFloat32 converts a single little-endian-decoded int16 PCM sample to
// a float32 in [-1, 1].
func pcm16ToFloat32(s int16) float32 {
	return float32(s) / pcm16Divisor
}

// windowFunc is called once per analysis window. window is a slice of exactly
// windowSamples float32 values in the range [-1, 1]. The slice is owned by
// decodeWindows and is reused across calls; the callback must not retain it.
// offset is the start time of the window relative to the beginning of the file.
// Returning a non-nil error aborts decoding.
type windowFunc func(window []float32, offset time.Duration) error

// decodeWindows shells out to ffmpegPath to decode filePath as mono PCM at
// sampleRate samples per second, then partitions the stream into windows of
// exactly windowSamples float32 values, calling fn for each. A trailing partial
// window is emitted only when it is at least minTailFraction full; otherwise it
// is silently dropped. The function returns the first error from fn, any ffmpeg
// exit error (with stderr included), or a context error if ctx is cancelled.
func decodeWindows(ctx context.Context, ffmpegPath, filePath string, sampleRate, windowSamples int, fn windowFunc) error {
	if sampleRate <= 0 || windowSamples <= 0 {
		return fmt.Errorf("decodeWindows: sampleRate (%d) and windowSamples (%d) must be positive", sampleRate, windowSamples)
	}

	var stderrBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-hide_banner", "-loglevel", "error",
		"-i", filePath,
		"-f", "s16le",
		"-acodec", "pcm_s16le",
		"-ac", "1",
		"-ar", strconv.Itoa(sampleRate),
		"-",
	)
	cmd.Stderr = &stderrBuf

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg start: %w", err)
	}

	// readChunkBytes is the number of bytes to read per iteration. We use a
	// multiple of 2 (each int16 sample is 2 bytes) that is large enough for
	// efficiency.
	const readChunkBytes = 65536 // 32768 int16 samples per read

	buf := make([]byte, readChunkBytes)
	window := make([]float32, windowSamples)
	windowPos := 0 // number of samples filled in window
	windowIdx := 0 // index of the current window (0-based)
	var carryByte [1]byte
	hasCarry := false

	// killAndWait terminates ffmpeg and waits for it, discarding the wait error
	// since we already have a more informative error to return.
	killAndWait := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}

	// flushWindow delivers the current full window to fn and resets the fill
	// position for the next one.
	flushWindow := func() error {
		offset := time.Duration(windowIdx) * time.Duration(windowSamples) * time.Second / time.Duration(sampleRate)
		if err := fn(window, offset); err != nil {
			return err
		}
		windowIdx++
		windowPos = 0
		return nil
	}

	// pushSample appends one sample to the current window, flushing when full.
	pushSample := func(s int16) error {
		window[windowPos] = pcm16ToFloat32(s)
		windowPos++
		if windowPos == windowSamples {
			return flushWindow()
		}
		return nil
	}

	for {
		// Read a chunk from ffmpeg stdout.
		n, readErr := stdout.Read(buf)

		// Process whatever bytes arrived, including on a partial/final read.
		data := buf[:n]

		// If we have a leftover byte from the previous read, combine it (low
		// byte) with data[0] (high byte) into a little-endian int16.
		start := 0
		if hasCarry && len(data) > 0 {
			sample := int16(carryByte[0]) | int16(data[0])<<8
			start = 1
			hasCarry = false
			if err := pushSample(sample); err != nil {
				killAndWait()
				return err
			}
		}

		// Process remaining complete pairs of bytes.
		remaining := data[start:]
		pairs := len(remaining) / 2
		for i := range pairs {
			b := remaining[i*2 : i*2+2]
			sample := int16(binary.LittleEndian.Uint16(b))
			if err := pushSample(sample); err != nil {
				killAndWait()
				return err
			}
		}

		// Check for a leftover odd byte.
		if len(remaining)%2 == 1 {
			carryByte[0] = remaining[len(remaining)-1]
			hasCarry = true
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				// Normal end of stream.
				break
			}
			// A non-EOF read error: kill ffmpeg so it cannot block writing
			// into a full pipe, then surface the read error (or the context
			// error if the read failed because ctx was cancelled).
			killAndWait()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("ffmpeg stdout read: %w", readErr)
		}
	}

	// Wait for ffmpeg to exit. This also drains any remaining stdout data.
	waitErr := cmd.Wait()

	// Check context first: if cancelled, report that.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// If ffmpeg exited with an error, surface stderr for diagnostics.
	if waitErr != nil {
		stderr := bytes.TrimSpace(stderrBuf.Bytes())
		if len(stderr) > 0 {
			return fmt.Errorf("ffmpeg: %w: %s", waitErr, stderr)
		}
		return fmt.Errorf("ffmpeg: %w", waitErr)
	}

	// Emit a trailing partial window if it meets the minimum fill threshold.
	if windowPos > 0 {
		filled := float64(windowPos) / float64(windowSamples)
		if filled >= minTailFraction {
			// Zero-pad the remainder.
			for i := windowPos; i < windowSamples; i++ {
				window[i] = 0
			}
			if err := flushWindow(); err != nil {
				return err
			}
		}
	}

	return nil
}
