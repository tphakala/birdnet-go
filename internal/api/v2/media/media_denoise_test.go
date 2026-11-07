package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
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
	// denoiseStartTimeout bounds waits for a job to reach or leave its hook so a
	// regression fails instead of hanging the package.
	denoiseStartTimeout = 10 * time.Second
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
	// Inside the bubble synctest.Wait returns once every goroutine is blocked, so
	// the leader is parked in the hook and every follower is waiting on its flight:
	// the join is observed, not slept for.
	synctest.Test(t, func(t *testing.T) {
		e, controller, dir := setupMediaTestEnvironment(t)
		controller.isClientAuthenticated = signedIn
		writeToneFLAC(t, fixturePath(dir, "clip.flac"), processTestRate, denoiseTestSeconds, fixtureToneHz)
		mockDS := mocks.NewMockInterface(t)
		mockDS.On("GetNoteClipPath", "7").Return("clip.flac", nil)
		controller.DS = mockDS

		var started atomic.Int32
		release := make(chan struct{})
		var releaseOnce sync.Once
		releaseJob := func() { releaseOnce.Do(func() { close(release) }) }
		t.Cleanup(releaseJob)
		controller.processingStartedHook = func() {
			started.Add(1)
			<-release
		}

		const total = denoiseFollowers + 1
		recs := make([]*httptest.ResponseRecorder, total)
		var wg sync.WaitGroup
		for i := range total {
			wg.Go(func() { recs[i] = getAudio(e, denoiseURL("7", denoiseTestPreset), nil) })
		}
		synctest.Wait()
		require.Equal(t, int32(1), started.Load(), "every request is waiting on the one job")
		releaseJob()
		wg.Wait()

		for i, rec := range recs {
			require.Equal(t, http.StatusOK, rec.Code, "request %d", i)
			assert.Equal(t, recs[0].Body.Bytes(), rec.Body.Bytes(), "request %d", i)
		}
	})
}

func TestServeAudioByID_DenoiseSharesJobWithPost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, controller, dir := setupMediaTestEnvironment(t)
		controller.isClientAuthenticated = signedIn
		src := fixturePath(dir, "clip.flac")
		writeToneFLAC(t, src, processTestRate, denoiseTestSeconds, fixtureToneHz)
		mockDS := mocks.NewMockInterface(t)
		mockDS.On("GetNoteClipPath", "7").Return("clip.flac", nil)
		controller.DS = mockDS

		var started atomic.Int32
		release := make(chan struct{})
		var releaseOnce sync.Once
		releaseJob := func() { releaseOnce.Do(func() { close(release) }) }
		t.Cleanup(releaseJob)
		controller.processingStartedHook = func() {
			started.Add(1)
			<-release
		}

		var wg sync.WaitGroup
		var getRec, postRec *httptest.ResponseRecorder
		wg.Go(func() { getRec = getAudio(e, denoiseURL("7", denoiseTestPreset), nil) })
		wg.Go(func() { postRec = postJSON(e, "/api/v2/audio/7/process", `{"denoise": "`+denoiseTestPreset+`"}`) })
		synctest.Wait()
		require.Equal(t, int32(1), started.Load(), "a denoise-only POST joins the GET's job")
		releaseJob()
		wg.Wait()

		require.Equal(t, http.StatusOK, getRec.Code)
		require.Equal(t, http.StatusOK, postRec.Code)
		assert.Equal(t, getRec.Body.Bytes(), postRec.Body.Bytes())
		assert.Equal(t, expectedDenoised(t, src), postRec.Body.Bytes())
	})
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
	var releaseOnce sync.Once
	releaseJob := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseJob)
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
	select {
	case <-entered:
	case err := <-errCh:
		t.Fatalf("the job never started: %v", err)
	case <-time.After(denoiseStartTimeout):
		t.Fatal("the job never started")
	}
	cancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(denoiseStartTimeout):
		t.Fatal("the caller did not leave after its request was cancelled")
	}

	// The job finishes and fills the cache for the next caller.
	releaseJob()
	require.Eventually(t, func() bool { return controller.processingCache.get(key) != nil },
		denoiseStartTimeout, 10*time.Millisecond, "the abandoned job still stores its result")
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
	// Inside the bubble synctest.Wait returns once every goroutine is blocked, so
	// "Wait is still blocked on the running job" is observed, not timed.
	synctest.Test(t, func(t *testing.T) {
		_, controller, dir := setupMediaTestEnvironment(t)
		src := fixturePath(dir, "clip.wav")
		writeToneWAV(t, src, processTestRate, denoiseTestSeconds, fixtureToneHz)
		release := make(chan struct{})
		var releaseOnce sync.Once
		releaseJob := func() { releaseOnce.Do(func() { close(release) }) }
		t.Cleanup(releaseJob)
		controller.processingStartedHook = func() { <-release }

		reqCtx, cancel := context.WithCancel(t.Context())
		go func() {
			_, _ = controller.processWAV(reqCtx, "shutdown.wav", src, transcode.Filters{Denoise: denoiseTestPreset})
		}()
		synctest.Wait() // the job is parked in the hook
		cancel()

		// Shutdown is Cancel then Wait.
		controller.Cancel()
		waited := make(chan struct{})
		go func() {
			controller.Wait()
			close(waited)
		}()
		synctest.Wait()
		select {
		case <-waited:
			t.Fatal("Shutdown must wait for a job that is still running")
		default:
		}

		releaseJob()
		<-waited
		assert.Empty(t, controller.processingSemaphore, "the job released its slot before Wait returned")
	})
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

