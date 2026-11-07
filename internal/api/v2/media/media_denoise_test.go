package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

const (
	denoiseTestPreset  = conf.DenoisePresetLightMedium
	denoiseTestSeconds = 1.0
	// denoiseRangeStart and denoiseRangeEnd bound the byte range the Range tests ask for.
	denoiseRangeStart = 100
	denoiseRangeEnd   = 199
	// denoiseFollowers is the number of extra concurrent requests in the dedup test.
	denoiseFollowers = 3
	// denoiseJoinGrace gives followers time to reach the flight before the leader
	// is released. The assertions hold even if one arrives late (it then hits the
	// cache), so this only makes the test exercise the join.
	denoiseJoinGrace = 100 * time.Millisecond
	// denoiseStartTimeout bounds waits for a job to reach or leave its hook so a
	// regression fails instead of hanging the package.
	denoiseStartTimeout = 10 * time.Second
	// denoiseNeverWindow and denoiseNeverTick size the negative check that Wait
	// stays blocked; it can pass spuriously on a slow machine but never fail.
	denoiseNeverWindow = 100 * time.Millisecond
	denoiseNeverTick   = 10 * time.Millisecond
)

// signedIn marks every request as coming from an authenticated client.
func signedIn(echo.Context) bool { return true }

func getAudio(e http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// expectedDenoised is what ProcessToWAV returns for the fixture at the preset.
func expectedDenoised(t *testing.T, path string) []byte {
	t.Helper()
	want, err := transcode.ProcessToWAV(t.Context(), path, transcode.Filters{Denoise: denoiseTestPreset})
	require.NoError(t, err)
	return want
}

// denoiseURL is the denoised audio URL of note id.
func denoiseURL(id, preset string) string {
	return "/api/v2/audio/" + id + "?denoise=" + preset
}

func TestServeAudioByID_Denoise(t *testing.T) {
	// birdClip and batClip name the two fixtures a row writes before the request.
	type fixture struct {
		file    string
		rate    int
		seconds float64
		freq    float64
	}
	birdClip := fixture{"clip.flac", processTestRate, denoiseTestSeconds, fixtureToneHz}
	batClip := fixture{"bat.flac", batTestRate, batTestSeconds, fixtureBatToneHz}
	denoiseQuery := "?denoise=" + denoiseTestPreset

	tests := []struct {
		name          string
		clip          fixture
		query         string
		authenticated bool
		privateMode   bool
		wantStatus    int
		wantDenoised  bool
		wantNoStore   bool
	}{
		{name: "no parameter serves the stored clip", clip: birdClip, authenticated: true, wantStatus: http.StatusOK},
		{name: "empty parameter serves the stored clip", clip: birdClip, query: "?denoise=", authenticated: true, wantStatus: http.StatusOK},
		{name: "preset serves a denoised WAV", clip: birdClip, query: denoiseQuery, authenticated: true, wantStatus: http.StatusOK, wantDenoised: true},
		{name: "bat clip denoises at the source rate", clip: batClip, query: denoiseQuery, authenticated: true, wantStatus: http.StatusOK, wantDenoised: true},
		{name: "unknown preset is rejected", clip: birdClip, query: "?denoise=bogus", authenticated: true, wantStatus: http.StatusBadRequest},
		{name: "guest gets the stored clip with no-store", clip: birdClip, query: denoiseQuery, wantStatus: http.StatusOK, wantNoStore: true},
		{name: "guest in private mode still gets no-store", clip: birdClip, query: denoiseQuery, privateMode: true, wantStatus: http.StatusOK, wantNoStore: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var opts []apitest.CoreOption
			if tc.privateMode {
				opts = append(opts, apitest.WithSettingsFunc(func(s *conf.Settings) { s.Security.PrivateMode = true }))
			}
			e, controller, dir := setupMediaTestEnvironment(t, opts...)
			if tc.authenticated {
				controller.isClientAuthenticated = signedIn
			}
			src := fixturePath(dir, tc.clip.file)
			writeToneFLAC(t, src, tc.clip.rate, tc.clip.seconds, tc.clip.freq)
			mockDS := mocks.NewMockInterface(t)
			mockDS.On("GetNoteClipPath", "7").Return(tc.clip.file, nil).Maybe()
			controller.DS = mockDS

			stored, err := os.ReadFile(src) //nolint:gosec // G304: test fixture path
			require.NoError(t, err)

			rec := getAudio(e, "/api/v2/audio/7"+tc.query, nil)
			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			if tc.wantStatus != http.StatusOK {
				return
			}
			if !tc.wantDenoised {
				assert.Equal(t, stored, rec.Body.Bytes(), "the stored bytes are served verbatim")
				assert.Equal(t, MimeTypeFLAC, rec.Header().Get(echo.HeaderContentType))
				if tc.wantNoStore {
					assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
				} else {
					assert.Empty(t, rec.Header().Get("Cache-Control"))
				}
				return
			}
			assert.Equal(t, expectedDenoised(t, src), rec.Body.Bytes())
			assert.Equal(t, MimeTypeWAV, rec.Header().Get(echo.HeaderContentType))
			assert.Equal(t, "bytes", rec.Header().Get(headerAcceptRanges))
			assert.Contains(t, rec.Header().Get(echo.HeaderContentDisposition), "inline")
			assert.Contains(t, rec.Header().Get(echo.HeaderContentDisposition), ".wav")
			assert.NotEmpty(t, rec.Header().Get("ETag"))
			assert.NotEmpty(t, rec.Header().Get("Last-Modified"))
			rate, _ := wavRateAndLength(t, rec.Body.Bytes())
			assert.Equal(t, tc.clip.rate, rate)
		})
	}
}

