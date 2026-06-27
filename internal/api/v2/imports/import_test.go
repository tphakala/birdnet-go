// internal/api/v2/imports/import_test.go: tests for the import API endpoints.
package importsapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/imports"
)

// testDBOnlyBody is the canonical valid db-only import request body used across tests.
const testDBOnlyBody = `{"mode":"db-only","source_path":"birds.db"}`

// fakeSource implements imports.Source for unit tests.
type fakeSource struct {
	mu       sync.Mutex
	batches  [][]imports.SourceDetection
	validate error
	block    chan struct{} // optional: gate before each batch, released by test to let processing proceed
	// entered, when non-nil, is closed by Iterate once the goroutine reaches the
	// first batch and is about to enter the block wait. The test waits for this
	// signal before posting cancel, ensuring cancel is always observed before any
	// batch processing completes (eliminates cancel-vs-complete ordering race).
	entered chan struct{}
	// gate, when non-nil, makes Iterate receive one value before producing each
	// batch. The test sends one token per batch and reads the resulting progress
	// event before sending the next, so progress updates cannot coalesce. This
	// gives the live-streaming test a deterministic multi-event stream.
	gate chan struct{}
}

func (f *fakeSource) Validate(_ context.Context) error {
	return f.validate
}

func (f *fakeSource) Count(_ context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.batches {
		n += len(b)
	}
	return n, nil
}

func (f *fakeSource) Iterate(ctx context.Context, _ int, fn func([]imports.SourceDetection) error) error {
	f.mu.Lock()
	batches := f.batches
	block := f.block
	gate := f.gate
	entered := f.entered
	f.entered = nil // consume once; prevents double-close if Iterate is called again
	f.mu.Unlock()
	for _, b := range batches {
		if gate != nil {
			// Wait for the test to release this batch so the SSE reader can
			// observe each progress event before the next batch is produced.
			select {
			case <-gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		// Signal that this goroutine has entered the batch loop and is about to
		// block. The test waits for this before posting cancel, guaranteeing the
		// context is already cancelled when blockCh is closed so the cancel is
		// always observed (fixes cancel-vs-complete ordering race).
		if entered != nil {
			close(entered)
			entered = nil // prevent double-close on subsequent iterations
		}
		// Block BEFORE fn(b) so that cancel posted after <-entered is received
		// is always observed: either <-ctx.Done() fires in the select, or the
		// explicit ctx.Err() check below catches it before fn(b) can complete.
		if block != nil {
			select {
			case <-block:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(b); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeSource) Close() error { return nil }

// newImportHandler creates a lightweight Handler for import tests.
func newImportHandler(t *testing.T) (*echo.Echo, *Handler) {
	t.Helper()
	e := echo.New()
	ctx, cancel := context.WithCancel(t.Context())
	cCore := &apicore.Core{Group: e.Group(apiV2Prefix), AuthMiddleware: func(next echo.HandlerFunc) echo.HandlerFunc { return next }}
	cCore.SetTestContext(ctx, cancel)
	c := &Handler{Core: cCore, importMgr: newImportManager()}
	c.Settings.Store(apitest.NewValidTestSettings())
	t.Cleanup(func() {
		cancel()
		c.Wait()
	})
	return e, c
}

// makeTempDB creates a temporary BirdNET-Pi SQLite file with the given number of rows.
func makeTempDB(t *testing.T, rows int) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "birds.db")

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

	for i := range rows {
		require.NoError(t, db.Exec(
			`INSERT INTO detections (Date, Time, Sci_Name, Com_Name, Confidence, Lat, Lon, Cutoff, Week, Sens, Overlap, File_Name)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, 0.0, ?)`,
			fmt.Sprintf("2024-01-0%d", (i%9)+1), "10:00:00",
			fmt.Sprintf("Parus major%d", i), fmt.Sprintf("Great Tit%d", i),
			0.9+float64(i)*0.001, 60.0, 25.0, 0.1, 1.0,
			fmt.Sprintf("clip%d.wav", i),
		).Error)
	}

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	return dir, path
}

// importSSEEvent holds parsed fields from a single import SSE event.
type importSSEEvent struct {
	id    string
	event string
	data  string
}

// readImportSSEEvent reads scanner lines until one complete SSE event (terminated
// by a blank line) has been assembled, then returns it. It returns ok=false when
// the stream ends before a complete event is read. Used by tests that need to read
// events incrementally rather than draining the whole stream at once.
func readImportSSEEvent(scanner *bufio.Scanner) (event importSSEEvent, ok bool) {
	var cur importSSEEvent
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			cur.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if cur.event != "" {
				return cur, true
			}
		}
	}
	return importSSEEvent{}, false
}

// parseImportSSEEvents parses SSE-formatted text into a slice of import events.
func parseImportSSEEvents(body string) []importSSEEvent {
	var events []importSSEEvent
	var cur importSSEEvent
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			cur.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if cur.event != "" {
				events = append(events, cur)
				cur = importSSEEvent{}
			}
		}
	}
	return events
}

// TestImportManager_ConcurrencyGuard verifies the single-slot guard.
func TestImportManager_ConcurrencyGuard(t *testing.T) {
	t.Parallel()
	mgr := newImportManager()

	_, cancel := context.WithCancel(t.Context())
	defer cancel()

	job1 := newImportJob("job1", cancel)
	assert.True(t, mgr.start(job1), "first start should succeed")
	assert.False(t, mgr.start(newImportJob("job2", func() {})), "second start while running should fail")

	// After job1 finishes, a new start should succeed.
	job1.finish(imports.ImportStats{Phase: "done"}, nil)
	assert.True(t, mgr.start(newImportJob("job3", func() {})), "start after completion should succeed")
}

// TestImportManager_GetByID verifies ID-based job lookup.
func TestImportManager_GetByID(t *testing.T) {
	t.Parallel()
	mgr := newImportManager()
	job := newImportJob("abc123", func() {})
	require.True(t, mgr.start(job))
	assert.Equal(t, job, mgr.get("abc123"))
	assert.Nil(t, mgr.get("wrong-id"))
}