func TestServeAudioByID_DenoiseValidators(t *testing.T) {
	e, controller, dir := setupMediaTestEnvironment(t)
	controller.isClientAuthenticated = signedIn
	src := fixturePath(dir, "clip.flac")
	writeToneFLAC(t, src, processTestRate, denoiseTestSeconds, fixtureToneHz)
	// The stored clip and the cache entry get different, known write times.
	sourceTime := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	require.NoError(t, os.Chtimes(src, sourceTime, sourceTime))
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "7").Return("clip.flac", nil)
	controller.DS = mockDS
	url := denoiseURL("7", denoiseTestPreset)

	miss := getAudio(e, url, nil)
	require.Equal(t, http.StatusOK, miss.Code)
	etag := miss.Header().Get("ETag")
	assert.Contains(t, etag, denoiseTestPreset, "the ETag carries the cache key")
	assert.True(t, strings.HasSuffix(etag, "-"+strconv.Itoa(miss.Body.Len())+`"`), "the ETag carries the output length: %s", etag)

	entries, err := os.ReadDir(filepath.Join(dir, processingCacheDirName))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	entryPath := filepath.Join(dir, processingCacheDirName, entries[0].Name())
	entryInfo, err := os.Stat(entryPath)
	require.NoError(t, err)
	assert.Equal(t, entryInfo.ModTime().UTC().Format(http.TimeFormat), miss.Header().Get("Last-Modified"),
		"the first response already carries the cache entry's write time")
	entryTime := time.Now().Add(-time.Minute).Truncate(time.Second)
	require.NoError(t, os.Chtimes(entryPath, entryTime, entryTime))

	hit := getAudio(e, url, nil)
	require.Equal(t, http.StatusOK, hit.Code)
	assert.Equal(t, etag, hit.Header().Get("ETag"), "a miss and a hit share one ETag")
	assert.Equal(t, entryTime.UTC().Format(http.TimeFormat), hit.Header().Get("Last-Modified"),
		"Last-Modified is the cache entry's write time, not the stored clip's")

	assert.Equal(t, http.StatusNotModified, getAudio(e, url, map[string]string{"If-None-Match": etag}).Code)

	rangeHeader := "bytes=" + strconv.Itoa(denoiseRangeStart) + "-" + strconv.Itoa(denoiseRangeEnd)
	assert.Equal(t, http.StatusPartialContent,
		getAudio(e, url, map[string]string{"Range": rangeHeader, "If-Range": etag}).Code,
		"If-Range with the ETag keeps the range")
	assert.Equal(t, http.StatusOK,
		getAudio(e, url, map[string]string{"Range": rangeHeader, "If-Range": sourceTime.UTC().Format(http.TimeFormat)}).Code,
		"an If-Range date taken from the stored clip must not match the denoised variant")
}

func TestServeAudioByID_DenoiseWaitsForEncodingClip(t *testing.T) {
	e, controller, dir := setupMediaTestEnvironment(t)
	controller.isClientAuthenticated = signedIn
	controller.audioWaitTimeoutOverride = testAudioAsyncTimeout
	const clipName = "encoding_88p_20260724T143045Z.wav"
	clipPath := fixturePath(dir, clipName)
	tempPath := clipPath + ".99999.1" + audiotemp.Ext
	require.NoError(t, os.WriteFile(tempPath, []byte("temp encoding data"), 0o600))
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "7").Return(clipName, nil)
	now := time.Now()
	mockDS.On("Get", "7").Return(datastore.Note{ID: 7, BeginTime: now, EndTime: now}, nil).Maybe()
	controller.DS = mockDS

	// The export finishes while the request waits for it.
	controller.audioWaitStartedHook = func() {
		writeToneWAV(t, clipPath, processTestRate, denoiseTestSeconds, fixtureToneHz)
		require.NoError(t, os.Remove(tempPath))
	}

	rec := getAudio(e, denoiseURL("7", denoiseTestPreset), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, MimeTypeWAV, rec.Header().Get(echo.HeaderContentType))
	assert.Equal(t, expectedDenoised(t, clipPath), rec.Body.Bytes(), "the clip that appeared is denoised")
}