func TestServeAudioByID_DenoiseServesCacheHitToGuest(t *testing.T) {
	e, controller, dir := setupMediaTestEnvironment(t)
	writeToneFLAC(t, fixturePath(dir, "clip.flac"), processTestRate, denoiseTestSeconds, fixtureToneHz)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "7").Return("clip.flac", nil)
	controller.DS = mockDS

	// A signed-in request fills the cache; a guest's Range follow-up for the same
	// URL then gets the same WAV instead of the stored clip.
	controller.isClientAuthenticated = signedIn
	first := getAudio(e, denoiseURL("7", denoiseTestPreset), nil)
	require.Equal(t, http.StatusOK, first.Code)

	controller.isClientAuthenticated = nil
	rec := getAudio(e, denoiseURL("7", denoiseTestPreset), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, first.Body.Bytes(), rec.Body.Bytes())
	assert.Equal(t, MimeTypeWAV, rec.Header().Get(echo.HeaderContentType))
}

func TestServeAudioByID_DenoiseRange(t *testing.T) {
	e, controller, dir := setupMediaTestEnvironment(t)
	controller.isClientAuthenticated = signedIn
	src := fixturePath(dir, "clip.flac")
	writeToneFLAC(t, src, processTestRate, denoiseTestSeconds, fixtureToneHz)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "7").Return("clip.flac", nil)
	controller.DS = mockDS

	want := expectedDenoised(t, src)[denoiseRangeStart : denoiseRangeEnd+1]
	rangeHeader := map[string]string{"Range": "bytes=" + strconv.Itoa(denoiseRangeStart) + "-" + strconv.Itoa(denoiseRangeEnd)}

	// First request is a cache miss, the second a hit; both seek.
	for _, phase := range []string{"miss", "hit"} {
		rec := getAudio(e, denoiseURL("7", denoiseTestPreset), rangeHeader)
		require.Equal(t, http.StatusPartialContent, rec.Code, phase)
		assert.Equal(t, want, rec.Body.Bytes(), phase)
		assert.Contains(t, rec.Header().Get("Content-Range"), "bytes 100-199/", phase)
	}
}

func TestServeAudioByID_DenoiseDedup(t *testing.T) {
	e, controller, dir := setupMediaTestEnvironment(t)
	controller.isClientAuthenticated = signedIn
	writeToneFLAC(t, fixturePath(dir, "clip.flac"), processTestRate, denoiseTestSeconds, fixtureToneHz)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "7").Return("clip.flac", nil)
	controller.DS = mockDS

	var started atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	controller.processingStartedHook = func() {
		if started.Add(1) == 1 {
			close(entered)
		}
		<-release
	}

	const total = denoiseFollowers + 1
	recs := make([]*httptest.ResponseRecorder, total)
	var wg sync.WaitGroup
	var launched atomic.Int32
	for i := range total {
		wg.Go(func() {
			launched.Add(1)
			recs[i] = getAudio(e, denoiseURL("7", denoiseTestPreset), nil)
		})
		if i == 0 {
			<-entered // the leader holds the job before followers start
		}
	}
	require.Eventually(t, func() bool { return launched.Load() == total }, 5*time.Second, time.Millisecond)
	time.Sleep(denoiseJoinGrace)
	close(release)
	wg.Wait()

	assert.Equal(t, int32(1), started.Load(), "concurrent requests for one key run one job")
	for i, rec := range recs {
		require.Equal(t, http.StatusOK, rec.Code, "request %d", i)
		assert.Equal(t, recs[0].Body.Bytes(), rec.Body.Bytes(), "request %d", i)
	}
}

