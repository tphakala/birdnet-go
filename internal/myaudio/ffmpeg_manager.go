package myaudio

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

func manageFfmpegLifecycle(ctx context.Context, config FFmpegConfig, restartChan chan struct{},
	audioLevelChan chan AudioLevelData) error {
	backoff := newBackoffStrategy(5, 5*time.Second, 2*time.Minute)

	for {
		// Check if context is done
		if ctx.Err() != nil {
			cleanupProcess(config.URL)
			return ctx.Err()
		}

		// Check if stream is still configured
		if !isStreamConfigured(config.URL) {
			log.Printf("ℹ️ Stream %s is no longer configured, stopping lifecycle manager", config.URL)
			cleanupProcess(config.URL)
			return nil
		}

		// Start FFmpeg process
		process, err := startFFmpeg(ctx, config)
		if err != nil {
			// Handle startup failure with backoff
			if !handleStartupFailure(ctx, config.URL, err, backoff) {
				return fmt.Errorf("failed to start FFmpeg after maximum attempts: %w", err)
			}
			continue
		}

		// Process started successfully
		backoff.reset()
		ffmpegProcesses.Store(config.URL, process)

		// Process audio with lifecycle management
		restartNeeded := processAudioWithLifecycle(ctx, process, config.URL, restartChan, audioLevelChan)

		// Handle restart if needed
		if restartNeeded && process != nil {
			if !handleRestartDelay(ctx, process, config.URL, restartChan) {
				return ctx.Err() // Context was cancelled during delay
			}
		}
	}
}

// Helper functions
func cleanupProcess(url string) {
	if process, exists := ffmpegProcesses.Load(url); exists {
		if p, ok := process.(*FFmpegProcess); ok && p != nil {
			p.Cleanup(url)
		} else {
			ffmpegProcesses.Delete(url)
		}
	}
}

func isStreamConfigured(url string) bool {
	for _, configURL := range conf.Setting().Realtime.RTSP.URLs {
		if configURL == url {
			return true
		}
	}
	return false
}

func handleStartupFailure(ctx context.Context, url string, err error,
	backoff *backoffStrategy) bool {
	cleanupProcess(url)

	delay, retry := backoff.nextDelay()
	if !retry {
		return false
	}

	log.Printf("⚠️ Failed to start FFmpeg for %s: %v. Retrying in %v...", url, err, delay)

	// Wait for either context cancellation or delay
	select {
	case <-ctx.Done():
		return false
	case <-time.After(delay):
		return true
	}
}

func processAudioWithLifecycle(ctx context.Context, process *FFmpegProcess, url string,
	restartChan chan struct{}, audioLevelChan chan AudioLevelData) bool {
	// Create child context for this process instance
	processCtx, processCancel := context.WithCancel(ctx)
	defer processCancel()

	// Start the audio processing
	processDone := make(chan error, 1)
	go func() {
		processDone <- process.processAudio(processCtx, url, restartChan, audioLevelChan)
	}()

	// Create a dedicated restart channel for this lifecycle
	localRestartChan := make(chan struct{}, 1)
	restartForwarderDone := forwardRestartSignals(processCtx, restartChan, localRestartChan)

	// Wait for completion or restart signal
	select {
	case <-ctx.Done():
		// Graceful shutdown
		processCancel()
		waitForCleanup(processDone, restartForwarderDone, url)
		cleanupProcess(url)
		return false

	case err := <-processDone:
		// Process ended
		processCancel()
		waitForCleanup(nil, restartForwarderDone, url)
		cleanupProcess(url)

		if !isStreamConfigured(url) {
			return false
		}

		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("⚠️ FFmpeg process ended unexpectedly: %v", err)
			return true // Needs restart
		}
		return false

	case <-localRestartChan:
		// Restart signal received
		log.Printf("🔄 Restart signal received for %s", url)
		processCancel()
		waitForCleanup(processDone, restartForwarderDone, url)
		cleanupProcess(url)
		return true // Needs restart
	}
}

func waitForCleanup(processDone <-chan error, forwarderDone <-chan struct{}, url string) {
	// Wait for process cleanup with timeout
	if processDone != nil {
		select {
		case err := <-processDone:
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("⚠️ Process for %s reported error: %v", url, err)
			}
		case <-time.After(5 * time.Second):
			log.Printf("⚠️ Timeout waiting for process cleanup for %s", url)
		}
	}

	// Wait for restart forwarder to finish
	if forwarderDone != nil {
		select {
		case <-forwarderDone:
		// Forwarder finished
		case <-time.After(2 * time.Second):
			log.Printf("⚠️ Timeout waiting for restart forwarder for %s", url)
		}
	}
}

func forwardRestartSignals(ctx context.Context, source, dest chan struct{}) <-chan struct{} {
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-source:
				// Forward restart signal with non-blocking send
				select {
				case dest <- struct{}{}:
				// Successfully forwarded
				default:
					// Channel is full, log and continue
					log.Printf("⚠️ Restart channel is full, dropping signal")
				}
			}
		}
	}()

	return done
}

func handleRestartDelay(ctx context.Context, process *FFmpegProcess, url string,
	restartChan chan struct{}) bool {
	process.updateRestartInfo()
	delay := process.getRestartDelay()

	select {
	case <-ctx.Done():
		return false
	case <-time.After(delay):
		return true
	case <-restartChan:
		log.Printf("🔄 Restart signal received during delay, restarting immediately")
		return true
	}
}
