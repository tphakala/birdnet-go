package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// errProcessingBusy reports that every processing slot is taken. Callers answer
// 503; no job was started.
var errProcessingBusy = errors.NewStd("processing queue full")

// clipFile is a detection's stored clip, resolved and statted for processing.
type clipFile struct {
	// normalizedPath is the validated path relative to the export root.
	normalizedPath string
	// absolutePath is normalizedPath under the export root.
	absolutePath string
	// sourceTag ties cache entries to this file (see clipSourceTag).
	sourceTag string
}

// clipLookupError is a failed clip resolution: the cause, the user-facing
// message and the HTTP status the endpoints answer it with.
type clipLookupError struct {
	err     error
	message string
	status  int
}

// respondClipLookupError writes the response for a failed clip lookup.
func (c *Handler) respondClipLookupError(ctx echo.Context, e *clipLookupError) error {
	return c.HandleError(ctx, e.err, e.message, e.status)
}

// resolveClipPath returns the validated, export-root-relative clip path of noteID.
func (c *Handler) resolveClipPath(noteID string) (string, *clipLookupError) {
	clipPath, err := c.DS.GetNoteClipPath(noteID)
	if err != nil {
		if isClipNotFoundErr(err) {
			return "", &clipLookupError{err: err, message: "No audio clip available", status: http.StatusNotFound}
		}
		return "", &clipLookupError{err: err, message: "Failed to get clip path", status: http.StatusInternalServerError}
	}
	if clipPath == "" {
		return "", &clipLookupError{err: fmt.Errorf("no audio file found"), message: "No audio clip available", status: http.StatusNotFound}
	}
	normalizedPath, err := c.normalizeAndValidatePathWithLogger(clipPath, c.APILogger)
	if err != nil {
		return "", &clipLookupError{err: err, message: "Invalid clip path", status: http.StatusBadRequest}
	}
	return normalizedPath, nil
}

// statClip stats the clip at normalizedPath and derives its cache source tag.
// The stat is fresh, not StatRel's cached one: cache keys carry the file's size
// and mtime, so a replacement that changes either misses the old entry on the
// next request. A stat failure keeps its cause so callers can tell a missing
// clip from other failures.
func (c *Handler) statClip(normalizedPath string) (clipFile, *clipLookupError) {
	absolutePath := filepath.Join(c.SFS.BaseDir(), normalizedPath)
	info, err := c.SFS.Stat(absolutePath)
	if err != nil {
		return clipFile{}, &clipLookupError{err: err, message: "Audio clip not found", status: http.StatusNotFound}
	}
	return clipFile{
		normalizedPath: normalizedPath,
		absolutePath:   absolutePath,
		sourceTag:      clipSourceTag(normalizedPath, info),
	}, nil
}

// lookupClip resolves noteID to its stored clip file: clip path lookup, path
// validation and a fresh stat. It is the one resolution the process, audible
// bats and denoised audio endpoints share.
func (c *Handler) lookupClip(noteID string) (clipFile, *clipLookupError) {
	normalizedPath, lookupErr := c.resolveClipPath(noteID)
	if lookupErr != nil {
		return clipFile{}, lookupErr
	}
	return c.statClip(normalizedPath)
}

// tryAcquireProcessingSlot takes one of the shared processing slots without
// blocking. When ok is true the caller must call release.
func (c *Handler) tryAcquireProcessingSlot() (release func(), ok bool) {
	select {
	case c.processingSemaphore <- struct{}{}:
		return func() { <-c.processingSemaphore }, true
	default:
		return nil, false
	}
}

// processedOutput is the output of a processing job (a WAV or a rendered PNG) and
// the time it was stored.
type processedOutput struct {
	data    []byte
	modTime time.Time
}

// processWAV runs ProcessToWAV for key through the shared flight group, so
// concurrent requests for one key (a GET seeking during processing, a POST for
// the same filters) run one job. The job is tracked by the core wait group (see
// runTracked) and runs on the handler's lifecycle
// context, not the leader's request, so an aborted request or a seek does not
// discard work other callers wait for; each caller waits on its own request
// context. The leader takes a processing slot without blocking and fails with
// errProcessingBusy when none is free.
func (c *Handler) processWAV(reqCtx context.Context, key, absolutePath string, filters transcode.Filters) (*processedOutput, error) {
	return doFlight(c, reqCtx, key, func() (*processedOutput, error) {
		return c.runProcessingJob(key, absolutePath, filters)
	})
}