// TestStartBirdNETPiImport_NoRepo_Returns503 verifies 503 when no datastore.
func TestStartBirdNETPiImport_NoRepo_Returns503(t *testing.T) {
	e := echo.New()
	c := &Handler{Core: &apicore.Core{Group: e.Group(apiV2Prefix)}, importMgr: newImportManager()}
	c.Settings.Store(apitest.NewValidTestSettings())

	body := testDBOnlyBody
	req := httptest.NewRequest(http.MethodPost, "/api/v2/import/birdnet-pi", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	// requireDatastore writes the 503 response and returns the sentinel error,
	// which the handler propagates (matching the established datastore-guard pattern).
	err := c.StartBirdNETPiImport(ctx)
	require.ErrorIs(t, err, apicore.ErrDatastoreUnavailable)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestStartBirdNETPiImport_BadJSON_Returns400 verifies 400 on malformed JSON.
func TestStartBirdNETPiImport_BadJSON_Returns400(t *testing.T) {
	_, c := newImportHandler(t)
	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	c.Repo = mocks.NewMockDetectionRepository(t)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := c.StartBirdNETPiImport(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestStartBirdNETPiImport_ModeDBAudio_Returns202 verifies db-audio mode is accepted.
func TestStartBirdNETPiImport_ModeDBAudio_Returns202(t *testing.T) {
	// Snapshot existing goroutines at test start (see verifyNoLeaks) so a
	// leftover transport-dial goroutine from a previously-run test under
	// -shuffle is ignored rather than wrongly attributed here.
	verifyNoLeaks(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)

	_, c := newImportHandler(t)
	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	mockRepo := mocks.NewMockDetectionRepository(t)
	mockRepo.EXPECT().Search(mock.Anything, mock.Anything).
		Return(nil, int64(0), nil).Maybe()
	mockRepo.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	c.Repo = mockRepo

	det := imports.SourceDetection{
		Date: "2024-01-01", Time: "10:00:00",
		ScientificName: "Parus major", CommonName: "Great Tit",
		Confidence: 0.9,
	}
	c.importSourceFactory = func(_ string) (imports.Source, error) {
		return &fakeSource{batches: [][]imports.SourceDetection{{det}}}, nil
	}

	// db-audio mode now requires a configured export path; provide one so the
	// handler accepts the request instead of returning 400.
	settings := apitest.NewValidTestSettings()
	settings.Realtime.Audio.Export.Path = t.TempDir()
	c.Settings.Store(settings)

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "birds.db"), []byte{}, 0o600)
	c.importSourceRoot = dir

	body := `{"mode":"db-audio","source_path":"birds.db"}`
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := c.StartBirdNETPiImport(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, rec.Code)

	var resp startImportResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.JobID)
	assert.Equal(t, importStatusStarted, resp.Status)
}

// TestStartBirdNETPiImport_ModeDBAudio_NoExportPath_Returns400 verifies that db-audio mode
// is rejected with 400 when the audio export path is not configured. Without this guard the
// import would start, copy no audio (the engine skips audio when ClipExportPath is empty),
// and silently produce detections with no clips, masking a misconfiguration.
func TestStartBirdNETPiImport_ModeDBAudio_NoExportPath_Returns400(t *testing.T) {
	// Snapshot existing goroutines at test start (see verifyNoLeaks) so a
	// leftover transport-dial goroutine from a previously-run test under
	// -shuffle is ignored rather than wrongly attributed here.
	verifyNoLeaks(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)

	_, c := newImportHandler(t)
	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	c.Repo = mocks.NewMockDetectionRepository(t)

	// Settings with an empty export path (apitest.NewValidTestSettings leaves it unset).
	c.Settings.Store(apitest.NewValidTestSettings())

	det := imports.SourceDetection{
		Date: "2024-01-01", Time: "10:00:00",
		ScientificName: "Parus major", CommonName: "Great Tit",
		Confidence: 0.9,
	}
	c.importSourceFactory = func(_ string) (imports.Source, error) {
		return &fakeSource{batches: [][]imports.SourceDetection{{det}}}, nil
	}

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "birds.db"), []byte{}, 0o600)
	c.importSourceRoot = dir

	body := `{"mode":"db-audio","source_path":"birds.db"}`
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := c.StartBirdNETPiImport(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "db-audio without an export path must be rejected")

	// No import slot may have been reserved: a subsequent status check must report idle.
	statusReq := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	statusRec := httptest.NewRecorder()
	statusCtx := echo.New().NewContext(statusReq, statusRec)
	require.NoError(t, c.GetImportStatus(statusCtx))
	var status importStatusResponse
	require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &status))
	assert.False(t, status.Running, "rejected db-audio request must not occupy the import slot")
}

// makeTempDBWithRow creates a temporary BirdNET-Pi SQLite file in dir with a single
// detection row using the supplied date, common name, and clip file name. It returns the
// directory holding the database. Used by the db-audio test so the row's audio path
// components are known and a matching source clip tree can be created.
func makeTempDBWithRow(t *testing.T, date, comName, fileName string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	path := filepath.Join(dir, "birds.db")

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
		date, "10:00:00", "Dendrocopos major", comName,
		0.74, 60.0, 25.0, 0.7, 1.25, fileName,
	).Error)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	return dir
}

