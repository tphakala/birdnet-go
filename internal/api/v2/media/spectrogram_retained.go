// spectrogram_retained.go: serving spectrogram renders that outlived their audio
// clip. Retention can delete a clip's audio while keeping its PNG renders; the
// detection then carries the old clip name in spectrogram_clip_name, and the by-ID
// endpoint serves an existing render from it without ever generating one.

package media

import (
	"cmp"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
	"github.com/tphakala/birdnet-go/internal/spectrogram/specfile"
)

const (
	// msgNoAudioClip is the 404 message for a note without an audio clip.
	msgNoAudioClip = "No audio clip available for this note"
	// msgServeSpectrogramFailed is the error message when serving a spectrogram file fails.
	msgServeSpectrogramFailed = "Failed to serve spectrogram image"
	// msgRetainedSpectrogramMissing is the 404 message when a detection has no
	// audio and no kept spectrogram render of its clip.
	msgRetainedSpectrogramMissing = "Spectrogram not available: the audio clip was removed and no spectrogram image was kept"
)

// errClipNameNotRelative is returned when a stored clip name does not normalize to a
// relative path inside the clips directory.
var errClipNameNotRelative = errors.NewStd("clip name is not a valid relative path")

// errNoClipForNote marks "the note exists but has no audio clip". The clip lookup
// returns it without writing a response so the caller can decide what to do.
var errNoClipForNote = errors.NewStd("note has no audio clip")

// noClipForNoteError wraps the underlying cause (the repository's ErrNoClipPath, or
// the legacy empty-path error) so a fallback 404 carries the same text as before,
// while errors.Is(err, errNoClipForNote) still matches.
type noClipForNoteError struct {
	cause error
}

func (e *noClipForNoteError) Error() string { return e.cause.Error() }

func (e *noClipForNoteError) Unwrap() error { return e.cause }

func (e *noClipForNoteError) Is(target error) bool { return target == errNoClipForNote }

// writeNoClip404 writes the "No audio clip available" 404 for a note without audio
// and returns cause, matching what the clip lookup did before it stopped writing
// this response itself.
func (c *Handler) writeNoClip404(ctx echo.Context, noteID string, cause error) error {
	if errors.Is(cause, repository.ErrNoClipPath) {
		c.LogErrorIfEnabled("Failed to get clip path from database",
			logger.String("note_id", noteID),
			logger.Error(cause),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()))
	} else {
		c.LogWarnIfEnabled("Empty clip path for note",
			logger.String("note_id", noteID),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()))
	}
	_ = c.HandleError(ctx, cause, msgNoAudioClip, http.StatusNotFound)
	return cause
}

// serveRetainedSpectrogram serves a spectrogram render for a detection whose audio
// was removed. It reads the kept clip name and the model type from the detection in
// one narrow query; without a kept name it answers the same 404 as a note that never
// had audio. It never generates.
func (c *Handler) serveRetainedSpectrogram(ctx echo.Context, noteID string, noClipErr error) error {
	keptClip, modelType, err := c.DS.GetNoteKeptSpectrogram(noteID)
	if err != nil {
		if isClipNotFoundErr(err) {
			return c.writeNoClip404(ctx, noteID, noClipErr)
		}
		return c.HandleError(ctx, err, "Failed to load detection", http.StatusInternalServerError)
	}
	if keptClip == "" {
		return c.writeNoClip404(ctx, noteID, noClipErr)
	}

	relClipPath, err := c.resolveClipRel(keptClip)
	if err != nil {
		c.LogWarnIfEnabled("Invalid kept spectrogram clip name",
			logger.String("note_id", noteID),
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()))
		return c.HandleError(ctx, err, msgRetainedSpectrogramMissing, http.StatusNotFound)
	}

	params := c.parseSpectrogramParameters(ctx)
	freqSuffix := spectrogram.ProfileSuffix(spectrogram.ProfileForModelType(modelType))

	served, err := c.serveExistingRender(ctx, noteID, relClipPath, params, freqSuffix)
	if served {
		return err
	}
	return c.HandleError(ctx, fmt.Errorf("no spectrogram render on disk for note %s", noteID),
		msgRetainedSpectrogramMissing, http.StatusNotFound)
}

