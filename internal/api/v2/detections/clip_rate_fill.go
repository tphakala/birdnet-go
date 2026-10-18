package detections

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// clipRateWorkTimeout bounds the shared probe-and-store work for one row, the
	// guarded write included. It runs detached from the request, so a client
	// disconnect does not cancel it.
	clipRateWorkTimeout = 5 * time.Second

	// clipRateResponseBudget is the longest GetDetection waits for the lazy fill.
	// On a miss the response omits clipSampleRate while the work finishes in the
	// background, and the next request reads the stored value.
	clipRateResponseBudget = 250 * time.Millisecond

	// clipRateFlightKeyPrefix prefixes the singleflight key (plus the note ID) so
	// one probe and one write per row run at a time.
	clipRateFlightKeyPrefix = "clip-rate:"

	// Error classes for the debug log of a failed probe (no path or error text,
	// since the endpoint is public).
	clipRateErrMissing     = "missing"
	clipRateErrPermission  = "permission"
	clipRateErrUnsupported = "unsupported"
	clipRateErrNoAudio     = "no_audio"
	clipRateErrOther       = "other"
)

// errClipRateWorkPanicked marks a lazy-fill probe that panicked and was contained.
var errClipRateWorkPanicked = errors.NewStd("clip sample rate work panicked")

// errClipRateInvalid marks a probe that returned a non-positive rate.
var errClipRateInvalid = errors.NewStd("clip sample rate is not positive")

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

// wantsClipRateFill reports whether note is a bat detection with a clip file and
// no stored clip sample rate. Spectrogram-only rows have no clip name and are
// never probed.
func wantsClipRateFill(note *datastore.Note) bool {
	return note.Model.ModelType == string(entities.ModelTypeBat) && note.ClipName != "" && note.ClipSampleRate == 0
}

// fillClipSampleRate learns the clip sample rate of an old bat detection that has
// none and stores it, so GetDetection can return it. Only GetDetection calls it;
// list, search and SSE paths never probe a clip for the rate.
//
// It reads one clip header (never the audio), shares the probe and the guarded
// write between concurrent requests for the same row, and waits at most
// clipRateResponseBudget. On a miss note.ClipSampleRate stays 0 and the work
// finishes in the background. A failed probe writes nothing, so the next request
// tries again, and so does a panic in the shared work, which is contained and
// treated as a failed probe. The write changes a NULL value only (DetectionRepository.FillClipSampleRate).
func (c *Handler) fillClipSampleRate(note *datastore.Note) {
	if !wantsClipRateFill(note) {
		return
	}
	absPath, ok := c.clipProbePath(note.ClipName)
	if !ok {
		return
	}

	noteID, clipName := note.ID, note.ClipName
	key := clipRateFlightKeyPrefix + strconv.FormatUint(uint64(noteID), 10)
	resultCh := c.clipRateGroup.DoChan(key, func() (res any, err error) {
		// DoChan runs this on its own goroutine, which the HTTP recover middleware
		// does not cover, and a panic that escapes it ends the process. A decoder
		// panic on a malformed clip header is turned into an error instead.
		defer func() {
			if recovered := recover(); recovered != nil {
				c.LogWarnIfEnabled("Clip sample rate probe panicked; the rate stays unset",
					logger.Uint64("note_id", uint64(noteID)))
				res, err = nil, errClipRateWorkPanicked
			}
		}()
		// Detached from the request: singleflight shares this result with every
		// waiter, so one client disconnecting must not fail the others.
		workCtx, cancel := context.WithTimeout(c.Context(), clipRateWorkTimeout)
		defer cancel()
		return c.probeAndStoreClipRate(workCtx, noteID, clipName, absPath)
	})

	timer := time.NewTimer(c.clipRateWait)
	defer timer.Stop()
	select {
	case res := <-resultCh:
		if res.Err == nil {
			note.ClipSampleRate, _ = res.Val.(int)
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
	rel, err := apicore.ResolveClipRel(c.SFS, clipName, c.CurrentSettings().Realtime.Audio.Export.Path)
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
func (c *Handler) probeAndStoreClipRate(ctx context.Context, noteID uint, clipName, absPath string) (int, error) {
	rate, err := c.probeClipRate(ctx, absPath)
	if err != nil {
		c.LogDebugIfEnabled("Clip sample rate probe failed",
			logger.Uint64("note_id", uint64(noteID)),
			logger.String("error_class", clipRateErrorClass(err)))
		return 0, err
	}
	// ProbeFile rejects out-of-range rates; this keeps a non-positive value from ever being stored.
	if rate <= 0 {
		return 0, errClipRateInvalid
	}
	// ctx is the work context, so a write that outlives clipRateWorkTimeout is
	// cancelled and releases the singleflight key instead of pinning it.
	if _, err := c.Repo.FillClipSampleRate(ctx, noteID, clipName, rate); err != nil {
		// A write that ran out the work timeout is a slow disk, not something the
		// operator can act on; the next request retries.
		if errors.Is(err, context.DeadlineExceeded) {
			c.LogDebugIfEnabled("Clip sample rate write timed out; it stays unset",
				logger.Uint64("note_id", uint64(noteID)))
		} else {
			c.LogWarnIfEnabled("Failed to store the clip sample rate; it stays unset",
				logger.Uint64("note_id", uint64(noteID)),
				logger.Error(err))
		}
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