func TestServeAudioByID_DenoiseSharesJobWithPost(t *testing.T) {
	e, controller, dir := setupMediaTestEnvironment(t)
	controller.isClientAuthenticated = signedIn
	src := fixturePath(dir, "clip.flac")
	writeToneFLAC(t, src, processTestRate, denoiseTestSeconds, fixtureToneHz)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "7").Return("clip.flac", nil)
	controller.DS = mockDS

	var started atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	controller.processingStartedHook = func() {
		if started.Add(1) == 1 {
			close(entered)
		}
		<-release
	}

	var wg sync.WaitGroup
	var getRec, postRec *httptest.ResponseRecorder
	wg.Go(func() { getRec = getAudio(e, denoiseURL("7", denoiseTestPreset), nil) })
	<-entered
	wg.Go(func() { postRec = postJSON(e, "/api/v2/audio/7/process", `{"denoise": "`+denoiseTestPreset+`"}`) })
	time.Sleep(denoiseJoinGrace)
	close(release)
	wg.Wait()

	assert.Equal(t, int32(1), started.Load(), "a denoise-only POST joins the GET's job")
	require.Equal(t, http.StatusOK, getRec.Code)
	require.Equal(t, http.StatusOK, postRec.Code)
	assert.Equal(t, getRec.Body.Bytes(), postRec.Body.Bytes())
	assert.Equal(t, expectedDenoised(t, src), postRec.Body.Bytes())
}

func TestServeAudioByID_DenoiseBusyIs503(t *testing.T) {
	e, controller, dir := setupMediaTestEnvironment(t)
	controller.isClientAuthenticated = signedIn
	writeToneFLAC(t, fixturePath(dir, "clip.flac"), processTestRate, denoiseTestSeconds, fixtureToneHz)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "7").Return("clip.flac", nil)
	controller.DS = mockDS

	// One free slot is enough for a different key to run.
	controller.processingSemaphore <- struct{}{}
	rec := getAudio(e, denoiseURL("7", denoiseTestPreset), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	<-controller.processingSemaphore

	// With every slot taken, an uncached key answers 503 + Retry-After and never
	// the stored clip.
	for range cap(controller.processingSemaphore) {
		controller.processingSemaphore <- struct{}{}
	}
	rec = getAudio(e, denoiseURL("7", conf.DenoisePresetHeavy), nil)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
	assert.NotEqual(t, MimeTypeFLAC, rec.Header().Get(echo.HeaderContentType))
	assert.NotEqual(t, MimeTypeWAV, rec.Header().Get(echo.HeaderContentType))

	// A cached key is still served while the slots are taken.
	rec = getAudio(e, denoiseURL("7", denoiseTestPreset), nil)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServeAudioByID_DenoiseMissingClip(t *testing.T) {
	now := time.Now()
	const missingClip = "2024/01/pending_95p_20240101T000000Z.wav"

	tests := []struct {
		name      string
		begin     time.Time
		end       time.Time
		wantCode  int
		wantRetry bool
	}{
		{name: "recent missing clip is not ready", begin: now, end: now.Add(30 * time.Second), wantCode: http.StatusServiceUnavailable, wantRetry: true},
		{name: "old missing clip is gone", begin: now.Add(-2 * time.Hour), end: now.Add(-2 * time.Hour), wantCode: http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, controller, _ := setupMediaTestEnvironment(t)
			controller.isClientAuthenticated = signedIn
			mockDS := mocks.NewMockInterface(t)
			mockDS.On("GetNoteClipPath", "42").Return(missingClip, nil)
			mockDS.On("Get", "42").Return(datastore.Note{ID: 42, BeginTime: tc.begin, EndTime: tc.end}, nil).Maybe()
			controller.DS = mockDS

			rec := getAudio(e, denoiseURL("42", denoiseTestPreset), nil)
			assert.Equal(t, tc.wantCode, rec.Code)
			assert.NotEqual(t, MimeTypeWAV, rec.Header().Get(echo.HeaderContentType), "a missing clip never serves audio")
			if tc.wantRetry {
				assert.NotEmpty(t, rec.Header().Get("Retry-After"))
			}
		})
	}
}

