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
	"math"
	"os/exec"
	"time"
)

// minTailFraction is the minimum fill ratio for a trailing partial window to
// be emitted. A tail below this fraction is silently dropped.
const minTailFraction = 0.25

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
	var stderrBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-hide_banner", "-loglevel", "error",
		"-i", filePath,
		"-f", "s16le",
		"-acodec", "pcm_s16le",
		"-ac", "1",
		"-ar", fmt.Sprintf("%d", sampleRate),
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

	var callbackErr error

	for {
		// Read a chunk from ffmpeg stdout.
		n, readErr := stdout.Read(buf)

		// Process whatever bytes arrived, including on a partial/final read.
		data := buf[:n]

		// If we have a leftover byte from the previous read, prepend it by
		// handling it as the low byte of the next sample.
		start := 0
		if hasCarry && len(data) > 0 {
			// Combine the carry byte (low byte) with data[0] (high byte) to
			// form a little-endian int16: low byte first, then high byte.
			sample := int16(carryByte[0]) | int16(data[0])<<8
			window[windowPos] = float32(sample) / math.MaxInt16
			windowPos++
			start = 1
			hasCarry = false

			if windowPos == windowSamples {
				offset := time.Duration(windowIdx) * time.Duration(windowSamples) * time.Second / time.Duration(sampleRate)
				if err := fn(window, offset); err != nil {
					callbackErr = err
					killAndWait()
					return callbackErr
				}
				windowIdx++
				windowPos = 0
			}
		}

		// Process remaining complete pairs of bytes.
		remaining := data[start:]
		pairs := len(remaining) / 2
		for i := range pairs {
			b := remaining[i*2 : i*2+2]
			sample := int16(binary.LittleEndian.Uint16(b))
			window[windowPos] = float32(sample) / math.MaxInt16
			windowPos++

			if windowPos == windowSamples {
				offset := time.Duration(windowIdx) * time.Duration(windowSamples) * time.Second / time.Duration(sampleRate)
				if err := fn(window, offset); err != nil {
					callbackErr = err
					killAndWait()
					return callbackErr
				}
				windowIdx++
				windowPos = 0
			}
		}

		// Check for a leftover odd byte.
		if len(remaining)%2 == 1 {
			carryByte[0] = remaining[len(remaining)-1]
			hasCarry = true
		}

		if readErr != nil {
			// Any error here (including io.EOF) means the stream is done.
			break
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
			offset := time.Duration(windowIdx) * time.Duration(windowSamples) * time.Second / time.Duration(sampleRate)
			if err := fn(window, offset); err != nil {
				return err
			}
		}
	}

	return nil
}
