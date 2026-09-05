//go:build integration

package containers

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// StreamPublisher manages an FFmpeg process that publishes audio to MediaMTX.
type StreamPublisher struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
}

// Publisher defaults for the tone generator.
const (
	defaultToneFrequencyHz = 1000.0
	defaultToneSampleRate  = 48000
	defaultToneChannels    = 1
	defaultToneLevelDBFS   = -12.0
	opusPublishBitrate     = "64k"
	aacPublishBitrate      = "96k"
)

// ToneOptions configures PublishToneToMediaMTX. Codec is the FFmpeg encoder name
// (for example "libopus", "pcm_mulaw", "pcm_alaw", "aac", "pcm_s16be"), which
// lets a caller cover the RTSP codec matrix from one helper. Zero fields fall
// back to a 1 kHz mono 48 kHz tone at -12 dBFS.
type ToneOptions struct {
	Codec       string
	SampleRate  int
	Channels    int
	FrequencyHz float64
	LevelDBFS   float64
	WithVideo   bool
}

func (o *ToneOptions) applyDefaults() {
	if o.FrequencyHz == 0 {
		o.FrequencyHz = defaultToneFrequencyHz
	}
	if o.SampleRate == 0 {
		o.SampleRate = defaultToneSampleRate
	}
	if o.Channels == 0 {
		o.Channels = defaultToneChannels
	}
	if o.LevelDBFS == 0 {
		o.LevelDBFS = defaultToneLevelDBFS
	}
	if o.Codec == "" {
		o.Codec = "libopus"
	}
}

// buildToneArgs assembles the FFmpeg argument list that publishes a synthesized
// tone (and optionally a test video track) to an RTSP URL.
func buildToneArgs(rtspURL string, o ToneOptions) []string {
	args := []string{"-hide_banner", "-loglevel", "error"}

	// Audio input: a synthesized sine, read in real time.
	sine := fmt.Sprintf("sine=frequency=%.0f:sample_rate=%d", o.FrequencyHz, o.SampleRate)
	args = append(args, "-re", "-f", "lavfi", "-i", sine)

	if o.WithVideo {
		args = append(args, "-re", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=15", "-map", "1:v", "-map", "0:a")
	}

	// Attenuate to the requested level so the tone is not full-scale.
	args = append(args, "-filter:a", fmt.Sprintf("volume=%gdB", o.LevelDBFS), "-c:a", o.Codec)
	switch o.Codec {
	case "libopus":
		args = append(args, "-b:a", opusPublishBitrate)
	case "aac":
		args = append(args, "-b:a", aacPublishBitrate)
	}
	args = append(args, "-ar", strconv.Itoa(o.SampleRate), "-ac", strconv.Itoa(o.Channels))

	if o.WithVideo {
		args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-pix_fmt", "yuv420p", "-g", "30")
	}

	args = append(args, "-f", "rtsp", "-rtsp_transport", "tcp", rtspURL)
	return args
}

// PublishToneToMediaMTX starts FFmpeg publishing a synthesized tone to MediaMTX
// via RTSP in the requested codec, looping until Stop is called. The caller
// should wait a couple of seconds for the stream to register on the server. It
// generalizes PublishWAVToMediaMTX across the RTSP codec matrix (Opus, G.711
// mu/A-law, AAC, L16) plus an optional video track for media-mode tests.
func PublishToneToMediaMTX(ctx context.Context, rtspURL string, opts ToneOptions) (*StreamPublisher, error) {
	opts.applyDefaults()
	pubCtx, cancel := context.WithCancel(ctx)

	//nolint:gosec // G204: args are built from test infrastructure, not user input
	cmd := exec.CommandContext(pubCtx, "ffmpeg", buildToneArgs(rtspURL, opts)...)
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start FFmpeg tone publisher: %w", err)
	}
	return &StreamPublisher{cmd: cmd, cancel: cancel}, nil
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
