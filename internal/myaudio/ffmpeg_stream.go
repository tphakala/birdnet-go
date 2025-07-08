package myaudio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

var (
	streamLogger *slog.Logger
	streamLevelVar = new(slog.LevelVar)
	closeStreamLogger func() error
)

func init() {
	var err error
	// Define log file path relative to working directory
	logFilePath := filepath.Join("logs", "ffmpeg-stream.log")
	initialLevel := slog.LevelInfo // Set desired initial level
	streamLevelVar.Set(initialLevel)

	// Initialize the service-specific file logger
	streamLogger, closeStreamLogger, err = logging.NewFileLogger(logFilePath, "ffmpeg-stream", streamLevelVar)
	if err != nil {
		// Fallback: Log error to standard log and use default logger
		log.Printf("Failed to initialize ffmpeg-stream file logger at %s: %v. Using default logger.", logFilePath, err)
		streamLogger = slog.Default().With("service", "ffmpeg-stream")
		closeStreamLogger = func() error { return nil } // No-op closer
	}
}

// StreamHealth represents the health status of a stream
type StreamHealth struct {
	IsHealthy        bool
	LastDataReceived time.Time
	RestartCount     int
	Error            error
}

// FFmpegStream handles a single FFmpeg process
type FFmpegStream struct {
	url              string
	transport        string
	audioChan        chan UnifiedAudioData
	
	// Process management
	cmd              *exec.Cmd
	cmdMu            sync.Mutex
	stdout           io.ReadCloser
	stderr           bytes.Buffer
	
	// State management
	ctx              context.Context
	cancel           context.CancelFunc
	restartChan      chan struct{}
	stopChan         chan struct{}
	stopped          bool
	stoppedMu        sync.RWMutex
	
	// Health tracking
	lastDataTime     time.Time
	lastDataMu       sync.RWMutex
	restartCount     int
	restartCountMu   sync.Mutex
	
	// Backoff for restarts
	backoffDuration  time.Duration
	maxBackoff       time.Duration
}

// NewFFmpegStream creates a new FFmpeg stream handler
func NewFFmpegStream(url, transport string, audioChan chan UnifiedAudioData) *FFmpegStream {
	return &FFmpegStream{
		url:             url,
		transport:       transport,
		audioChan:       audioChan,
		restartChan:     make(chan struct{}, 1),
		stopChan:        make(chan struct{}),
		backoffDuration: 5 * time.Second,
		maxBackoff:      2 * time.Minute,
		lastDataTime:    time.Now(),
	}
}

// Run starts and manages the FFmpeg process lifecycle
func (s *FFmpegStream) Run(parentCtx context.Context) {
	s.ctx, s.cancel = context.WithCancel(parentCtx)
	defer s.cancel()
	
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.stopChan:
			return
		default:
			// Start FFmpeg process
			if err := s.startProcess(); err != nil {
				streamLogger.Error("failed to start FFmpeg process",
					"url", privacy.SanitizeRTSPUrl(s.url),
					"error", err,
					"operation", "start_process")
				log.Printf("❌ Failed to start FFmpeg for %s: %v", privacy.SanitizeRTSPUrl(s.url), err)
				s.handleRestartBackoff()
				continue
			}
			
			// Process audio data
			err := s.processAudio()
			
			// Check if we should stop
			s.stoppedMu.RLock()
			stopped := s.stopped
			s.stoppedMu.RUnlock()
			
			if stopped {
				return
			}
			
			// Handle process exit
			if err != nil && !errors.Is(err, context.Canceled) {
				streamLogger.Warn("FFmpeg process ended",
					"url", privacy.SanitizeRTSPUrl(s.url),
					"error", err,
					"operation", "process_ended")
				log.Printf("⚠️ FFmpeg process ended for %s: %v", privacy.SanitizeRTSPUrl(s.url), err)
			}
			
			// Apply backoff before restart
			s.handleRestartBackoff()
		}
	}
}