func TestServeAudioByID_DenoiseUsesAlternateExtension(t *testing.T) {
	e, controller, dir := setupMediaTestEnvironment(t)
	controller.isClientAuthenticated = signedIn
	actual := fixturePath(dir, "drifted.flac")
	writeToneFLAC(t, actual, processTestRate, denoiseTestSeconds, fixtureToneHz)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "7").Return("drifted.mp3", nil)
	controller.DS = mockDS

	rec := getAudio(e, denoiseURL("7", denoiseTestPreset), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, expectedDenoised(t, actual), rec.Body.Bytes())
	assert.Contains(t, rec.Header().Get(echo.HeaderContentDisposition), "drifted.wav",
		"the download name follows the file served")
}

func TestServeAudioByID_DenoiseResolutionErrors(t *testing.T) {
	tests := []struct {
		name       string
		clipPath   string
		dsErr      error
		wantStatus int
	}{
		{name: "datastore failure", dsErr: assert.AnError, wantStatus: http.StatusInternalServerError},
		{name: "detection not found", dsErr: os.ErrNotExist, wantStatus: http.StatusNotFound},
		{name: "detection has no clip", clipPath: "", wantStatus: http.StatusNotFound},
		{name: "clip path escapes the export root", clipPath: "../outside.flac", wantStatus: http.StatusBadRequest},
		{name: "source cannot be decoded", clipPath: "corrupt.flac", wantStatus: http.StatusUnprocessableEntity},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, controller, dir := setupMediaTestEnvironment(t)
			controller.isClientAuthenticated = signedIn
			writeCorrupt(t, fixturePath(dir, "corrupt.flac"))
			mockDS := mocks.NewMockInterface(t)
			mockDS.On("GetNoteClipPath", "7").Return(tc.clipPath, tc.dsErr)
			controller.DS = mockDS

			rec := getAudio(e, denoiseURL("7", denoiseTestPreset), nil)
			assert.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			assert.NotEqual(t, MimeTypeWAV, rec.Header().Get(echo.HeaderContentType))
		})
	}
}

func TestServeAudioByID_DenoiseGuestMissingClipFollowsStoredPath(t *testing.T) {
	e, controller, _ := setupMediaTestEnvironment(t)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "42").Return("2024/01/gone_95p_20240101T000000Z.wav", nil)
	old := time.Now().Add(-2 * time.Hour)
	mockDS.On("Get", "42").Return(datastore.Note{ID: 42, BeginTime: old, EndTime: old}, nil).Maybe()
	controller.DS = mockDS

	rec := getAudio(e, denoiseURL("42", denoiseTestPreset), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code, "a guest gets the stored-clip answer for a missing clip")
}

func TestProcessWAV_ServesCachedEntryWhenSlotsAreTaken(t *testing.T) {
	_, controller, dir := setupMediaTestEnvironment(t)
	src := fixturePath(dir, "clip.wav")
	writeToneWAV(t, src, processTestRate, denoiseTestSeconds, fixtureToneHz)
	const key = "cached.wav"
	require.NoError(t, controller.processingCache.put(key, []byte("cached")))
	for range cap(controller.processingSemaphore) {
		controller.processingSemaphore <- struct{}{}
	}

	// A flight that starts after another stored the entry answers from the cache
	// instead of failing for want of a slot.
	got, err := controller.processWAV(t.Context(), key, src, transcode.Filters{Denoise: denoiseTestPreset})
	require.NoError(t, err)
	assert.Equal(t, []byte("cached"), got.data)

	_, err = controller.processWAV(t.Context(), "uncached.wav", src, transcode.Filters{Denoise: denoiseTestPreset})
	require.ErrorIs(t, err, errProcessingBusy)
}

func TestProcessWAV_CallerLeavesWithoutCancellingTheJob(t *testing.T) {
	_, controller, dir := setupMediaTestEnvironment(t)
	src := fixturePath(dir, "clip.wav")
	writeToneWAV(t, src, processTestRate, denoiseTestSeconds, fixtureToneHz)
	entered := make(chan struct{})
	release := make(chan struct{})
	controller.processingStartedHook = func() {
		close(entered)
		<-release
	}

	const key = "abandoned.wav"
	reqCtx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		_, err := controller.processWAV(reqCtx, key, src, transcode.Filters{Denoise: denoiseTestPreset})
		errCh <- err
	}()
	<-entered
	cancel()
	require.ErrorIs(t, <-errCh, context.Canceled)

	// The job finishes and fills the cache for the next caller.
	close(release)
	require.Eventually(t, func() bool { return controller.processingCache.get(key) != nil },
		5*time.Second, 10*time.Millisecond, "the abandoned job still stores its result")
}

