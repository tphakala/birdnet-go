package audio

// Native HLS: the in-process path that replaces the FFmpeg process entirely.
//
// It lives in its own file rather than as branches inside audio_hls.go because
// the two paths share almost nothing below the handler. There is no subprocess,
// no FIFO, no output directory and no securefs here: PCM goes straight from the
// audio router into internal/audiocore/hlsmux, and the playlist and segments are
// served out of memory. audio_hls.go keeps its branch points to one line each and
// is otherwise untouched, so the FFmpeg fallback stays exactly as it was.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/audiocore/hlsmux"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

const (
	// nativeHLSSampleRate is the output rate the native path pins, regardless of
	// what the source or the live-stream setting says.
	//
	// Pinning is a real trade, not a shortcut. go-aac accepts only 44100 and
	// 48000, so an arbitrary source rate cannot be followed anyway, and a fixed
	// output rate removes a whole class of rate-mismatch playback bugs. The cost
	// is that a source at some other rate now goes through a resampler the
	// router would not otherwise have created, which is real CPU per listener.
	// That trade is accepted; what is not acceptable is doing it silently, so
	// createNativeHLSStream warns when the configured rate disagrees.
	nativeHLSSampleRate = 48000

	// nativeHLSChannels is the output channel count. It must equal
	// hlsConsumerChannels, which is what setupAudioCallback declares to the
	// router; see the compile-time assertion there, and the note on why a
	// mismatch is silently wrong rather than loudly rejected.
	nativeHLSChannels = 1

	// nativeHLSBytesPerSample is the width of one sample of one channel. The
	// capture pipeline is 16-bit end to end, which hlsmux also assumes.
	nativeHLSBytesPerSample = 2

	// nativeHLSDefaultBitrate is the target in kbps when the setting is unset.
	// The FFmpeg path resolves its own default through the same helper, so the
	// two encoders cannot drift apart.
	nativeHLSDefaultBitrate = 128

	// nativeFeedDrainTimeout bounds how long teardown waits for the feed
	// goroutine to stop before closing the muxer anyway. The wait exists to make
	// a write-after-close impossible; the bound exists so a wedged goroutine
	// cannot block shutdown.
	//
	// It must exceed the router's own drainerStopTimeout (5s). The feed
	// goroutine's first deferred action is res.cleanup(), which calls
	// RemoveRoute and blocks there for up to that long, so a shorter bound here
	// would expire during an ordinary stop and report a wedged feed that is
	// merely inside a teardown with a larger budget.
	nativeFeedDrainTimeout = 8 * time.Second
)

// isNative reports whether this stream is served by the in-process muxer rather
// than by an FFmpeg process.
func (s *HLSStreamInfo) isNative() bool { return s != nil && s.mux != nil }

// closeNativeMux stops the feed goroutine and closes the in-process muxer.
//
// It is a no-op on an FFmpeg-path stream and safe to call more than once, since
// hlsmux.Stream.Close is idempotent. Every teardown path must call it: the muxer
// owns an encoder, and the paths that delete a stream from the registry
// themselves (a force restart, the inactivity sweep) bypass the context watcher
// that would otherwise do the cleanup.
//
// Waiting for the feed goroutine before closing is load-bearing, not politeness.
// Every caller cancels the stream context first, but cancellation only makes the
// ctx.Done() arm of the feed's select ready; Go picks uniformly between ready
// arms, so a queued chunk would otherwise let the feed call Write on a muxer
// this function just closed. hlsmux rejects that write and reports it through
// internal/errors, whose Build() forwards to telemetry, so the race would fire a
// spurious Sentry event on roughly half of all clean stream stops.
func closeNativeMux(sourceID string, stream *HLSStreamInfo) {
	if !stream.isNative() {
		return
	}

	if stream.feedDone != nil {
		select {
		case <-stream.feedDone:
		case <-time.After(nativeFeedDrainTimeout):
			apicore.GetLogger().Warn("Native HLS feed did not stop before the drain timeout; closing the muxer anyway",
				logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
				logger.String("timeout", nativeFeedDrainTimeout.String()))
		}
	}

	closeNativeMuxGuarded(sourceID, stream)
}

