package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
)

const (
	// specDenoiseID is the note ID the spectrogram denoise tests use.
	specDenoiseID = "7"
	// specDenoiseClip is the stored clip name of that note.
	specDenoiseClip = "clip.flac"
	// specDenoiseSeconds is the fixture length; long enough for a render.
	specDenoiseSeconds = 1.0
	// specModelBird and specModelBat are the model types the lookup returns.
	specModelBird = "bird"
	specModelBat  = "bat"
	// specPNGExt is the extension of a cached rendered spectrogram.
	specPNGExt = ".png"
	// denoisePollInterval is how often Eventually re-checks.
	denoisePollInterval = 10 * time.Millisecond
)

// specDenoiseEnv is one handler with a clip written under it and the model
// lookup mocked.
type specDenoiseEnv struct {
	e          *echo.Echo
	controller *Handler
	dir        string
	// wavJobs counts the processing jobs that reached the transcode.
	wavJobs *atomic.Int32
}

// newSpecDenoiseEnv writes the fixture clip for note 7 and mocks its lookups.
func newSpecDenoiseEnv(t *testing.T, modelType string, rate int, freq float64, opts ...apitest.CoreOption) *specDenoiseEnv {
	t.Helper()
	e, controller, dir := setupMediaTestEnvironment(t, opts...)
	clearBinaryEnvironment(t, controller.Settings.Load())
	controller.isClientAuthenticated = signedIn
	writeToneFLAC(t, fixturePath(dir, specDenoiseClip), rate, specDenoiseSeconds, freq)
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", specDenoiseID).Return(specDenoiseClip, nil).Maybe()
	mockDS.On("GetNoteModelType", specDenoiseID).Return(modelType, nil).Maybe()
	controller.DS = mockDS
	env := &specDenoiseEnv{e: e, controller: controller, dir: dir, wavJobs: &atomic.Int32{}}
	controller.processingStartedHook = func() { env.wavJobs.Add(1) }
	return env
}

func specDenoiseURL(preset string) string {
	return "/api/v2/spectrogram/" + specDenoiseID + "?denoise=" + preset
}

// getURL issues a GET against the test server.
func getURL(e http.Handler, path string) *httptest.ResponseRecorder {
	return getAudio(e, path, nil)
}

// cachedPNGs lists the rendered spectrograms in the processing cache.
func cachedPNGs(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, processingCacheDirName))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), specPNGExt) {
			names = append(names, entry.Name())
		}
	}
	return names
}

func TestServeSpectrogramByID_Denoise(t *testing.T) {
	env := newSpecDenoiseEnv(t, specModelBird, processTestRate, fixtureToneHz)

	stored := getURL(env.e, "/api/v2/spectrogram/"+specDenoiseID)
	require.Equal(t, http.StatusOK, stored.Code, stored.Body.String())
	assert.Zero(t, env.wavJobs.Load(), "no parameter never denoises")

	want := postJSON(env.e, "/api/v2/spectrogram/"+specDenoiseID+"/process", `{"denoise":"`+denoiseTestPreset+`"}`)
	require.Equal(t, http.StatusOK, want.Code, want.Body.String())
	env.wavJobs.Store(0)

	rec := getURL(env.e, specDenoiseURL(denoiseTestPreset))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "image/png", rec.Header().Get(echo.HeaderContentType))
	assert.Equal(t, want.Body.Bytes(), rec.Body.Bytes(), "the PNG is a render of the denoised clip")
	assert.NotEqual(t, stored.Body.Bytes(), rec.Body.Bytes(), "the variant differs from the stored spectrogram")
	etag := rec.Header().Get("ETag")
	assert.NotEmpty(t, etag)
	assert.Equal(t, int32(1), env.wavJobs.Load(), "one denoise for the spectrogram")

	audio := getAudio(env.e, denoiseURL(specDenoiseID, denoiseTestPreset), nil)
	require.Equal(t, http.StatusOK, audio.Code)
	assert.Equal(t, MimeTypeWAV, audio.Header().Get(echo.HeaderContentType))
	assert.Equal(t, int32(1), env.wavJobs.Load(), "the first play hits the WAV the spectrogram made")

	again := getURL(env.e, specDenoiseURL(denoiseTestPreset))
	require.Equal(t, http.StatusOK, again.Code)
	assert.Equal(t, rec.Body.Bytes(), again.Body.Bytes())
	assert.Equal(t, etag, again.Header().Get("ETag"))
	assert.Equal(t, int32(1), env.wavJobs.Load(), "a repeat is a cache hit")
	assert.Len(t, cachedPNGs(t, env.dir), 1)

	bad := getURL(env.e, specDenoiseURL("bogus"))
	assert.Equal(t, http.StatusBadRequest, bad.Code)
}

