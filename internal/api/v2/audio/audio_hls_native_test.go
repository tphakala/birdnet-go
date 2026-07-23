// internal/api/v2/audio/audio_hls_native_test.go
// Tests for the native (FFmpeg-free) HLS serving path.
package audio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore/hlsmux"
	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	nativeTestRate     = 48000
	nativeTestChannels = 1
)

// newNativeMux builds a muxer with the production codec and releases it through
// t.Cleanup, so a require failure mid-test cannot leak the encoder.
func newNativeMux(t *testing.T) *hlsmux.Stream {
	t.Helper()

	mux, err := hlsmux.New(&hlsmux.Config{
		Codec:       hlsmux.AACLC(),
		SampleRate:  nativeTestRate,
		Channels:    nativeTestChannels,
		BitrateKbps: 128,
	})
	require.NoError(t, err)
	// assert, not require: this runs on the cleanup goroutine, where a
	// require failure would Goexit out of the remaining cleanups.
	t.Cleanup(func() { assert.NoError(t, mux.Close()) })
	return mux
}

// newEmptyNativeStream is for tests that never touch the media window, so they do
// not pay for encoding audio they will not read.
func newEmptyNativeStream(t *testing.T) *HLSStreamInfo {
	t.Helper()
	return &HLSStreamInfo{SourceID: "native_src", mux: newNativeMux(t)}
}

// newNativeTestStream builds a real muxer carrying real encoded audio, so the
// serving paths are exercised against genuine segments rather than stubs.
func newNativeTestStream(t *testing.T, seconds int) *HLSStreamInfo {
	t.Helper()

	mux := newNativeMux(t)

	// Feed in chunks that do not divide the 1024-sample access unit, so the
	// encoder's partial-frame buffering is exercised rather than sidestepped.
	const chunkSamples = 1200
	total := seconds * nativeTestRate
	epoch := time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC)
	silence := make([]byte, chunkSamples*nativeTestChannels*2)
	for written := 0; written < total; written += chunkSamples {
		n := min(chunkSamples, total-written)
		at := epoch.Add(time.Duration(written) * time.Second / nativeTestRate)
		require.NoError(t, mux.Write(silence[:n*nativeTestChannels*2], at))
	}

	return &HLSStreamInfo{SourceID: "native_src", mux: mux, streamEpoch: epoch}
}

// newNativeTestHandler builds a handler for the native path.
//
// Tests using this must NOT call t.Parallel(). apitest.NewCore publishes into
// the process-global settings snapshot, and Core.CurrentSettings prefers that
// global over the core's own copy, so parallel tests would observe each other's
// settings. The rest of this package follows the same rule.
func newNativeTestHandler(t *testing.T, opts ...apitest.CoreOption) (*Handler, *echo.Echo) {
	t.Helper()
	e := echo.New()
	return &Handler{Core: apitest.NewCore(t, append([]apitest.CoreOption{apitest.WithEcho(e)}, opts...)...)}, e
}

func TestIsNativeDiscriminatesThePaths(t *testing.T) {
	assert.False(t, (*HLSStreamInfo)(nil).isNative(), "a nil stream is not native")
	assert.False(t, (&HLSStreamInfo{OutputDir: "/tmp/x"}).isNative(),
		"an FFmpeg-path stream has no muxer")

	stream := newNativeTestStream(t, 1)
	assert.True(t, stream.isNative())
}

// TestEffectiveBitrateAppliesLimits pins the resolution both encoders share.
// Since buildFFmpegArgs now resolves through this same helper, a change here
// cannot silently move the two paths apart.
//
// No t.Parallel: each case publishes a setting into the process-global snapshot.
func TestEffectiveBitrateAppliesLimits(t *testing.T) {
	tests := []struct {
		name    string
		setting int
		want    int
	}{
		{"unset selects the default", 0, nativeHLSDefaultBitrate},
		{"negative selects the default", -5, nativeHLSDefaultBitrate},
		{"below the floor clamps up", 4, hlsMinBitrate},
		{"above the ceiling clamps down", 9999, hlsMaxBitrate},
		{"in range passes through", 96, 96},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Publish the setting at construction rather than mutating a
			// snapshot afterwards, so the value under test is the one the
			// handler actually reads back.
			h, _ := newNativeTestHandler(t, apitest.WithSettingsFunc(func(s *conf.Settings) {
				s.WebServer.LiveStream.BitRate = tt.setting
			}))
			assert.Equal(t, tt.want, h.effectiveBitrateKbps())
		})
	}
}

