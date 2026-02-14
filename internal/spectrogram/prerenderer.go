// Package spectrogram provides background pre-rendering of spectrograms to eliminate UI lag.
// Pre-rendering feeds PCM data directly to Sox (bypassing FFmpeg) in a background worker pool.
package spectrogram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// Sentinel errors for stable error checking
var (
	ErrQueueFull = errors.Newf("pre-render queue full").Build()
)

const (
	// Worker pool size - conservative for background processing
	defaultWorkers = 2

	// Job queue size - minimal buffer for memory efficiency
	// Size of 3 = 2 workers busy + 1 waiting (~4 MB worst case for 15s clips)
	// On queue full: drop job (spectrogram generated on-demand when accessed)
	defaultQueueSize = 3

	// Timeout for individual spectrogram generation
	generationTimeout = 60 * time.Second

	// Timeout for graceful shutdown
	shutdownTimeout = 10 * time.Second
)

// PreRenderer manages background spectrogram pre-rendering.
// It uses a worker pool to process jobs without blocking the detection pipeline.
type PreRenderer struct {
	settings  *conf.Settings
	sfs       *securefs.SecureFS
	logger    logger.Logger
	generator *Generator // Shared generator for actual generation

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
	ClipPath  string    // Full absolute path to audio clip; PNG path is derived by swapping extension
	NoteID    uint      // For logging correlation
	Timestamp time.Time // Job submission time
}

// Methods to match the interface (allows Job to be submitted directly in tests)
func (j *Job) GetPCMData() []byte      { return j.PCMData }
func (j *Job) GetClipPath() string     { return j.ClipPath }
func (j *Job) GetNoteID() uint         { return j.NoteID }
func (j *Job) GetTimestamp() time.Time { return j.Timestamp }

// Stats tracks pre-rendering statistics.
type Stats struct {
	Queued    int64 // Number of jobs submitted
	Completed int64 // Number of spectrograms successfully generated
	Failed    int64 // Number of failed generations
	Skipped   int64 // Number skipped (already exist)
}

// NewPreRenderer creates a new pre-renderer instance.
// The parentCtx is used for lifecycle management and cancellation.
// If logger is nil, GetPreRendererLogger() is used to prevent nil pointer panics.
func NewPreRenderer(parentCtx context.Context, settings *conf.Settings, sfs *securefs.SecureFS, log logger.Logger) *PreRenderer {
	if log == nil {
		log = GetPreRendererLogger()
	}
	ctx, cancel := context.WithCancel(parentCtx)

	return &PreRenderer{
		settings:  settings,
		sfs:       sfs,
		logger:    log,
		generator: NewGenerator(settings, sfs, log), // Initialize shared generator
		ctx:       ctx,
		cancel:    cancel,
		jobs:      make(chan *Job, defaultQueueSize),
		workers:   defaultWorkers,
	}
}

// Start initializes the worker pool and begins processing jobs.
func (pr *PreRenderer) Start() {
	pr.logger.Info("Starting spectrogram pre-renderer",
		logger.Int("workers", pr.workers),
		logger.Int("queue_size", defaultQueueSize),
		logger.String("size", pr.settings.Realtime.Dashboard.Spectrogram.Size),
		logger.Bool("raw", pr.settings.Realtime.Dashboard.Spectrogram.Raw))

	for i := range pr.workers {
		pr.wg.Add(1)
		go pr.worker(i)
	}
}

// Stop gracefully shuts down the pre-renderer.
// It waits for in-flight jobs to complete (up to shutdownTimeout).
//
// Shutdown behavior:
//   - Cancels context to signal workers to stop accepting new jobs
//   - Closes job channel to prevent new submissions
//   - Waits up to shutdownTimeout (10s) for workers to finish current jobs
//   - On timeout: logs warning and continues (workers exit when context cancels)
//   - Workers are not force-killed; they complete current job or exit on context cancellation
//
// This graceful degradation prevents losing in-progress work while ensuring timely shutdown.
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
			logger.Duration("timeout", shutdownTimeout))
	}

	// Log final stats
	stats := pr.GetStats()
	pr.logger.Info("Spectrogram pre-renderer final stats",
		logger.Int64("queued", stats.Queued),
		logger.Int64("completed", stats.Completed),
		logger.Int64("failed", stats.Failed),
		logger.Int64("skipped", stats.Skipped))
}

