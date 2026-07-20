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
	ErrQueueFull = errors.Newf("pre-render queue full").
		Component("spectrogram").
		Category(errors.CategoryLimit).
		Build()
)

const (
	// Worker pool size - conservative for background processing
	defaultWorkers = 2

	// Job queue size - large enough to absorb detection bursts without dropping jobs.
	// Each queued job holds ~4 MB of PCM data (15s clip), so 100 jobs ≈ 400 MB worst case.
	// On queue full: drop job (spectrogram generated on-demand when accessed)
	defaultQueueSize = 100

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
	PCMData    []byte // Raw PCM data from memory (s16le, mono)
	SampleRate int    // PCM sample rate in Hz (0 = use conf.SampleRate default)
	// ClipPath is the audio clip joined against the configured export directory,
	// so it is absolute whenever that directory is. The PNG path is derived by
	// swapping the extension. It is the full path because the renderer has to
	// open the file; never log it directly, run it through exportRelPath.
	ClipPath         string
	NoteID           uint             // For logging correlation
	Timestamp        time.Time        // Job submission time
	FrequencyProfile FrequencyProfile // Frequency profile for spectrogram generation (bird vs bat)
	modelType        string           // Original model type string for DTO getter
}

// Methods to match the interface (allows Job to be submitted directly in tests)
func (j *Job) GetPCMData() []byte      { return j.PCMData }
func (j *Job) GetSampleRate() int      { return j.SampleRate }
func (j *Job) GetClipPath() string     { return j.ClipPath }
func (j *Job) GetNoteID() uint         { return j.NoteID }
func (j *Job) GetTimestamp() time.Time { return j.Timestamp }

// GetModelType returns the model type string for DTO interface compatibility.
func (j *Job) GetModelType() string { return j.modelType }

// Stats tracks pre-rendering statistics.
type Stats struct {
	Queued    int64 // Number of jobs submitted
	Completed int64 // Number of spectrograms successfully generated
	Failed    int64 // Number of failed generations
	Skipped   int64 // Number skipped (already exist)
}

// currentSettings returns the latest settings snapshot so UI changes to
// spectrogram size/raw/export path take effect on the next rendered job
// without restarting the pre-renderer.
func (pr *PreRenderer) currentSettings() *conf.Settings {
	return conf.CurrentOrFallback(pr.settings)
}

// normalizeSpectrogramPath converts a relative spectrogram path to absolute
// using SecureFS base directory, avoiding path doubling when the path
// already includes the export prefix (e.g., "clips/2026/03/file.png").
// filepath.Rel with two relative paths is safe (no os.Getwd dependency).
// settings is the snapshot captured by the caller for this unit of work so
// any hot-reloaded values stay consistent with the caller's other reads.
func (pr *PreRenderer) normalizeSpectrogramPath(settings *conf.Settings, spectrogramPath string) string {
	if filepath.IsAbs(spectrogramPath) {
		return spectrogramPath
	}
	exportPath := settings.Realtime.Audio.Export.Path
	if relToExport, err := filepath.Rel(exportPath, spectrogramPath); err == nil && !strings.HasPrefix(relToExport, "..") {
		return filepath.Join(pr.sfs.BaseDir(), relToExport)
	}
	return filepath.Join(pr.sfs.BaseDir(), spectrogramPath)
}