// TestStartBirdNETPiImport_ModeDBAudio_CopiesClip verifies that db-audio mode actually
// exercises the audio copy path end to end: it sets Realtime.Audio.Export.Path so the
// engine receives a non-empty ClipExportPath, provides a matching source clip in the
// BirdNET-Pi Extracted/By_Date tree alongside the database, and asserts the clip is
// copied into the export directory. The previous db-audio test left the export path
// empty, so the engine skipped audio entirely (IncludeAudio && ClipExportPath != "" was
// false) and the new path was never covered.
func TestStartBirdNETPiImport_ModeDBAudio_CopiesClip(t *testing.T) {
	// Snapshot existing goroutines at test start (see verifyNoLeaks) so a
	// leftover transport-dial goroutine from a previously-run test under
	// -shuffle is ignored rather than wrongly attributed here.
	verifyNoLeaks(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)

	const (
		date     = "2024-01-01"
		comName  = "Great Spotted Woodpecker"
		fileName = "woodpecker.mp3"
	)
	clipContent := []byte("fake source audio content")

	e, c := newImportHandler(t)
	c.RegisterImportRoutes(c.Group)
	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	mockRepo := mocks.NewMockDetectionRepository(t)
	mockRepo.EXPECT().Search(mock.Anything, mock.Anything).
		Return(nil, int64(0), nil).Maybe()
	mockRepo.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	c.Repo = mockRepo

	// Database directory doubles as the BirdNET-Pi audio source root: the handler sets
	// AudioSourceDir to filepath.Dir(resolvedPath), so the Extracted/By_Date tree must
	// live next to birds.db.
	sourceRoot := makeTempDBWithRow(t, date, comName, fileName)
	c.importSourceRoot = sourceRoot

	clipDir := filepath.Join(sourceRoot, "Extracted", "By_Date", date, comName)
	require.NoError(t, os.MkdirAll(clipDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(clipDir, fileName), clipContent, 0o644))

	// Configure a real export directory so the engine receives a non-empty
	// ClipExportPath and the audio path is exercised.
	exportDir := t.TempDir()
	settings := apitest.NewValidTestSettings()
	settings.Realtime.Audio.Export.Path = exportDir
	c.Settings.Store(settings)

	// Use the default source factory (birdnetpi.New) by leaving importSourceFactory unset
	// so the real adapter reads the database created above.

	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)

	startResp, err := http.Post(srv.URL+"/api/v2/import/birdnet-pi", //nolint:noctx // test uses simplified HTTP calls without context
		"application/json", strings.NewReader(`{"mode":"db-audio","source_path":"birds.db"}`))
	require.NoError(t, err)
	defer func() { _ = startResp.Body.Close() }()
	require.Equal(t, http.StatusAccepted, startResp.StatusCode)

	var startBody startImportResponse
	require.NoError(t, json.NewDecoder(startResp.Body).Decode(&startBody))
	require.NotEmpty(t, startBody.JobID)

	// Poll status until the import reaches a terminal done state.
	deadline := time.Now().Add(10 * time.Second)
	var finalStatus importStatusResponse
	e2 := echo.New()
	for time.Now().Before(deadline) {
		statusReq := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		statusRec := httptest.NewRecorder()
		statusCtx := e2.NewContext(statusReq, statusRec)
		require.NoError(t, c.GetImportStatus(statusCtx))
		require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &finalStatus))
		if !finalStatus.Running && finalStatus.Status == importStatusDone {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	require.Equal(t, importStatusDone, finalStatus.Status, "db-audio import must reach done state")
	assert.Empty(t, finalStatus.Error)
	require.NotNil(t, finalStatus.Progress)
	assert.Equal(t, 1, finalStatus.Progress.Inserted)

	// The source clip must have been copied into the export tree. The relative layout
	// mirrors buildClipPath: YYYY/MM/<sci>_<conf>p_<timestamp>.<ext>. The engine parses
	// the wall-clock "10:00:00" and formats it directly (no UTC conversion), so the
	// timestamp segment is 20240101T100000Z regardless of the host timezone.
	destAbs := filepath.Join(exportDir, "2024", "01", "dendrocopos_major_74p_20240101T100000Z.mp3")
	copied, readErr := os.ReadFile(destAbs)
	require.NoError(t, readErr, "db-audio import must copy the source clip to %s", destAbs)
	assert.Equal(t, clipContent, copied, "copied clip content must match the source")
}

// TestStartBirdNETPiImport_UnknownMode_Returns400 verifies unknown modes are rejected.
func TestStartBirdNETPiImport_UnknownMode_Returns400(t *testing.T) {
	_, c := newImportHandler(t)
	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	c.Repo = mocks.NewMockDetectionRepository(t)
	c.importSourceRoot = t.TempDir()

	for _, mode := range []string{"", "csv", "xml"} {
		body := fmt.Sprintf(`{"mode":%q,"source_path":"birds.db"}`, mode)
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		require.NoError(t, c.StartBirdNETPiImport(ctx))
		assert.Equal(t, http.StatusBadRequest, rec.Code, "mode %q should be rejected", mode)
	}
}

// TestStartBirdNETPiImport_TraversalPath_Returns400 verifies path traversal is blocked.
func TestStartBirdNETPiImport_TraversalPath_Returns400(t *testing.T) {
	_, c := newImportHandler(t)
	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	c.Repo = mocks.NewMockDetectionRepository(t)
	c.importSourceRoot = t.TempDir()
	c.importSourceFactory = func(_ string) (imports.Source, error) { return &fakeSource{}, nil }

	// filepath.Join(t.TempDir(), ...) is drive-absolute on Windows and outside the
	// import root on all platforms, so it covers the filepath.IsAbs rejection path
	// on every OS (unlike "/etc/passwd" which is not absolute on Windows).
	outsidePath := filepath.Join(t.TempDir(), "outside.db")
	for _, badPath := range []string{"../etc/passwd", "../../secret", "/etc/passwd", outsidePath} {
		body := fmt.Sprintf(`{"mode":"db-only","source_path":%q}`, badPath)
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		require.NoError(t, c.StartBirdNETPiImport(ctx))
		assert.Equal(t, http.StatusBadRequest, rec.Code, "path %q should be rejected", badPath)
	}
}