// TestServeNativeContentRejectsNonFlatNames covers the reason this path needs
// none of the securefs machinery the disk path uses: every name it serves is a
// single flat element, so rejecting separators makes traversal unreachable.
func TestServeNativeContentRejectsNonFlatNames(t *testing.T) {
	bad := []string{
		"",
		"../../etc/passwd",
		"sub/init.mp4",
		`sub\init.mp4`,
		"/init.mp4",
	}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			h, e := newNativeTestHandler(t)
			stream := newEmptyNativeStream(t)

			rec := httptest.NewRecorder()
			ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
			_ = h.serveNativeContent(ctx, stream, name)

			assert.Equal(t, http.StatusBadRequest, rec.Code,
				"a name that is not a single flat element must be rejected")
		})
	}
}

// TestServeNativeContentRejectsNonCanonicalSegmentNames pins that exactly one
// name maps to each segment. Accepting leading zeros would let a client mint
// unlimited distinct cache keys for the same body.
func TestServeNativeContentRejectsNonCanonicalSegmentNames(t *testing.T) {
	bad := []string{"segment.m4s", "segmentX.m4s", "segment007.m4s", "segment+1.m4s", "segment1.ts", "playlist.m3u8"}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			h, e := newNativeTestHandler(t)
			stream := newEmptyNativeStream(t)

			rec := httptest.NewRecorder()
			ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
			_ = h.serveNativeContent(ctx, stream, name)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestServeNativeContentServesInitSegment(t *testing.T) {
	h, e := newNativeTestHandler(t)
	stream := newNativeTestStream(t, 1)

	rec := httptest.NewRecorder()
	ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
	require.NoError(t, h.serveNativeContent(ctx, stream, hlsmux.InitSegmentName))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "audio/mp4", rec.Header().Get("Content-Type"))
	body := rec.Body.Bytes()
	require.GreaterOrEqual(t, len(body), 8)
	assert.Equal(t, "ftyp", string(body[4:8]), "an init segment starts with ftyp")
	assert.Equal(t, stream.mux.InitSegment(), body)
}

func TestServeNativeContentServesMediaSegment(t *testing.T) {
	h, e := newNativeTestHandler(t)
	stream := newNativeTestStream(t, 5)
	require.True(t, stream.mux.Ready(1), "five seconds must produce at least one segment")

	rec := httptest.NewRecorder()
	ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
	require.NoError(t, h.serveNativeContent(ctx, stream, hlsmux.SegmentName(0)))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "video/iso.segment", rec.Header().Get("Content-Type"))
	body := rec.Body.Bytes()
	require.GreaterOrEqual(t, len(body), 8)
	assert.Equal(t, "styp", string(body[4:8]), "a media segment starts with styp")
}