// exportRelPath is the name of a clip or spectrogram relative to the export
// directory, and is the only shape a path may take in a log field or an error
// context here.
//
// The pre-renderer is handed the full path (it has to open the file), but a
// support log and an uploaded support dump are read by someone other than the
// operator, and an absolute path leaks the account name and the directory
// layout of their machine. The year/month segments leak nothing and are what
// makes the value resolvable back to a file, so they are kept. This mirrors
// SaveAudioAction.relativeClipPath, which strips the same prefix on the export
// side; without the same treatment here, a dump that no longer leaks the path
// from the export logs still leaks it from the pre-render logs.
//
// Two roots are tried because the two available answers can disagree. The
// clip path arrives joined against settings.Realtime.Audio.Export.Path, which
// is hot-reloadable and may be relative; sfs.BaseDir() is that same directory
// resolved to absolute, but frozen at construction (securefs.New is called once
// with the startup value). Either can be the one that matches, so both get a
// turn before falling back.
//
// IsLocal rejects a result that walks UP out of the export directory: Rel
// happily answers "../../../etc/passwd" when both paths are absolute but the
// input sits outside, and that both leaks the layout above the export directory
// and names nothing any consumer can resolve. The basename is the safe answer
// in that case; it still identifies the file and still contains no directory.
func exportRelPath(settings *conf.Settings, sfs *securefs.SecureFS, p string) string {
	if p == "" {
		return ""
	}
	roots := [2]string{}
	if settings != nil {
		roots[0] = settings.Realtime.Audio.Export.Path
	}
	if sfs != nil {
		roots[1] = sfs.BaseDir()
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		if rel, err := filepath.Rel(root, p); err == nil && filepath.IsLocal(rel) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(p)
}

// relPath is the Generator's access to the same helper. The generator receives
// the derived ABSOLUTE spectrogram and audio paths from the pre-renderer (it has
// to, it opens and writes them), so without this every path the pre-renderer
// strips is re-leaked microseconds later by the code it calls, including at Info
// level where a default-level support dump picks it up.
func (g *Generator) relPath(p string) string {
	return exportRelPath(g.currentSettings(), g.sfs, p)
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
	specSettings := pr.currentSettings().Realtime.Dashboard.Spectrogram
	pr.logger.Info("Starting spectrogram pre-renderer",
		logger.Int("workers", pr.workers),
		logger.Int("queue_size", defaultQueueSize),
		logger.String("size", specSettings.Size),
		logger.Bool("raw", specSettings.Raw))

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

	// Cancel context to signal workers to stop.
	// Workers exit via pr.ctx.Done(); the jobs channel is left open
	// so Submit() cannot panic on a closed-channel send.
	pr.cancel()

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
	GetSampleRate() int
	GetClipPath() string
	GetNoteID() uint
	GetTimestamp() time.Time
	GetModelType() string
}) (err error) {
	// Convert DTO to internal Job type
	modelType := jobDTO.GetModelType()
	job := &Job{
		PCMData:          jobDTO.GetPCMData(),
		SampleRate:       jobDTO.GetSampleRate(),
		ClipPath:         jobDTO.GetClipPath(),
		NoteID:           jobDTO.GetNoteID(),
		Timestamp:        jobDTO.GetTimestamp(),
		FrequencyProfile: ProfileForModelType(modelType),
		modelType:        modelType,
	}

	// One settings snapshot for this submission, for the same reason processJob
	// captures one: a UI edit to the export path landing mid-call must not make
	// two path fields on the same log line resolve against different roots.
	settings := pr.currentSettings()

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
			logger.String("clip_path", exportRelPath(settings, pr.sfs, job.ClipPath)),
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
			Context("clip_path", exportRelPath(settings, pr.sfs, job.ClipPath)).
			Build()
	}

	// Path-traversal guard: ensure spectrogram path is within export directory
	// Use SecureFS base dir (resolved to absolute at init time) instead of
	// filepath.Abs() which depends on os.Getwd(), unreliable on Windows
	// when running as a service without a working directory set (#2342).
	absRoot := pr.sfs.BaseDir()

	// Make spectrogram path absolute using SecureFS base dir, stripping export
	// prefix to avoid path doubling (e.g., "clips/clips/..."). See #2342.
	absOut := pr.normalizeSpectrogramPath(settings, spectrogramPath)

	relPath, err := filepath.Rel(absRoot, absOut)
	if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) {
		// export_path is deliberately not logged: it IS the absolute export
		// directory, so reporting it here would leak exactly what exportRelPath
		// strips everywhere else on this line.
		//
		// reason carries what export_path used to. The two arms of this guard
		// fail for different causes and only one of them explains itself:
		// on the escape arm relative_path holds the "../..." result and says it
		// all, but on the Rel-error arm (different Windows volumes) Rel returns
		// "" and exportRelPath likewise falls through to a bare basename, so
		// without reason the line would report a traversal and give the operator
		// nothing to act on.
		//
		// relative_path is ToSlash'd to match the other two path fields; mixing
		// separators within one line makes it unparseable by any consumer that
		// normalizes on one convention.
		reason := "escapes_export_root"
		if err != nil {
			reason = "path_not_relatable_to_export_root"
		}
		pr.logger.Error("Path traversal attempt detected, rejecting job",
			logger.Any("note_id", job.NoteID),
			logger.String("clip_path", exportRelPath(settings, pr.sfs, job.ClipPath)),
			logger.String("spectrogram_path", exportRelPath(settings, pr.sfs, absOut)),
			logger.String("relative_path", filepath.ToSlash(relPath)),
			logger.String("reason", reason),
			logger.String("operation", "spectrogram_path_validation"))
		pr.mu.Lock()
		pr.stats.Failed++
		pr.mu.Unlock()
		return errors.Newf("path traversal detected: spectrogram path outside export directory").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "path_validation").
			Context("note_id", job.NoteID).
			Context("clip_path", exportRelPath(settings, pr.sfs, job.ClipPath)).
			Context("spectrogram_path", exportRelPath(settings, pr.sfs, absOut)).
			Context("relative_path", filepath.ToSlash(relPath)).
			Context("reason", reason).
			Build()
	}

	if _, err := os.Stat(absOut); err == nil {
		// File already exists, skip queueing
		pr.mu.Lock()
		pr.stats.Skipped++
		pr.mu.Unlock()
		pr.logger.Debug("Spectrogram already exists, skipping queue",
			logger.Any("note_id", job.NoteID),
			logger.String("spectrogram_path", exportRelPath(settings, pr.sfs, absOut)))
		return nil
	}

	// Check context first to reject jobs after Stop()
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
			logger.Int("queue_depth", currentQueueDepth), // Current backlog
			logger.Int64("total_queued", totalQueued),    // Lifetime counter
			logger.String("operation", "spectrogram_queued"))
		return nil
	default:
		pr.logger.Warn("Pre-render queue full, dropping job",
			logger.Any("note_id", job.NoteID),
			logger.String("clip_path", exportRelPath(settings, pr.sfs, job.ClipPath)),
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
		case job := <-pr.jobs:
			pr.processJob(job, id)
		}
	}
}