// doFlight runs job once per key across concurrent callers through the shared
// flight group, tracked by the core wait group (see runTracked) and on the
// handler's lifecycle context, and waits for it or for reqCtx, whichever ends
// first.
func doFlight[T any](c *Handler, reqCtx context.Context, key string, job func() (T, error)) (T, error) {
	var zero T
	ch := c.processingFlight.DoChan(key, func() (any, error) {
		out, err := runTracked(c, key, job)
		if err != nil {
			return nil, err
		}
		return out, nil
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return zero, res.Err
		}
		out, ok := res.Val.(T)
		if !ok {
			return zero, fmt.Errorf("unexpected processing result type %T", res.Val)
		}
		return out, nil
	case <-reqCtx.Done():
		return zero, reqCtx.Err()
	}
}

// runTracked runs job in a goroutine registered with the core's wait group, so
// Shutdown waits for a job still holding a slot, a temp file or a cache write, and
// waits for its result. A panic in the job becomes an error: the flight function
// runs on a goroutine of its own, where nothing could recover it and the process
// would exit; the panic is logged with its stack and the cache key. Once Shutdown
// has cancelled the core no job starts and the call fails with context.Canceled
// (see Core.GoUnlessStopped), so a job never outlives Shutdown.
func runTracked[T any](c *Handler, key string, job func() (T, error)) (T, error) {
	type jobResult struct {
		out T
		err error
	}
	done := make(chan jobResult, 1)
	started := c.GoUnlessStopped(func() {
		var res jobResult
		defer func() {
			if r := recover(); r != nil {
				c.LogErrorIfEnabled("Processing job panicked",
					logger.String("cache_key", key),
					logger.String("panic", fmt.Sprint(r)),
					logger.String("stack", string(debug.Stack())),
				)
				res = jobResult{err: fmt.Errorf("processing job panicked: %v", r)}
			}
			done <- res
		}()
		res.out, res.err = job()
	})
	if !started {
		var zero T
		return zero, context.Canceled
	}
	res := <-done
	return res.out, res.err
}

// runProcessingJob is the flight leader's work: a last cache check (a flight that
// just finished may have stored the entry after the caller's miss), a slot, the
// transcode and the cache store.
func (c *Handler) runProcessingJob(key, absolutePath string, filters transcode.Filters) (*processedOutput, error) {
	if c.processingCache != nil {
		if cached := c.processingCache.get(key); cached != nil {
			return &processedOutput{data: cached, modTime: c.entryWriteTime(key)}, nil
		}
	}

	release, ok := c.tryAcquireProcessingSlot()
	if !ok {
		return nil, errProcessingBusy
	}
	defer release()
	if c.processingStartedHook != nil {
		c.processingStartedHook()
	}

	wavData, err := transcode.ProcessToWAV(c.Context(), absolutePath, filters)
	if err != nil {
		return nil, err
	}

	// Cache the result (non-fatal on failure).
	if c.processingCache != nil {
		if err := c.processingCache.put(key, wavData); err != nil {
			c.LogWarnIfEnabled("Failed to cache processed audio",
				logger.String("cache_key", key),
				logger.Error(err),
			)
		}
	}
	return &processedOutput{data: wavData, modTime: c.entryWriteTime(key)}, nil
}

// entryWriteTime is the write time of the cache entry for key, the value a later
// cache hit serves as Last-Modified, or the current time when there is no entry
// (a failed store or no cache).
func (c *Handler) entryWriteTime(key string) time.Time {
	if c.processingCache != nil {
		if modTime, ok := c.processingCache.entryModTime(key); ok {
			return modTime
		}
	}
	return time.Now()
}

// mayDenoise reports whether the request may start a denoise: a signed-in client
// always may, a guest only for the playback default currently in effect. The
// setting is read per request so a change takes effect without a restart.
func (c *Handler) mayDenoise(ctx echo.Context, preset string) bool {
	if c.clientAuthenticated(ctx) {
		return true
	}
	effective := c.CurrentSettings().EffectivePlaybackDenoise()
	return effective != "" && effective == preset
}

// clientAuthenticated reports whether the request comes from a signed-in client.
// Without an injected check every client is unauthenticated.
func (c *Handler) clientAuthenticated(ctx echo.Context) bool {
	return c.isClientAuthenticated != nil && c.isClientAuthenticated(ctx)
}

