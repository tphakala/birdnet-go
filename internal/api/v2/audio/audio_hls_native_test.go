// internal/api/v2/audio/audio_hls_native_test.go
// Tests for the native (FFmpeg-free) HLS serving path.
package audio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore/hlsmux"
)

const (
	nativeTestRate     = 48000
	nativeTestChannels = 1
)

// newNativeTestStream builds a real muxer carrying real encoded audio, so the
// serving paths are exercised against genuine segments rather than stubs.
func newNativeTestStream(t *testing.T, seconds int) *HLSStreamInfo {
	t.Helper()

	mux, err := hlsmux.New(&hlsmux.Config{
		Codec:       hlsmux.AACLC(),
		SampleRate:  nativeTestRate,
		Channels:    nativeTestChannels,
		BitrateKbps: 128,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mux.Close() })

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

func newNativeTestHandler(t *testing.T) (*Handler, *echo.Echo) {
	t.Helper()
	e := echo.New()
	return &Handler{Core: apitest.NewCore(t, apitest.WithEcho(e))}, e
}

func TestIsNativeDiscriminatesThePaths(t *testing.T) {
	t.Parallel()

	assert.False(t, (*HLSStreamInfo)(nil).isNative(), "a nil stream is not native")
	assert.False(t, (&HLSStreamInfo{OutputDir: "/tmp/x"}).isNative(),
		"an FFmpeg-path stream has no muxer")

	stream := newNativeTestStream(t, 1)
	assert.True(t, stream.isNative())
}

func TestNativeBitrateAppliesFFmpegPathLimits(t *testing.T) {
	t.Parallel()

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
			t.Parallel()
			h, _ := newNativeTestHandler(t)
			settings := h.CurrentSettings()
			settings.WebServer.LiveStream.BitRate = tt.setting
			assert.Equal(t, tt.want, h.nativeBitrateKbps())
		})
	}
}

// TestServeNativeContentRejectsNonFlatNames covers the reason this path needs
// none of the securefs machinery the disk path uses: every name it serves is a
// single flat element, so rejecting separators makes traversal unreachable.
func TestServeNativeContentRejectsNonFlatNames(t *testing.T) {
	t.Parallel()

	bad := []string{
		"",
		"../../etc/passwd",
		"sub/init.mp4",
		`sub\init.mp4`,
		"/init.mp4",
	}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, e := newNativeTestHandler(t)
			stream := newNativeTestStream(t, 1)

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
	t.Parallel()

	bad := []string{"segment.m4s", "segmentX.m4s", "segment007.m4s", "segment+1.m4s", "segment1.ts", "playlist.m3u8"}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, e := newNativeTestHandler(t)
			stream := newNativeTestStream(t, 5)

			rec := httptest.NewRecorder()
			ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
			_ = h.serveNativeContent(ctx, stream, name)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestServeNativeContentServesInitSegment(t *testing.T) {
	t.Parallel()

	h, e := newNativeTestHandler(t)
	stream := newNativeTestStream(t, 1)

	rec := httptest.NewRecorder()
	ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
	require.NoError(t, h.serveNativeContent(ctx, stream, hlsmux.InitSegmentName))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "audio/mp4", rec.Header().Get("Content-Type"))
	body := rec.Body.Bytes()
	require.NotEmpty(t, body)
	assert.Equal(t, "ftyp", string(body[4:8]), "an init segment starts with ftyp")
	assert.Equal(t, stream.mux.InitSegment(), body)
}

func TestServeNativeContentServesMediaSegment(t *testing.T) {
	t.Parallel()

	h, e := newNativeTestHandler(t)
	stream := newNativeTestStream(t, 5)
	require.True(t, stream.mux.Ready(1), "five seconds must produce at least one segment")

	rec := httptest.NewRecorder()
	ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
	require.NoError(t, h.serveNativeContent(ctx, stream, hlsmux.SegmentName(0)))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "video/iso.segment", rec.Header().Get("Content-Type"))
	body := rec.Body.Bytes()
	require.NotEmpty(t, body)
	assert.Equal(t, "styp", string(body[4:8]), "a media segment starts with styp")
}