// TestResolveImportSourcePath_SymlinkEscape verifies that a symlinked parent
// directory pointing outside the root is rejected, including for files that do
// not yet exist (the TOCTOU-resistant containment check).
func TestResolveImportSourcePath_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	// t.TempDir may itself sit under a symlink (macOS /var -> /private/var), so the
	// resolver legitimately returns the symlink-resolved root. Compare against that.
	resolvedRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)

	// A legitimate non-existent file directly under root resolves without error
	// (the handler's os.Stat reports it missing). Platform-independent, so check it
	// before the symlink-dependent cases below.
	resolved, err := resolveImportSourcePath(root, "legit.db")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(resolvedRoot, "legit.db"), resolved)

	// The escape cases need a symlink inside root pointing outside it. Creating a
	// symlink needs a privilege the Windows CI runner lacks, so skip them when the
	// symlink cannot be created rather than failing the whole test.
	escapeLink := filepath.Join(root, "escape")
	if symErr := os.Symlink(outside, escapeLink); symErr != nil {
		t.Skipf("skipping symlink-escape cases: cannot create symlink: %v", symErr)
	}

	// A non-existent file under the symlinked parent must be rejected because
	// its physical parent resolves outside root.
	_, err = resolveImportSourcePath(root, filepath.Join("escape", "missing.db"))
	require.ErrorIs(t, err, errInvalidSourcePath)

	// An existing file reached through the escaping symlink must also be rejected.
	existing := filepath.Join(outside, "real.db")
	require.NoError(t, os.WriteFile(existing, []byte("x"), 0o600))
	_, err = resolveImportSourcePath(root, filepath.Join("escape", "real.db"))
	require.ErrorIs(t, err, errInvalidSourcePath)

	// Multi-level: a path under a symlinked ancestor where multiple intermediate
	// directories do not exist yet must also be rejected.
	_, err = resolveImportSourcePath(root, filepath.Join("escape", "missingA", "missingB", "x.db"))
	require.ErrorIs(t, err, errInvalidSourcePath)
}

// TestStartBirdNETPiImport_MissingFile_Returns400 verifies missing file returns 400.
func TestStartBirdNETPiImport_MissingFile_Returns400(t *testing.T) {
	_, c := newImportHandler(t)
	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	c.Repo = mocks.NewMockDetectionRepository(t)
	c.importSourceRoot = t.TempDir()
	c.importSourceFactory = func(_ string) (imports.Source, error) { return &fakeSource{}, nil }

	body := `{"mode":"db-only","source_path":"nonexistent.db"}`
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, c.StartBirdNETPiImport(ctx))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestStartBirdNETPiImport_FakeValidationFailure_Returns400 verifies validation errors return 400.
func TestStartBirdNETPiImport_FakeValidationFailure_Returns400(t *testing.T) {
	_, c := newImportHandler(t)
	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	c.Repo = mocks.NewMockDetectionRepository(t)

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "birds.db"), []byte{}, 0o600)
	c.importSourceRoot = dir
	c.importSourceFactory = func(_ string) (imports.Source, error) {
		return &fakeSource{validate: fmt.Errorf("bad schema")}, nil
	}

	body := testDBOnlyBody
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, c.StartBirdNETPiImport(ctx))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "source validation failed")
}

// TestStartBirdNETPiImport_GoodFakeSource_Returns202 verifies 202 on a valid request.
func TestStartBirdNETPiImport_GoodFakeSource_Returns202(t *testing.T) {
	// Snapshot existing goroutines at test start (see verifyNoLeaks) so a
	// leftover transport-dial goroutine from a previously-run test under
	// -shuffle is ignored rather than wrongly attributed here.
	verifyNoLeaks(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)

	_, c := newImportHandler(t)
	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	mockRepo := mocks.NewMockDetectionRepository(t)
	mockRepo.EXPECT().Search(mock.Anything, mock.Anything).
		Return(nil, int64(0), nil).Maybe()
	mockRepo.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	c.Repo = mockRepo

	det := imports.SourceDetection{
		Date: "2024-01-01", Time: "10:00:00",
		ScientificName: "Parus major", CommonName: "Great Tit",
		Confidence: 0.9,
	}
	c.importSourceFactory = func(_ string) (imports.Source, error) {
		return &fakeSource{batches: [][]imports.SourceDetection{{det}}}, nil
	}

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "birds.db"), []byte{}, 0o600)
	c.importSourceRoot = dir

	body := testDBOnlyBody
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, c.StartBirdNETPiImport(ctx))
	assert.Equal(t, http.StatusAccepted, rec.Code)

	var resp startImportResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.JobID)
	assert.Equal(t, importStatusStarted, resp.Status)
}

// TestStartBirdNETPiImport_ConflictWhileRunning_Returns409 verifies 409 on concurrent starts.
func TestStartBirdNETPiImport_ConflictWhileRunning_Returns409(t *testing.T) {
	// Snapshot existing goroutines at test start (see verifyNoLeaks) so a
	// leftover transport-dial goroutine from a previously-run test under
	// -shuffle is ignored rather than wrongly attributed here.
	verifyNoLeaks(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)
	_, c := newImportHandler(t)
	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	mockRepo := mocks.NewMockDetectionRepository(t)
	mockRepo.EXPECT().Search(mock.Anything, mock.Anything).
		Return(nil, int64(0), nil).Maybe()
	mockRepo.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	c.Repo = mockRepo

	// Use a blocking source so the import stays running.
	blockCh := make(chan struct{})
	det := imports.SourceDetection{
		Date: "2024-01-01", Time: "10:00:00",
		ScientificName: "Turdus merula", CommonName: "Blackbird",
		Confidence: 0.8,
	}
	c.importSourceFactory = func(_ string) (imports.Source, error) {
		return &fakeSource{
			batches: [][]imports.SourceDetection{{det}},
			block:   blockCh,
		}, nil
	}

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "birds.db"), []byte{}, 0o600)
	c.importSourceRoot = dir

	body := testDBOnlyBody

	// First start.
	e := echo.New()
	req1 := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	ctx1 := e.NewContext(req1, rec1)
	require.NoError(t, c.StartBirdNETPiImport(ctx1))
	assert.Equal(t, http.StatusAccepted, rec1.Code)

	// Second start while first is still blocked.
	req2 := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	ctx2 := e.NewContext(req2, rec2)
	require.NoError(t, c.StartBirdNETPiImport(ctx2))
	assert.Equal(t, http.StatusConflict, rec2.Code)

	// Unblock and let goroutines exit.
	close(blockCh)
}

