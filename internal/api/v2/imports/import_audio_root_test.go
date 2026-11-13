// internal/api/v2/imports/import_audio_root_test.go: tests for the audio root
// a db-audio import reads its clips from.
package importsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

// The synthetic BirdNET-Pi source of the audio root tests: the database lives
// in ~/BirdNET-Pi/scripts and the clip of a real BirdNET-Pi installation in
// ~/BirdSongs/Extracted/By_Date/<date>/<common name>/<file>.
const (
	audioRootTestDate     = "2024-01-01"
	audioRootTestComName  = "Great Spotted Woodpecker"
	audioRootTestFileName = "woodpecker.mp3"
	// audioRootTestExportRel is where the engine exports the clip of the one
	// source row: YYYY/MM/<sci>_<conf>p_<timestamp>.<ext>.
	audioRootTestExportRel = "2024/01/dendrocopos_major_74p_20240101T100000Z.mp3"
)

// audioRootTestClipRel is the clip path relative to the home directory.
var audioRootTestClipRel = filepath.Join("BirdSongs", "Extracted", "By_Date",
	audioRootTestDate, audioRootTestComName, audioRootTestFileName)

// TestDBAudioImport_AudioRoot verifies that a db-audio import finds the clips of
// a BirdNET-Pi installation, where the clips are not next to the database, and
// that the search stays inside the directory each kind of import may read from.
// Every case imports the one source row; only the clip differs.
func TestDBAudioImport_AudioRoot(t *testing.T) {
	clipContent := []byte("fake source audio content")

	tests := []struct {
		name string
		// sourceRootRel is the import source root relative to the home
		// directory, "" for the home directory itself.
		sourceRootRel string
		// native selects a native install: no container, the search is unbounded.
		// A staged import is always native.
		native bool
		// staged runs the import as a sudo-staged one, bounded by the directory
		// of the database, which then sits in a staging directory.
		staged bool
		// noClip leaves the source without any audio tree.
		noClip bool
		// wantClip is true when the clip must be copied into the export tree.
		wantClip bool
	}{
		{
			name:     "container: clips of a BirdNET-Pi installation are copied",
			wantClip: true,
		},
		{
			name:          "container: clips outside the import source root are not read",
			sourceRootRel: "BirdNET-Pi",
		},
		{
			name:          "native: the search is not bounded by the import source root",
			sourceRootRel: "BirdNET-Pi",
			native:        true,
			wantClip:      true,
		},
		{
			// No audio is staged, so the only outcome that can happen is that
			// nothing outside the staging directory is read.
			name:   "staged: clips outside the staging directory are not read",
			staged: true,
		},
		{
			name:   "no audio tree: the detection is imported without a clip",
			noClip: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.VerifyNoLeaks(t)
			if tt.staged && runtime.GOOS == osWindows {
				t.Skip("native import staging is unix-only")
			}

			e, c := newImportHandler(t)
			c.DS = mocks.NewMockInterface(t)
			mockRepo := mocks.NewMockDetectionRepository(t)
			mockRepo.EXPECT().Search(mock.Anything, mock.Anything).Return(nil, int64(0), nil).Maybe()
			var savedClipName string
			mockRepo.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything).
				RunAndReturn(func(_ context.Context, r *detection.Result, _ []detection.AdditionalResult) error {
					savedClipName = r.ClipName
					return nil
				}).Once()
			c.Repo = mockRepo

			home := t.TempDir()
			db := filepath.Join(home, "BirdNET-Pi", "scripts", "birds.db")
			if tt.staged {
				db = filepath.Join(home, "staging", "birds.db")
			}
			makeBirdsDBAt(t, db, audioRootTestDate, audioRootTestComName, audioRootTestFileName)
			if !tt.noClip {
				clipPath := filepath.Join(home, audioRootTestClipRel)
				require.NoError(t, os.MkdirAll(filepath.Dir(clipPath), 0o755))
				require.NoError(t, os.WriteFile(clipPath, clipContent, 0o644))
			}
			c.importSourceRoot = filepath.Join(home, tt.sourceRootRel)
			if tt.native || tt.staged {
				c.isContainerEnv = func() bool { return false }
			}

			exportDir := t.TempDir()
			settings := apitest.NewValidTestSettings()
			settings.Realtime.Audio.Export.Path = exportDir
			c.Settings.Store(settings)

			if tt.staged {
				c.stagingBase = home
				ctx := e.NewContext(httptest.NewRequest(http.MethodPost, "/", http.NoBody), httptest.NewRecorder())
				id, err := c.launchImport(ctx, db, importModeDBaudio, nil, filepath.Dir(db))
				require.NoError(t, err)
				require.NotEmpty(t, id)
			} else {
				body := fmt.Sprintf(`{"mode":"db-audio","source_path":%q}`, db)
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				require.NoError(t, c.StartBirdNETPiImport(e.NewContext(req, rec)))
				require.Equal(t, http.StatusAccepted, rec.Code)
				var started startImportResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &started))
				require.NotEmpty(t, started.JobID)
			}

			finalStatus := waitImportDone(t, c)
			require.Equal(t, importStatusDone, finalStatus.Status, "db-audio import must reach done state")
			assert.Empty(t, finalStatus.Error)
			require.NotNil(t, finalStatus.Progress)
			assert.Equal(t, 1, finalStatus.Progress.Inserted)

			destAbs := filepath.Join(exportDir, filepath.FromSlash(audioRootTestExportRel))
			if !tt.wantClip {
				assert.NoFileExists(t, destAbs)
				assert.Empty(t, savedClipName, "a detection without a copied clip is saved without a clip name")
				return
			}
			assert.Equal(t, audioRootTestExportRel, savedClipName, "the saved detection points to the exported clip")
			copied, err := os.ReadFile(destAbs)
			require.NoError(t, err, "the source clip must be copied to %s", destAbs)
			assert.Equal(t, clipContent, copied)
		})
	}
}
