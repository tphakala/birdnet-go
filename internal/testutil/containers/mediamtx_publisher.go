//go:build integration

package containers

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// publisherStderrCap bounds the captured publisher stderr so a chatty or
// long-running publisher cannot grow the buffer without limit. cappedBuffer keeps
// the first N bytes (where FFmpeg prints its arg/codec startup errors) and
// discards the rest; with -loglevel error this is generous headroom for that
// diagnostic prefix.
const publisherStderrCap = 64 * 1024

// cappedBuffer is a concurrency-safe io.Writer that retains up to cap bytes of
// what is written to it and discards the rest, so it can serve as an exec.Cmd
// stderr sink read from another goroutine. It always reports a full write so the
// os/exec stderr copier keeps draining the pipe.
type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	cap int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if remaining := b.cap - b.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			b.buf.Write(p[:remaining])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// StreamPublisher manages an FFmpeg process that publishes audio to MediaMTX.
type StreamPublisher struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stderr *cappedBuffer
	// exited is flipped once the single background Wait completes; IsRunning reads
	// it (cmd.ProcessState stays nil until Wait returns, so it is not a reliable
	// liveness signal on its own).
	exited atomic.Bool
	// waitErr is the process exit error, written once before done is closed and
	// safe to read after receiving from done.
	waitErr error
	done    chan struct{}
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
	p, err := startPublisher(cmd, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to start FFmpeg tone publisher: %w", err)
	}
	return p, nil
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

	p, err := startPublisher(cmd, cancel)
	if err != nil {
		return nil, fmt.Errorf("failed to start FFmpeg publisher: %w", err)
	}
	return p, nil
}

// startPublisher captures the process stderr, starts it, and owns the single
// cmd.Wait() call in a background goroutine. Concentrating Wait() here is what
// makes IsRunning reliable (it flips exited when the process actually ends) and
// lets Stop wait on done without a second, illegal Wait() call.
func startPublisher(cmd *exec.Cmd, cancel context.CancelFunc) (*StreamPublisher, error) {
	sb := &cappedBuffer{cap: publisherStderrCap}
	cmd.Stderr = sb
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	p := &StreamPublisher{cmd: cmd, cancel: cancel, stderr: sb, done: make(chan struct{})}
	go func() {
		p.waitErr = p.cmd.Wait()
		p.exited.Store(true)
		close(p.done)
	}()
	return p, nil
}

// Stop terminates the FFmpeg publisher process. It cancels the context and waits
// for the background Wait() to finish, force-killing after a timeout. It never
// calls cmd.Wait() itself: that goroutine already owns the single Wait().
func (p *StreamPublisher) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	if p.done == nil {
		return
	}
	select {
	case <-p.done:
		// Process exited.
	case <-time.After(5 * time.Second):
		// Force kill if it hasn't stopped, then wait for the background Wait().
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-p.done
	}
}

// IsRunning reports whether the publisher process is still running. It relies on
// the background Wait() flipping exited, because cmd.ProcessState stays nil until
// Wait() returns and so cannot detect a process that has already died.
func (p *StreamPublisher) IsRunning() bool {
	return p.done != nil && !p.exited.Load()
}

// Stderr returns whatever the publisher subprocess has written to stderr so far
// (bounded by publisherStderrCap). It is safe to call while the process runs and
// after it has exited, and is meant to explain a publisher that died on bad args
// or an unsupported codec.
func (p *StreamPublisher) Stderr() string {
	if p.stderr == nil {
		return ""
	}
	return p.stderr.String()
}

// ExitError returns the process exit error once the publisher has exited. The
// boolean is false while it is still running (the error is not yet known).
func (p *StreamPublisher) ExitError() (error, bool) {
	if p.done == nil {
		return nil, false
	}
	select {
	case <-p.done:
		return p.waitErr, true
	default:
		return nil, false
	}
}
