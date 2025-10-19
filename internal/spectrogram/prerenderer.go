// Package spectrogram provides background pre-rendering of spectrograms to eliminate UI lag.
// Pre-rendering feeds PCM data directly to Sox (bypassing FFmpeg) in a background worker pool.
package spectrogram

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

const (
	// Worker pool size - conservative for background processing
	defaultWorkers = 2

	// Job queue size - buffer to handle burst detections
	defaultQueueSize = 100

	// Timeout for individual spectrogram generation
	generationTimeout = 60 * time.Second

	// Timeout for graceful shutdown
	shutdownTimeout = 10 * time.Second
)

// validSizes maps size strings to pixel widths (single source of truth)
var validSizes = map[string]int{
	"sm": 400,  // Small - 400px (default, matches frontend RecentDetectionsCard)
	"md": 800,  // Medium - 800px
	"lg": 1000, // Large - 1000px
	"xl": 1200, // Extra Large - 1200px
}

// PreRenderer manages background spectrogram pre-rendering.
// It uses a worker pool to process jobs without blocking the detection pipeline.
type PreRenderer struct {
	settings *conf.Settings
	sfs      *securefs.SecureFS
	logger   *slog.Logger

	// Lifecycle management
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Worker pool
	jobs    chan *Job
	workers int

	// Statistics
	mu    sync.RWMutex
	stats Stats
}

// Job represents a single spectrogram generation task.
type Job struct {
	PCMData   []byte    // Raw PCM data from memory (s16le, 48kHz, mono)
	ClipPath  string    // Relative path to audio clip (for spectrogram naming)
	NoteID    uint      // For logging correlation
	Timestamp time.Time // Job submission time
}

// Stats tracks pre-rendering statistics.
type Stats struct {
	Queued    int64 // Number of jobs submitted
	Completed int64 // Number of spectrograms successfully generated
	Failed    int64 // Number of failed generations
	Skipped   int64 // Number skipped (already exist)
}

// NewPreRenderer creates a new pre-renderer instance.
// The parentCtx is used for lifecycle management and cancellation.
func NewPreRenderer(parentCtx context.Context, settings *conf.Settings, sfs *securefs.SecureFS, logger *slog.Logger) *PreRenderer {
	ctx, cancel := context.WithCancel(parentCtx)

	return &PreRenderer{
		settings: settings,
		sfs:      sfs,
		logger:   logger,
		ctx:      ctx,
		cancel:   cancel,
		jobs:     make(chan *Job, defaultQueueSize),
		workers:  defaultWorkers,
	}
}

// Start initializes the worker pool and begins processing jobs.
func (pr *PreRenderer) Start() {
	pr.logger.Info("Starting spectrogram pre-renderer",
		"workers", pr.workers,
		"queue_size", defaultQueueSize,
		"size", pr.settings.Realtime.Dashboard.Spectrogram.Size,
		"raw", pr.settings.Realtime.Dashboard.Spectrogram.Raw)

	for i := 0; i < pr.workers; i++ {
		pr.wg.Add(1)
		go pr.worker(i)
	}
}

// Stop gracefully shuts down the pre-renderer.
// It waits for in-flight jobs to complete (up to shutdownTimeout).
func (pr *PreRenderer) Stop() {
	pr.logger.Info("Stopping spectrogram pre-renderer")

	// Cancel context to signal workers to stop
	pr.cancel()

	// Close job channel to prevent new submissions
	close(pr.jobs)

	// Wait for workers to finish with timeout
	done := make(chan struct{})
	go func() {
		pr.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		pr.logger.Info("Spectrogram pre-renderer stopped gracefully")
	case <-time.After(shutdownTimeout):
		pr.logger.Warn("Spectrogram pre-renderer shutdown timeout",
			"timeout", shutdownTimeout)
	}

	// Log final stats
	stats := pr.GetStats()
	pr.logger.Info("Spectrogram pre-renderer final stats",
		"queued", stats.Queued,
		"completed", stats.Completed,
		"failed", stats.Failed,
		"skipped", stats.Skipped)
}

