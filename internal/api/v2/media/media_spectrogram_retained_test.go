// media_spectrogram_retained_test.go: coverage for serving a spectrogram that
// outlived its audio clip (GitHub #4155). Retention moves clip_name into the
// spectrogram clip name when a render is kept; the by-ID endpoint must serve that
// render without generating anything.

package media

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	retainedNoteID   = "77"
	retainedClipName = "2026/01/a.wav"
)

// newRetainedTestHandler builds a media Handler in the given spectrogram mode and
// returns it with its echo instance and the SecureFS root.
func newRetainedTestHandler(t *testing.T, mode string) (*Handler, *echo.Echo, string) {
	t.Helper()
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithSettingsFunc(func(s *conf.Settings) {
		s.Realtime.Dashboard.Spectrogram.Mode = mode
	}))
	return New(core), e, core.SFS.BaseDir()
}

// writeRetainedFile writes content to root/rel, creating parent directories.
func writeRetainedFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

// noClipDS returns a datastore mock for a detection whose clip_name was moved to
// spectrogramClipName. legacy selects the empty-string flavour instead of the v2
// ErrNoClipPath sentinel.
func noClipDS(t *testing.T, spectrogramClipName string, legacy bool) *mocks.MockInterface {
	t.Helper()
	ds := mocks.NewMockInterface(t)
	if legacy {
		ds.EXPECT().GetNoteClipPath(retainedNoteID).Return("", nil)
	} else {
		ds.EXPECT().GetNoteClipPath(retainedNoteID).Return("", repository.ErrNoClipPath)
	}
	ds.EXPECT().GetNoteKeptSpectrogram(retainedNoteID).Return(spectrogramClipName, "bird", nil)
	return ds
}

// serveSpectrogram issues GET /spectrogram/:id with the given query.
func serveSpectrogram(t *testing.T, h *Handler, e *echo.Echo, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/spectrogram/"+retainedNoteID+query, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(retainedNoteID)
	_ = h.ServeSpectrogramByID(c)
	return rec
}

func TestServeSpectrogramByID_SpectrogramOnly_ServesExactRender(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "v2 ErrNoClipPath"
		if legacy {
			name = "legacy empty clip path"
		}
		t.Run(name, func(t *testing.T) {
			h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
			writeRetainedFile(t, root, "2026/01/a_514px.png", "exact render")
			h.DS = noClipDS(t, retainedClipName, legacy)

			rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
			assert.Equal(t, "exact render", rec.Body.String())
			assert.Contains(t, rec.Header().Get("Cache-Control"), "immutable")
		})
	}
}

func TestServeSpectrogramByID_SpectrogramOnly_FallsBackToNearestRender(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "only a larger legend render exists",
			files: map[string]string{"2026/01/a_1026px-legend.png": "legend 1026"},
			want:  "legend 1026",
		},
		{
			name: "raw match beats width",
			files: map[string]string{
				"2026/01/a_514px-legend.png": "legend 514",
				"2026/01/a_2050px.png":       "raw 2050",
			},
			want: "raw 2050",
		},
		{
			name: "sized beats unsized",
			files: map[string]string{
				"2026/01/a.png":        "prerender",
				"2026/01/a_2050px.png": "raw 2050", // farther from 514 than the unsized file, so only the rank prefers it
			},
			want: "raw 2050",
		},
		{
			name: "tie goes to the larger width",
			files: map[string]string{
				"2026/01/a_258px.png": "raw 258",
				"2026/01/a_770px.png": "raw 770",
			},
			want: "raw 770",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
			for rel, content := range tt.files {
				writeRetainedFile(t, root, rel, content)
			}
			h.DS = noClipDS(t, retainedClipName, false)

			rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, tt.want, rec.Body.String())
		})
	}
}

func TestServeSpectrogramByID_SpectrogramOnly_ServesPrerenderFile(t *testing.T) {
	h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
	writeRetainedFile(t, root, "2026/01/a.png", "prerender")
	h.DS = noClipDS(t, retainedClipName, false)

	rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "prerender", rec.Body.String())
}

func TestServeSpectrogramByID_SpectrogramOnly_IgnoresOtherClipsRenders(t *testing.T) {
	h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
	// An extended-capture sibling of the same base is another clip's render.
	writeRetainedFile(t, root, "2026/01/a_15s_514px.png", "other clip")
	h.DS = noClipDS(t, retainedClipName, false)

	rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "other clip")
}

func TestServeSpectrogramByID_SpectrogramOnly_NoRenderReturns404WithoutModeEnvelope(t *testing.T) {
	for _, mode := range []string{conf.SpectrogramModeAuto, conf.SpectrogramModeUserRequested} {
		t.Run(mode, func(t *testing.T) {
			h, e, _ := newRetainedTestHandler(t, mode)
			h.DS = noClipDS(t, retainedClipName, false)

			rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

			require.Equal(t, http.StatusNotFound, rec.Code)
			var body map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.NotContains(t, body, "data", "no Generate envelope for audio that is gone")
			assert.Contains(t, rec.Body.String(), msgRetainedSpectrogramMissing)
		})
	}
}