// TestServeNativeContentReportsEvictedSegmentAsGone is what a client that fell
// too far behind should see, and it must be a 404 rather than an empty 200.
func TestServeNativeContentReportsEvictedSegmentAsGone(t *testing.T) {
	h, e := newNativeTestHandler(t)
	stream := newNativeTestStream(t, 1)

	rec := httptest.NewRecorder()
	ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
	_ = h.serveNativeContent(ctx, stream, hlsmux.SegmentName(9999))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeNativePlaylistRendersFromMemory(t *testing.T) {
	h, e := newNativeTestHandler(t)
	stream := newNativeTestStream(t, 5)

	rec := httptest.NewRecorder()
	ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
	require.NoError(t, h.serveNativePlaylist(ctx, "native_src", stream))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/vnd.apple.mpegurl", rec.Header().Get("Content-Type"))

	body := rec.Body.String()
	assert.Contains(t, body, "#EXTM3U")
	assert.Contains(t, body, `#EXT-X-MAP:URI="`+hlsmux.InitSegmentName+`"`)
	assert.Contains(t, body, hlsmux.SegmentName(0))
	assert.Contains(t, body, "#EXT-X-PROGRAM-DATE-TIME:2026-07-23T09:00:00.000Z",
		"PDT must be anchored to the capture timestamp, not to when the playlist was rendered")
	assert.Empty(t, rec.Header().Get("Retry-After"), "a ready stream needs no retry hint")
}

// TestServeNativePlaylistHintsRetryBeforeFirstSegment covers the join case: a
// client polling before any segment exists still gets a valid playlist naming
// the init segment, plus a hint for when to come back.
func TestServeNativePlaylistHintsRetryBeforeFirstSegment(t *testing.T) {
	h, e := newNativeTestHandler(t)
	mux, err := hlsmux.New(&hlsmux.Config{
		Codec:       hlsmux.AACLC(),
		SampleRate:  nativeTestRate,
		Channels:    nativeTestChannels,
		BitrateKbps: 128,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mux.Close() })
	stream := &HLSStreamInfo{SourceID: "native_src", mux: mux}

	rec := httptest.NewRecorder()
	ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
	require.NoError(t, h.serveNativePlaylist(ctx, "native_src", stream))

	assert.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "#EXTM3U")
	assert.Contains(t, body, `#EXT-X-MAP:URI="`+hlsmux.InitSegmentName+`"`,
		"the init segment must be advertised before any media exists so a player can prefetch it")
	assert.NotContains(t, body, "#EXTINF")
	assert.Equal(t, strconv.Itoa(h.getEffectiveSegmentLength()), rec.Header().Get("Retry-After"),
		"the hint must be the real segment length; a regression yielding \"0\" is non-empty too")
}

// TestNativeFeedLoopAnchorsPDTToCaptureTime is the test for the item the design
// flagged as most likely to be missed. The router's frame.Timestamp has to
// survive the hop through the channel and reach the muxer, because that is what
// EXT-X-PROGRAM-DATE-TIME is anchored to. If the timestamp were dropped and the
// timeline derived from the sample count instead, a source that stalls and
// resumes would report wall-clock times that never happened.
func TestNativeFeedLoopAnchorsPDTToCaptureTime(t *testing.T) {
	h, _ := newNativeTestHandler(t)
	mux := newNativeMux(t)
	stream := &HLSStreamInfo{SourceID: "native_src", mux: mux}

	// A capture time deliberately unrelated to now, so a "used time.Now()"
	// implementation cannot accidentally pass.
	epoch := time.Date(2019, 3, 4, 5, 6, 7, 0, time.UTC)

	const chunkSamples = 1200
	ch := make(chan audioChunk, 512)
	silence := make([]byte, chunkSamples*nativeTestChannels*2)
	for written := 0; written < 5*nativeTestRate; written += chunkSamples {
		ch <- audioChunk{
			data:      silence,
			timestamp: epoch.Add(time.Duration(written) * time.Second / nativeTestRate),
		}
	}
	close(ch)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	// The loop returns when the channel closes.
	h.runNativeAudioFeedLoop(ctx, "native_src", stream, &audioFeedResources{
		audioChan: ch,
		cleanup:   func() {},
	})

	require.True(t, mux.Ready(1), "the feed loop must have produced segments")
	seg, ok := mux.Segment(0)
	require.True(t, ok)
	assert.Equal(t, epoch, seg.PDT,
		"the first segment's program-date-time must be the capture time of its first sample")

	assert.Equal(t, epoch.UnixNano(), stream.firstDataTime.Load(),
		"first-data diagnostics must record the capture time, not the processing time")

}