// Submit queues a job for background processing.
// Returns an error if the queue is full (non-blocking).
// Accepts any to match the PreRendererSubmit interface in processor package.
func (pr *PreRenderer) Submit(jobInterface any) error {
	// Type assert to *Job
	job, ok := jobInterface.(*Job)
	if !ok {
		pr.logger.Error("Invalid job type submitted to pre-renderer",
			"type", fmt.Sprintf("%T", jobInterface))
		return errors.Newf("invalid job type: expected *spectrogram.Job, got %T", jobInterface).
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "submit_job").
			Context("received_type", fmt.Sprintf("%T", jobInterface)).
			Build()
	}

	// Early check: skip if spectrogram already exists (avoid queueing duplicate jobs)
	// Note: TOCTOU (time-of-check-time-of-use) race condition is intentional here.
	// The file might be created between this check and processJob(), which is fine:
	// - If created by on-demand generation: processJob() will skip it (redundant work avoided)
	// - If created by another pre-render worker: processJob() will skip it (idempotent)
	// - Impact: Job logged as "skipped" instead of caught here (no functional issue)
	spectrogramPath, err := pr.buildSpectrogramPath(job.ClipPath)
	if err != nil {
		pr.logger.Error("Invalid clip path, rejecting job",
			"note_id", job.NoteID,
			"clip_path", job.ClipPath,
			"error", err)
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "build_spectrogram_path").
			Context("note_id", job.NoteID).
			Context("clip_path", job.ClipPath).
			Build()
	}

	if _, err := os.Stat(spectrogramPath); err == nil {
		// File already exists, skip queueing
		pr.mu.Lock()
		pr.stats.Skipped++
		pr.mu.Unlock()
		pr.logger.Debug("Spectrogram already exists, skipping queue",
			"note_id", job.NoteID,
			"spectrogram_path", spectrogramPath)
		return nil
	}

	select {
	case pr.jobs <- job:
		pr.mu.Lock()
		pr.stats.Queued++
		pr.mu.Unlock()
		return nil
	default:
		pr.logger.Warn("Pre-render queue full, dropping job",
			"note_id", job.NoteID,
			"clip_path", job.ClipPath,
			"queue_size", defaultQueueSize)
		return errors.Newf("pre-render queue full (size: %d)", defaultQueueSize).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "submit_job").
			Context("note_id", job.NoteID).
			Context("queue_size", defaultQueueSize).
			Build()
	}
}

// worker processes jobs from the queue until the context is cancelled.
func (pr *PreRenderer) worker(id int) {
	defer pr.wg.Done()

	pr.logger.Debug("Pre-render worker started", "worker_id", id)

	for {
		select {
		case <-pr.ctx.Done():
			pr.logger.Debug("Pre-render worker stopping", "worker_id", id)
			return
		case job, ok := <-pr.jobs:
			if !ok {
				pr.logger.Debug("Pre-render worker channel closed", "worker_id", id)
				return
			}
			pr.processJob(job, id)
		}
	}
}

// processJob generates a spectrogram for a single job.
func (pr *PreRenderer) processJob(job *Job, workerID int) {
	start := time.Now()

	pr.logger.Debug("Processing pre-render job",
		"worker_id", workerID,
		"note_id", job.NoteID,
		"clip_path", job.ClipPath,
		"pcm_bytes", len(job.PCMData))

	// Build spectrogram path from clip path
	spectrogramPath, err := pr.buildSpectrogramPath(job.ClipPath)
	if err != nil {
		pr.logger.Error("Failed to build spectrogram path",
			"worker_id", workerID,
			"note_id", job.NoteID,
			"clip_path", job.ClipPath,
			"error", err)
		pr.mu.Lock()
		pr.stats.Failed++
		pr.mu.Unlock()
		return
	}

	// Check if spectrogram already exists (race condition with on-demand generation)
	if _, err := os.Stat(spectrogramPath); err == nil {
		pr.logger.Debug("Spectrogram already exists, skipping",
			"worker_id", workerID,
			"note_id", job.NoteID,
			"spectrogram_path", spectrogramPath)
		pr.mu.Lock()
		pr.stats.Skipped++
		pr.mu.Unlock()
		return
	}

	// Convert size string to pixels
	width, err := pr.sizeToPixels(pr.settings.Realtime.Dashboard.Spectrogram.Size)
	if err != nil {
		pr.logger.Error("Invalid spectrogram size",
			"worker_id", workerID,
			"note_id", job.NoteID,
			"size", pr.settings.Realtime.Dashboard.Spectrogram.Size,
			"error", err)
		pr.mu.Lock()
		pr.stats.Failed++
		pr.mu.Unlock()
		return
	}

	// Create context with timeout for this job
	ctx, cancel := context.WithTimeout(pr.ctx, generationTimeout)
	defer cancel()

	// Generate spectrogram
	if err := pr.generateWithSox(ctx, job.PCMData, spectrogramPath, width, pr.settings.Realtime.Dashboard.Spectrogram.Raw); err != nil {
		pr.logger.Error("Failed to generate spectrogram",
			"worker_id", workerID,
			"note_id", job.NoteID,
			"clip_path", job.ClipPath,
			"spectrogram_path", spectrogramPath,
			"error", err,
			"duration", time.Since(start))
		pr.mu.Lock()
		pr.stats.Failed++
		pr.mu.Unlock()
		return
	}

	pr.logger.Debug("Spectrogram pre-rendered successfully",
		"worker_id", workerID,
		"note_id", job.NoteID,
		"spectrogram_path", spectrogramPath,
		"duration", time.Since(start))

	pr.mu.Lock()
	pr.stats.Completed++
	pr.mu.Unlock()
}