// serveDenoisedAudio answers GET /api/v2/audio/:id?denoise=<preset>: a WAV of the
// stored clip denoised at the preset, with Range support. It shares cache entries
// and jobs with POST /process for a denoise-only request.
//
// A cache hit is served before guest gating, so a guest's Range follow-up for a
// WAV it already got keeps working. On a miss a guest starts a denoise only for
// the playback default currently in effect; otherwise it gets the stored clip,
// marked no-store. A missing clip is never answered with
// raw bytes: it follows the same not-ready and gone decision as the stored-clip
// path, or is denoised from its alternate-extension file.
func (c *Handler) serveDenoisedAudio(ctx echo.Context, noteID, preset string) error {
	allowed := c.mayDenoise(ctx, preset)
	normalizedPath, lookupErr := c.resolveClipPath(noteID)
	if lookupErr != nil {
		return c.respondClipLookupError(ctx, lookupErr)
	}
	clip, lookupErr := c.statClip(normalizedPath)
	if lookupErr != nil {
		if !errors.Is(lookupErr.err, os.ErrNotExist) {
			return c.respondClipLookupError(ctx, lookupErr)
		}
		if !allowed {
			// Guests get the stored-clip answer, including its not-ready handling.
			return c.serveStoredAudioByID(ctx, noteID, true)
		}
		var done bool
		var err error
		clip, done, err = c.recoverMissingClip(ctx, noteID, normalizedPath, lookupErr.err)
		if !done {
			return err
		}
	}

	filters := transcode.Filters{Denoise: preset}
	key := processingCacheKey(noteID, clip.sourceTag, false, preset, 0)
	downloadName := filepath.Base(clip.normalizedPath)

	if c.processingCache != nil {
		if f, info, ok := c.processingCache.open(key); ok {
			defer func() { _ = f.Close() }()
			return c.serveProcessedWAV(ctx, key, downloadName, f, info.Size(), info.ModTime())
		}
	}

	if !allowed {
		return c.serveStoredAudioByID(ctx, noteID, true)
	}

	processed, err := c.processWAV(ctx.Request().Context(), key, clip.absolutePath, filters)
	if errors.Is(err, errProcessingBusy) {
		c.LogDebugIfEnabled("Denoised audio deferred: processing queue full",
			logger.String("note_id", noteID))
		return c.writeAudioNotReady(ctx, err, "Server busy, try again later", minRetryAfterSeconds)
	}
	if err != nil {
		return c.transcodeError(ctx, noteID, "media_denoised_audio_failed", processedAudioFormat,
			filters, err, "Failed to process audio")
	}
	return c.serveProcessedWAV(ctx, key, downloadName, bytes.NewReader(processed.data),
		int64(len(processed.data)), processed.modTime)
}

// recoverMissingClip handles a clip that is not on disk for the denoise path. It
// returns the clip to process (the alternate-extension file, or the original once
// an in-flight encode has finished) with done true, or done false with the
// response already written (or the error to return).
func (c *Handler) recoverMissingClip(ctx echo.Context, noteID, normalizedPath string, statErr error) (clip clipFile, done bool, err error) {
	if altPath, found := c.findAlternateAudioPath(normalizedPath); found {
		return c.statRecovered(ctx, altPath)
	}

	begin, end := c.noteCaptureTimes(noteID)
	verdict := c.assessMissingClip(ctx, normalizedPath, begin, end)
	switch verdict.outcome {
	case missingClipAppearedAfterEncoding, missingClipAppearedAfterGrace:
		return c.statRecovered(ctx, normalizedPath)
	default:
		return clipFile{}, false, c.writeMissingClipVerdict(ctx, verdict, statErr,
			logger.String("note_id", noteID),
			logger.String("path", ctx.Request().URL.Path),
		)
	}
}

// statRecovered stats a clip found after a miss, answering a stat failure with
// the lookup error response.
func (c *Handler) statRecovered(ctx echo.Context, normalizedPath string) (clipFile, bool, error) {
	clip, lookupErr := c.statClip(normalizedPath)
	if lookupErr != nil {
		return clipFile{}, false, c.respondClipLookupError(ctx, lookupErr)
	}
	return clip, true, nil
}

// serveProcessedWAV serves processed WAV content through http.ServeContent, which
// adds Range, If-Range and conditional-request handling. The ETag comes from the
// cache key and the output length, and the modification time is the cache entry's
// write time, never the source file's, so an If-Range date cannot match the
// stored clip served at the same URL for a guest.
func (c *Handler) serveProcessedWAV(ctx echo.Context, key, originalName string, content io.ReadSeeker, size int64, modTime time.Time) error {
	header := ctx.Response().Header()
	header.Set(echo.HeaderContentType, MimeTypeWAV)
	setAudioContentDisposition(ctx, strings.TrimSuffix(originalName, filepath.Ext(originalName))+".wav")
	header.Set(headerAcceptRanges, acceptRangesBytes)
	header.Set("ETag", processedETag(key, size))
	c.setPrivateAudioCacheControl(ctx)
	http.ServeContent(ctx.Response(), ctx.Request(), "", modTime, content)
	return nil
}

// processedETag is the ETag of a processed artifact: the cache key, which names
// the clip file and every parameter, and the output length.
func processedETag(key string, size int64) string {
	return `"` + strings.TrimSuffix(key, filepath.Ext(key)) + "-" + strconv.FormatInt(size, 10) + `"`
}