func TestServeSpectrogramByID_DenoiseKeepsBatProfile(t *testing.T) {
	env := newSpecDenoiseEnv(t, specModelBat, batTestRate, fixtureBatToneHz)

	want := postJSON(env.e, "/api/v2/spectrogram/"+specDenoiseID+"/process", `{"denoise":"`+denoiseTestPreset+`"}`)
	require.Equal(t, http.StatusOK, want.Code, want.Body.String())

	rec := getURL(env.e, specDenoiseURL(denoiseTestPreset))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, want.Body.Bytes(), rec.Body.Bytes())
	names := cachedPNGs(t, env.dir)
	require.Len(t, names, 1)
	assert.Contains(t, names[0], "bat", "the frequency profile is part of the cache key")
}

func TestServeSpectrogramByID_DenoiseGuest(t *testing.T) {
	env := newSpecDenoiseEnv(t, specModelBird, processTestRate, fixtureToneHz)
	stored := getURL(env.e, "/api/v2/spectrogram/"+specDenoiseID)
	require.Equal(t, http.StatusOK, stored.Code)

	previous := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(previous) })
	publish := func(playbackOn bool) {
		settings := conf.CloneSettings(env.controller.CurrentSettings())
		settings.Realtime.Dashboard.PlaybackDenoise = conf.DenoiseSettings{Enabled: playbackOn, Strength: denoiseTestPreset}
		conftest.SetTestSettings(settings)
	}
	env.controller.isClientAuthenticated = nil

	publish(false)
	rec := getURL(env.e, specDenoiseURL(denoiseTestPreset))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, stored.Body.Bytes(), rec.Body.Bytes(), "default off: a guest gets today's spectrogram")
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Zero(t, env.wavJobs.Load())

	publish(true)
	other := getURL(env.e, specDenoiseURL("heavy"))
	require.Equal(t, http.StatusOK, other.Code)
	assert.Equal(t, stored.Body.Bytes(), other.Body.Bytes(), "a preset other than the default is not a guest's to start")
	assert.Equal(t, "no-store", other.Header().Get("Cache-Control"))
	assert.Zero(t, env.wavJobs.Load())

	rec = getURL(env.e, specDenoiseURL(denoiseTestPreset))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotEqual(t, stored.Body.Bytes(), rec.Body.Bytes(), "default on: the guest gets the denoised render")
	assert.Equal(t, int32(1), env.wavJobs.Load())

	publish(false)
	rec = getURL(env.e, specDenoiseURL(denoiseTestPreset))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotEqual(t, stored.Body.Bytes(), rec.Body.Bytes(), "a cached render is served to a guest first")
}

func TestServeSpectrogramByID_DenoiseFollowsStyleSettings(t *testing.T) {
	env := newSpecDenoiseEnv(t, specModelBird, processTestRate, fixtureToneHz)
	previous := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(previous) })
	publish := func(style, dynamicRange string) {
		settings := conf.CloneSettings(env.controller.CurrentSettings())
		settings.Realtime.Dashboard.Spectrogram.Style = style
		settings.Realtime.Dashboard.Spectrogram.DynamicRange = dynamicRange
		conftest.SetTestSettings(settings)
	}

	publish(conf.SpectrogramStyleDefault, conf.SpectrogramDynamicRangeStandard)
	first := getURL(env.e, specDenoiseURL(denoiseTestPreset))
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())

	publish(conf.SpectrogramStyleScientificDark, conf.SpectrogramDynamicRangeStandard)
	styled := getURL(env.e, specDenoiseURL(denoiseTestPreset))
	require.Equal(t, http.StatusOK, styled.Code)
	assert.NotEqual(t, first.Header().Get("ETag"), styled.Header().Get("ETag"), "a Style change gives a new ETag")
	assert.NotEqual(t, first.Body.Bytes(), styled.Body.Bytes(), "a Style change renders again")

	publish(conf.SpectrogramStyleScientificDark, conf.SpectrogramDynamicRangeExtended)
	ranged := getURL(env.e, specDenoiseURL(denoiseTestPreset))
	require.Equal(t, http.StatusOK, ranged.Code)
	assert.NotEqual(t, styled.Header().Get("ETag"), ranged.Header().Get("ETag"), "a DynamicRange change gives a new ETag")
	assert.NotEqual(t, styled.Body.Bytes(), ranged.Body.Bytes(), "a DynamicRange change renders again")
	assert.Len(t, cachedPNGs(t, env.dir), 3)
	assert.Equal(t, int32(1), env.wavJobs.Load(), "the denoised WAV is shared by every render")
}