// TestServeNativeContentReportsEvictedSegmentAsGone is what a client that fell
// too far behind should see, and it must be a 404 rather than an empty 200.
func TestServeNativeContentReportsEvictedSegmentAsGone(t *testing.T) {
	t.Parallel()

	h, e := newNativeTestHandler(t)
	stream := newNativeTestStream(t, 1)

	rec := httptest.NewRecorder()
	ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
	_ = h.serveNativeContent(ctx, stream, hlsmux.SegmentName(9999))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeNativePlaylistRendersFromMemory(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
}

// TestCheckHLSPlaylistReadyUsesTheMuxer proves the readiness gate does not fall
// through to the disk check, which would look at a PlaylistPath this path never
// sets and report "not ready" forever.
func TestCheckHLSPlaylistReadyUsesTheMuxer(t *testing.T) {
	t.Parallel()

	h, _ := newNativeTestHandler(t)

	ready := newNativeTestStream(t, 6)
	assert.True(t, h.checkHLSPlaylistReady(ready),
		"six seconds at the default segment length must satisfy the two-segment gate")

	mux, err := hlsmux.New(&hlsmux.Config{
		Codec:       hlsmux.AACLC(),
		SampleRate:  nativeTestRate,
		Channels:    nativeTestChannels,
		BitrateKbps: 128,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mux.Close() })
	assert.False(t, h.checkHLSPlaylistReady(&HLSStreamInfo{mux: mux}),
		"a stream with no segments yet is not ready")
}

// TestNativeFeedLoopAnchorsPDTToCaptureTime is the test for the item the design
// flagged as most likely to be missed. The router's frame.Timestamp has to
// survive the hop through the channel and reach the muxer, because that is what
// EXT-X-PROGRAM-DATE-TIME is anchored to. If the timestamp were dropped and the
// timeline derived from the sample count instead, a source that stalls and
// resumes would report wall-clock times that never happened.
func TestNativeFeedLoopAnchorsPDTToCaptureTime(t *testing.T) {
	t.Parallel()

	h, _ := newNativeTestHandler(t)
	mux, err := hlsmux.New(&hlsmux.Config{
		Codec:       hlsmux.AACLC(),
		SampleRate:  nativeTestRate,
		Channels:    nativeTestChannels,
		BitrateKbps: 128,
	})
	require.NoError(t, err)
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

	_ = mux.Close()
}

// TestNativeFeedLoopFallsBackWhenCaptureTimeMissing covers a source that does
// not stamp its frames. Forwarding the zero time would put every segment in
// year 1, which a monitoring UI renders as nonsense.
func TestNativeFeedLoopFallsBackWhenCaptureTimeMissing(t *testing.T) {
	t.Parallel()

	h, _ := newNativeTestHandler(t)
	mux, err := hlsmux.New(&hlsmux.Config{
		Codec:       hlsmux.AACLC(),
		SampleRate:  nativeTestRate,
		Channels:    nativeTestChannels,
		BitrateKbps: 128,
	})
	require.NoError(t, err)
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

	_ = mux.Close()
}

// TestNativeFeedLoopStopsOnContextCancel proves the loop honours cancellation,
// which is what stops the goroutine when a stream is torn down.
func TestNativeFeedLoopStopsOnContextCancel(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	assert.NotPanics(t, func() {
		closeNativeMux("src", &HLSStreamInfo{OutputDir: "/tmp/x"})
	}, "an FFmpeg-path stream has no muxer to close")

	stream := newNativeTestStream(t, 1)
	assert.NotPanics(t, func() {
		closeNativeMux("src", stream)
		closeNativeMux("src", stream)
	}, "close must be idempotent; several teardown paths can reach the same stream")
}