func TestProcessWAV_JobPanicBecomesError(t *testing.T) {
	_, controller, dir := setupMediaTestEnvironment(t)
	src := fixturePath(dir, "clip.wav")
	writeToneWAV(t, src, processTestRate, denoiseTestSeconds, fixtureToneHz)
	controller.processingStartedHook = func() { panic("job failure") }

	// Without recovery the job's panic is re-raised on a goroutine nothing can
	// recover and ends the test binary instead of failing an assertion.
	_, err := controller.processWAV(t.Context(), "panics.wav", src, transcode.Filters{Denoise: denoiseTestPreset})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "job failure")

	// The slot is released and the key is free for the next caller.
	controller.processingStartedHook = nil
	_, err = controller.processWAV(t.Context(), "panics.wav", src, transcode.Filters{Denoise: denoiseTestPreset})
	require.NoError(t, err)
}

func TestProcessWAV_ShutdownWaitsForJob(t *testing.T) {
	_, controller, dir := setupMediaTestEnvironment(t)
	src := fixturePath(dir, "clip.wav")
	writeToneWAV(t, src, processTestRate, denoiseTestSeconds, fixtureToneHz)
	entered := make(chan struct{})
	release := make(chan struct{})
	controller.processingStartedHook = func() {
		close(entered)
		<-release
	}

	const key = "shutdown.wav"
	reqCtx, cancel := context.WithCancel(t.Context())
	go func() { _, _ = controller.processWAV(reqCtx, key, src, transcode.Filters{Denoise: denoiseTestPreset}) }()
	select {
	case <-entered:
	case <-time.After(denoiseStartTimeout):
		t.Fatal("the job never started")
	}
	cancel()

	// Shutdown is Cancel then Wait.
	controller.Cancel()
	waited := make(chan struct{})
	go func() {
		controller.Wait()
		close(waited)
	}()
	assert.Never(t, func() bool {
		select {
		case <-waited:
			return true
		default:
			return false
		}
	}, denoiseNeverWindow, denoiseNeverTick, "Shutdown must wait for a job that is still running")

	close(release)
	select {
	case <-waited:
	case <-time.After(denoiseStartTimeout):
		t.Fatal("Wait did not return after the job finished")
	}
	assert.Empty(t, controller.processingSemaphore, "the job released its slot before Wait returned")
}

func TestProcessWAV_NoJobStartsAfterShutdown(t *testing.T) {
	_, controller, dir := setupMediaTestEnvironment(t)
	src := fixturePath(dir, "clip.wav")
	writeToneWAV(t, src, processTestRate, denoiseTestSeconds, fixtureToneHz)
	var started atomic.Int32
	controller.processingStartedHook = func() { started.Add(1) }

	controller.Cancel()
	_, err := controller.processWAV(t.Context(), "late.wav", src, transcode.Filters{Denoise: denoiseTestPreset})
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, started.Load(), "no job starts once the lifecycle context is cancelled")
}

func TestProcessWAV_ModTimeIsCacheEntryWriteTime(t *testing.T) {
	_, controller, dir := setupMediaTestEnvironment(t)
	src := fixturePath(dir, "clip.wav")
	writeToneWAV(t, src, processTestRate, denoiseTestSeconds, fixtureToneHz)
	cacheDir := filepath.Join(dir, processingCacheDirName)

	// A fresh job reports the mtime of the entry it stored, the value a later
	// cache hit reports, so Last-Modified does not change between the two.
	const stored = "stored.wav"
	got, err := controller.processWAV(t.Context(), stored, src, transcode.Filters{Denoise: denoiseTestPreset})
	require.NoError(t, err)
	info, err := os.Stat(filepath.Join(cacheDir, stored))
	require.NoError(t, err)
	assert.True(t, info.ModTime().Equal(got.modTime), "job result carries %v, cache entry has %v", got.modTime, info.ModTime())

	// A flight that finds the entry already stored reports its write time too.
	const earlier = "earlier.wav"
	require.NoError(t, controller.processingCache.put(earlier, []byte("cached")))
	written := time.Now().Add(-time.Minute).Truncate(time.Second)
	require.NoError(t, os.Chtimes(filepath.Join(cacheDir, earlier), written, written))
	got, err = controller.processWAV(t.Context(), earlier, src, transcode.Filters{Denoise: denoiseTestPreset})
	require.NoError(t, err)
	assert.True(t, written.Equal(got.modTime), "cached result carries %v, entry was written %v", got.modTime, written)
}