// A settings change while the render runs does not reach it: the image matches
// the Style in its cache key, which was read from the request's snapshot.
func TestServeSpectrogramByID_DenoiseRendersWithTheKeySnapshot(t *testing.T) {
	env := newSpecDenoiseEnv(t, specModelBird, processTestRate, fixtureToneHz)
	previous := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(previous) })
	publish := func(style string) {
		settings := conf.CloneSettings(env.controller.CurrentSettings())
		settings.Realtime.Dashboard.Spectrogram.Style = style
		conftest.SetTestSettings(settings)
	}

	publish(conf.SpectrogramStyleDefault)
	env.controller.spectrogramRenderHook = func() { publish(conf.SpectrogramStyleScientificDark) }
	rec := getURL(env.e, specDenoiseURL(denoiseTestPreset))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	env.controller.spectrogramRenderHook = nil

	publish(conf.SpectrogramStyleDefault)
	want := postJSON(env.e, "/api/v2/spectrogram/"+specDenoiseID+"/process", `{"denoise":"`+denoiseTestPreset+`"}`)
	require.Equal(t, http.StatusOK, want.Code, want.Body.String())
	assert.Equal(t, want.Body.Bytes(), rec.Body.Bytes(), "the render used the Style the key was built from")
	names := cachedPNGs(t, env.dir)
	require.Len(t, names, 1)
	assert.True(t, strings.HasSuffix(names[0], "_"+defaultProfileKeyToken+specPNGExt), "the key names the default Style: %s", names[0])
}

func TestServeSpectrogramByID_DenoiseUserRequestedMode(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		rate      int
		freq      float64
		freqToken string
	}{
		{name: "bird", model: specModelBird, rate: processTestRate, freq: fixtureToneHz},
		{name: "bat", model: specModelBat, rate: batTestRate, freq: fixtureBatToneHz, freqToken: spectrogram.ProfileSuffix(spectrogram.BatProfile())},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newSpecDenoiseEnv(t, tc.model, tc.rate, tc.freq,
				apitest.WithSettingsFunc(func(s *conf.Settings) {
					s.Realtime.Dashboard.Spectrogram.Mode = conf.SpectrogramModeUserRequested
				}))

			plain := getURL(env.e, "/api/v2/spectrogram/"+specDenoiseID)
			require.Equal(t, http.StatusNotFound, plain.Code)

			rec := getURL(env.e, specDenoiseURL(denoiseTestPreset))
			assert.Equal(t, http.StatusNotFound, rec.Code, "a page load must not render in this mode")
			assert.Contains(t, rec.Body.String(), conf.SpectrogramModeUserRequested, "the not-generated response is today's")
			assert.Zero(t, env.wavJobs.Load(), "no denoise runs")
			assert.Empty(t, cachedPNGs(t, env.dir))

			// The user has generated the stored spectrogram, named with the
			// clip's frequency profile token: the denoised one then renders.
			spec := env.controller.CurrentSettings().Realtime.Dashboard.Spectrogram
			_, _, _, rel := buildSpectrogramPaths(specDenoiseClip, SpectrogramSizeLg, true, spec.Style, spec.DynamicRange, tc.freqToken)
			require.NoError(t, os.WriteFile(fixturePath(env.dir, rel), []byte(nativeGateMarker), 0o600))

			rec = getURL(env.e, specDenoiseURL(denoiseTestPreset))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, "image/png", rec.Header().Get(echo.HeaderContentType))
			assert.Equal(t, int32(1), env.wavJobs.Load())
		})
	}
}

func TestServeSpectrogramByID_DenoiseIgnoredWithoutAudio(t *testing.T) {
	env := newSpecDenoiseEnv(t, specModelBird, processTestRate, fixtureToneHz)
	rec := getURL(env.e, "/api/v2/spectrogram/"+specDenoiseID)
	require.Equal(t, http.StatusOK, rec.Code)
	stored := rec.Body.Bytes()

	require.NoError(t, os.Remove(fixturePath(env.dir, specDenoiseClip)))
	rec = getURL(env.e, specDenoiseURL(denoiseTestPreset))
	require.Equal(t, http.StatusOK, rec.Code, "the kept spectrogram is still served")
	assert.Equal(t, stored, rec.Body.Bytes())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Zero(t, env.wavJobs.Load())
}