// TestStreamImportProgress_JobNotFound_Returns404 verifies 404 for unknown job.
func TestStreamImportProgress_JobNotFound_Returns404(t *testing.T) {
	_, c := newImportHandler(t)
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/import/jobs/notexist/progress", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("jobId")
	ctx.SetParamValues("notexist")

	require.NoError(t, c.StreamImportProgress(ctx))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestStreamImportProgress_DoneJob_EmitsCompleteEvent verifies complete event is emitted.
func TestStreamImportProgress_DoneJob_EmitsCompleteEvent(t *testing.T) {
	_, c := newImportHandler(t)

	jobCtx, jobCancel := context.WithCancel(t.Context())
	jobCancel()
	job := newImportJob("testjob", jobCancel)
	require.True(t, c.importMgr.start(job))
	job.Report(imports.ImportStats{Total: 5, Processed: 5, Inserted: 5, Phase: "import"})
	job.finish(imports.ImportStats{Total: 5, Processed: 5, Inserted: 5, Phase: "done"}, nil)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(jobCtx)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("jobId")
	ctx.SetParamValues("testjob")

	require.NoError(t, c.StreamImportProgress(ctx))

	body := rec.Body.String()
	events := parseImportSSEEvents(body)
	require.NotEmpty(t, events, "expected at least one SSE event")

	var found bool
	for _, ev := range events {
		if ev.event == importEventComplete {
			found = true
			var prog importProgress
			require.NoError(t, json.Unmarshal([]byte(ev.data), &prog))
			assert.Equal(t, "done", prog.Phase)
		}
	}
	assert.True(t, found, "expected a complete event")
}

// TestStreamImportProgress_EventIDsMonotonic verifies SSE event IDs are monotonically increasing.
// This test covers the connect-after-done path (terminal-only event), complementing
// TestStreamImportProgress_LiveStreaming which covers the live multi-event path.
func TestStreamImportProgress_EventIDsMonotonic(t *testing.T) {
	_, c := newImportHandler(t)

	_, jobCancel := context.WithCancel(t.Context())
	defer jobCancel()
	job := newImportJob("monotone", jobCancel)
	require.True(t, c.importMgr.start(job))

	for i := range 3 {
		job.Report(imports.ImportStats{Total: 10, Processed: i + 1, Phase: "import"})
	}
	job.finish(imports.ImportStats{Total: 10, Processed: 10, Inserted: 10, Phase: "done"}, nil)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(t.Context())
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("jobId")
	ctx.SetParamValues("monotone")

	require.NoError(t, c.StreamImportProgress(ctx))

	body := rec.Body.String()
	events := parseImportSSEEvents(body)
	require.NotEmpty(t, events)

	var prevID int64 = -1
	for _, ev := range events {
		if ev.event == importEventHeartbeat {
			continue
		}
		require.NotEmpty(t, ev.id, "event %q must have an id", ev.event)
		id, err := strconv.ParseInt(ev.id, 10, 64)
		require.NoError(t, err, "id must be numeric, got %q", ev.id)
		assert.Greater(t, id, prevID, "ids must be strictly increasing")
		prevID = id
	}

	for _, ev := range events {
		if ev.event != importEventProgress && ev.event != importEventComplete {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(ev.data), &m))
		for key := range m {
			assert.Equal(t, strings.ToLower(key), key, "JSON key %q should be lowercase/snake_case", key)
		}
	}
}

// TestCancelImport_JobNotFound_Returns404 verifies 404 for unknown job.
func TestCancelImport_JobNotFound_Returns404(t *testing.T) {
	_, c := newImportHandler(t)
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("jobId")
	ctx.SetParamValues("notexist")

	require.NoError(t, c.CancelImport(ctx))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestCancelImport_RunningJob_Returns200Cancelling verifies cancel returns 200 with cancelling status.
func TestCancelImport_RunningJob_Returns200Cancelling(t *testing.T) {
	// Snapshot existing goroutines at test start (see verifyNoLeaks) so a
	// leftover transport-dial goroutine from a previously-run test under
	// -shuffle is ignored rather than wrongly attributed here.
	verifyNoLeaks(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)
	_, c := newImportHandler(t)
	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	mockRepo := mocks.NewMockDetectionRepository(t)
	mockRepo.EXPECT().Search(mock.Anything, mock.Anything).
		Return(nil, int64(0), nil).Maybe()
	mockRepo.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	c.Repo = mockRepo

	blockCh := make(chan struct{})
	det := imports.SourceDetection{
		Date: "2024-01-01", Time: "10:00:00",
		ScientificName: "Corvus cornix", CommonName: "Hooded Crow",
		Confidence: 0.85,
	}
	c.importSourceFactory = func(_ string) (imports.Source, error) {
		return &fakeSource{
			batches: [][]imports.SourceDetection{{det}},
			block:   blockCh,
		}, nil
	}

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "birds.db"), []byte{}, 0o600)
	c.importSourceRoot = dir

	body := testDBOnlyBody
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	startCtx := e.NewContext(req, rec)
	require.NoError(t, c.StartBirdNETPiImport(startCtx))
	assert.Equal(t, http.StatusAccepted, rec.Code)

	var startResp startImportResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &startResp))
	jobID := startResp.JobID

	req2 := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	rec2 := httptest.NewRecorder()
	cancelCtx := e.NewContext(req2, rec2)
	cancelCtx.SetParamNames("jobId")
	cancelCtx.SetParamValues(jobID)
	require.NoError(t, c.CancelImport(cancelCtx))
	assert.Equal(t, http.StatusOK, rec2.Code)

	var cancelResp cancelImportResponse
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &cancelResp))
	assert.Contains(t, []string{importStatusCancelling, importStatusDone}, cancelResp.Status)

	close(blockCh)
}

// TestGetImportStatus_NoJob_ReturnsIdle verifies idle status when no job exists.
func TestGetImportStatus_NoJob_ReturnsIdle(t *testing.T) {
	_, c := newImportHandler(t)
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, c.GetImportStatus(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp importStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.Running)
	assert.Equal(t, importStatusIdle, resp.Status)
}

