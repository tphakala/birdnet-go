package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// logger for the FFmpeg package
var logger *slog.Logger

func init() {
	logger = logging.ForService("audiocore-ffmpeg")
	
	// Defensive initialization for early startup
	if logger == nil {
		logger = slog.Default().With("service", "audiocore-ffmpeg")
	}
}

// process implements the Process interface
type process struct {
	id           string
	config       *ProcessConfig
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       io.ReadCloser
	stderr       io.ReadCloser
	audioOutput  chan []byte
	errorOutput  chan error
	metrics      ProcessMetrics
	running      atomic.Bool
	mu           sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
	startOnce    sync.Once
	stopOnce     sync.Once
	closeOnce    sync.Once
	startErr     error // Stores the error from the first Start() call
}

// NewProcess creates a new FFmpeg process
func NewProcess(config *ProcessConfig) Process {
	return &process{
		id:          config.ID,
		config:      config,
		audioOutput: make(chan []byte, config.BufferSize),
		errorOutput: make(chan error, 10),
	}
}

// ID returns the unique identifier for this process
func (p *process) ID() string {
	return p.id
}

// Start starts the FFmpeg process
func (p *process) Start(ctx context.Context) error {
	p.startOnce.Do(func() {
		p.ctx, p.cancel = context.WithCancel(ctx)
		p.startErr = p.start()
	})
	return p.startErr
}

func (p *process) start() error {
	startTime := time.Now()
	
	logger.Debug("starting FFmpeg process",
		"process_id", p.id,
		"input_url", privacy.ScrubMessage(p.config.InputURL),
		"output_format", p.config.OutputFormat,
		"sample_rate", p.config.SampleRate,
		"channels", p.config.Channels,
		"buffer_size", p.config.BufferSize)
	
	// Build FFmpeg command
	args := p.buildFFmpegArgs()
	logger.Debug("built FFmpeg command",
		"process_id", p.id,
		"command", p.config.FFmpegPath,
		"arg_count", len(args))
	
	p.cmd = exec.CommandContext(p.ctx, p.config.FFmpegPath, args...)

	// Set up pipes
	var err error
	p.stdin, err = p.cmd.StdinPipe()
	if err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategoryConfiguration).
			Context("operation", "create-stdin-pipe").
			Context("process_id", p.id).
			Build()
	}

	p.stdout, err = p.cmd.StdoutPipe()
	if err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategoryConfiguration).
			Context("operation", "create-stdout-pipe").
			Context("process_id", p.id).
			Build()
	}

	p.stderr, err = p.cmd.StderrPipe()
	if err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategoryConfiguration).
			Context("operation", "create-stderr-pipe").
			Context("process_id", p.id).
			Build()
	}

	// Start the process
	if err := p.cmd.Start(); err != nil {
		logger.Error("failed to start FFmpeg process",
			"process_id", p.id,
			"error", err,
			"command", p.config.FFmpegPath,
			"startup_duration_ms", time.Since(startTime).Milliseconds())
		
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "start-ffmpeg").
			Context("process_id", p.id).
			Context("command", p.config.FFmpegPath).
			Context("args", fmt.Sprintf("%v", args)).
			Build()
	}

	// Update metrics
	p.mu.Lock()
	p.metrics.StartTime = time.Now()
	p.metrics.RestartCount++
	p.metrics.LastRestart = time.Now()
	p.mu.Unlock()

	p.running.Store(true)

	logger.Info("FFmpeg process started successfully",
		"process_id", p.id,
		"pid", p.cmd.Process.Pid,
		"restart_count", p.metrics.RestartCount,
		"startup_duration_ms", time.Since(startTime).Milliseconds())

	// Start goroutines to read output
	go p.readAudioOutput()
	go p.readErrorOutput()

	return nil
}

// Stop gracefully stops the FFmpeg process
func (p *process) Stop() error {
	var stopErr error
	p.stopOnce.Do(func() {
		stopErr = p.stop()
	})
	return stopErr
}