// TestNativeFeedLoopFallsBackWhenCaptureTimeMissing covers a source that does
// not stamp its frames. Forwarding the zero time would put every segment in
// year 1, which a monitoring UI renders as nonsense.
func TestNativeFeedLoopFallsBackWhenCaptureTimeMissing(t *testing.T) {
	h, _ := newNativeTestHandler(t)
	mux := newNativeMux(t)
	stream := &HLSStreamInfo{SourceID: "native_src", mux: mux}

	const chunkSamples = 1200
	ch := make(chan audioChunk, 512)
	silence := make([]byte, chunkSamples*nativeTestChannels*2)
	for written := 0; written < 3*nativeTestRate; written += chunkSamples {
		ch <- audioChunk{data: silence} // no timestamp
	}
	close(ch)

	before := time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	h.runNativeAudioFeedLoop(ctx, "native_src", stream, &audioFeedResources{
		audioChan: ch,
		cleanup:   func() {},
	})

	require.True(t, mux.Ready(1))
	seg, ok := mux.Segment(0)
	require.True(t, ok)
	assert.False(t, seg.PDT.IsZero(), "an unstamped frame must not produce a zero program-date-time")
	assert.False(t, seg.PDT.Before(before.Add(-time.Minute)),
		"the fallback must land on the real timeline, near now")
	assert.False(t, seg.PDT.After(time.Now().Add(time.Minute)),
		"a far-future fallback is just as wrong as a zero one")

}

// TestNativeFeedLoopStopsOnContextCancel proves the loop honours cancellation,
// which is what stops the goroutine when a stream is torn down.
func TestNativeFeedLoopStopsOnContextCancel(t *testing.T) {
	h, _ := newNativeTestHandler(t)
	stream := newNativeTestStream(t, 1)

	cleanupCalled := false
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	h.runNativeAudioFeedLoop(ctx, "native_src", stream, &audioFeedResources{
		audioChan: make(chan audioChunk),
		cleanup:   func() { cleanupCalled = true },
	})

	assert.True(t, cleanupCalled, "the route must be unregistered when the loop exits")
}

// TestCloseNativeMuxIsSafeOnBothPaths guards the teardown helper that every
// cleanup path calls, including the ones that bypass performHLSCleanup.
func TestCloseNativeMuxIsSafeOnBothPaths(t *testing.T) {
	assert.NotPanics(t, func() {
		closeNativeMux("src", &HLSStreamInfo{OutputDir: "/tmp/x"})
	}, "an FFmpeg-path stream has no muxer to close")

	stream := newNativeTestStream(t, 1)
	assert.NotPanics(t, func() {
		closeNativeMux("src", stream)
		closeNativeMux("src", stream)
	}, "close must be idempotent; several teardown paths can reach the same stream")

	// Assert the muxer actually closed, not merely that nothing panicked.
	// Without this, replacing closeNativeMux's body with a bare return leaves
	// the test green while every teardown path silently stops releasing the
	// encoder.
	require.Error(t, stream.mux.Write(make([]byte, 2), time.Now()),
		"a closed muxer must refuse further writes; if this passes, the close never happened")
}

// TestCloseNativeMuxWaitsForTheFeed pins the ordering that keeps a clean stop
// from looking like a failure.
//
// Cancelling only makes the feed's ctx.Done() arm ready; Go picks uniformly
// among ready arms, so without this wait a queued chunk lets the feed write to a
// muxer that teardown just closed. hlsmux rejects that write through
// internal/errors, whose Build() forwards to telemetry, so the race would fire a
// spurious Sentry event on roughly half of all clean stops.
func TestCloseNativeMuxWaitsForTheFeed(t *testing.T) {
	stream := newEmptyNativeStream(t)
	stream.feedDone = make(chan struct{})

	released := make(chan struct{})
	go func() {
		// Hold the muxer briefly, as a feed goroutine mid-write would.
		time.Sleep(50 * time.Millisecond)
		close(stream.feedDone)
		close(released)
	}()

	closeNativeMux("src", stream)

	select {
	case <-released:
	default:
		t.Fatal("closeNativeMux returned before the feed signalled it had stopped")
	}
}