func TestServeSpectrogramByID_SpectrogramOnly_NoSpectrogramNameReturnsNoAudio404(t *testing.T) {
	h, e, _ := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
	h.DS = noClipDS(t, "", false)

	rec := serveSpectrogram(t, h, e, "?size=md")

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "No audio clip available for this note")
}

func TestServeSpectrogramByID_SpectrogramOnly_UserRequestedModeStillServes(t *testing.T) {
	h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeUserRequested)
	writeRetainedFile(t, root, "2026/01/a_1026px-legend.png", "legend")
	h.DS = noClipDS(t, retainedClipName, false)

	rec := serveSpectrogram(t, h, e, "?size=lg&raw=false")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "legend", rec.Body.String())
}

func TestServeSpectrogramByID_SpectrogramOnly_RejectsTraversal(t *testing.T) {
	h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "x_514px.png"), []byte("secret"), 0o600))
	h.DS = noClipDS(t, "../../x.wav", false)

	rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), msgRetainedSpectrogramMissing)
	assert.NotContains(t, rec.Body.String(), "secret")

	t.Run("symlinked render pointing outside the root is not served", func(t *testing.T) {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "2026", "01"), 0o750))
		link := filepath.Join(root, "2026", "01", "a_514px.png")
		if err := os.Symlink(filepath.Join(outside, "x_514px.png"), link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		h.DS = noClipDS(t, retainedClipName, false)

		rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

		assert.NotEqual(t, http.StatusOK, rec.Code)
		assert.NotContains(t, rec.Body.String(), "secret")
	})
}

func TestServeSpectrogramByID_DetectionNotFoundUnchanged(t *testing.T) {
	h, e, _ := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
	ds := mocks.NewMockInterface(t)
	ds.EXPECT().GetNoteClipPath(retainedNoteID).Return("", repository.ErrDetectionNotFound)
	h.DS = ds // no Get expectation: the mock fails the test if Get is called

	rec := serveSpectrogram(t, h, e, "?size=md")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "No audio clip available for this note")
}

func TestGenerateSpectrogramByID_NoClipUnchanged(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "v2 ErrNoClipPath"
		if legacy {
			name = "legacy empty clip path"
		}
		t.Run(name, func(t *testing.T) {
			h, e, _ := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
			ds := mocks.NewMockInterface(t)
			if legacy {
				ds.EXPECT().GetNoteClipPath(retainedNoteID).Return("", nil)
			} else {
				ds.EXPECT().GetNoteClipPath(retainedNoteID).Return("", repository.ErrNoClipPath)
			}
			h.DS = ds

			req := httptest.NewRequest(http.MethodPost, "/api/v2/spectrogram/"+retainedNoteID+"/generate", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(retainedNoteID)
			_ = h.GenerateSpectrogramByID(c)

			assert.Equal(t, http.StatusNotFound, rec.Code)
			assert.Contains(t, rec.Body.String(), "No audio clip available for this note")
			assert.NotContains(t, rec.Body.String(), msgRetainedSpectrogramMissing)
		})
	}
}

// audioGoneDS returns a mock for a detection whose clip_name is still set but whose
// audio file is gone (the window before the database move).
func audioGoneDS(t *testing.T) *mocks.MockInterface {
	t.Helper()
	ds := mocks.NewMockInterface(t)
	ds.EXPECT().GetNoteClipPath(retainedNoteID).Return(retainedClipName, nil)
	ds.EXPECT().Get(retainedNoteID).Return(datastore.Note{ClipName: retainedClipName}, nil).Maybe()
	ds.EXPECT().GetNoteModelType(retainedNoteID).Return("bird", nil).Maybe()
	return ds
}

func TestServeSpectrogramByID_ClipSetAudioGone_ServesExistingRender(t *testing.T) {
	t.Run("auto mode serves the existing render", func(t *testing.T) {
		h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
		writeRetainedFile(t, root, "2026/01/a_1026px-legend.png", "legend")
		h.DS = audioGoneDS(t)

		rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "legend", rec.Body.String())
	})

	t.Run("user-requested mode serves it instead of the mode envelope", func(t *testing.T) {
		h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeUserRequested)
		writeRetainedFile(t, root, "2026/01/a_1026px-legend.png", "legend")
		h.DS = audioGoneDS(t)

		rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "legend", rec.Body.String())
	})

	t.Run("auto mode without any render keeps the 404", func(t *testing.T) {
		h, e, _ := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
		h.DS = audioGoneDS(t)

		rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Body.String(), "Source audio file not found")
	})
}