// closeNativeMuxGuarded closes the muxer, containing a panic from the encoder.
//
// Close flushes the encoder tail, which runs the same codec code that may have
// just panicked. Without this the recover() on the feed goroutine would only
// move the crash: it cancels the stream, teardown reaches Close on a DIFFERENT
// goroutine (the context watcher, or the inactivity sweep), the flush re-enters
// the corrupt state, and that second panic is unrecovered. The process dies
// anyway, one hop later, which is exactly what the feed's recover exists to
// prevent.
func closeNativeMuxGuarded(sourceID string, stream *HLSStreamInfo) {
	defer func() {
		if r := recover(); r != nil {
			apicore.GetLogger().Error("Native HLS muxer panicked while closing; abandoning it",
				logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
				logger.Any("panic", r),
				logger.String("stack", string(debug.Stack())))
		}
	}()

	if err := stream.mux.Close(); err != nil {
		apicore.GetLogger().Warn("Native HLS muxer close reported an error",
			logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
			logger.Error(err))
	}
}

// effectiveBitrateKbps resolves the configured live-stream bitrate, applying the
// default and the same clamp for both encoders, so switching between them does
// not silently change the audio quality a listener gets.
func (c *Handler) effectiveBitrateKbps() int {
	bitrate := c.CurrentSettings().WebServer.LiveStream.BitRate
	switch {
	case bitrate <= 0:
		return nativeHLSDefaultBitrate
	case bitrate < hlsMinBitrate:
		return hlsMinBitrate
	case bitrate > hlsMaxBitrate:
		return hlsMaxBitrate
	default:
		return bitrate
	}
}

// createNativeHLSStream builds a stream served by the in-process muxer.
func (c *Handler) createNativeHLSStream(sourceID string) (*HLSStreamInfo, error) {
	sanitizedID := privacy.SanitizeRTSPUrl(sourceID)
	apicore.GetLogger().Info("Creating new HLS stream (native encoder)",
		logger.String("source_id", sanitizedID))

	// The configured sample rate cannot be honoured here (see nativeHLSSampleRate).
	// Say so rather than letting a setting the UI accepts, and which triggers a
	// disruptive restart of every live stream when edited, quietly do nothing.
	if configured := c.CurrentSettings().WebServer.LiveStream.SampleRate; configured > 0 && configured != nativeHLSSampleRate {
		apicore.GetLogger().Warn("Configured live-stream sample rate is ignored by the native encoder",
			logger.String("source_id", sanitizedID),
			logger.Int("configured_rate", configured),
			logger.Int("effective_rate", nativeHLSSampleRate))
	}

	segmentDuration := time.Duration(c.getEffectiveSegmentLength()) * time.Second
	bitrate := c.effectiveBitrateKbps()

	mux, err := hlsmux.New(&hlsmux.Config{
		Codec:           hlsmux.AACLC(),
		SampleRate:      nativeHLSSampleRate,
		Channels:        nativeHLSChannels,
		BitrateKbps:     bitrate,
		SegmentDuration: segmentDuration,
		WindowSize:      hlsListSize,
	})
	if err != nil {
		// Plain wrap, not another errors.Build(): hlsmux already terminated this
		// error with Build(), which reported it to telemetry. Rebuilding would
		// fire the reporter a second time for one failure, under a different
		// component and category.
		return nil, fmt.Errorf("create native HLS muxer: %w", err)
	}

	// Derive from the controller lifecycle, not the HTTP request: the stream
	// must outlive the /start call that created it.
	streamCtx, streamCancel := context.WithCancel(c.Context())

	// Register the audio route before publishing the stream, so PCM is already
	// buffering by the time a client can ask for a playlist.
	feed, err := c.prepareNativeAudioFeed(sourceID)
	if err != nil {
		streamCancel()
		if closeErr := mux.Close(); closeErr != nil {
			apicore.GetLogger().Warn("Failed to close native HLS muxer after feed setup failure",
				logger.String("source_id", sanitizedID), logger.Error(closeErr))
		}
		return nil, errors.New(err).
			Component("api").
			Category(errors.CategoryAudio).
			Context("operation", "prepare_native_audio_feed").
			Build()
	}

	stream := &HLSStreamInfo{
		SourceID:    sourceID,
		mux:         mux,
		feedDone:    make(chan struct{}),
		ctx:         streamCtx,
		cancel:      streamCancel,
		streamEpoch: time.Now(),
	}

	c.publishHLSStream(sourceID, stream)

	go func() {
		// The encoder now runs in this process rather than in an FFmpeg
		// subprocess, so the process boundary that used to contain a codec crash
		// is gone. An unrecovered panic in go-aac, go-m4a or hlsmux would take
		// down detection and the web server along with the stream, so it is
		// contained here and turned into a stream teardown.
		defer func() {
			if r := recover(); r != nil {
				apicore.GetLogger().Error("Native HLS feed panicked; tearing down the stream",
					logger.String("source_id", sanitizedID),
					logger.Any("panic", r))
			}
			close(stream.feedDone)
			if stream.ctx.Err() == nil {
				apicore.GetLogger().Warn("Native audio feed exited unexpectedly, cancelling stream",
					logger.String("source_id", sanitizedID))
				stream.cancel()
			}
		}()
		c.runNativeAudioFeedLoop(stream.ctx, sourceID, stream, feed)
	}()

	c.watchHLSStreamContext(sourceID, stream)

	apicore.GetLogger().Info("Native HLS stream created",
		logger.String("source_id", sanitizedID),
		logger.Int("sample_rate", nativeHLSSampleRate),
		logger.Int("bitrate_kbps", bitrate),
		logger.String("segment_duration", segmentDuration.String()))

	return stream, nil
}