// TestSpectrogramRender_DoesNotHoldProcessingSlot holds a render in flight and
// checks that every processing slot is free meanwhile, for the processed
// spectrogram POST and the denoised GET alike. Catches a slot held until the
// handler returns.
func TestSpectrogramRender_DoesNotHoldProcessingSlot(t *testing.T) {
	tests := []struct {
		name string
		do   func(e http.Handler) *httptest.ResponseRecorder
	}{
		{"processed spectrogram POST", func(e http.Handler) *httptest.ResponseRecorder {
			return postJSON(e, "/api/v2/spectrogram/"+specDenoiseID+"/process", `{"denoise":"`+denoiseTestPreset+`"}`)
		}},
		{"denoised spectrogram GET", func(e http.Handler) *httptest.ResponseRecorder {
			return getURL(e, specDenoiseURL(denoiseTestPreset))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newSpecDenoiseEnv(t, specModelBird, processTestRate, fixtureToneHz)
			inRender := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseRender := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(releaseRender)
			env.controller.spectrogramRenderHook = func() {
				close(inRender)
				<-release
			}

			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- tc.do(env.e) }()
			select {
			case <-inRender:
			case <-time.After(denoiseStartTimeout):
				t.Fatal("the render never started")
			}

			var releases []func()
			for range processingSemaphoreSize {
				release, ok := env.controller.tryAcquireProcessingSlot()
				assert.True(t, ok, "a slot is free while a render runs")
				if ok {
					releases = append(releases, release)
				}
			}
			for _, free := range releases {
				free()
			}

			releaseRender()
			select {
			case rec := <-done:
				assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			case <-time.After(denoiseStartTimeout):
				t.Fatal("the request never finished")
			}
		})
	}
}

func TestServeSpectrogramByID_DenoiseBusyIs503(t *testing.T) {
	env := newSpecDenoiseEnv(t, specModelBird, processTestRate, fixtureToneHz)
	for range processingSemaphoreSize {
		release, ok := env.controller.tryAcquireProcessingSlot()
		require.True(t, ok)
		t.Cleanup(release)
	}

	rec := getURL(env.e, specDenoiseURL(denoiseTestPreset))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
	assert.Empty(t, cachedPNGs(t, env.dir))
}

func TestServeSpectrogramByID_DenoiseUndecodableClipIs422(t *testing.T) {
	env := newSpecDenoiseEnv(t, specModelBird, processTestRate, fixtureToneHz)
	writeCorrupt(t, fixturePath(env.dir, specDenoiseClip))

	rec := getURL(env.e, specDenoiseURL(denoiseTestPreset))
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

func TestServeSpectrogramByID_DenoiseRenderFailureIs500(t *testing.T) {
	env := newSpecDenoiseEnv(t, specModelBird, processTestRate, fixtureToneHz)
	tmpDir := filepath.Join(env.dir, ".tmp-processing")
	// The render's input vanishes just before it starts.
	env.controller.spectrogramRenderHook = func() {
		entries, err := os.ReadDir(tmpDir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".wav") {
				_ = os.Remove(filepath.Join(tmpDir, entry.Name()))
			}
		}
	}

	rec := getURL(env.e, specDenoiseURL(denoiseTestPreset))
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Empty(t, cachedPNGs(t, env.dir), "a failed render is not cached")
}

// TestServeSpectrogramByID_DenoiseCallerLeavesJobFinishes cancels the request
// while the denoise is running: no response is written, and the job still fills
// the cache for the next request.
func TestServeSpectrogramByID_DenoiseCallerLeavesJobFinishes(t *testing.T) {
	env := newSpecDenoiseEnv(t, specModelBird, processTestRate, fixtureToneHz)
	inJob := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseJob := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseJob)
	env.controller.processingStartedHook = func() {
		close(inJob)
		<-release
	}

	reqCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, specDenoiseURL(denoiseTestPreset), http.NoBody).WithContext(reqCtx)
	rec := httptest.NewRecorder()
	served := make(chan struct{})
	go func() {
		defer close(served)
		env.e.ServeHTTP(rec, req)
	}()
	select {
	case <-inJob:
	case <-time.After(denoiseStartTimeout):
		t.Fatal("the denoise never started")
	}
	cancel()
	select {
	case <-served:
	case <-time.After(denoiseStartTimeout):
		t.Fatal("the request did not return after its client left")
	}
	assert.Empty(t, rec.Body.Bytes(), "no response for a client that left")

	releaseJob()
	require.Eventually(t, func() bool { return len(cachedPNGs(t, env.dir)) == 1 },
		denoiseStartTimeout, denoisePollInterval, "the job outlives its caller")
}