// serveExistingRender serves a non-empty regular-file spectrogram render of the clip at
// relClipPath (the clip's SecureFS-relative audio path, which need not exist). It
// prefers the exact file the request parameters name and otherwise picks the
// nearest render of the same clip. served is false, with no response written, when
// the clip has no usable render. It never generates a spectrogram.
func (c *Handler) serveExistingRender(ctx echo.Context, noteID, relClipPath string, params spectrogramParameters, freqSuffix string) (served bool, err error) {
	_, _, _, exact := buildSpectrogramPaths(relClipPath, params.width, params.raw, params.style, params.dynamicRange, freqSuffix)
	target := exact
	if info, statErr := c.SFS.StatRel(exact); statErr != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		target = c.nearestRender(relClipPath, params)
		if target == "" {
			return false, nil
		}
	}

	c.LogDebugIfEnabled("Serving existing spectrogram render without audio",
		logger.String("note_id", noteID),
		logger.String("spectrogram_path", target),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()))

	if serveErr := c.serveSpectrogramFile(ctx, target); serveErr != nil {
		return true, c.translateSecureFSError(ctx, serveErr, msgServeSpectrogramFailed)
	}
	return true, nil
}

// serveSpectrogramFile serves the spectrogram image at relPath with the long-lived
// immutable cache header: a render is deterministic for its clip and parameters and
// never changes once written, so browsers can keep it. On a failure that left the
// response uncommitted the header is dropped, so an error response is not cached;
// the caller translates the returned error.
func (c *Handler) serveSpectrogramFile(ctx echo.Context, relPath string) error {
	ctx.Response().Header().Set("Cache-Control", fmt.Sprintf("%s, max-age=%d, immutable", c.mediaCacheVisibility(), SpectrogramCacheSeconds))
	err := c.SFS.ServeRelativeFile(ctx, relPath)
	if err != nil && !ctx.Response().Committed {
		ctx.Response().Header().Del("Cache-Control")
	}
	return err
}

// resolveClipRel turns a stored clip name into a SecureFS-relative path. A name that
// normalizes to nothing (traversal, absolute) would resolve to the root directory,
// so it is an error.
func (c *Handler) resolveClipRel(clipName string) (string, error) {
	normalized := apicore.NormalizeClipPath(clipName, c.CurrentSettings().Realtime.Audio.Export.Path)
	if normalized == "" {
		return "", errClipNameNotRelative
	}
	return c.SFS.ValidateRelativePath(normalized)
}

// nearestRender lists the clip's directory and returns the relative path of the
// best non-empty regular-file render of that clip, or "" when there is none. The
// order is: same raw/legend kind as requested, sized before unsized, smallest
// width distance (ties to the larger width), then name. It depends only on the
// clip's own base name, so it cannot pick another clip's file.
func (c *Handler) nearestRender(relClipPath string, params spectrogramParameters) string {
	dir := filepath.Dir(relClipPath)
	entries, err := c.SFS.ReadDirRel(dir)
	if err != nil {
		return ""
	}
	clipBase := strings.TrimSuffix(filepath.Base(relClipPath), filepath.Ext(relClipPath))

	found, _ := specfile.Renders(entries, clipBase)
	if len(found) == 0 {
		return ""
	}
	best := slices.MinFunc(found, func(a, b specfile.Found) int {
		return compareRenders(a, b, params)
	})
	return filepath.Join(dir, best.Name)
}

// compareRenders orders renders from best to worst substitute for the requested
// parameters, so the smallest element is the best one.
func compareRenders(a, b specfile.Found, params spectrogramParameters) int {
	return cmp.Or(
		// A render of the requested raw/legend kind first.
		cmp.Compare(boolRank(a.Render.Raw != params.raw), boolRank(b.Render.Raw != params.raw)),
		// Sized renders before the unsized prerender file.
		cmp.Compare(boolRank(a.Render.Unsized), boolRank(b.Render.Unsized)),
		// Nearest width, then the larger width.
		cmp.Compare(absInt(a.Render.Width-params.width), absInt(b.Render.Width-params.width)),
		cmp.Compare(b.Render.Width, a.Render.Width),
		cmp.Compare(a.Name, b.Name),
	)
}

// boolRank maps false to 0 and true to 1, so false sorts first.
func boolRank(v bool) int {
	if v {
		return 1
	}
	return 0
}

// absInt returns the absolute value of n.
func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