// prepareNativeAudioFeed registers the audio route. Unlike the FFmpeg path there
// is no FIFO to open and no secure filesystem to hold, so the resources are just
// the channel and the route cleanup.
func (c *Handler) prepareNativeAudioFeed(sourceID string) (*audioFeedResources, error) {
	audioChan, callbackCleanup, err := c.setupAudioCallback(sourceID, nativeHLSSampleRate)
	if err != nil {
		return nil, err
	}
	return &audioFeedResources{
		audioChan: audioChan,
		cleanup:   callbackCleanup,
	}, nil
}

// runNativeAudioFeedLoop pumps PCM from the router into the muxer until the
// stream context is cancelled or the muxer latches a failure.
func (c *Handler) runNativeAudioFeedLoop(ctx context.Context, sourceID string, stream *HLSStreamInfo, res *audioFeedResources) {
	defer res.cleanup()

	sanitizedID := privacy.SanitizeRTSPUrl(sourceID)
	apicore.GetLogger().Debug("Native audio feed loop starting", logger.String("source_id", sanitizedID))

	const frameBytes = nativeHLSChannels * nativeHLSBytesPerSample

	var (
		dataWritten   bool
		framesWritten int64
		bytesWritten  int64
		realigned     int64
		rejected      int64
		lastRejectLog time.Time

		// carry holds the bytes of a partial sample frame left over from the
		// previous chunk, and joined is the scratch buffer the join happens in.
		//
		// This is not defensive: the router only guarantees whole sample frames
		// when it resamples or applies EQ/gain, and with the common default
		// (source already at the pinned rate, no EQ, 0 dB gain) it does neither,
		// so a partial read from an FFmpeg source arrives here as-is. Truncating
		// the odd byte would be worse than useless, because the dropped half of a
		// sample desynchronises every following byte and the audio becomes noise.
		// Carrying it into the next chunk keeps the sample stream aligned.
		carry  []byte
		joined []byte
	)

	defer func() {
		apicore.GetLogger().Debug("Native audio feed stats on exit",
			logger.String("source_id", sanitizedID),
			logger.Int64("frames_written", framesWritten),
			logger.Int64("bytes_written", bytesWritten),
			logger.Int64("frames_realigned", realigned),
			logger.Int64("chunks_rejected", rejected))
	}()

	for {
		select {
		case <-ctx.Done():
			apicore.GetLogger().Debug("Native audio feed stopped due to context cancellation",
				logger.String("source_id", sanitizedID))
			return
		case chunk, ok := <-res.audioChan:
			if !ok {
				apicore.GetLogger().Debug("Audio channel closed", logger.String("source_id", sanitizedID))
				return
			}

			// A frame with no capture time would anchor program-date-time to the
			// zero instant, putting every segment in year 1. Falling back to now
			// is slightly wrong for a delayed frame but stays on the real
			// timeline, which is what a monitoring UI needs.
			ts := chunk.timestamp
			if ts.IsZero() {
				ts = time.Now()
			}

			data := chunk.data
			if len(carry) > 0 {
				joined = append(joined[:0], carry...)
				joined = append(joined, data...)
				data = joined
				carry = carry[:0]
			}
			if rem := len(data) % frameBytes; rem != 0 {
				realigned++
				carry = append(carry[:0], data[len(data)-rem:]...)
				data = data[:len(data)-rem]
			}
			if len(data) == 0 {
				continue
			}

			if err := stream.mux.Write(data, ts); err != nil {
				if c.handleNativeWriteError(ctx, sanitizedID, stream, err, &rejected, &lastRejectLog) {
					return
				}
				continue
			}
			framesWritten++
			bytesWritten += int64(len(data))

			if !dataWritten {
				stream.firstDataTime.Store(ts.UnixNano())
				apicore.GetLogger().Debug("First audio data encoded",
					logger.String("source_id", sanitizedID),
					logger.String("first_data_time", ts.UTC().Format(time.RFC3339Nano)))
				dataWritten = true
			}
		}
	}
}