// TestCloseNativeMuxGivesUpOnAWedgedFeed proves the wait is bounded, so a feed
// goroutine that never returns cannot block shutdown forever.
func TestCloseNativeMuxGivesUpOnAWedgedFeed(t *testing.T) {
	stream := newEmptyNativeStream(t)
	stream.feedDone = make(chan struct{}) // never closed

	done := make(chan struct{})
	go func() {
		closeNativeMux("src", stream)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(nativeFeedDrainTimeout + 5*time.Second):
		t.Fatal("closeNativeMux blocked past its drain timeout on a wedged feed")
	}
	require.Error(t, stream.mux.Write(make([]byte, 2), time.Now()),
		"the muxer must still be closed after the drain timeout expires")
}

// TestNativeFeedLoopAccumulatesSubSampleChunks is the assertion with teeth.
//
// Every chunk here is shorter than one sample frame, so it carries no whole
// sample at all. Carrying the remainder accumulates them into real samples and
// the stream plays; dropping the remainder (the obvious "just truncate to an
// even length" fix) discards every chunk entirely and the stream produces
// nothing, forever, while looking healthy.
//
// This shape is what makes the test able to fail. Feeding merely odd-length
// chunks cannot distinguish the two: segments quantize to 1024-sample access
// units, so the one byte lost per chunk stays invisible until it accumulates
// past a whole unit, and byte-misalignment is undetectable in the silence a
// test would otherwise feed.
func TestNativeFeedLoopAccumulatesSubSampleChunks(t *testing.T) {
	h, _ := newNativeTestHandler(t)

	// Short segments so half a second of audio still cuts one. Feeding the
	// default 2 s target byte by byte would need a multi-megabyte channel, on a
	// project that targets 512 MB boards and runs this in CI.
	mux, err := hlsmux.New(&hlsmux.Config{
		Codec:           hlsmux.AACLC(),
		SampleRate:      nativeTestRate,
		Channels:        nativeTestChannels,
		BitrateKbps:     128,
		SegmentDuration: 200 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, mux.Close()) })
	stream := &HLSStreamInfo{SourceID: "native_src", mux: mux}

	epoch := time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC)
	// Half a second of audio, delivered one byte at a time.
	const singleBytes = nativeTestRate * nativeHLSBytesPerSample / 2
	ch := make(chan audioChunk, singleBytes+1)
	one := []byte{0x11}
	for i := range singleBytes {
		ch <- audioChunk{data: one, timestamp: epoch.Add(time.Duration(i/nativeHLSBytesPerSample) * time.Second / nativeTestRate)}
	}
	close(ch)

	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	h.runNativeAudioFeedLoop(ctx, "native_src", stream, &audioFeedResources{
		audioChan: ch, cleanup: func() {},
	})

	assert.False(t, stream.mux.Stats().Failed, "sub-sample chunks are not an encoder failure")
	require.True(t, stream.mux.Ready(1),
		"single-byte chunks must accumulate into samples; if no segment exists, the remainder was discarded instead of carried")
}

// TestNativeFeedLoopSurvivesAlternatingOddChunks covers the shape a real pipe
// read produces: lengths that vary rather than being uniformly odd.
func TestNativeFeedLoopSurvivesAlternatingOddChunks(t *testing.T) {
	h, _ := newNativeTestHandler(t)
	stream := &HLSStreamInfo{SourceID: "native_src", mux: newNativeMux(t)}

	epoch := time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC)
	ch := make(chan audioChunk, 4096)
	// Sizes sum to 3413 bytes per cycle; 1200 chunks is about six seconds of
	// mono 16-bit, comfortably several segments rather than borderline one.
	sizes := []int{1, 3, 1201, 2, 999, 1200, 7}
	pcm := make([]byte, 2048)
	var sent int
	for range 1200 {
		n := sizes[sent%len(sizes)]
		ch <- audioChunk{data: pcm[:n], timestamp: epoch.Add(time.Duration(sent) * time.Millisecond)}
		sent++
	}
	close(ch)

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	h.runNativeAudioFeedLoop(ctx, "native_src", stream, &audioFeedResources{
		audioChan: ch, cleanup: func() {},
	})

	assert.False(t, stream.mux.Stats().Failed,
		"ragged chunk lengths are a producer reality, not an encoder failure")
	assert.True(t, stream.mux.Ready(1), "the stream must still have produced segments")
}

