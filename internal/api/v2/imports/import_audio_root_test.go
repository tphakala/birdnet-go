// internal/api/v2/imports/import_audio_root_test.go: tests for the resolution
// of the BirdNET-Pi audio root that a db-audio import reads its clips from.
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

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

// The synthetic BirdNET-Pi source of the audio root tests: the database lives
// in ~/BirdNET-Pi/scripts and the clips of a real BirdNET-Pi installation live
// in ~/BirdSongs/Extracted/By_Date/<date>/<common name>/<file>.
const (
	audioRootTestDate     = "2024-01-01"
	audioRootTestComName  = "Great Spotted Woodpecker"
	audioRootTestFileName = "woodpecker.mp3"
	audioRootTestSciName  = "Dendrocopos major"
)

// makeBirdsDBAt creates a BirdNET-Pi SQLite database with a single detection
// row at the given path. The schema matches the one the BirdNET-Pi adapter
// validates.
func makeBirdsDBAt(t *testing.T, path, date, comName, fileName string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)

	require.NoError(t, db.Exec(`CREATE TABLE detections (
		Date DATE, Time TIME,
		Sci_Name VARCHAR(100) NOT NULL, Com_Name VARCHAR(100) NOT NULL,
		Confidence FLOAT, Lat FLOAT, Lon FLOAT, Cutoff FLOAT,
		Week INT, Sens FLOAT, Overlap FLOAT,
		File_Name VARCHAR(100) NOT NULL
	)`).Error)

	require.NoError(t, db.Exec(
		`INSERT INTO detections (Date, Time, Sci_Name, Com_Name, Confidence, Lat, Lon, Cutoff, Week, Sens, Overlap, File_Name)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, 0.0, ?)`,
		date, "10:00:00", audioRootTestSciName, comName,
		0.74, 60.0, 25.0, 0.7, 1.25, fileName).Error)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
}

