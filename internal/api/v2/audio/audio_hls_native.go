package audio

// Native HLS: the in-process path that replaces the FFmpeg process entirely.
//
// It lives in its own file rather than as branches inside audio_hls.go because
// the two paths share almost nothing below the handler. There is no subprocess,
// no FIFO, no output directory and no securefs here: PCM goes straight from the
// audio router into internal/audiocore/hlsmux, and the playlist and segments are
// served out of memory. audio_hls.go keeps six one-line branch points and is
// otherwise untouched, so the FFmpeg fallback stays exactly as it was.

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/audiocore/hlsmux"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

const (
	// nativeHLSSampleRate is the output rate the native path pins, regardless of
	// what the source or the live-stream setting says.
	//
	// Pinning is correct rather than lazy. The audio router already inserts a
	// resampler whenever a source's rate differs from the consumer's declared
	// rate, so a fixed output rate costs nothing upstream. Downstream it removes
	// a whole class of rate-mismatch bugs, and go-aac only supports 44100 and
	// 48000, so following an arbitrary source rate would make stream creation
	// fail for any source that is neither.
	nativeHLSSampleRate = 48000

	// nativeHLSChannels is the output channel count. The capture pipeline is
	// mono end to end.
	nativeHLSChannels = 1

	// nativeHLSDefaultBitrate is the target in kbps when the setting is unset,
	// matching the FFmpeg path's default.
	nativeHLSDefaultBitrate = 128

	// nativePlaylistPollInterval is how often stream creation checks whether
	// enough segments exist to start playback. It matches the FFmpeg path's
	// poll rate, which is set to catch the second segment promptly.
	nativePlaylistPollInterval = 500 * time.Millisecond
)

// isNative reports whether this stream is served by the in-process muxer rather
// than by an FFmpeg process.
func (s *HLSStreamInfo) isNative() bool { return s != nil && s.mux != nil }

// closeNativeMux closes the in-process muxer if this stream has one.
//
// It is a no-op on an FFmpeg-path stream and safe to call more than once, since
// hlsmux.Stream.Close is idempotent. Every teardown path must call it: the
// muxer owns an encoder, and the paths that delete a stream from the registry
// themselves (a force restart, the inactivity sweep) bypass the context watcher
// that would otherwise do the cleanup.
func closeNativeMux(sourceID string, stream *HLSStreamInfo) {
	if !stream.isNative() {
		return
	}
	if err := stream.mux.Close(); err != nil {
		apicore.GetLogger().Warn("Native HLS muxer close reported an error",
			logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
			logger.Error(err))
	}
}

// nativeBitrateKbps applies the same defaults and clamping to the configured
// bitrate as the FFmpeg path, so switching encoders does not silently change
// the audio quality a listener gets.
func (c *Handler) nativeBitrateKbps() int {
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
		logger.String("source_id", sanitizedID),
		logger.String("encoder", conf.EnvNativeHLSEncoder))

	segmentDuration := time.Duration(c.getEffectiveSegmentLength()) * time.Second
	bitrate := c.nativeBitrateKbps()

	mux, err := hlsmux.New(&hlsmux.Config{
		Codec:           hlsmux.AACLC(),
		SampleRate:      nativeHLSSampleRate,
		Channels:        nativeHLSChannels,
		BitrateKbps:     bitrate,
		SegmentDuration: segmentDuration,
		WindowSize:      hlsListSize,
	})
	if err != nil {
		return nil, err
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
		return nil, err
	}

	stream := &HLSStreamInfo{
		SourceID:    sourceID,
		mux:         mux,
		ctx:         streamCtx,
		cancel:      streamCancel,
		streamEpoch: time.Now(),
	}

	c.publishHLSStream(sourceID, stream)

	go func() {
		c.runNativeAudioFeedLoop(stream.ctx, sourceID, stream, feed)
		if stream.ctx.Err() == nil {
			apicore.GetLogger().Warn("Native audio feed exited unexpectedly, cancelling stream",
				logger.String("source_id", sanitizedID))
			stream.cancel()
		}
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

	dataWritten := false
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

			if err := stream.mux.Write(chunk.data, ts); err != nil {
				// The muxer latches encode failures, so there is no recovering
				// this stream; exiting cancels it and the client reconnects.
				apicore.GetLogger().Error("Native HLS encode failed, stopping feed",
					logger.String("source_id", sanitizedID), logger.Error(err))
				return
			}

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

	ticker := time.NewTicker(nativePlaylistPollInterval)
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

	// A stream that has stopped producing segments is the native equivalent of
	// the FFmpeg path's stale-segment check. Report it rather than letting a
	// silently dead stream look healthy.
	if stats := stream.mux.Stats(); stats.Failed {
		apicore.GetLogger().Warn("Native HLS stream has latched an encode failure",
			logger.String("source_id", privacy.SanitizeRTSPUrl(sourceID)),
			logger.Uint64("segments", stats.Segments))
	}

	// Tell a client polling before the first segment how long to wait, matching
	// what the FFmpeg path does with its placeholder playlist.
	if !stream.mux.Ready(1) {
		ctx.Response().Header().Set("Retry-After", strconv.Itoa(c.getEffectiveSegmentLength()))
	}

	return ctx.String(http.StatusOK, playlist)
}

// serveNativeContent writes the init segment or one media segment from memory.
func (c *Handler) serveNativeContent(ctx echo.Context, stream *HLSStreamInfo, requestPath string) error {
	// Everything the native path serves is a single flat name. Rejecting any
	// separator up front means no path traversal is reachable here at all, which
	// is why this needs none of the securefs machinery the disk path uses.
	if requestPath == "" || strings.ContainsAny(requestPath, "/\\") {
		return c.HandleError(ctx, nil, "Invalid segment path", http.StatusBadRequest)
	}

	if requestPath == hlsmux.InitSegmentName {
		c.setHLSHeaders(ctx)
		c.setHLSContentType(ctx, requestPath)
		return ctx.Blob(http.StatusOK, ctx.Response().Header().Get(echo.HeaderContentType), stream.mux.InitSegment())
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

	c.setHLSHeaders(ctx)
	c.setHLSContentType(ctx, requestPath)
	return ctx.Blob(http.StatusOK, ctx.Response().Header().Get(echo.HeaderContentType), segment.Data)
}