// startProcess starts the FFmpeg process
func (s *FFmpegStream) startProcess() error {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	
	// Validate FFmpeg path
	settings := conf.Setting().Realtime.Audio
	if err := validateFFmpegPath(settings.FfmpegPath); err != nil {
		return errors.New(fmt.Errorf("FFmpeg validation failed: %w", err)).
			Category(errors.CategoryValidation).
			Component("ffmpeg-stream").
			Build()
	}
	
	// Get FFmpeg format settings
	sampleRate, numChannels, format := getFFmpegFormat(conf.SampleRate, conf.NumChannels, conf.BitDepth)
	
	// Create FFmpeg command
	s.cmd = exec.CommandContext(s.ctx, settings.FfmpegPath,
		"-rtsp_transport", s.transport,
		"-i", s.url,
		"-loglevel", "error",
		"-vn",
		"-f", format,
		"-ar", sampleRate,
		"-ac", numChannels,
		"-hide_banner",
		"pipe:1",
	)
	
	// Setup process group
	setupProcessGroup(s.cmd)
	
	// Capture stderr
	s.stderr.Reset()
	s.cmd.Stderr = &s.stderr
	
	// Get stdout pipe
	var err error
	s.stdout, err = s.cmd.StdoutPipe()
	if err != nil {
		return errors.New(fmt.Errorf("failed to create stdout pipe: %w", err)).
			Category(errors.CategorySystem).
			Component("ffmpeg-stream").
			Build()
	}
	
	// Start process
	if err := s.cmd.Start(); err != nil {
		return errors.New(fmt.Errorf("failed to start FFmpeg: %w", err)).
			Category(errors.CategorySystem).
			Component("ffmpeg-stream").
			Build()
	}
	
	streamLogger.Info("FFmpeg process started",
		"url", privacy.SanitizeRTSPUrl(s.url),
		"pid", s.cmd.Process.Pid,
		"transport", s.transport,
		"operation", "start_process")
	
	log.Printf("✅ FFmpeg started for %s (PID: %d)", privacy.SanitizeRTSPUrl(s.url), s.cmd.Process.Pid)
	return nil
}

// processAudio reads and processes audio data from FFmpeg
func (s *FFmpegStream) processAudio() error {
	buf := make([]byte, 32768)
	startTime := time.Now()
	
	for {
		// Set read deadline for timeout handling
		n, err := s.stdout.Read(buf)
		if err != nil {
			// Check if process exited too quickly
			if time.Since(startTime) < 5*time.Second {
				stderrOutput := s.stderr.String()
				return errors.New(fmt.Errorf("FFmpeg exited too quickly: %s", stderrOutput)).
					Category(errors.CategoryRTSP).
					Component("ffmpeg-stream").
					Build()
			}
			
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return nil // Normal shutdown
			}
			
			return errors.New(fmt.Errorf("error reading from FFmpeg: %w", err)).
				Category(errors.CategoryRTSP).
				Component("ffmpeg-stream").
				Build()
		}
		
		if n > 0 {
			// Update last data time
			s.updateLastDataTime()
			
			// Process the audio data
			if err := s.handleAudioData(buf[:n]); err != nil {
				log.Printf("⚠️ Error processing audio data for %s: %v", privacy.SanitizeRTSPUrl(s.url), err)
			}
		}
		
		// Check for restart signal
		select {
		case <-s.restartChan:
			streamLogger.Info("restart requested",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"operation", "restart_requested")
			log.Printf("🔄 Restart requested for %s", privacy.SanitizeRTSPUrl(s.url))
			s.cleanupProcess()
			return nil
		case <-s.ctx.Done():
			s.cleanupProcess()
			return s.ctx.Err()
		default:
			// Continue processing
		}
	}
}

// handleAudioData processes a chunk of audio data
func (s *FFmpegStream) handleAudioData(data []byte) error {
	// Write to analysis buffer
	if err := WriteToAnalysisBuffer(s.url, data); err != nil {
		return errors.New(fmt.Errorf("failed to write to analysis buffer: %w", err)).
			Category(errors.CategoryAudio).
			Component("ffmpeg-stream").
			Build()
	}
	
	// Write to capture buffer
	if err := WriteToCaptureBuffer(s.url, data); err != nil {
		return errors.New(fmt.Errorf("failed to write to capture buffer: %w", err)).
			Category(errors.CategoryAudio).
			Component("ffmpeg-stream").
			Build()
	}
	
	// Broadcast to WebSocket clients
	broadcastAudioData(s.url, data)
	
	// Calculate audio level
	audioLevel := calculateAudioLevel(data, s.url, "")
	
	// Create unified audio data
	unifiedData := UnifiedAudioData{
		AudioLevel: audioLevel,
		Timestamp:  time.Now(),
	}
	
	// Process sound level if enabled
	if conf.Setting().Realtime.Audio.SoundLevel.Enabled {
		if soundLevel, err := ProcessSoundLevelData(s.url, data); err != nil {
			streamLogger.Debug("failed to process sound level data",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"error", err,
				"operation", "process_sound_level")
			log.Printf("⚠️ Error processing sound level for %s: %v", s.url, err)
		} else if soundLevel != nil {
			unifiedData.SoundLevel = soundLevel
		}
	}
	
	// Send to audio channel (non-blocking)
	select {
	case s.audioChan <- unifiedData:
		// Data sent successfully
	default:
		// Channel full, drop data to avoid blocking
	}
	
	// Update stream health
	UpdateStreamDataReceived(s.url)
	
	return nil
}