// Submit queues a job for background processing.
// Returns an error if the queue is full (non-blocking).
// Accepts PreRenderJob from processor package to avoid circular dependency.
func (pr *PreRenderer) Submit(jobDTO interface {
	GetPCMData() []byte
	GetClipPath() string
	GetNoteID() uint
	GetTimestamp() time.Time
}) (err error) {
	// Convert DTO to internal Job type
	job := &Job{
		PCMData:   jobDTO.GetPCMData(),
		ClipPath:  jobDTO.GetClipPath(),
		NoteID:    jobDTO.GetNoteID(),
		Timestamp: jobDTO.GetTimestamp(),
	}

	// Early check: skip if spectrogram already exists (avoid queueing duplicate jobs)
	// Note: TOCTOU (time-of-check-time-of-use) race condition is intentional here.
	// The file might be created between this check and processJob(), which is fine:
	// - If created by on-demand generation: processJob() will skip it (redundant work avoided)
	// - If created by another pre-render worker: processJob() will skip it (idempotent)
	// - Impact: Job logged as "skipped" instead of caught here (no functional issue)
	spectrogramPath, err := BuildSpectrogramPath(job.ClipPath)
	if err != nil {
		pr.logger.Error("Invalid clip path, rejecting job",
			logger.Any("note_id", job.NoteID),
			logger.String("clip_path", job.ClipPath),
			logger.Error(err))
		// Increment Failed stat for validation errors
		pr.mu.Lock()
		pr.stats.Failed++
		pr.mu.Unlock()
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "build_spectrogram_path").
			Context("note_id", job.NoteID).
			Context("clip_path", job.ClipPath).
			Build()
	}

	// Path-traversal guard: ensure spectrogram path is within export directory
	// Use absolute paths to prevent filepath.Rel misclassification on relative inputs
	exportPath := pr.settings.Realtime.Audio.Export.Path
	absRoot, err := filepath.Abs(exportPath)
	if err != nil {
		pr.logger.Error("Failed to resolve export path to absolute",
			logger.Any("note_id", job.NoteID),
			logger.String("export_path", exportPath),
			logger.Error(err))
		pr.mu.Lock()
		pr.stats.Failed++
		pr.mu.Unlock()
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategoryFileIO).
			Context("operation", "resolve_export_path").
			Context("note_id", job.NoteID).
			Context("export_path", exportPath).
			Build()
	}

	absOut, err := filepath.Abs(spectrogramPath)
	if err != nil {
		pr.logger.Error("Failed to resolve spectrogram path to absolute",
			logger.Any("note_id", job.NoteID),
			logger.String("spectrogram_path", spectrogramPath),
			logger.Error(err))
		pr.mu.Lock()
		pr.stats.Failed++
		pr.mu.Unlock()
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategoryFileIO).
			Context("operation", "resolve_spectrogram_path").
			Context("note_id", job.NoteID).
			Context("spectrogram_path", spectrogramPath).
			Build()
	}

	relPath, err := filepath.Rel(absRoot, absOut)
	if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) {
		pr.logger.Error("Path traversal attempt detected, rejecting job",
			logger.Any("note_id", job.NoteID),
			logger.String("clip_path", job.ClipPath),
			logger.String("spectrogram_path", absOut),
			logger.String("export_path", absRoot),
			logger.String("relative_path", relPath))
		pr.mu.Lock()
		pr.stats.Failed++
		pr.mu.Unlock()
		return errors.Newf("path traversal detected: spectrogram path outside export directory").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "path_validation").
			Context("note_id", job.NoteID).
			Context("clip_path", job.ClipPath).
			Context("spectrogram_path", absOut).
			Context("export_path", absRoot).
			Context("relative_path", relPath).
			Build()
	}

	if _, err := os.Stat(spectrogramPath); err == nil {
		// File already exists, skip queueing
		pr.mu.Lock()
		pr.stats.Skipped++
		pr.mu.Unlock()
		pr.logger.Debug("Spectrogram already exists, skipping queue",
			logger.Any("note_id", job.NoteID),
			logger.String("spectrogram_path", spectrogramPath))
		return nil
	}

	// Panic protection for concurrent channel close
	defer func() {
		if r := recover(); r != nil {
			pr.logger.Error("Panic during job submission (channel likely closed)",
				logger.Any("note_id", job.NoteID),
				logger.Any("panic", r))
			pr.mu.Lock()
			pr.stats.Failed++
			pr.mu.Unlock()
			// Set named return value to report the panic as an error
			err = errors.Newf("panic during job submission: %v", r).
				Component("spectrogram").
				Category(errors.CategorySystem).
				Context("operation", "submit_job").
				Context("note_id", job.NoteID).
				Build()
		}
	}()

	// Check context first to avoid select race with closed channel
	// When Stop() is called, context is cancelled before channel is closed,
	// so checking this first ensures we don't race with channel closure
	select {
	case <-pr.ctx.Done():
		// Context cancelled, don't attempt to send
		pr.logger.Debug("Pre-renderer context cancelled, rejecting job",
			logger.Any("note_id", job.NoteID))
		pr.mu.Lock()
		pr.stats.Failed++
		pr.mu.Unlock()
		return errors.New(pr.ctx.Err()).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "submit_job").
			Context("note_id", job.NoteID).
			Build()
	default:
		// Context not cancelled, proceed to queue
	}

	// Try to send job to queue (non-blocking)
	select {
	case pr.jobs <- job:
		pr.mu.Lock()
		pr.stats.Queued++
		totalQueued := pr.stats.Queued
		pr.mu.Unlock()

		// Get actual current queue depth for diagnostic visibility
		currentQueueDepth := len(pr.jobs)

		// Log at INFO level when spectrogram generation is queued (BG-18)
		// This provides visibility into the pre-rendering pipeline without debug mode
		pr.logger.Info("Spectrogram generation queued",
			logger.Any("note_id", job.NoteID),
			logger.Int("queue_depth", currentQueueDepth), // Current backlog (0-3 for default queue size)
			logger.Int64("total_queued", totalQueued),    // Lifetime counter
			logger.String("operation", "spectrogram_queued"))
		return nil
	default:
		pr.logger.Warn("Pre-render queue full, dropping job",
			logger.Any("note_id", job.NoteID),
			logger.String("clip_path", job.ClipPath),
			logger.Int("queue_size", defaultQueueSize))
		pr.mu.Lock()
		pr.stats.Failed++
		pr.mu.Unlock()
		return errors.New(ErrQueueFull).
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

	pr.logger.Debug("Pre-render worker started", logger.Int("worker_id", id))

	for {
		select {
		case <-pr.ctx.Done():
			pr.logger.Debug("Pre-render worker stopping", logger.Int("worker_id", id))
			return
		case job, ok := <-pr.jobs:
			if !ok {
				pr.logger.Debug("Pre-render worker channel closed", logger.Int("worker_id", id))
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
		logger.Int("worker_id", workerID),
		logger.Any("note_id", job.NoteID),
		logger.String("clip_path", job.ClipPath),
		logger.Int("pcm_bytes", len(job.PCMData)))

	// Build spectrogram path from clip path
	spectrogramPath, err := BuildSpectrogramPath(job.ClipPath)
	if err != nil {
		pr.logger.Error("Failed to build spectrogram path",
			logger.Int("worker_id", workerID),
			logger.Any("note_id", job.NoteID),
			logger.String("clip_path", job.ClipPath),
			logger.Error(err))
		pr.mu.Lock()
		pr.stats.Failed++
		pr.mu.Unlock()
		return
	}

	// Check if spectrogram already exists
	// Race conditions are acceptable here (idempotent operation):
	// 1. On-demand generation might create file between Submit() check and now
	// 2. Another worker might process duplicate job (edge case with rapid submissions)
	// 3. File might be created externally (manual intervention)
	// Impact: Job skipped instead of caught in Submit() - no functional difference
	if _, err := os.Stat(spectrogramPath); err == nil {
		pr.logger.Debug("Spectrogram already exists, skipping",
			logger.Int("worker_id", workerID),
			logger.Any("note_id", job.NoteID),
			logger.String("spectrogram_path", spectrogramPath))
		pr.mu.Lock()
		pr.stats.Skipped++
		pr.mu.Unlock()
		return
	}

	// Convert size string to pixels
	width, err := SizeToPixels(pr.settings.Realtime.Dashboard.Spectrogram.Size)
	if err != nil {
		pr.logger.Error("Invalid spectrogram size",
			logger.Int("worker_id", workerID),
			logger.Any("note_id", job.NoteID),
			logger.String("size", pr.settings.Realtime.Dashboard.Spectrogram.Size),
			logger.Error(err))
		pr.mu.Lock()
		pr.stats.Failed++
		pr.mu.Unlock()
		return
	}

	// Create context with timeout for this job
	ctx, cancel := context.WithTimeout(pr.ctx, generationTimeout)
	defer cancel()

	// Log at INFO level when generation starts (BG-18)
	// This provides visibility into the generation pipeline
	pr.logger.Info("Spectrogram generation started",
		logger.Any("note_id", job.NoteID),
		logger.String("audio_path", job.ClipPath),
		logger.String("size", pr.settings.Realtime.Dashboard.Spectrogram.Size),
		logger.String("operation", "spectrogram_generation_start"))

	// Generate spectrogram using shared generator
	if err := pr.generator.GenerateFromPCM(ctx, job.PCMData, spectrogramPath, width, pr.settings.Realtime.Dashboard.Spectrogram.Raw); err != nil {
		// Check if this is an expected operational error (context canceled, process killed)
		// These are normal events during shutdown, timeout, or resource management
		if IsOperationalError(err) {
			// Log at Debug level for expected operational events
			pr.logger.Debug("Spectrogram generation canceled or interrupted",
				logger.Int("worker_id", workerID),
				logger.Any("note_id", job.NoteID),
				logger.String("clip_path", job.ClipPath),
				logger.String("spectrogram_path", spectrogramPath),
				logger.Error(err),
				logger.Int64("duration_ms", time.Since(start).Milliseconds()),
				logger.String("operation", "spectrogram_generation_canceled"))
		} else {
			// Log at Error level for unexpected failures
			pr.logger.Error("Failed to generate spectrogram",
				logger.Int("worker_id", workerID),
				logger.Any("note_id", job.NoteID),
				logger.String("clip_path", job.ClipPath),
				logger.String("spectrogram_path", spectrogramPath),
				logger.Error(err),
				logger.Int64("duration_ms", time.Since(start).Milliseconds()),
				logger.String("operation", "spectrogram_generation_failed"))
		}
		pr.mu.Lock()
		pr.stats.Failed++
		pr.mu.Unlock()
		return
	}

	// Get file size for logging
	fileInfo, err := os.Stat(spectrogramPath)
	var fileSize int64
	if err == nil {
		fileSize = fileInfo.Size()
	} else {
		// Debug log if we can't stat the file (shouldn't happen after successful generation)
		pr.logger.Debug("Failed to stat spectrogram file for size logging",
			logger.Any("note_id", job.NoteID),
			logger.Error(err),
			logger.String("path", spectrogramPath))
	}

	// Log at INFO level when generation succeeds (BG-18)
	// This provides confirmation that spectrograms are being created successfully
	pr.logger.Info("Spectrogram generated successfully",
		logger.Any("note_id", job.NoteID),
		logger.String("output_path", spectrogramPath),
		logger.Int64("file_size_bytes", fileSize),
		logger.Int64("duration_ms", time.Since(start).Milliseconds()),
		logger.String("operation", "spectrogram_generation_success"))

	// Allow GC to reclaim PCM buffer promptly
	job.PCMData = nil

	pr.mu.Lock()
	pr.stats.Completed++
	pr.mu.Unlock()
}

// GetStats returns a copy of the current statistics.
func (pr *PreRenderer) GetStats() Stats {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	return pr.stats
}