// handleNativeWriteError classifies a muxer write failure and reports whether
// the feed must stop.
//
// Only two of hlsmux.Write's rejections latch the stream (an encoder error and a
// failed segment cut); the rest are per-call validation rejections that leave the
// muxer able to accept the next well-formed chunk. Treating all of them as fatal
// would let one malformed chunk destroy a live stream and force the client to
// reconnect, so a non-latching rejection drops that chunk and continues.
func (c *Handler) handleNativeWriteError(
	ctx context.Context, sanitizedID string, stream *HLSStreamInfo,
	err error, rejected *int64, lastRejectLog *time.Time,
) (stop bool) {
	switch {
	case ctx.Err() != nil:
		// Teardown in flight. Reaching a rejected write here is the expected
		// shape of a clean stop, not a failure.
		apicore.GetLogger().Debug("Native audio feed stopped during teardown",
			logger.String("source_id", sanitizedID))
		return true

	case stream.mux.Stats().Failed:
		apicore.GetLogger().Error("Native HLS encode failed, stopping feed",
			logger.String("source_id", sanitizedID), logger.Error(err))
		return true

	default:
		*rejected++
		// Rate limited: a persistently malformed producer would otherwise log
		// once per audio frame.
		if now := time.Now(); now.Sub(*lastRejectLog) >= hlsDropLogInterval {
			*lastRejectLog = now
			apicore.GetLogger().Warn("Native HLS rejected a PCM chunk; dropping it and continuing",
				logger.String("source_id", sanitizedID),
				logger.Int64("rejected_total", *rejected),
				logger.Error(err))
		}
		return false
	}
}

// serveNativePlaylist writes the muxer's current media playlist.
//
// Unlike the FFmpeg path there is no PDT rewriting: program-date-time is
// anchored to the capture timestamp of each segment's first sample, so it is
// already correct when rendered and there is nothing to correct after the fact.
func (c *Handler) serveNativePlaylist(ctx echo.Context, sourceID string, stream *HLSStreamInfo) error {
	c.setHLSHeaders(ctx)
	ctx.Response().Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	ctx.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	playlist := stream.mux.Playlist()
	stats := stream.mux.Stats()

	c.checkNativeStreamFreshness(sourceID, &stats)

	// Tell a client polling before the first segment how long to wait, matching
	// what the FFmpeg path does with its placeholder playlist. Retained comes
	// from the same Stats already sampled, so the answer is consistent with the
	// playlist just rendered rather than read at a third instant.
	if stats.Retained == 0 {
		ctx.Response().Header().Set("Retry-After", strconv.Itoa(c.getEffectiveSegmentLength()))
	}

	return ctx.String(http.StatusOK, playlist)
}

// checkNativeStreamFreshness warns when a stream has stopped producing segments,
// which is the native equivalent of the FFmpeg path's checkSegmentFreshness.
//
// It keys on LastSegmentPDT rather than on Stats.Failed, because Failed reports
// only a latched encode error. The failure this exists to catch is the opposite
// one: a source that goes quiet produces no error at all, so the muxer stays
// healthy while the playlist silently stops advancing.
func (c *Handler) checkNativeStreamFreshness(sourceID string, stats *hlsmux.Stats) {
	if stats.LastSegmentPDT.IsZero() {
		return // no segment cut yet; the stream is still starting up
	}

	// Rate limited on the same interval and through the same map as the FFmpeg
	// path, so a polling client cannot turn this into a per-request warning.
	now := time.Now()
	if last, ok := lastFreshnessCheck.Load(sourceID); ok {
		if lastTime, isTime := last.(time.Time); isTime && now.Sub(lastTime) < hlsFreshnessCheckInterval {
			return
		}
	}
	lastFreshnessCheck.Store(sourceID, now)

	staleAfter := time.Duration(c.getEffectiveSegmentLength()*hlsSegmentFreshnessMultiplier) * time.Second
	if age := now.Sub(stats.LastSegmentPDT); age > staleAfter {
		apicore.GetLogger().Warn("Stale native HLS segments detected (source may have stopped delivering audio)",
			logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
			logger.String("newest_segment_age", age.Round(time.Second).String()),
			logger.String("stale_threshold", staleAfter.String()),
			logger.Uint64("segments_cut", stats.Segments),
			logger.Bool("encode_failed", stats.Failed))
	}
}