func TestServeSpectrogramByID_SpectrogramOnly_DetectionLookupFailures(t *testing.T) {
	tests := []struct {
		name     string
		getErr   error
		wantCode int
		wantBody string
	}{
		{"detection vanished", repository.ErrDetectionNotFound, http.StatusNotFound, "No audio clip available for this note"},
		{"datastore failure", errors.NewStd("db down"), http.StatusInternalServerError, "Failed to load detection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, e, _ := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
			ds := mocks.NewMockInterface(t)
			ds.EXPECT().GetNoteClipPath(retainedNoteID).Return("", repository.ErrNoClipPath)
			ds.EXPECT().GetNoteKeptSpectrogram(retainedNoteID).Return("", "", tt.getErr)
			h.DS = ds

			rec := serveSpectrogram(t, h, e, "?size=md")

			assert.Equal(t, tt.wantCode, rec.Code)
			assert.Contains(t, rec.Body.String(), tt.wantBody)
		})
	}
}

func TestServeSpectrogramByID_SpectrogramOnly_RenderChoiceIsDeterministic(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "nearest width wins",
			files: map[string]string{
				"2026/01/a_258px.png":  "raw 258",
				"2026/01/a_2050px.png": "raw 2050",
			},
			want: "raw 258", // |258-514| = 256 beats |2050-514| = 1536
		},
		{
			name: "same rank falls back to the lexical name",
			files: map[string]string{
				"2026/01/a.png": "lower",
				"2026/01/a.PNG": "upper",
			},
			want: "upper", // "a.PNG" sorts before "a.png"
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
			for rel, content := range tt.files {
				writeRetainedFile(t, root, rel, content)
			}
			if len(tt.files) == 2 {
				// A case-insensitive filesystem collapses a.png and a.PNG into one file.
				entries, err := os.ReadDir(filepath.Join(root, "2026", "01"))
				require.NoError(t, err)
				if len(entries) != 2 {
					t.Skip("case-insensitive filesystem")
				}
			}
			h.DS = noClipDS(t, retainedClipName, false)

			rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, tt.want, rec.Body.String())
		})
	}
}

func TestServeSpectrogramByID_SpectrogramOnly_ClipNameThatNormalizesToNothingServesNothing(t *testing.T) {
	h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
	// "." normalizes to nothing. Without the empty-name guard it would resolve to the
	// root with an empty clip base, whose exact render name is "_514px.png".
	writeRetainedFile(t, root, "_514px.png", "root level file")
	h.DS = noClipDS(t, ".", false)

	rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "root level file")
}

func TestServeSpectrogramByID_SpectrogramOnly_ZeroByteExactRenderFallsThrough(t *testing.T) {
	h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
	writeRetainedFile(t, root, "2026/01/a_514px.png", "")
	writeRetainedFile(t, root, "2026/01/a_1026px.png", "raw 1026")
	h.DS = noClipDS(t, retainedClipName, false)

	rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "raw 1026", rec.Body.String(), "an empty file is not a render")
}

func TestServeSpectrogramByID_UserRequestedModeWithAudioKeepsGenerateEnvelope(t *testing.T) {
	h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeUserRequested)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "2026", "01"), 0o750))
	require.NoError(t, createTestAudioFile(t, filepath.Join(root, "2026", "01", "a.wav")))
	// A render of another size exists, but the audio is present, so the user must be
	// able to generate the size they asked for.
	writeRetainedFile(t, root, "2026/01/a_1026px-legend.png", "legend")
	h.DS = audioGoneDS(t)

	rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

	require.Equal(t, http.StatusNotFound, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	data, ok := body["data"].(map[string]any)
	require.True(t, ok, "the not-generated response carries the mode")
	assert.Equal(t, conf.SpectrogramModeUserRequested, data["mode"])
}

func TestServeSpectrogramByID_SpectrogramOnly_ExactNameDirectoryFallsThroughToNearestRender(t *testing.T) {
	h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "2026", "01", "a_514px.png"), 0o750))
	writeRetainedFile(t, root, "2026/01/a_1026px.png", "raw 1026")
	h.DS = noClipDS(t, retainedClipName, false)

	rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "raw 1026", rec.Body.String())
}

// skipIfDirPermissionsNotEnforced skips a test that relies on chmod 0 making a
// directory unusable: root ignores directory permissions, and on Windows chmod
// only toggles the read-only attribute.
func skipIfDirPermissionsNotEnforced(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions are not enforced here")
	}
}

func TestServeSpectrogramByID_SpectrogramOnly_UnexpectedStatErrorIsReportedNotA404(t *testing.T) {
	skipIfDirPermissionsNotEnforced(t)

	h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
	writeRetainedFile(t, root, "2026/01/a_514px.png", "exact render")
	dir := filepath.Join(root, "2026", "01")
	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) })
	h.DS = noClipDS(t, retainedClipName, false)

	rec := serveSpectrogram(t, h, e, "?size=md&raw=true")

	assert.NotEqual(t, http.StatusOK, rec.Code)
	assert.NotEqual(t, http.StatusNotFound, rec.Code, "a filesystem failure must not look like a missing render")
	assert.NotContains(t, rec.Body.String(), msgRetainedSpectrogramMissing)
}