// processJob generates a spectrogram for a single job.
func (pr *PreRenderer) processJob(job *Job, workerID int) {
	start := time.Now()

	// Capture a single settings snapshot for this job so size, raw, and
	// export path are read from the same view. A UI edit that lands
	// mid-job can't produce a mix of old/new values. The Generator below
	// captures its own snapshot when its public entry point runs; that is
	// a deliberate per-component boundary (Size/Raw are per-call inputs
	// passed across, render-engine config stays on Generator's side).
	settings := pr.currentSettings()
	specSettings := settings.Realtime.Dashboard.Spectrogram

	pr.logger.Debug("Processing pre-render job",
		logger.Int("worker_id", workerID),
		logger.Any("note_id", job.NoteID),
		logger.String("clip_path", exportRelPath(settings, pr.sfs, job.ClipPath)),
		logger.Int("pcm_bytes", len(job.PCMData)))

	// Build spectrogram path from clip path
	spectrogramPath, err := BuildSpectrogramPath(job.ClipPath)
	if err != nil {
		pr.logger.Error("Failed to build spectrogram path",
			logger.Int("worker_id", workerID),
			logger.Any("note_id", job.NoteID),
			logger.String("clip_path", exportRelPath(settings, pr.sfs, job.ClipPath)),
			logger.Error(err))
		pr.mu.Lock()
		pr.stats.Failed++
		pr.mu.Unlock()
		return
	}

	// Make spectrogram path absolute using SecureFS base dir, stripping export
	// prefix to avoid path doubling (e.g., "clips/clips/..."). See #2342.
	spectrogramPath = pr.normalizeSpectrogramPath(settings, spectrogramPath)

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
			logger.String("spectrogram_path", exportRelPath(settings, pr.sfs, spectrogramPath)))
		pr.mu.Lock()
		pr.stats.Skipped++
		pr.mu.Unlock()
		return
	}

	// Convert size string to pixels
	width, err := SizeToPixels(specSettings.Size)
	if err != nil {
		pr.logger.Error("Invalid spectrogram size",
			logger.Int("worker_id", workerID),
			logger.Any("note_id", job.NoteID),
			logger.String("size", specSettings.Size),
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
		logger.String("audio_path", exportRelPath(settings, pr.sfs, job.ClipPath)),
		logger.String("size", specSettings.Size),
		logger.String("operation", "spectrogram_generation_start"))

	// Generate spectrogram using shared generator
	if err := pr.generator.GenerateFromPCM(ctx, job.PCMData, spectrogramPath, width, specSettings.Raw, job.SampleRate, WithFrequencyProfile(job.FrequencyProfile)); err != nil {
		// Check if this is an expected operational error (context canceled, process killed)
		// These are normal events during shutdown, timeout, or resource management
		if IsOperationalError(err) {
			// Log at Debug level for expected operational events
			pr.logger.Debug("Spectrogram generation canceled or interrupted",
				logger.Int("worker_id", workerID),
				logger.Any("note_id", job.NoteID),
				logger.String("clip_path", exportRelPath(settings, pr.sfs, job.ClipPath)),
				logger.String("spectrogram_path", exportRelPath(settings, pr.sfs, spectrogramPath)),
				logger.Error(err),
				logger.Int64("duration_ms", time.Since(start).Milliseconds()),
				logger.String("operation", "spectrogram_generation_canceled"))
		} else {
			// Log at Error level for unexpected failures
			pr.logger.Error("Failed to generate spectrogram",
				logger.Int("worker_id", workerID),
				logger.Any("note_id", job.NoteID),
				logger.String("clip_path", exportRelPath(settings, pr.sfs, job.ClipPath)),
				logger.String("spectrogram_path", exportRelPath(settings, pr.sfs, spectrogramPath)),
				logger.Error(err),
				logger.Int64("duration_ms", time.Since(start).Milliseconds()),
				logger.String("operation", "spectrogram_generation_failed"))
			// Only increment failure stats for genuine errors, not operational interruptions
			pr.mu.Lock()
			pr.stats.Failed++
			pr.mu.Unlock()
		}
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
			logger.String("path", exportRelPath(settings, pr.sfs, spectrogramPath)))
	}

	// Log at INFO level when generation succeeds (BG-18)
	// This provides confirmation that spectrograms are being created successfully
	pr.logger.Info("Spectrogram generated successfully",
		logger.Any("note_id", job.NoteID),
		logger.String("output_path", exportRelPath(settings, pr.sfs, spectrogramPath)),
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
