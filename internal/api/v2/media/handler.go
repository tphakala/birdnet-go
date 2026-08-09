// Package media is the api/v2 media domain handler. It owns the media-serving
// endpoints (audio clips and spectrogram images served from the SecureFS
// sandbox), the on-demand spectrogram generation pipeline, the ID-based audio
// and spectrogram endpoints (including the greedy GET /api/v2/audio/:id route
// registered directly on the Echo instance), audio clip extraction and
// processing, the cached bird-image proxy (ServeSpeciesImageProxy, which the
// species domain injects for its thumbnail endpoint), and the external-media
// mount-status endpoint.
//
// The Handler embeds *apicore.Core by pointer so the shared dependencies and
// helpers (the media SecureFS sandbox, BirdImageCache, Settings accessors,
// AuthMiddleware, the HandleError/logging helpers, and the Context/Cancel/Wait
// lifecycle) are available without re-plumbing. Beyond the core it owns the
// audio-processing cache and concurrency limiter, the spectrogram generator,
// and the injectable external-media probe seams.
package media

import (
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

const (
	// processingCacheDirName is the SecureFS-relative directory under the media
	// export root that holds temporary processed-audio cache files.
	processingCacheDirName = ".processing-cache"

	// processingSemaphoreSize bounds the number of concurrent audio-processing
	// (normalize/denoise/gain) operations to limit CPU/memory pressure.
	processingSemaphoreSize = 2
)

// Handler serves the api/v2 media domain endpoints. It embeds the shared
// *apicore.Core (by pointer) and additionally owns the audio-processing cache
// and concurrency limiter, the spectrogram generator, and the injectable
// external-media probe seams.
type Handler struct {
	*apicore.Core

	// processingCache caches processed (normalize/denoise/gain) audio previews
	// under the media export root; processingSemaphore bounds concurrent
	// processing work. Both are owned by this handler and built in New.
	processingCache     *processingCache
	processingSemaphore chan struct{}

	// spectrogramGenerator renders spectrogram images from audio clips via the
	// media SecureFS sandbox. Built in New from the shared SecureFS.
	spectrogramGenerator *spectrogram.Generator

	// externalMediaEnv and externalMediaProbe are injectable dependencies for the
	// GET /api/v2/system/external-media endpoint. Both default to the real
	// sysinfo implementations at request time when nil; tests override them.
	externalMediaEnv   sysinfo.EnvGetter
	externalMediaProbe sysinfo.MountProber

	// audioWaitTimeoutOverride overrides the server-side wait for an in-progress
	// audio encoding used by waitForAudioFile. Zero in production, where the wait
	// uses audioWaitTimeout. Tests set a short timeout to exercise the
	// 503-after-timeout path without waiting the full default.
	audioWaitTimeoutOverride time.Duration

	// audioWaitStartedHook synchronizes tests with the point after an initial
	// audio serve miss enters a wait path. Production leaves it nil.
	audioWaitStartedHook func()
}

// New constructs the media domain handler around the shared core. It builds the
// audio-processing cache and concurrency limiter and the spectrogram generator
// from the shared SecureFS, and starts the cache-cleanup goroutine on the core's
// wait group; the facade's Shutdown (Cancel + Wait) tears it down, so no
// separate shutdown hook is needed (the audio-domain precedent). The
// external-media probe seams default to nil (real sysinfo at request time).
func New(core *apicore.Core) *Handler {
	h := &Handler{Core: core}

	// Initialize the audio processing cache and concurrency limiter under the
	// media SecureFS export root.
	cacheDir := filepath.Join(core.SFS.BaseDir(), processingCacheDirName)
	h.processingCache = newProcessingCache(cacheDir, processingCacheMaxFiles)
	h.processingSemaphore = make(chan struct{}, processingSemaphoreSize)

	// Start the cache cleanup goroutine (tracked by the core wait group, stopped
	// when the core context is cancelled by the facade's Shutdown).
	core.Go(func() {
		ticker := time.NewTicker(processingCacheTickerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-core.Context().Done():
				return
			case <-ticker.C:
				h.processingCache.cleanExpired()
			}
		}
	})

	// Spectrogram generator (needs the media SecureFS). The construction-time
	// settings snapshot from the shared core matches the monolith's behavior.
	h.spectrogramGenerator = spectrogram.NewGenerator(core.Settings.Load(), core.SFS, getSpectrogramLogger())

	return h
}