// TestGetImportStatus_RunningJob verifies running status with progress.
func TestGetImportStatus_RunningJob(t *testing.T) {
	_, c := newImportHandler(t)

	_, jobCancel := context.WithCancel(t.Context())
	defer jobCancel()
	job := newImportJob("running1", jobCancel)
	require.True(t, c.importMgr.start(job))
	job.Report(imports.ImportStats{Total: 100, Processed: 50, Phase: "import"})

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, c.GetImportStatus(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp importStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Running)
	assert.Equal(t, importStatusRunning, resp.Status)
	assert.Equal(t, "running1", resp.JobID)
	require.NotNil(t, resp.Progress)
	assert.Equal(t, 100, resp.Progress.Total)
	assert.Equal(t, 50, resp.Progress.Processed)
}

// TestGetImportStatus_DoneJob verifies done status after completion.
func TestGetImportStatus_DoneJob(t *testing.T) {
	_, c := newImportHandler(t)

	_, jobCancel := context.WithCancel(t.Context())
	jobCancel()
	job := newImportJob("done1", jobCancel)
	require.True(t, c.importMgr.start(job))
	job.finish(imports.ImportStats{Total: 10, Processed: 10, Inserted: 8, Skipped: 2, Phase: "done"}, nil)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, c.GetImportStatus(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp importStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.Running)
	assert.Equal(t, importStatusDone, resp.Status)
	assert.Empty(t, resp.Error)
}

// TestImportRoutes_Registered verifies all import routes are registered.
func TestImportRoutes_Registered(t *testing.T) {
	e, c := newImportHandler(t)
	c.RegisterImportRoutes(c.Group)

	expected := []string{
		"POST " + apiV2Prefix + "/import/birdnet-pi",
		"GET " + apiV2Prefix + "/import/jobs/:jobId/progress",
		"POST " + apiV2Prefix + "/import/jobs/:jobId/cancel",
		"GET " + apiV2Prefix + "/import/status",
	}
	apitest.AssertRoutesRegistered(t, e, expected)
}

// TestImportRoutes_FailClosedWhenNoAuth verifies the import group fails closed:
// with no auth middleware configured it denies access with 401 rather than
// registering the state-changing endpoints unprotected.
func TestImportRoutes_FailClosedWhenNoAuth(t *testing.T) {
	e, c := newImportHandler(t)
	c.AuthMiddleware = nil // exercise the fail-closed path: no auth middleware configured
	c.RegisterImportRoutes(c.Group)

	req := httptest.NewRequest(http.MethodGet, apiV2Prefix+"/import/status", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestStartBirdNETPiImport_RealSQLiteSource_EndToEnd verifies the full pipeline with a real SQLite file.
func TestStartBirdNETPiImport_RealSQLiteSource_EndToEnd(t *testing.T) {
	// Snapshot existing goroutines at test start (see verifyNoLeaks) so a
	// leftover transport-dial goroutine from a previously-run test under
	// -shuffle is ignored rather than wrongly attributed here.
	verifyNoLeaks(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)

	const rowCount = 3

	dir, _ := makeTempDB(t, rowCount)

	_, c := newImportHandler(t)
	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	mockRepo := mocks.NewMockDetectionRepository(t)
	mockRepo.EXPECT().Search(mock.Anything, mock.Anything).
		Return(nil, int64(0), nil).Maybe()
	mockRepo.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	c.Repo = mockRepo
	c.importSourceRoot = dir

	body := testDBOnlyBody
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	startCtx := e.NewContext(req, rec)

	require.NoError(t, c.StartBirdNETPiImport(startCtx))
	assert.Equal(t, http.StatusAccepted, rec.Code)

	var startResp startImportResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &startResp))
	jobID := startResp.JobID
	assert.NotEmpty(t, jobID)

	// Poll status until done.
	deadline := time.Now().Add(10 * time.Second)
	var finalStatus importStatusResponse
	for time.Now().Before(deadline) {
		statusReq := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		statusRec := httptest.NewRecorder()
		statusCtx := e.NewContext(statusReq, statusRec)
		require.NoError(t, c.GetImportStatus(statusCtx))
		require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &finalStatus))
		if !finalStatus.Running && finalStatus.Status == importStatusDone {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	assert.Equal(t, importStatusDone, finalStatus.Status, "import should reach done state")
	assert.Empty(t, finalStatus.Error)
	require.NotNil(t, finalStatus.Progress)
	assert.Equal(t, rowCount, finalStatus.Progress.Total)
	assert.Equal(t, rowCount, finalStatus.Progress.Inserted)
	assert.Equal(t, 0, finalStatus.Progress.Errors)
	assert.Equal(t, 0, finalStatus.Progress.Skipped)
}

// panicIterateSource implements imports.Source. It processes the first panicAfter
// batches normally (allowing the engine to report progress), then panics on the
// next batch, simulating a mid-run engine crash.
type panicIterateSource struct {
	mu         sync.Mutex
	batches    [][]imports.SourceDetection
	panicAfter int
}

func (p *panicIterateSource) Validate(_ context.Context) error { return nil }
func (p *panicIterateSource) Close() error                     { return nil }
func (p *panicIterateSource) Count(_ context.Context) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, b := range p.batches {
		n += len(b)
	}
	return n, nil
}

func (p *panicIterateSource) Iterate(_ context.Context, _ int, fn func([]imports.SourceDetection) error) error {
	p.mu.Lock()
	batches := p.batches
	p.mu.Unlock()
	for i, b := range batches {
		if i >= p.panicAfter {
			panic("simulated import panic after progress")
		}
		if err := fn(b); err != nil {
			return err
		}
	}
	return nil
}

