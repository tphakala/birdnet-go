// Package api provides the HTTP API for BirdNET-Go.
package api

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imports"
	"github.com/tphakala/birdnet-go/internal/imports/birdnetpi"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// Import mode constants.
const (
	importModeDBOnly  = "db-only"
	importModeDBaudio = "db-audio" // reserved; not yet available
)

// Import SSE event type constants.
const (
	importEventProgress  = "progress"
	importEventComplete  = "complete"
	importEventCancelled = "cancelled"
	importEventError     = "error"
	importEventHeartbeat = "heartbeat"
)

// Import status constants.
const (
	importStatusStarted    = "started"
	importStatusRunning    = "running"
	importStatusCancelling = "cancelling"
	importStatusDone       = "done"
	importStatusIdle       = "idle"
)

// importEnginePhaseDone matches the engine's terminal phase string.
// Progress events with this phase are suppressed; the terminal complete/cancelled/error
// event is the authoritative completion signal.
const importEnginePhaseDone = "done"

// importErrorMessage is the generic, non-leaking message reported when an import fails.
const importErrorMessage = "import failed"

// errImportInProgress is returned when a second start is attempted while one is running.
var errImportInProgress = errors.NewStd("import already in progress")

// errInvalidSourcePath is returned when the source path fails containment validation.
var errInvalidSourcePath = errors.NewStd("invalid source path")

// startImportRequest is the JSON body for POST /import/birdnet-pi.
type startImportRequest struct {
	Mode       string `json:"mode"`        // must be "db-only"
	SourcePath string `json:"source_path"` // path relative to the external mount root
	Location   string `json:"location"`    // optional IANA timezone name e.g. "Europe/Helsinki"
}

// startImportResponse is the 202 Accepted reply body.
type startImportResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

// cancelImportResponse is the reply body for the cancel endpoint.
type cancelImportResponse struct {
	Status string `json:"status"`
}

// importProgress is the SSE payload carrying live import stats.
type importProgress struct {
	Total     int    `json:"total"`
	Processed int    `json:"processed"`
	Inserted  int    `json:"inserted"`
	Skipped   int    `json:"skipped"`
	Errors    int    `json:"errors"`
	Phase     string `json:"phase"`
}

// importErrorPayload is the SSE payload for the error terminal event.
// importProgress is embedded so the wire format is identical to the progress event
// (flattened snake_case fields), with an additional "message" field prepended.
type importErrorPayload struct {
	Message string `json:"message"`
	importProgress
}