// TestStartBirdNETPiImport_ModeDBAudio_AudioRoot verifies that a db-audio
// import finds the clips of the source wherever the BirdNET-Pi audio tree
// lives, and that a source without an audio tree still imports its
// detections.
//
// The tree of a real BirdNET-Pi installation is not next to the database:
// birds.db lives in ~/BirdNET-Pi/scripts and the clips in ~/BirdSongs. The
// import must find that tree, and it must not be fooled by an "Extracted"
// link that leaves the database directory: the importer reads the clips
// through an os.Root sandbox, so a tree that is only reachable by leaving the
// audio root is not readable and must not be selected.
func TestStartBirdNETPiImport_ModeDBAudio_AudioRoot(t *testing.T) {
	clipContent := []byte("fake source audio content")

	tests := []struct {
		name string
		// mountRel is the mount root relative to the temporary home directory,
		// or "" when the home directory itself is the mount root.
		mountRel string
		// dbRelPath is the database path relative to the mount root.
		dbRelPath string
		// clipRelPath is the clip path relative to the home directory, or ""
		// when the source has no audio tree at all.
		clipRelPath string
		// linkRelPath is a symbolic link relative to the home directory, or ""
		// when the source has no symbolic link.
		linkRelPath string
		// linkTarget is the target of that link, relative to the directory
		// that holds the link.
		linkTarget string
		// wantClip is true when the clip must be copied into the export tree.
		wantClip bool
	}{
		{
			name:        "clips next to the database",
			dbRelPath:   "scripts/birds.db",
			clipRelPath: "scripts/Extracted/By_Date/2024-01-01/Great Spotted Woodpecker/woodpecker.mp3",
			wantClip:    true,
		},
		{
			name:        "clips of a BirdNET-Pi installation",
			dbRelPath:   "BirdNET-Pi/scripts/birds.db",
			clipRelPath: "BirdSongs/Extracted/By_Date/2024-01-01/Great Spotted Woodpecker/woodpecker.mp3",
			wantClip:    true,
		},
		{
			name:        "clips of a BirdNET-Pi installation behind an Extracted link that leaves the database directory",
			dbRelPath:   "BirdNET-Pi/scripts/birds.db",
			clipRelPath: "BirdSongs/Extracted/By_Date/2024-01-01/Great Spotted Woodpecker/woodpecker.mp3",
			linkRelPath: "BirdNET-Pi/scripts/Extracted",
			linkTarget:  "../../BirdSongs/Extracted",
			wantClip:    true,
		},
		{
			name:      "no audio tree",
			dbRelPath: "scripts/birds.db",
			wantClip:  false,
		},
		{
			// The clip tree sits above the mount root. The search must stay
			// inside the mount, so the clip is not copied even though an
			// unbounded search would find it.
			name:        "clips above the mount root are not read",
			mountRel:    "mount",
			dbRelPath:   "birds.db",
			clipRelPath: "BirdSongs/Extracted/By_Date/2024-01-01/Great Spotted Woodpecker/woodpecker.mp3",
			wantClip:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.VerifyNoLeaks(t)

			e, c := newImportHandler(t)
			mockDS := mocks.NewMockInterface(t)
			c.DS = mockDS
			mockRepo := mocks.NewMockDetectionRepository(t)
			mockRepo.EXPECT().Search(mock.Anything, mock.Anything).
				Return(nil, int64(0), nil).Maybe()
			// The one source row is saved exactly once; its ClipName is checked
			// below against the exported clip.
			var savedClipName string
			mockRepo.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything).
				RunAndReturn(func(_ context.Context, r *detection.Result, _ []detection.AdditionalResult) error {
					savedClipName = r.ClipName
					return nil
				}).Once()
			c.Repo = mockRepo

			// The mount root plays the role of the home directory of the
			// BirdNET-Pi installation, unless the case puts it one level down.
			home := t.TempDir()
			mountRoot := filepath.Join(home, filepath.FromSlash(tt.mountRel))
			makeBirdsDBAt(t, filepath.Join(mountRoot, filepath.FromSlash(tt.dbRelPath)),
				audioRootTestDate, audioRootTestComName, audioRootTestFileName)
			if tt.clipRelPath != "" {
				clipPath := filepath.Join(home, filepath.FromSlash(tt.clipRelPath))
				require.NoError(t, os.MkdirAll(filepath.Dir(clipPath), 0o755))
				require.NoError(t, os.WriteFile(clipPath, clipContent, 0o644))
			}
			if tt.linkRelPath != "" {
				linkPath := filepath.Join(home, filepath.FromSlash(tt.linkRelPath))
				if err := os.Symlink(tt.linkTarget, linkPath); err != nil {
					t.Skipf("cannot create symlink: %v", err)
				}
			}
			c.importSourceRoot = mountRoot

			exportDir := t.TempDir()
			settings := apitest.NewValidTestSettings()
			settings.Realtime.Audio.Export.Path = exportDir
			c.Settings.Store(settings)

			body := fmt.Sprintf(`{"mode":"db-audio","source_path":%q}`, tt.dbRelPath)
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			require.NoError(t, c.StartBirdNETPiImport(ctx))
			require.Equal(t, http.StatusAccepted, rec.Code)

			var startBody startImportResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &startBody))
			require.NotEmpty(t, startBody.JobID)

			finalStatus := waitImportDone(t, c)

			require.Equal(t, importStatusDone, finalStatus.Status, "db-audio import must reach done state")
			assert.Empty(t, finalStatus.Error)
			require.NotNil(t, finalStatus.Progress)
			assert.Equal(t, 1, finalStatus.Progress.Inserted)

			// The relative layout of the exported clip mirrors buildClipPath:
			// YYYY/MM/<sci>_<conf>p_<timestamp>.<ext>.
			destRel := "2024/01/dendrocopos_major_74p_20240101T100000Z.mp3"
			destAbs := filepath.Join(exportDir, filepath.FromSlash(destRel))
			if !tt.wantClip {
				assert.NoFileExists(t, destAbs, "no clip is copied when the source has no readable audio tree")
				assert.Empty(t, savedClipName, "a detection without a copied clip is saved without a clip name")
				return
			}
			assert.Equal(t, destRel, savedClipName, "the saved detection points to the exported clip")
			copied, readErr := os.ReadFile(destAbs)
			require.NoError(t, readErr, "db-audio import must copy the source clip to %s", destAbs)
			assert.Equal(t, clipContent, copied, "copied clip content must match the source")
		})
	}
}