// TestStreamImportProgress_LiveStreaming verifies that a connected SSE client receives
// multiple strictly-increasing progress events followed by a terminal complete event.
func TestStreamImportProgress_LiveStreaming(t *testing.T) {
	// Snapshot existing goroutines at test start (see verifyNoLeaks) so a
	// leftover transport-dial goroutine from a previously-run test under
	// -shuffle is ignored rather than wrongly attributed here.
	verifyNoLeaks(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)

	e, c := newImportHandler(t)
	c.RegisterImportRoutes(c.Group)

	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	mockRepo := mocks.NewMockDetectionRepository(t)
	mockRepo.EXPECT().Search(mock.Anything, mock.Anything).
		Return(nil, int64(0), nil).Maybe()
	mockRepo.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	c.Repo = mockRepo

	// gate releases one batch at a time. The test sends one token per batch and
	// reads the resulting progress event before releasing the next, so progress
	// updates cannot coalesce. This makes the multi-event assertion deterministic.
	const batchCount = 3
	gate := make(chan struct{})
	det := imports.SourceDetection{
		Date: "2024-06-01", Time: "08:00:00",
		ScientificName: "Parus major", CommonName: "Great Tit",
		Confidence: 0.9,
	}
	c.importSourceFactory = func(_ string) (imports.Source, error) {
		return &fakeSource{
			batches: [][]imports.SourceDetection{
				{det},
				{det},
				{det},
			},
			gate: gate,
		}, nil
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "birds.db"), []byte{}, 0o600))
	c.importSourceRoot = dir

	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)

	// Start the import.
	startResp, err := http.Post(srv.URL+"/api/v2/import/birdnet-pi", //nolint:noctx // test uses simplified HTTP calls without context
		"application/json", strings.NewReader(testDBOnlyBody))
	require.NoError(t, err)
	defer func() { _ = startResp.Body.Close() }()
	require.Equal(t, http.StatusAccepted, startResp.StatusCode)

	var startBody startImportResponse
	require.NoError(t, json.NewDecoder(startResp.Body).Decode(&startBody))
	jobID := startBody.JobID
	require.NotEmpty(t, jobID)

	// Connect SSE stream before releasing any batch so we catch every event.
	streamResp, err := http.Get(srv.URL + "/api/v2/import/jobs/" + jobID + "/progress") //nolint:noctx // test uses simplified HTTP calls without context
	require.NoError(t, err)
	defer func() { _ = streamResp.Body.Close() }()
	require.Equal(t, http.StatusOK, streamResp.StatusCode)

	scanner := bufio.NewScanner(streamResp.Body)
	var events []importSSEEvent

	// Release every batch except the last, reading its progress event before
	// releasing the next. Because the source parks on the next gate token after
	// reporting, each of these updates is observed individually (no coalesce), so
	// the stream is guaranteed to carry batchCount-1 distinct progress events.
	// The final batch is released afterwards; its progress may legitimately
	// coalesce into the terminal event (finish supersedes a pending progress),
	// so we do not require a separate progress event for it.
	for range batchCount - 1 {
		gate <- struct{}{}
		for {
			ev, ok := readImportSSEEvent(scanner)
			require.True(t, ok, "stream closed before a progress event arrived")
			events = append(events, ev)
			if ev.event == importEventProgress {
				break
			}
		}
	}

	// Release the final batch and drain the remaining events through the terminal.
	gate <- struct{}{}
	for {
		ev, ok := readImportSSEEvent(scanner)
		if !ok {
			break
		}
		events = append(events, ev)
	}
	require.NoError(t, scanner.Err(), "SSE stream must not error during read")

	require.NotEmpty(t, events, "should receive at least one SSE event")

	// Validate: strictly increasing ids, snake_case keys, no phase=done in progress,
	// stream ends with complete.
	var prevID int64 = -1
	var progressCount int
	var lastEvent string
	for _, ev := range events {
		if ev.event == importEventHeartbeat {
			continue
		}
		require.NotEmpty(t, ev.id, "event %q must have an id", ev.event)
		id, parseErr := strconv.ParseInt(ev.id, 10, 64)
		require.NoError(t, parseErr, "id must be numeric, got %q", ev.id)
		assert.Greater(t, id, prevID, "ids must be strictly increasing")
		prevID = id
		lastEvent = ev.event

		if ev.event == importEventProgress {
			progressCount++
			var m map[string]any
			require.NoError(t, json.Unmarshal([]byte(ev.data), &m))
			for key := range m {
				assert.Equal(t, strings.ToLower(key), key, "JSON key %q should be snake_case", key)
			}
			phase, _ := m["phase"].(string)
			assert.NotEqual(t, importEnginePhaseDone, phase, "progress event must not carry phase=done")
		}
	}

	assert.GreaterOrEqual(t, progressCount, 2, "should have received at least two progress events (3-batch source)")
	assert.Equal(t, importEventComplete, lastEvent, "stream must end with complete event")
}

