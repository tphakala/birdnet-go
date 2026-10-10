package detections

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

const keptClipName = "2026/01/a.wav"

func TestNoteToDetectionResponse_SpectrogramOnly(t *testing.T) {
	_, _, h := setupTestEnvironment(t)

	tests := []struct {
		name string
		note datastore.Note
		want bool
	}{
		{"audio present", datastore.Note{ClipName: keptClipName}, false},
		{"spectrogram only", datastore.Note{SpectrogramClipName: "2026/01/kept.wav"}, true},
		{"audio present with stale spectrogram name", datastore.Note{ClipName: keptClipName, SpectrogramClipName: "2026/01/kept.wav"}, false},
		{"neither", datastore.Note{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := h.noteToDetectionResponse(&tt.note, false, nil)
			assert.Equal(t, tt.want, resp.SpectrogramOnly)

			body, err := json.Marshal(resp)
			require.NoError(t, err)
			assert.NotContains(t, string(body), "kept.wav", "the kept clip name must never be exposed")
		})
	}
}

// seedKeptRenders writes the renders of a spectrogram-only clip plus another
// clip's extended-capture render and returns the directory.
func seedKeptRenders(t *testing.T, baseDir string) string {
	t.Helper()
	dir := filepath.Join(baseDir, "2026", "01")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	for _, name := range []string{"a_514px.png", "a.png", "a_15s_514px.png"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("png"), 0o600))
	}
	return dir
}

// assertOnlyOtherClipRenderSurvives checks that a's renders are gone and the
// extended-capture sibling (another clip) is kept.
func assertOnlyOtherClipRenderSurvives(t *testing.T, dir string) {
	t.Helper()
	assert.NoFileExists(t, filepath.Join(dir, "a_514px.png"))
	assert.NoFileExists(t, filepath.Join(dir, "a.png"))
	assert.FileExists(t, filepath.Join(dir, "a_15s_514px.png"), "another clip's render must be kept")
}

func TestDeleteDetection_SpectrogramOnlyRemovesKeptRenders(t *testing.T) {
	e, mockDS, controller := setupTestEnvironment(t)
	dir := seedKeptRenders(t, controller.SFS.BaseDir())

	mockDS.On("Get", "10").Return(datastore.Note{ID: 10, SpectrogramClipName: keptClipName}, nil)
	mockDS.On("Delete", "10").Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v2/detections/10", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("10")

	require.NoError(t, controller.DeleteDetection(c))
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assertOnlyOtherClipRenderSurvives(t, dir)
	mockDS.AssertExpectations(t)
}

func TestBatchDeleteDetections_SpectrogramOnlyRemovesKeptRenders(t *testing.T) {
	e, mockDS, controller := setupTestEnvironment(t)
	dir := seedKeptRenders(t, controller.SFS.BaseDir())

	mockDS.On("Get", "10").Return(datastore.Note{ID: 10, SpectrogramClipName: keptClipName}, nil)
	mockDS.On("Delete", "10").Return(nil)

	body, err := json.Marshal(BatchIDsRequest{IDs: []string{"10"}})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/batch/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	require.NoError(t, controller.BatchDeleteDetections(c))
	assert.Equal(t, http.StatusOK, rec.Code)
	assertOnlyOtherClipRenderSurvives(t, dir)
	mockDS.AssertExpectations(t)
}
