package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
)

const (
	// denoiseQueryParam names the query parameter that selects a denoised variant
	// of an audio clip or its spectrogram.
	denoiseQueryParam = "denoise"
	// mimePNG is the content type of a rendered spectrogram.
	mimePNG = "image/png"
	// cacheControlNoStore keeps a cache from storing a response under a URL whose
	// bytes it does not describe.
	cacheControlNoStore = "no-store"
)

// denoisedSpectrogramRequest is what a denoised spectrogram request resolved from
// the note, the query and the settings.
type denoisedSpectrogramRequest struct {
	noteID   string
	clipPath string
	preset   string
	params   spectrogramParameters
	profile  spectrogram.FrequencyProfile
	// settings is the one snapshot the cache key's Style and DynamicRange and the
	// render both read, so a settings change in between cannot store an image
	// under a key it does not match.
	settings *conf.Settings
}

// renderSpectrogramPNG renders the spectrogram of wavData, a complete WAV file in
// memory, through temp files in the processing temp directory, which it removes
// before returning. Rendering is bounded by the generator's own render budget; it
// holds no processing slot.
func (c *Handler) renderSpectrogramPNG(ctx context.Context, wavData []byte, width int, raw bool, opts ...spectrogram.GenerateOption) ([]byte, error) {
	tmpDir, err := transcode.ProcessingTempDir(c.SFS.BaseDir())
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	wavFile, err := os.CreateTemp(tmpDir, "processed-*.wav")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	wavPath := wavFile.Name()
	defer func() { _ = os.Remove(wavPath) }()
	_, writeErr := wavFile.Write(wavData)
	if closeErr := wavFile.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return nil, fmt.Errorf("failed to write processed audio: %w", writeErr)
	}

	pngFile, err := os.CreateTemp(tmpDir, "spectrogram-*.png")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp spectrogram file: %w", err)
	}
	pngPath := pngFile.Name()
	_ = pngFile.Close()
	defer func() { _ = os.Remove(pngPath) }()

	if c.spectrogramRenderHook != nil {
		c.spectrogramRenderHook()
	}
	if err := c.spectrogramGenerator.GenerateFromFile(ctx, wavPath, pngPath, width, raw, opts...); err != nil {
		return nil, err
	}
	pngData, err := os.ReadFile(pngPath) //nolint:gosec // G304: temp file created above
	if err != nil {
		return nil, fmt.Errorf("failed to read spectrogram: %w", err)
	}
	return pngData, nil
}

// processToWAVInSlot runs ProcessToWAV holding one processing slot only for the
// transcode, so the render that follows does not occupy a slot. It fails with
// errProcessingBusy when no slot is free.
func (c *Handler) processToWAVInSlot(ctx context.Context, absolutePath string, filters transcode.Filters) ([]byte, error) {
	release, ok := c.tryAcquireProcessingSlot()
	if !ok {
		return nil, errProcessingBusy
	}
	defer release()
	return transcode.ProcessToWAV(ctx, absolutePath, filters)
}

// queryDenoisePreset reads and validates the denoise query parameter ("" when
// absent). ok is false when it is not a preset: the 400 is written and err is the
// handler's result.
func (c *Handler) queryDenoisePreset(ctx echo.Context) (preset string, ok bool, err error) {
	preset = ctx.QueryParam(denoiseQueryParam)
	if !transcode.IsValidDenoisePreset(preset) {
		return "", false, c.HandleError(ctx, fmt.Errorf("invalid denoise preset: %q", preset),
			"Invalid denoise preset", http.StatusBadRequest)
	}
	return preset, true, nil
}

// noStoreBeforeWrite marks the response Cache-Control: no-store at the moment the
// header is written, overriding any cache header the handler that answers sets
// itself. Used when a URL that names a denoised variant is answered with the
// stored spectrogram.
func noStoreBeforeWrite(ctx echo.Context) {
	resp := ctx.Response()
	resp.Before(func() { resp.Header().Set("Cache-Control", cacheControlNoStore) })
}

// serveDenoisedSpectrogram answers GET /api/v2/spectrogram/:id?denoise=<preset>
// with a render of the stored clip denoised at the preset, made from the same
// cached WAV the denoised audio GET uses. It reports handled false when it did
// not answer and the caller must answer with today's stored spectrogram; the
// caller marks it no-store, since it is not the bytes the URL names. That happens for a guest asking for anything but the playback default, a
// clip that is not on disk (the stored path owns pending, gone and kept-render
// handling), and the user-requested mode before the user generated the stored
// spectrogram, so a page load never starts a denoise there.
//
// A cached render is served first, before guest and mode gating. The key's Style
// and DynamicRange come from req.settings, the snapshot the render reads too.
func (c *Handler) serveDenoisedSpectrogram(ctx echo.Context, req *denoisedSpectrogramRequest) (handled bool, err error) {
	clip, ok := c.denoiseSourceClip(req.clipPath)
	if !ok {
		return false, nil
	}
	freqToken := spectrogram.ProfileSuffix(req.profile)
	visual := req.settings.Realtime.Dashboard.Spectrogram
	key := denoisedSpectrogramCacheKey(req.noteID, clip.sourceTag, req.preset, req.params.width, req.params.raw,
		freqToken, buildStyleSuffix(visual.Style, visual.DynamicRange))

	if c.processingCache != nil {
		if f, info, hit := c.processingCache.open(key); hit {
			defer func() { _ = f.Close() }()
			return true, c.serveProcessedPNG(ctx, key, f, info.Size(), info.ModTime())
		}
	}

	if !c.mayDenoise(ctx, req.preset) || !c.mayRenderOnLoad(req, clip) {
		return false, nil
	}

	out, renderErr := c.denoisedSpectrogramPNG(ctx.Request().Context(), req, clip, key)
	if renderErr != nil {
		return true, c.denoisedSpectrogramError(ctx, req, renderErr)
	}
	return true, c.serveProcessedPNG(ctx, key, bytes.NewReader(out.data), int64(len(out.data)), out.modTime)
}