func (p *process) stop() error {
	if !p.running.Load() {
		logger.Debug("stop called on already stopped process", "process_id", p.id)
		return nil
	}

	stopTime := time.Now()
	var uptime time.Duration
	p.mu.RLock()
	if !p.metrics.StartTime.IsZero() {
		uptime = stopTime.Sub(p.metrics.StartTime)
	}
	p.mu.RUnlock()

	logger.Info("stopping FFmpeg process",
		"process_id", p.id,
		"uptime_ms", uptime.Milliseconds())

	// Cancel context to signal goroutines
	if p.cancel != nil {
		p.cancel()
	}

	// Close stdin to signal FFmpeg to exit
	if p.stdin != nil {
		if err := p.stdin.Close(); err != nil {
			logger.Warn("failed to close stdin for FFmpeg process",
				"process_id", p.id,
				"error", err)
		}
	}

	// Wait for process to exit with timeout
	done := make(chan error, 1)
	go func() {
		if p.cmd != nil && p.cmd.Process != nil {
			done <- p.cmd.Wait()
		} else {
			done <- nil
		}
	}()

	select {
	case err := <-done:
		p.running.Store(false)
		p.closeChannels()
		
		shutdownDuration := time.Since(stopTime)
		if err != nil && err.Error() != "signal: killed" {
			logger.Error("FFmpeg process exited with error",
				"process_id", p.id,
				"error", err,
				"uptime_ms", uptime.Milliseconds(),
				"shutdown_duration_ms", shutdownDuration.Milliseconds())
			
			return errors.New(err).
				Component("audiocore").
				Category(errors.CategorySystem).
				Context("operation", "stop-ffmpeg").
				Context("process_id", p.id).
				Build()
		}
		
		logger.Info("FFmpeg process stopped successfully",
			"process_id", p.id,
			"uptime_ms", uptime.Milliseconds(),
			"shutdown_duration_ms", shutdownDuration.Milliseconds())
		return nil
		
	case <-time.After(5 * time.Second):
		// Force kill if graceful shutdown fails
		logger.Warn("FFmpeg process did not respond to graceful shutdown, forcing kill",
			"process_id", p.id,
			"uptime_ms", uptime.Milliseconds())
		
		if p.cmd != nil && p.cmd.Process != nil {
			if err := p.cmd.Process.Kill(); err != nil {
				logger.Error("failed to forcefully kill FFmpeg process",
					"process_id", p.id,
					"error", err)
				
				return errors.New(err).
					Component("audiocore").
					Category(errors.CategorySystem).
					Context("operation", "kill-ffmpeg").
					Context("process_id", p.id).
					Build()
			}
		}
		
		p.running.Store(false)
		p.closeChannels()
		
		logger.Info("FFmpeg process forcefully terminated",
			"process_id", p.id,
			"uptime_ms", uptime.Milliseconds(),
			"shutdown_duration_ms", time.Since(stopTime).Milliseconds())
		return nil
	}
}

// closeChannels safely closes the output channels only once
func (p *process) closeChannels() {
	p.closeOnce.Do(func() {
		close(p.audioOutput)
		close(p.errorOutput)
	})
}

// Wait waits for the process to exit
func (p *process) Wait() error {
	if p.cmd == nil {
		return nil
	}
	return p.cmd.Wait()
}

// IsRunning returns true if the process is currently running
func (p *process) IsRunning() bool {
	return p.running.Load()
}

// AudioOutput returns the channel for audio data output
func (p *process) AudioOutput() <-chan []byte {
	return p.audioOutput
}

// ErrorOutput returns the channel for error messages
func (p *process) ErrorOutput() <-chan error {
	return p.errorOutput
}

// Metrics returns current process metrics
func (p *process) Metrics() ProcessMetrics {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	metrics := p.metrics
	if p.running.Load() {
		metrics.Uptime = time.Since(metrics.StartTime)
	}
	return metrics
}

// buildFFmpegArgs builds the FFmpeg command arguments
func (p *process) buildFFmpegArgs() []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
	}

	// Input options
	if p.config.InputURL != "" {
		// Add specific options for RTSP streams
		if isRTSPURL(p.config.InputURL) {
			args = append(args,
				"-rtsp_transport", "tcp",
				"-buffer_size", "2048000",
				"-max_delay", "5000000",
				"-reorder_queue_size", "16384",
			)
		}
		args = append(args, "-i", p.config.InputURL)
	}

	// Output format options
	args = append(args,
		"-f", p.config.OutputFormat,
		"-ar", fmt.Sprintf("%d", p.config.SampleRate),
		"-ac", fmt.Sprintf("%d", p.config.Channels),
	)

	// Add bit depth if specified
	if p.config.BitDepth > 0 {
		switch p.config.BitDepth {
		case 16:
			args = append(args, "-sample_fmt", "s16")
		case 24:
			args = append(args, "-sample_fmt", "s24")
		case 32:
			args = append(args, "-sample_fmt", "s32")
		}
	}

	// Add any extra arguments
	args = append(args, p.config.ExtraArgs...)

	// Output to stdout
	args = append(args, "pipe:1")

	return args
}