// generateWithSox generates a spectrogram by feeding PCM data directly to Sox stdin.
// This bypasses FFmpeg entirely, reducing CPU overhead and memory usage.
func (pr *PreRenderer) generateWithSox(ctx context.Context, pcmData []byte, outputPath string, width int, raw bool) error {
	soxBinary := pr.settings.Realtime.Audio.SoxPath
	if soxBinary == "" {
		return fmt.Errorf("sox binary not configured")
	}

	// Ensure output directory exists
	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Calculate height (half of width for consistent aspect ratio)
	height := width / 2
	heightStr := strconv.Itoa(height)
	widthStr := strconv.Itoa(width)

	// Build Sox arguments for direct PCM input
	// Format: sox -t raw -r 48000 -e signed -b 16 -c 1 - -n spectrogram -x WIDTH -y HEIGHT -o OUTPUT [-r]
	args := []string{
		"-t", "raw",              // Input type: raw/headerless PCM
		"-r", "48000",            // Sample rate: 48kHz (conf.SampleRate)
		"-e", "signed",           // Encoding: signed integer
		"-b", "16",               // Bit depth: 16-bit (conf.BitDepth)
		"-c", "1",                // Channels: mono
		"-",                      // Read from stdin
		"-n",                     // No audio output (null output)
		"rate", "24k",            // Resample to 24kHz for spectrogram (matches existing behavior)
		"spectrogram",            // Effect: spectrogram
		"-x", widthStr,           // Width in pixels
		"-y", heightStr,          // Height in pixels
		"-o", outputPath,         // Output PNG file
	}

	// Add raw flag if requested (no axes/legend)
	if raw {
		args = append(args, "-r")
	}

	// Build command with low priority (nice -n 19 on Linux/macOS)
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// #nosec G204 - soxBinary is validated by exec.LookPath during config initialization
		cmd = exec.CommandContext(ctx, soxBinary, args...)
	} else {
		// #nosec G204 - soxBinary is validated by exec.LookPath during config initialization
		cmd = exec.CommandContext(ctx, "nice", append([]string{"-n", "19", soxBinary}, args...)...)
	}

	// Prepare to feed PCM data to stdin
	cmd.Stdin = bytes.NewReader(pcmData)

	// Capture stderr for debugging
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Run command
	if err := cmd.Run(); err != nil {
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "generate_with_sox").
			Context("output_path", outputPath).
			Context("width", width).
			Context("height", height).
			Context("raw", raw).
			Context("sox_stderr", stderr.String()).
			Context("pcm_bytes", len(pcmData)).
			Build()
	}

	return nil
}

// buildSpectrogramPath constructs the spectrogram file path from the audio clip path.
// Example: "clips/2024-01-15/Accipiter_striatus/Accipiter_striatus.2024-01-15T10:00:00.wav"
//       -> "clips/2024-01-15/Accipiter_striatus/Accipiter_striatus.2024-01-15T10:00:00.png"
func (pr *PreRenderer) buildSpectrogramPath(clipPath string) (string, error) {
	// Replace audio extension with .png
	ext := filepath.Ext(clipPath)
	if ext == "" {
		return "", fmt.Errorf("clip path has no extension: %s", clipPath)
	}

	spectrogramPath := clipPath[:len(clipPath)-len(ext)] + ".png"
	return spectrogramPath, nil
}

// sizeToPixels converts a size string to pixel width.
// Uses validSizes map as single source of truth for size validation.
func (pr *PreRenderer) sizeToPixels(size string) (int, error) {
	width, ok := validSizes[size]
	if !ok {
		return 0, fmt.Errorf("invalid size: %s (valid sizes: sm, md, lg, xl)", size)
	}
	return width, nil
}

// GetValidSizes returns a list of valid size strings.
// Useful for runtime validation in web UI.
func GetValidSizes() []string {
	sizes := make([]string, 0, len(validSizes))
	for size := range validSizes {
		sizes = append(sizes, size)
	}
	return sizes
}

// GetStats returns a copy of the current statistics.
func (pr *PreRenderer) GetStats() Stats {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	return pr.stats
}