// denoiseSourceClip resolves the stored clip of a denoised request, or reports
// false when it cannot be denoised from disk: an invalid path or a clip that is
// not there.
func (c *Handler) denoiseSourceClip(clipPath string) (clipFile, bool) {
	normalizedPath, err := c.normalizeAndValidatePathWithLogger(clipPath, c.APILogger)
	if err != nil {
		return clipFile{}, false
	}
	clip, lookupErr := c.statClip(normalizedPath)
	return clip, lookupErr == nil
}

// mayRenderOnLoad reports whether a request may start a render. Outside the
// user-requested mode it always may. In that mode a page load must not start
// processing, so it may only when the stored spectrogram for these parameters
// exists, which means the user asked for this clip's spectrogram.
func (c *Handler) mayRenderOnLoad(req *denoisedSpectrogramRequest, clip clipFile) bool {
	if c.CurrentSettings().Realtime.Dashboard.Spectrogram.GetMode() != conf.SpectrogramModeUserRequested {
		return true
	}
	_, _, _, relSpectrogramPath := buildSpectrogramPaths(clip.normalizedPath, req.params.width, req.params.raw,
		req.params.style, req.params.dynamicRange, spectrogram.ProfileSuffix(req.profile))
	info, statErr := c.SFS.StatRel(relSpectrogramPath)
	return statErr == nil && info.Size() > 0
}

// denoisedSpectrogramPNG renders the spectrogram of the denoised clip once for
// concurrent requests of one key. The job runs on the handler's lifecycle
// context, tracked by the core wait group, so a caller that leaves does not
// discard work others wait for. The WAV step shares its cache entry, flight and
// slot with the denoised audio GET and holds the slot only until the transcode
// returns; the render runs outside it.
func (c *Handler) denoisedSpectrogramPNG(reqCtx context.Context, req *denoisedSpectrogramRequest, clip clipFile, key string) (*processedOutput, error) {
	return doFlight(c, reqCtx, key, func() (*processedOutput, error) {
		return c.renderDenoisedSpectrogram(req, clip, key)
	})
}

// renderDenoisedSpectrogram is the flight leader's work: a last cache check, the
// denoised WAV, the render and the cache store.
func (c *Handler) renderDenoisedSpectrogram(req *denoisedSpectrogramRequest, clip clipFile, key string) (*processedOutput, error) {
	if c.processingCache != nil {
		if cached := c.processingCache.get(key); cached != nil {
			return &processedOutput{data: cached, modTime: c.entryWriteTime(key)}, nil
		}
	}

	filters := transcode.Filters{Denoise: req.preset}
	wavKey := processingCacheKey(req.noteID, clip.sourceTag, false, req.preset, 0)
	wav, err := c.processWAV(c.Context(), wavKey, clip.absolutePath, filters)
	if err != nil {
		return nil, err
	}
	pngData, err := c.renderSpectrogramPNG(c.Context(), wav.data, req.params.width, req.params.raw,
		spectrogram.WithFrequencyProfile(req.profile), spectrogram.WithSettings(req.settings))
	if err != nil {
		return nil, err
	}

	if c.processingCache != nil {
		if err := c.processingCache.put(key, pngData); err != nil {
			c.LogWarnIfEnabled("Failed to cache denoised spectrogram",
				logger.String("cache_key", key),
				logger.Error(err),
			)
		}
	}
	return &processedOutput{data: pngData, modTime: c.entryWriteTime(key)}, nil
}

// denoisedSpectrogramError answers a failed denoised render: 503 with Retry-After
// when every processing slot is taken, the render's own source status when the
// clip cannot be rendered, otherwise the transcode failure mapping.
func (c *Handler) denoisedSpectrogramError(ctx echo.Context, req *denoisedSpectrogramRequest, err error) error {
	if errors.Is(err, errProcessingBusy) {
		c.LogDebugIfEnabled("Denoised spectrogram deferred: processing queue full",
			logger.String("note_id", req.noteID))
		return c.writeAudioNotReady(ctx, err, "Server busy, try again later", minRetryAfterSeconds)
	}
	if handled, contextErr := c.handleRequestContextError(ctx); handled {
		return contextErr
	}
	if status, message, ok := spectrogramSourceStatus(err); ok {
		return c.HandleError(ctx, err, message, status)
	}
	return c.transcodeError(ctx, req.noteID, "media_denoised_spectrogram_failed", processedAudioFormat,
		transcode.Filters{Denoise: req.preset}, err, "Failed to generate spectrogram")
}

// serveProcessedPNG serves a rendered spectrogram through http.ServeContent, which
// adds conditional-request handling. The ETag comes from the cache key and the
// image length, and the modification time is the cache entry's write time.
func (c *Handler) serveProcessedPNG(ctx echo.Context, key string, content io.ReadSeeker, size int64, modTime time.Time) error {
	header := ctx.Response().Header()
	header.Set(echo.HeaderContentType, mimePNG)
	header.Set("ETag", processedETag(key, size))
	c.setPrivateAudioCacheControl(ctx)
	http.ServeContent(ctx.Response(), ctx.Request(), "", modTime, content)
	return nil
}