// cleanupProcess cleans up the FFmpeg process
func (s *FFmpegStream) cleanupProcess() {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	
	// Close stdout
	if s.stdout != nil {
		if err := s.stdout.Close(); err != nil {
			// Log but don't fail - process cleanup is more important
			streamLogger.Debug("failed to close stdout",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"error", err,
				"operation", "cleanup_process")
			log.Printf("⚠️ Error closing stdout for %s: %v", privacy.SanitizeRTSPUrl(s.url), err)
		}
	}
	
	// Kill process
	if err := killProcessGroup(s.cmd); err != nil {
		if killErr := s.cmd.Process.Kill(); killErr != nil {
			// Only log if kill also fails
			log.Printf("⚠️ Error killing process for %s: %v", privacy.SanitizeRTSPUrl(s.url), killErr)
		}
	}
	
	// Wait for process to exit
	done := make(chan struct{})
	go func() {
		if err := s.cmd.Wait(); err != nil {
			// This is expected when we kill the process
			// Only log if it's not an expected exit status
			if !strings.Contains(err.Error(), "signal: killed") {
				log.Printf("⚠️ Process wait error for %s: %v", privacy.SanitizeRTSPUrl(s.url), err)
			}
		}
		close(done)
	}()
	
	select {
	case <-done:
		streamLogger.Info("FFmpeg process stopped",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"operation", "cleanup_process")
		log.Printf("🛑 FFmpeg process stopped for %s", privacy.SanitizeRTSPUrl(s.url))
	case <-time.After(5 * time.Second):
		streamLogger.Warn("FFmpeg process cleanup timeout",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"operation", "cleanup_process")
		log.Printf("⚠️ FFmpeg process cleanup timeout for %s", privacy.SanitizeRTSPUrl(s.url))
	}
	
	s.cmd = nil
}

// handleRestartBackoff handles exponential backoff for restarts
func (s *FFmpegStream) handleRestartBackoff() {
	s.restartCountMu.Lock()
	s.restartCount++
	backoff := s.backoffDuration * time.Duration(1<<uint(s.restartCount-1))
	if backoff > s.maxBackoff {
		backoff = s.maxBackoff
	}
	s.restartCountMu.Unlock()
	
	streamLogger.Debug("applying restart backoff",
		"url", privacy.SanitizeRTSPUrl(s.url),
		"backoff_ms", backoff.Milliseconds(),
		"restart_count", s.restartCount,
		"operation", "restart_backoff")
	
	log.Printf("⏳ Waiting %v before restart attempt #%d for %s", backoff, s.restartCount, privacy.SanitizeRTSPUrl(s.url))
	
	select {
	case <-time.After(backoff):
		// Continue with restart
	case <-s.ctx.Done():
		// Context cancelled
	case <-s.stopChan:
		// Stop requested
	}
}

// Stop stops the stream
func (s *FFmpegStream) Stop() {
	s.stoppedMu.Lock()
	s.stopped = true
	s.stoppedMu.Unlock()
	
	// Signal stop
	close(s.stopChan)
	
	// Cancel context
	if s.cancel != nil {
		s.cancel()
	}
	
	// Cleanup process
	s.cleanupProcess()
}

// Restart requests a stream restart
func (s *FFmpegStream) Restart() {
	// Reset restart count on manual restart
	s.restartCountMu.Lock()
	s.restartCount = 0
	s.restartCountMu.Unlock()
	
	// Send restart signal (non-blocking)
	select {
	case s.restartChan <- struct{}{}:
		// Signal sent
	default:
		// Channel full, restart already pending
	}
}

// GetHealth returns the current health status
func (s *FFmpegStream) GetHealth() StreamHealth {
	s.lastDataMu.RLock()
	lastData := s.lastDataTime
	s.lastDataMu.RUnlock()
	
	s.restartCountMu.Lock()
	restarts := s.restartCount
	s.restartCountMu.Unlock()
	
	// Consider unhealthy if no data for 60 seconds
	isHealthy := time.Since(lastData) < 60*time.Second
	
	return StreamHealth{
		IsHealthy:        isHealthy,
		LastDataReceived: lastData,
		RestartCount:     restarts,
	}
}

// updateLastDataTime updates the last data received timestamp
func (s *FFmpegStream) updateLastDataTime() {
	s.lastDataMu.Lock()
	s.lastDataTime = time.Now()
	s.lastDataMu.Unlock()
}