// TestStreamImportProgress_CancelEmitsCancelledEvent verifies that cancelling a
// running import results in a terminal cancelled SSE event on any connected stream.
func TestStreamImportProgress_CancelEmitsCancelledEvent(t *testing.T) {
	// Snapshot existing goroutines at test start (see verifyNoLeaks) so a
	// leftover transport-dial goroutine from a previously-run test under
	// -shuffle is ignored rather than wrongly attributed here.
	verifyNoLeaks(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)

	e, c := newImportHandler(t)
	c.RegisterImportRoutes(c.Group)

	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	mockRepo := mocks.NewMockDetectionRepository(t)
	mockRepo.EXPECT().Search(mock.Anything, mock.Anything).
		Return(nil, int64(0), nil).Maybe()
	mockRepo.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	c.Repo = mockRepo

	blockCh := make(chan struct{})
	// entered is closed by the import goroutine when it reaches the first batch
	// and is about to enter the block wait. Waiting for it before posting cancel
	// ensures the context is cancelled before blockCh is released, making the
	// cancel-vs-complete race deterministic: ctx.Err() is always non-nil when
	// the goroutine unblocks, so Iterate always returns context.Canceled.
	entered := make(chan struct{})
	det := imports.SourceDetection{
		Date: "2024-06-01", Time: "09:00:00",
		ScientificName: "Erithacus rubecula", CommonName: "Robin",
		Confidence: 0.88,
	}
	c.importSourceFactory = func(_ string) (imports.Source, error) {
		return &fakeSource{
			batches: [][]imports.SourceDetection{{det}},
			block:   blockCh,
			entered: entered,
		}, nil
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "birds.db"), []byte{}, 0o600))
	c.importSourceRoot = dir

	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)

	// Start the import.
	startResp, err := http.Post(srv.URL+"/api/v2/import/birdnet-pi", //nolint:noctx // test uses simplified HTTP calls without context
		"application/json", strings.NewReader(testDBOnlyBody))
	require.NoError(t, err)
	defer func() { _ = startResp.Body.Close() }()
	require.Equal(t, http.StatusAccepted, startResp.StatusCode)

	var startBody startImportResponse
	require.NoError(t, json.NewDecoder(startResp.Body).Decode(&startBody))
	jobID := startBody.JobID

	// Connect SSE stream.
	streamResp, err := http.Get(srv.URL + "/api/v2/import/jobs/" + jobID + "/progress") //nolint:noctx // test uses simplified HTTP calls without context
	require.NoError(t, err)
	defer func() { _ = streamResp.Body.Close() }()

	// Wait for the import goroutine to enter batch processing (and block).
	// Only after this handshake is it safe to post cancel: the goroutine is
	// parked in the block select, so cancel is guaranteed to precede any batch
	// completion.
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for import goroutine to enter batch processing")
	}

	// Cancel the import now that the goroutine is confirmed to be blocked.
	cancelResp, err := http.Post(srv.URL+"/api/v2/import/jobs/"+jobID+"/cancel", //nolint:noctx // test uses simplified HTTP calls without context
		"application/json", http.NoBody)
	require.NoError(t, err)
	defer func() { _ = cancelResp.Body.Close() }()
	assert.Equal(t, http.StatusOK, cancelResp.StatusCode)

	// Release the block. The context is already cancelled, so the goroutine
	// exits via context.Canceled regardless of which select case fires.
	close(blockCh)

	// Drain the SSE stream and find the terminal event.
	scanner := bufio.NewScanner(streamResp.Body)
	var events []importSSEEvent
	var cur importSSEEvent
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			cur.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if cur.event != "" {
				events = append(events, cur)
				cur = importSSEEvent{}
			}
		}
	}
	require.NoError(t, scanner.Err(), "SSE stream must not error during read")

	require.NotEmpty(t, events, "should receive at least one SSE event")
	lastEvent := events[len(events)-1]
	assert.Equal(t, importEventCancelled, lastEvent.event, "terminal event must be cancelled")
}

// TestStartBirdNETPiImport_PanicInEngine_RecoverAndPreserveStats verifies that a
// panic in the import engine is recovered, the job reaches a terminal error state,
// and the last-reported progress is preserved (not zeroed).
func TestStartBirdNETPiImport_PanicInEngine_RecoverAndPreserveStats(t *testing.T) {
	// Snapshot existing goroutines at test start (see verifyNoLeaks) so a
	// leftover transport-dial goroutine from a previously-run test under
	// -shuffle is ignored rather than wrongly attributed here.
	verifyNoLeaks(t,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)

	e, c := newImportHandler(t)
	c.RegisterImportRoutes(c.Group)

	mockDS := mocks.NewMockInterface(t)
	c.DS = mockDS
	mockRepo := mocks.NewMockDetectionRepository(t)
	mockRepo.EXPECT().Search(mock.Anything, mock.Anything).
		Return(nil, int64(0), nil).Maybe()
	mockRepo.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	c.Repo = mockRepo

	det := imports.SourceDetection{
		Date: "2024-06-01", Time: "07:00:00",
		ScientificName: "Cyanistes caeruleus", CommonName: "Blue Tit",
		Confidence: 0.75,
	}
	// panicIterateSource: processes first batch (engine reports progress), then panics.
	c.importSourceFactory = func(_ string) (imports.Source, error) {
		return &panicIterateSource{
			batches:    [][]imports.SourceDetection{{det}, {det}},
			panicAfter: 1,
		}, nil
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "birds.db"), []byte{}, 0o600))
	c.importSourceRoot = dir

	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)

	// Start the import.
	startResp, err := http.Post(srv.URL+"/api/v2/import/birdnet-pi", //nolint:noctx // test uses simplified HTTP calls without context
		"application/json", strings.NewReader(testDBOnlyBody))
	require.NoError(t, err)
	defer func() { _ = startResp.Body.Close() }()
	require.Equal(t, http.StatusAccepted, startResp.StatusCode)

	var startBody startImportResponse
	require.NoError(t, json.NewDecoder(startResp.Body).Decode(&startBody))
	require.NotEmpty(t, startBody.JobID)

	// Poll status until done.
	deadline := time.Now().Add(10 * time.Second)
	var finalStatus importStatusResponse
	e2 := echo.New()
	for time.Now().Before(deadline) {
		statusReq := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		statusRec := httptest.NewRecorder()
		statusCtx := e2.NewContext(statusReq, statusRec)
		require.NoError(t, c.GetImportStatus(statusCtx))
		require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &finalStatus))
		if !finalStatus.Running && finalStatus.Status == importStatusDone {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	assert.Equal(t, importStatusDone, finalStatus.Status, "job must reach done state after panic")
	assert.NotEmpty(t, finalStatus.Error, "error must be set after panic")
	require.NotNil(t, finalStatus.Progress, "progress must be preserved after panic")
	// The engine reported stats for the first batch before panicking, so Total
	// must be non-zero rather than the all-zeros default (regression guard for Fix A).
	assert.Positive(t, finalStatus.Progress.Total, "Total must be non-zero (panic stats preserved)")

	// The slot must be freed: a subsequent start must succeed.
	startResp2, err := http.Post(srv.URL+"/api/v2/import/birdnet-pi", //nolint:noctx // test uses simplified HTTP calls without context
		"application/json", strings.NewReader(testDBOnlyBody))
	require.NoError(t, err)
	defer func() { _ = startResp2.Body.Close() }()
	assert.Equal(t, http.StatusAccepted, startResp2.StatusCode, "slot must be freed after panic recovery")
}
