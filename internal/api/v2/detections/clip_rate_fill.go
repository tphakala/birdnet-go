package detections

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// clipRateWorkTimeout bounds the shared probe-and-store work for one row. It
	// runs detached from the request, so a client disconnect does not cancel it.
	clipRateWorkTimeout = 5 * time.Second

	// clipRateResponseBudget is the longest GetDetection waits for the lazy fill.
	// On a miss the response omits clipSampleRate while the work finishes in the
	// background, and the next request reads the stored value.
	clipRateResponseBudget = 250 * time.Millisecond

	// clipRateFlightKeyPrefix prefixes the singleflight key (plus the note ID) so
	// one probe and one write per row run at a time.
	clipRateFlightKeyPrefix = "clip-rate:"

	// maxClipSampleRateHz is the highest clip sample rate the lazy fill stores.
	// The decoder's header probe enforces the same bound; this is a defensive
	// check so a bad value is never written.
	maxClipSampleRateHz = 1_000_000

	// Error classes for the debug log of a failed probe (no path or error text,
	// since the endpoint is public).
	clipRateErrMissing     = "missing"
	clipRateErrPermission  = "permission"
	clipRateErrUnsupported = "unsupported"
	clipRateErrNoAudio     = "no_audio"
	clipRateErrOther       = "other"
)

// clipRateProbe reads a clip's sample rate from its header without decoding it.
type clipRateProbe func(ctx context.Context, path string) (int, error)

// probeClipFileRate is the production clipRateProbe: the decoder's header probe,
// the same one the native spectrogram renderer opens clips with.
func probeClipFileRate(ctx context.Context, path string) (int, error) {
	info, err := transcode.ProbeFile(ctx, path)
	if err != nil {
		return 0, err
	}
	return info.SampleRate, nil
}

// clipRateFlight de-duplicates the fill work per row while it is in flight. It
// holds no result afterwards, so the fill is stateless.
type clipRateFlight = singleflight.Group

// wantsClipRateFill reports whether note is a bat detection with a clip file and
// no stored clip sample rate. Spectrogram-only rows have no clip name and are
// never probed.
func wantsClipRateFill(note *datastore.Note) bool {
	return note.Model.ModelType == "bat" && note.ClipName != "" && note.ClipSampleRate == 0
}

// fillClipSampleRate learns the clip sample rate of an old bat detection that has
// none and stores it, so GetDetection can return it. Only GetDetection calls it;
// list, search, SSE and media paths never read a clip.
//
// It reads one clip header (never the audio), shares the probe and the guarded
// write between concurrent requests for the same row, and waits at most
// clipRateResponseBudget. On a miss note.ClipSampleRate stays 0 and the work
// finishes in the background. A failed probe writes nothing, so the next request
// tries again. The write changes a NULL value only (FillClipSampleRate).
func (c *Handler) fillClipSampleRate(note *datastore.Note) {
	if !wantsClipRateFill(note) {
		return
	}
	filler, ok := c.DS.(datastore.ClipSampleRateFiller)
	if !ok {
		return
	}
	absPath, ok := c.clipProbePath(note.ClipName)
	if !ok {
		return
	}

	noteID, clipName := note.ID, note.ClipName
	key := clipRateFlightKeyPrefix + strconv.FormatUint(uint64(noteID), 10)
	resultCh := c.clipRateGroup.DoChan(key, func() (any, error) {
		// Detached from the request: singleflight shares this result with every
		// waiter, so one client disconnecting must not fail the others.
		workCtx, cancel := context.WithTimeout(c.Context(), clipRateWorkTimeout)
		defer cancel()
		return c.probeAndStoreClipRate(workCtx, filler, noteID, clipName, absPath)
	})

	wait := c.clipRateWait
	if wait <= 0 {
		wait = clipRateResponseBudget
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case res := <-resultCh:
		if rate, isRate := res.Val.(int); isRate && res.Err == nil {
			note.ClipSampleRate = rate
		}
	case <-timer.C:
		c.LogDebugIfEnabled("Clip sample rate fill exceeded the response budget",
			logger.Uint64("note_id", uint64(noteID)))
	}
}

// clipProbePath resolves a stored clip name to the absolute path to probe, using
// the same helpers as the spectrogram status endpoint. ok is false when the name
// does not resolve inside the export directory or the clip is not a regular file.
func (c *Handler) clipProbePath(clipName string) (string, bool) {
	if c.SFS == nil {
		return "", false
	}
	normalized := apicore.NormalizeClipPath(clipName, c.CurrentSettings().Realtime.Audio.Export.Path)
	if normalized == "" {
		return "", false
	}
	rel, err := c.SFS.ValidateRelativePath(normalized)
	if err != nil {
		return "", false
	}
	// StatRel resolves inside the SecureFS root, so a symlink cannot lead the probe
	// outside it.
	info, err := c.SFS.StatRel(rel)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return filepath.Join(c.SFS.BaseDir(), rel), true
}

// probeAndStoreClipRate probes the clip header and stores the rate while the row
// is still NULL. It returns the probed rate even when the write fails or changes
// no row, because the rate belongs to the clip the response names.
func (c *Handler) probeAndStoreClipRate(ctx context.Context, filler datastore.ClipSampleRateFiller, noteID uint, clipName, absPath string) (any, error) {
	probe := c.probeClipRate
	if probe == nil {
		probe = probeClipFileRate
	}
	rate, err := probe(ctx, absPath)
	if err != nil {
		c.LogDebugIfEnabled("Clip sample rate probe failed",
			logger.Uint64("note_id", uint64(noteID)),
			logger.String("error_class", clipRateErrorClass(err)))
		return nil, err
	}
	if rate <= 0 || rate > maxClipSampleRateHz {
		return nil, errors.NewStd("clip sample rate out of range")
	}
	if _, err := filler.FillClipSampleRate(noteID, clipName, rate); err != nil {
		c.LogWarnIfEnabled("Failed to store the clip sample rate; it stays unset",
			logger.Uint64("note_id", uint64(noteID)),
			logger.Error(err))
	}
	return rate, nil
}

// clipRateErrorClass names the kind of a probe failure for the debug log.
func clipRateErrorClass(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return clipRateErrMissing
	case errors.Is(err, os.ErrPermission):
		return clipRateErrPermission
	case errors.Is(err, transcode.ErrUnsupportedSource):
		return clipRateErrUnsupported
	case errors.Is(err, transcode.ErrUndecodableSource):
		return clipRateErrNoAudio
	default:
		return clipRateErrOther
	}
}