// The tests below exercise the isNative() dispatch itself rather than the
// helpers behind it. Without them, deleting any branch point leaves the suite
// green while the native path silently falls through to the FFmpeg code, which
// then looks at a PlaylistPath this path never sets.

// TestCheckHLSPlaylistReadyDispatchesOnNative proves the readiness gate does not
// fall through to the disk check.
func TestCheckHLSPlaylistReadyDispatchesOnNative(t *testing.T) {
	h, _ := newNativeTestHandler(t)

	ready := newNativeTestStream(t, 6)
	assert.True(t, h.checkHLSPlaylistReady(ready),
		"six seconds at the default segment length must satisfy the two-segment gate")

	assert.False(t, h.checkHLSPlaylistReady(newEmptyNativeStream(t)),
		"a native stream with no segments is not ready")
	assert.False(t, h.checkHLSPlaylistReady(nil), "a nil stream is never ready")

	// An FFmpeg-path stream must still take the disk branch, which reports not
	// ready for an unset PlaylistPath rather than consulting a nil muxer.
	assert.False(t, h.checkHLSPlaylistReady(&HLSStreamInfo{OutputDir: "/tmp/x"}),
		"the FFmpeg branch must still be reachable")
}

// TestPerformHLSCleanupClosesTheNativeMuxer covers the teardown branch. The
// muxer owns an encoder, so a dispatch regression here is a leak per stream.
func TestPerformHLSCleanupClosesTheNativeMuxer(t *testing.T) {
	h, _ := newNativeTestHandler(t)

	stream := newNativeTestStream(t, 1)
	ctx, cancel := context.WithCancel(t.Context())
	stream.ctx, stream.cancel = ctx, cancel
	stream.feedDone = make(chan struct{})
	close(stream.feedDone) // no feed goroutine in this test

	h.performHLSCleanup("native_src", stream, "test")

	require.Error(t, stream.mux.Write(make([]byte, 2), time.Now()),
		"performHLSCleanup must close the muxer on the native path")
	assert.Error(t, ctx.Err(), "cleanup must cancel the stream context")
}

// TestPerformHLSCleanupLeavesFFmpegPathAlone is the other half: the native
// branch must not swallow the FFmpeg cleanup.
func TestPerformHLSCleanupLeavesFFmpegPathAlone(t *testing.T) {
	h, _ := newNativeTestHandler(t)

	ctx, cancel := context.WithCancel(t.Context())
	stream := &HLSStreamInfo{SourceID: "ffmpeg_src", ctx: ctx, cancel: cancel}

	assert.NotPanics(t, func() {
		h.performHLSCleanup("ffmpeg_src", stream, "test")
	}, "an FFmpeg stream has no muxer; cleanup must not dereference one")
	assert.Error(t, ctx.Err())
}

// TestWaitForHLSPlaylistDispatchesOnNative pins that /start does not block on a
// playlist file the native path never writes. Before the dispatch existed this
// waited out the full 20 s timeout and then reported failure.
func TestWaitForHLSPlaylistDispatchesOnNative(t *testing.T) {
	h, e := newNativeTestHandler(t)
	stream := newNativeTestStream(t, 6)

	hlsMgr.streamsMu.Lock()
	hlsMgr.streams["native_src"] = stream
	hlsMgr.streamsMu.Unlock()
	t.Cleanup(func() {
		hlsMgr.streamsMu.Lock()
		delete(hlsMgr.streams, "native_src")
		hlsMgr.streamsMu.Unlock()
	})

	ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), httptest.NewRecorder())

	start := time.Now()
	ok := h.waitForHLSPlaylist(ctx, "native_src", stream)
	elapsed := time.Since(start)

	assert.True(t, ok, "a native stream with segments must be reported ready")
	assert.Less(t, elapsed, hlsPlaylistWaitTimeout,
		"readiness must come from the muxer, not from waiting out the file-poll timeout")
}