// readAudioOutput reads audio data from stdout
func (p *process) readAudioOutput() {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("panic in audio reader",
				"process_id", p.id,
				"panic", r)
			
			p.errorOutput <- errors.New(fmt.Errorf("panic in audio reader: %v", r)).
				Component("audiocore").
				Category(errors.CategorySystem).
				Context("process_id", p.id).
				Build()
		}
	}()

	logger.Debug("started audio output reader", "process_id", p.id)

	buffer := make([]byte, p.config.BufferSize)
	lastLogTime := time.Now()
	bytesReadSinceLastLog := int64(0)
	framesReadSinceLastLog := int64(0)
	
	for {
		select {
		case <-p.ctx.Done():
			return
		default:
			n, err := p.stdout.Read(buffer)
			if err != nil {
				if err != io.EOF {
					logger.Error("failed to read audio data from FFmpeg",
						"process_id", p.id,
						"error", err)
					
					p.errorOutput <- errors.New(err).
						Component("audiocore").
						Category(errors.CategoryAudio).
						Context("operation", "read-audio").
						Context("process_id", p.id).
						Build()
				}
				logger.Debug("audio output reader exiting", 
					"process_id", p.id,
					"reason", err.Error())
				return
			}

			if n > 0 {
				// Send copy of data to avoid race conditions
				data := make([]byte, n)
				copy(data, buffer[:n])
				
				select {
				case p.audioOutput <- data:
					p.mu.Lock()
					p.metrics.BytesRead += int64(n)
					p.metrics.FramesRead++
					p.mu.Unlock()
					
					// Update local counters for periodic logging
					bytesReadSinceLastLog += int64(n)
					framesReadSinceLastLog++
					
					// Log audio data flow metrics every 30 seconds
					if time.Since(lastLogTime) >= 30*time.Second {
						logger.Debug("audio data flow metrics",
							"process_id", p.id,
							"bytes_read_last_30s", bytesReadSinceLastLog,
							"frames_read_last_30s", framesReadSinceLastLog,
							"avg_frame_size_bytes", func() int64 {
								if framesReadSinceLastLog > 0 {
									return bytesReadSinceLastLog / framesReadSinceLastLog
								}
								return 0
							}(),
							"data_rate_bytes_per_sec", bytesReadSinceLastLog/30)
						
						// Reset counters
						lastLogTime = time.Now()
						bytesReadSinceLastLog = 0
						framesReadSinceLastLog = 0
					}
					
				case <-p.ctx.Done():
					return
				default:
					// Channel might be closed or full, skip this data
					return
				}
			}
		}
	}
}

// readErrorOutput reads error messages from stderr
func (p *process) readErrorOutput() {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("panic in error reader",
				"process_id", p.id,
				"panic", r)
			
			p.errorOutput <- errors.New(fmt.Errorf("panic in error reader: %v", r)).
				Component("audiocore").
				Category(errors.CategorySystem).
				Context("process_id", p.id).
				Build()
		}
	}()

	logger.Debug("started error output reader", "process_id", p.id)

	scanner := bufio.NewScanner(p.stderr)
	for scanner.Scan() {
		select {
		case <-p.ctx.Done():
			return
		default:
			line := scanner.Text()
			if line != "" {
				// Scrub potentially sensitive information from FFmpeg output
				scrubbedLine := privacy.ScrubMessage(line)
				
				logger.Debug("FFmpeg stderr output",
					"process_id", p.id,
					"message", scrubbedLine)
				
				err := errors.New(fmt.Errorf("ffmpeg: %s", line)).
					Component("audiocore").
					Category(errors.CategoryAudio).
					Context("process_id", p.id).
					Build()
				
				p.mu.Lock()
				p.metrics.LastError = err
				p.mu.Unlock()
				
				select {
				case p.errorOutput <- err:
				case <-p.ctx.Done():
					return
				default:
					// Channel might be closed or full, skip this error
					logger.Debug("error output channel full or closed, dropping message",
						"process_id", p.id)
					return
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Error("error reading from FFmpeg stderr",
			"process_id", p.id,
			"error", err)
		
		enhancedErr := errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("operation", "read-stderr").
			Context("process_id", p.id).
			Build()
		
		select {
		case p.errorOutput <- enhancedErr:
		case <-p.ctx.Done():
			return
		default:
			// Channel might be closed, skip this error
			logger.Debug("error output channel closed, cannot send scanner error",
				"process_id", p.id)
		}
	}
	
	logger.Debug("error output reader exiting", "process_id", p.id)
}

// isRTSPURL checks if the URL is an RTSP stream
func isRTSPURL(url string) bool {
	return strings.HasPrefix(url, "rtsp://") || strings.HasPrefix(url, "rtsps://")
}