// TestAudioSearchBound verifies which directory bounds the audio-root search
// of each kind of import.
func TestAudioSearchBound(t *testing.T) {
	t.Parallel()

	inContainer := func() bool { return true }
	native := func() bool { return false }

	t.Run("staged import is bounded by its staging directory", func(t *testing.T) {
		t.Parallel()
		c := &Handler{isContainerEnv: native, importSourceRoot: "/srv/import"}
		assert.Equal(t, "/staging/abc", c.audioSearchBound("/staging/abc"))
	})
	t.Run("container import is bounded by the import source root", func(t *testing.T) {
		t.Parallel()
		c := &Handler{isContainerEnv: inContainer, importSourceRoot: "/srv/import"}
		assert.Equal(t, "/srv/import", c.audioSearchBound(""))
	})
	t.Run("container import without a root uses the default mount", func(t *testing.T) {
		t.Parallel()
		c := &Handler{isContainerEnv: inContainer}
		assert.Equal(t, sysinfo.DefaultExternalMountPath, c.audioSearchBound(""))
	})
	t.Run("native import is unbounded", func(t *testing.T) {
		t.Parallel()
		c := &Handler{isContainerEnv: native, importSourceRoot: "/srv/import"}
		assert.Empty(t, c.audioSearchBound(""))
	})
}

// TestLaunchImport_StagedAudioSearchStaysInStagingDir verifies the staging
// bound end to end through launchImport: a staged db-audio import reads the
// clip tree that was staged next to the database, and never a tree that sits
// next to the staging directory, outside it.
func TestLaunchImport_StagedAudioSearchStaysInStagingDir(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("native import staging is unix-only")
	}
	clipContent := []byte("staged clip")
	outsideContent := []byte("clip outside the staging dir")
	clipRel := "Extracted/By_Date/2024-01-01/Great Spotted Woodpecker/woodpecker.mp3"

	tests := []struct {
		name string
		// stagedTree places the clip tree inside the staging directory.
		stagedTree bool
		wantClip   []byte
	}{
		{name: "the staged tree is read", stagedTree: true, wantClip: clipContent},
		{name: "a tree next to the staging dir is not read", stagedTree: false, wantClip: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.VerifyNoLeaks(t)

			base := t.TempDir()
			stagingDir := filepath.Join(base, "birdnet-go-import-staging")
			db := filepath.Join(stagingDir, "birds.db")
			makeBirdsDBAt(t, db, audioRootTestDate, audioRootTestComName, audioRootTestFileName)

			// A BirdSongs tree right next to the staging dir: the walk up from
			// the staged database would reach it without the bound.
			outside := filepath.Join(base, "BirdSongs", filepath.FromSlash(clipRel))
			require.NoError(t, os.MkdirAll(filepath.Dir(outside), 0o755))
			require.NoError(t, os.WriteFile(outside, outsideContent, 0o644))
			if tt.stagedTree {
				staged := filepath.Join(stagingDir, filepath.FromSlash(clipRel))
				require.NoError(t, os.MkdirAll(filepath.Dir(staged), 0o755))
				require.NoError(t, os.WriteFile(staged, clipContent, 0o644))
			}

			_, c := newImportHandler(t)
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
			c.isContainerEnv = func() bool { return false }
			c.stagingBase = base

			exportDir := t.TempDir()
			settings := apitest.NewValidTestSettings()
			settings.Realtime.Audio.Export.Path = exportDir
			c.Settings.Store(settings)

			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
			ctx := e.NewContext(req, httptest.NewRecorder())
			id, launchErr := c.launchImport(ctx, db, importModeDBaudio, nil, stagingDir)
			require.NoError(t, launchErr)
			require.NotEmpty(t, id)

			finalStatus := waitImportDone(t, c)
			require.Equal(t, importStatusDone, finalStatus.Status)

			destRel := "2024/01/dendrocopos_major_74p_20240101T100000Z.mp3"
			destAbs := filepath.Join(exportDir, filepath.FromSlash(destRel))
			if tt.wantClip == nil {
				assert.NoFileExists(t, destAbs, "a clip tree outside the staging dir must not be read")
				assert.Empty(t, savedClipName)
				return
			}
			copied, err := os.ReadFile(destAbs)
			require.NoError(t, err)
			assert.Equal(t, tt.wantClip, copied, "the staged clip is the one copied")
			assert.Equal(t, destRel, savedClipName)
		})
	}
}