// serveNativeContent writes the init segment or one media segment from memory.
func (c *Handler) serveNativeContent(ctx echo.Context, stream *HLSStreamInfo, requestPath string) error {
	// Traversal is unreachable here because nothing below this line touches a
	// filesystem: the name is either matched against a literal or parsed to a
	// sequence number and looked up in an in-memory ring. That, not the check
	// immediately below, is what makes securefs unnecessary. The separator
	// rejection is defence in depth, and a reminder that any future branch which
	// did resolve a name against disk would need securefs rather than this.
	if requestPath == "" || strings.ContainsAny(requestPath, `/\`) {
		return c.HandleError(ctx, nil, "Invalid segment path", http.StatusBadRequest)
	}

	if requestPath == hlsmux.InitSegmentName {
		return c.serveNativeBytes(ctx, requestPath, stream.mux.InitSegment(), stream.streamEpoch)
	}

	seq, ok := hlsmux.ParseSegmentName(requestPath)
	if !ok {
		return c.HandleError(ctx, nil, "Invalid segment path", http.StatusBadRequest)
	}

	segment, ok := stream.mux.Segment(seq)
	if !ok {
		// The segment has scrolled out of the window, which is what a client
		// that fell too far behind should see.
		return c.HandleError(ctx, nil, "Segment no longer available", http.StatusNotFound)
	}

	// Validate on the stream epoch, not segment.PDT. PDT is the SOURCE's capture
	// clock, and sequence numbers restart at zero on every stream restart, so a
	// client holding segment0 from a previous stream could send If-Modified-Since
	// with a newer PDT and get a 304 for different audio. The epoch is this
	// process's own monotonic-enough per-stream value and cannot collide.
	return c.serveNativeBytes(ctx, requestPath, segment.Data, stream.streamEpoch)
}

// serveNativeBytes writes one in-memory artifact with the same HTTP semantics
// the disk path gets from securefs.
//
// http.ServeContent rather than a plain blob write, so range and conditional
// requests are answered. That is parity with how the FFmpeg path serves its
// media segments (securefs.ServeFile wraps the same function), not a fix for a
// known break: the FFmpeg path serves its INIT segment with a plain 200 and
// plays on iOS today, so a 200-only handler is evidently tolerated. Range
// support is worth having because AVFoundation does issue byte-range requests
// for fMP4 and the frontend falls back to native HLS there, but treat it as
// matching the stricter of the two existing behaviours rather than as a fix.
//
// modTime is the caller's choice of validator; see the call sites for why it is
// the stream epoch rather than per-segment capture time.
func (c *Handler) serveNativeBytes(ctx echo.Context, name string, data []byte, modTime time.Time) error {
	c.setHLSHeaders(ctx)
	c.setHLSContentType(ctx, name)
	http.ServeContent(ctx.Response(), ctx.Request(), name, modTime, bytes.NewReader(data))
	return nil
}

// waitForNativePlaylist polls until the muxer advertises enough segments for
// immediate playback, mirroring the FFmpeg path's wait but reading the ring
// directly instead of a file that this path never writes.
//
// The segment count matters: hls.js needs two fragments before it calls play(),
// so returning after one costs the client a full playlist reload cycle, which
// is audible as a delayed start.
func (c *Handler) waitForNativePlaylist(ctx echo.Context, sourceID string, stream *HLSStreamInfo) bool {
	playlistCtx, cancel := context.WithTimeout(ctx.Request().Context(), hlsPlaylistWaitTimeout)
	defer cancel()

	ticker := time.NewTicker(hlsPlaylistPollInterval)
	defer ticker.Stop()

	for {
		if stream.mux.Ready(hlsMinSegments) {
			return true
		}
		if !c.hlsStreamExists(sourceID) {
			return false
		}
		select {
		case <-playlistCtx.Done():
			return false
		case <-ticker.C:
		}
	}
}
