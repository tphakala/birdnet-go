//go:build integration

package containers

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// StreamPublisher manages an FFmpeg process that publishes audio to MediaMTX.
type StreamPublisher struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
}

// PublishWAVToMediaMTX starts FFmpeg to publish a WAV file to MediaMTX via RTSP.
// The stream loops indefinitely until Stop() is called.
// Uses libopus codec for RTSP compatibility (pcm_s16le not supported over RTSP).
// The caller should wait a few seconds after calling this for the stream to become
// available on all MediaMTX protocols (RTSP, RTMP, HLS).
func PublishWAVToMediaMTX(ctx context.Context, wavPath, rtspURL string) (*StreamPublisher, error) {
	pubCtx, cancel := context.WithCancel(ctx)

	//nolint:gosec // G204: paths are from test infrastructure, not user input
	cmd := exec.CommandContext(pubCtx, "ffmpeg",
		"-re",                // Read input at native framerate (real-time playback)
		"-stream_loop", "-1", // Loop forever
		"-i", wavPath, // Input file
		"-c:a", "libopus", // Opus codec (RTSP-compatible, low CPU)
		"-b:a", "64k", // Bitrate
		"-ar", "48000", // Sample rate matching BirdNET-Go
		"-ac", "1", // Mono
		"-f", "rtsp", // Output format
		"-rtsp_transport", "tcp", // Use TCP for Docker compatibility
		rtspURL, // Destination
	)

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start FFmpeg publisher: %w", err)
	}

	return &StreamPublisher{cmd: cmd, cancel: cancel}, nil
}

// Stop terminates the FFmpeg publisher process.
func (p *StreamPublisher) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		// Wait with a timeout to avoid hanging
		done := make(chan error, 1)
		go func() {
			done <- p.cmd.Wait()
		}()

		select {
		case <-done:
			// Process exited
		case <-time.After(5 * time.Second):
			// Force kill if it hasn't stopped
			_ = p.cmd.Process.Kill()
			<-done
		}
	}
}

// IsRunning checks if the publisher process is still running.
func (p *StreamPublisher) IsRunning() bool {
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	// ProcessState is nil while process is running
	return p.cmd.ProcessState == nil
}