// importStatusResponse is the JSON body for GET /import/status.
type importStatusResponse struct {
	Running  bool            `json:"running"`
	JobID    string          `json:"job_id,omitempty"`
	Status   string          `json:"status"`
	Progress *importProgress `json:"progress,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// toImportProgress converts engine stats to the API DTO.
func toImportProgress(s imports.ImportStats) importProgress {
	return importProgress{
		Total:     s.Total,
		Processed: s.Processed,
		Inserted:  s.Inserted,
		Skipped:   s.Skipped,
		Errors:    s.Errors,
		Phase:     s.Phase,
	}
}

// resolveImportSourcePath resolves userPath under root with traversal and symlink-escape protection.
// userPath must be a relative path. Returns the resolved absolute path on success.
func resolveImportSourcePath(root, userPath string) (string, error) {
	if userPath == "" {
		return "", errInvalidSourcePath
	}
	cleaned := filepath.Clean(userPath)
	if filepath.IsAbs(cleaned) {
		return "", errInvalidSourcePath
	}
	full := filepath.Join(root, cleaned)
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errInvalidSourcePath
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return "", errInvalidSourcePath
		}
		rootResolved = root
	}
	// Find the deepest existing ancestor of full, resolving symlinks, and verify it
	// is physically contained within root. This rejects a symlinked ancestor that
	// escapes root even when the target (or several intermediate dirs) do not yet
	// exist, closing the TOCTOU window at any depth.
	ancestor := full
	for {
		resolved, evalErr := filepath.EvalSymlinks(ancestor)
		if evalErr == nil {
			if !isContained(rootResolved, resolved) {
				return "", errInvalidSourcePath
			}
			suffix, relErr := filepath.Rel(ancestor, full)
			if relErr != nil {
				return "", errInvalidSourcePath
			}
			return filepath.Join(resolved, suffix), nil
		}
		if !errors.Is(evalErr, fs.ErrNotExist) {
			return "", errInvalidSourcePath
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			// Reached the filesystem root without finding an existing ancestor.
			return "", errInvalidSourcePath
		}
		ancestor = parent
	}
}

// isContained reports whether target resides within root after both have been
// resolved to their physical (symlink-free) forms.
func isContained(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// initImportRoutes registers all import-related API routes.
//
// These endpoints start, cancel, and inspect a database import, so the group
// must fail closed. c.authMiddleware is non-nil in every real deployment: the
// server always injects it via WithAuthMiddleware, and that middleware itself
// enforces or passes through based on the auth configuration. It can only be nil
// in unit tests or a misconfiguration; in that case install a middleware that
// denies access with 401 rather than registering the routes unprotected (Echo's
// applyMiddleware would also panic on a literal nil entry).
func (c *Controller) initImportRoutes() {
	authMiddleware := c.authMiddleware
	if authMiddleware == nil {
		authMiddleware = func(echo.HandlerFunc) echo.HandlerFunc {
			return func(ctx echo.Context) error {
				return c.HandleError(ctx, nil, "authentication is not configured", http.StatusUnauthorized)
			}
		}
	}
	g := c.Group.Group("/import", authMiddleware)
	g.POST("/birdnet-pi", c.StartBirdNETPiImport)
	g.GET("/jobs/:jobId/progress", c.StreamImportProgress)
	g.POST("/jobs/:jobId/cancel", c.CancelImport)
	g.GET("/status", c.GetImportStatus)
}

// StartBirdNETPiImport validates a BirdNET-Pi SQLite source and starts a DB-only import.
// Returns 202 Accepted with a job_id on success.
func (c *Controller) StartBirdNETPiImport(ctx echo.Context) error {
	// Verify datastore is available.
	if err := c.requireDatastore(ctx); err != nil {
		return err
	}
	if c.Repo == nil {
		return c.HandleError(ctx, errDatastoreUnavailable, "datastore is not available", http.StatusServiceUnavailable)
	}

	// Parse and bind request body.
	var req startImportRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "invalid request body", http.StatusBadRequest)
	}

	// Validate import mode.
	switch req.Mode {
	case importModeDBOnly:
		// accepted
	case importModeDBaudio:
		return c.HandleError(ctx, nil, "audio import is not available yet", http.StatusBadRequest)
	default:
		return c.HandleError(ctx, nil, "unsupported import mode", http.StatusBadRequest)
	}

	// Resolve and validate source path.
	root := c.importSourceRoot
	if root == "" {
		root = sysinfo.DefaultExternalMountPath
	}
	resolvedPath, err := resolveImportSourcePath(root, req.SourcePath)
	if err != nil {
		return c.HandleError(ctx, err, "invalid source path", http.StatusBadRequest)
	}
	info, statErr := os.Stat(resolvedPath)
	if statErr != nil || !info.Mode().IsRegular() {
		return c.HandleError(ctx, statErr, "source file not found or not a regular file", http.StatusBadRequest)
	}

	// Parse optional timezone.
	var loc *time.Location
	if req.Location != "" {
		loc, err = time.LoadLocation(req.Location)
		if err != nil {
			return c.HandleError(ctx, err, "invalid location", http.StatusBadRequest)
		}
	}

	// Build source using the injectable factory (defaults to birdnetpi.New).
	factory := c.importSourceFactory
	if factory == nil {
		factory = func(p string) (imports.Source, error) {
			return birdnetpi.New(p)
		}
	}
	src, err := factory(resolvedPath)
	if err != nil {
		return c.HandleError(ctx, err, "failed to open source", http.StatusBadRequest)
	}

	// Validate source synchronously for an immediate error response.
	if err := src.Validate(ctx.Request().Context()); err != nil {
		_ = src.Close()
		return c.HandleError(ctx, err, "source validation failed", http.StatusBadRequest)
	}

	// Reserve the single import slot.
	id := generateCorrelationID()
	parent := c.ctx
	if parent == nil {
		parent = context.Background()
	}
	jobCtx, jobCancel := context.WithCancel(parent)
	job := newImportJob(id, jobCancel)
	if !c.importMgr.start(job) {
		jobCancel()
		_ = src.Close()
		return c.HandleError(ctx, errImportInProgress, "an import is already in progress", http.StatusConflict)
	}

	// Run the engine in a goroutine tracked by the controller WaitGroup.
	opts := imports.ImportOptions{
		SourceNode: imports.DefaultSourceNode,
		Location:   loc,
	}
	eng := imports.NewEngine(c.Repo)
	c.wg.Go(func() {
		var stats imports.ImportStats
		var runErr error
		// Registered first so it runs last: it recovers panics from eng.Run AND from
		// the cleanup defers below, and always drives the job to a terminal state so the
		// single import slot is released and SSE streams unblock.
		defer func() {
			if r := recover(); r != nil {
				runErr = errors.Newf("import panicked: %v", r).Component("api").Category(errors.CategoryGeneric).Build()
				// The local stats var is stale on panic; recover the last reported progress.
				stats, _, _, _, _ = job.snapshot()
			}
			job.finish(stats, runErr)
		}()
		defer jobCancel()
		defer func() { _ = src.Close() }()
		stats, runErr = eng.Run(jobCtx, src, opts, job)
	})

	return ctx.JSON(http.StatusAccepted, startImportResponse{
		JobID:  id,
		Status: importStatusStarted,
	})
}

// StreamImportProgress streams import progress as Server-Sent Events.
// Closing the SSE connection does NOT cancel the import; use the cancel endpoint.
func (c *Controller) StreamImportProgress(ctx echo.Context) error {
	id := ctx.Param("jobId")
	job := c.importMgr.get(id)
	if job == nil {
		return c.HandleError(ctx, nil, "import job not found", http.StatusNotFound)
	}

	setSSEHeaders(ctx)

	// Bound the stream lifetime independently of the import itself.
	reqCtx, cancel := context.WithTimeout(ctx.Request().Context(), maxSSEStreamDuration)
	defer cancel()

	// Send initial snapshot before entering the event loop. If the job already
	// finished, emit only the terminal event so the latest sequence number is
	// not reused for both a progress and a terminal event (event ids stay unique).
	stats, seq, done, changed, runErr := job.snapshot()
	if done {
		_ = c.sendImportTerminal(ctx, seq, stats, runErr)
		return nil
	}
	if stats.Phase != importEnginePhaseDone {
		if err := c.sendImportEvent(ctx, seq, importEventProgress, toImportProgress(stats)); err != nil {
			return nil
		}
	}

	hb := time.NewTicker(sseHeartbeatInterval)
	defer hb.Stop()

	// Capture shutdown channel defensively; c.ctx may be nil in isolated unit tests.
	var shutdown <-chan struct{}
	if c.ctx != nil {
		shutdown = c.ctx.Done()
	} else {
		neverClose := make(chan struct{})
		shutdown = neverClose
	}

	for {
		select {
		case <-reqCtx.Done():
			return nil
		case <-shutdown:
			return nil
		case <-hb.C:
			if err := c.sendImportHeartbeat(ctx); err != nil {
				return nil
			}
		case <-changed:
			stats, seq, done, changed, runErr = job.snapshot()
			// On the terminal transition emit only the terminal event so the
			// sequence number is not reused for both a progress and a terminal
			// event (event ids stay unique even when updates coalesce).
			if done {
				_ = c.sendImportTerminal(ctx, seq, stats, runErr)
				return nil
			}
			if stats.Phase != importEnginePhaseDone {
				if err := c.sendImportEvent(ctx, seq, importEventProgress, toImportProgress(stats)); err != nil {
					return nil
				}
			}
		}
	}
}

// CancelImport requests cancellation of a running import job.
// Returns 200 with status "cancelling" (or "done" if already finished).
func (c *Controller) CancelImport(ctx echo.Context) error {
	id := ctx.Param("jobId")
	job := c.importMgr.get(id)
	if job == nil {
		return c.HandleError(ctx, nil, "import job not found", http.StatusNotFound)
	}
	job.cancel()
	status := importStatusCancelling
	if job.isDone() {
		status = importStatusDone
	}
	return ctx.JSON(http.StatusOK, cancelImportResponse{Status: status})
}

// GetImportStatus returns the current import state for polling UIs.
func (c *Controller) GetImportStatus(ctx echo.Context) error {
	job := c.importMgr.active()
	if job == nil {
		return ctx.JSON(http.StatusOK, importStatusResponse{
			Running: false,
			Status:  importStatusIdle,
		})
	}
	stats, _, done, _, runErr := job.snapshot()
	prog := toImportProgress(stats)
	if done {
		resp := importStatusResponse{
			Running:  false,
			JobID:    job.id,
			Status:   importStatusDone,
			Progress: &prog,
		}
		if runErr != nil {
			resp.Error = importErrorMessage
		}
		return ctx.JSON(http.StatusOK, resp)
	}
	return ctx.JSON(http.StatusOK, importStatusResponse{
		Running:  true,
		JobID:    job.id,
		Status:   importStatusRunning,
		Progress: &prog,
	})
}

// sendImportEvent sends a single SSE event with a monotonic id field.
func (c *Controller) sendImportEvent(ctx echo.Context, id uint64, event string, data any) error {
	payload, err := c.safeMarshalJSON(event, data)
	if err != nil {
		return fmt.Errorf("marshal import event: %w", err)
	}
	msg := fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", id, event, payload)
	if conn, ok := ctx.Response().Writer.(WriteDeadlineSetter); ok {
		if err := conn.SetWriteDeadline(time.Now().Add(sseWriteDeadline)); err != nil {
			// Best-effort: not all response writers support deadlines; log and proceed,
			// matching sendSSEMessage in sse.go.
			c.logDebugIfEnabled("Failed to set write deadline for import SSE event", logger.Error(err))
		}
	}
	if _, err := ctx.Response().Write([]byte(msg)); err != nil {
		return fmt.Errorf("write import event: %w", err)
	}
	if f, ok := ctx.Response().Writer.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

// sendImportHeartbeat sends a lightweight SSE event to keep the connection alive.
func (c *Controller) sendImportHeartbeat(ctx echo.Context) error {
	return c.sendSSEMessage(ctx, importEventHeartbeat, map[string]int64{"ts": time.Now().Unix()})
}

// sendImportTerminal sends the final SSE event based on the import outcome.
func (c *Controller) sendImportTerminal(ctx echo.Context, seq uint64, stats imports.ImportStats, runErr error) error {
	switch {
	case runErr == nil:
		return c.sendImportEvent(ctx, seq, importEventComplete, toImportProgress(stats))
	case errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded):
		return c.sendImportEvent(ctx, seq, importEventCancelled, toImportProgress(stats))
	default:
		return c.sendImportEvent(ctx, seq, importEventError, importErrorPayload{
			Message:        importErrorMessage,
			importProgress: toImportProgress(stats),
		})
	}
}
