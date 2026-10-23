package detections

import (
	"context"
	"encoding/binary"
	"encoding/json"
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
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	rateTestBatRate  = 384000
	rateTestNoteID   = uint(31)
	rateTestClipName = "2026/01/bat.flac"
	rateTestModelBat = "bat"
	rateTestConcur   = 16
	rateTestMargin   = 50 * time.Millisecond
	// rateTestWait is the response wait tests use unless they exercise the budget,
	// so a slow machine cannot make a response miss the probe.
	rateTestWait = time.Minute
)

// rateStore is a datastore double for the lazy fill: it keeps one detection row
// and applies the same guard as the real FillClipSampleRate SQL (only from NULL,
// only while clip_name matches). The SQL itself is covered in the v2only tests.
type rateStore struct {
	*mocks.MockInterface
	mu        sync.Mutex
	note      datastore.Note
	fillCalls int
	fillErr   error
	// stallNext makes the next FillClipSampleRate block until its context ends
	// and fail with the context's error; stallErr records that error.
	stallNext bool
	stallErr  error
	// onFill, when set, runs at the start of FillClipSampleRate, before the lock.
	onFill func()
}

func (s *rateStore) Get(string) (datastore.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.note, nil
}

func (s *rateStore) FillClipSampleRate(ctx context.Context, _ uint, clipName string, rate int) (bool, error) {
	if s.onFill != nil {
		s.onFill()
	}
	s.mu.Lock()
	s.fillCalls++
	if s.stallNext {
		s.stallNext = false
		// Wait without holding the lock so a write that never ends fails the
		// test instead of deadlocking the readers of stored().
		s.mu.Unlock()
		<-ctx.Done()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.stallErr = ctx.Err()
		return false, ctx.Err()
	}
	defer s.mu.Unlock()
	if s.fillErr != nil {
		return false, s.fillErr
	}
	if s.note.ClipSampleRate != 0 || s.note.ClipName != clipName {
		return false, nil
	}
	s.note.ClipSampleRate = rate
	return true, nil
}

func (s *rateStore) stored() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.note.ClipSampleRate
}

func (s *rateStore) fills() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fillCalls
}

// fakeProber replaces the clip header probe. When gate is non-nil it blocks until
// the gate closes.
type fakeProber struct {
	calls atomic.Int32
	rate  int
	err   error
	gate  chan struct{}
}

func (p *fakeProber) probe(context.Context, *os.File, string) (int, error) {
	p.calls.Add(1)
	if p.gate != nil {
		<-p.gate
	}
	return p.rate, p.err
}

func batNote(clip string, rate int) *datastore.Note {
	return &datastore.Note{
		ID:             rateTestNoteID,
		ClipName:       clip,
		ClipSampleRate: rate,
		Model:          detection.ModelInfo{ModelType: rateTestModelBat},
	}
}

// newRateHandler builds a handler over a rateStore holding note, with a clip file
// on disk when the note has a clip name. A name that escapes the export directory
// gets its file in the test's own temporary parent directory, so only the path
// check keeps the probe away from it.
func newRateHandler(t *testing.T, note *datastore.Note, prober *fakeProber) (*echo.Echo, *Handler, *rateStore) {
	t.Helper()
	e := echo.New()
	store := &rateStore{MockInterface: mocks.NewMockInterface(t), note: *note}
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithDatastore(store))
	h := buildTestHandler(t, core, map[string]string{}, map[string]string{})
	h.clipRateWait = rateTestWait
	if prober != nil {
		h.probeClipRate = prober.probe
	}
	if note.ClipName != "" {
		abs := filepath.Join(h.SFS.BaseDir(), filepath.FromSlash(note.ClipName))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o750))
		require.NoError(t, os.WriteFile(abs, []byte("clip"), 0o600))
	}
	return e, h, store
}

// fetchDetectionRate requests GET /detections/:id with ctx as the request
// context and returns the status and the clipSampleRate in the body (0 when
// omitted). It makes no assertions, so goroutines may call it.
func fetchDetectionRate(ctx context.Context, e *echo.Echo, h *Handler) (status, rate int, err error) {
	id := strconv.Itoa(int(rateTestNoteID))
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v2/detections/"+id, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(id)
	if err := h.GetDetection(c); err != nil {
		return 0, 0, err
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		return 0, 0, err
	}
	if v, ok := body["clipSampleRate"].(float64); ok {
		rate = int(v)
	}
	return rec.Code, rate, nil
}

// getDetectionRate is fetchDetectionRate for the test goroutine.
func getDetectionRate(t *testing.T, e *echo.Echo, h *Handler) (status, rate int) {
	t.Helper()
	status, rate, err := fetchDetectionRate(t.Context(), e, h)
	require.NoError(t, err)
	return status, rate
}

func TestGetDetection_LazyFillsBatClipSampleRate(t *testing.T) {
	prober := &fakeProber{rate: rateTestBatRate}
	e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), prober)

	// 1. A NULL bat row with a clip is probed and stored.
	status, rate := getDetectionRate(t, e, h)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, rateTestBatRate, rate)
	require.Eventually(t, func() bool { return store.stored() == rateTestBatRate }, time.Second, time.Millisecond)

	// 2. The second request reads the stored value and does not probe.
	_, rate = getDetectionRate(t, e, h)
	assert.Equal(t, rateTestBatRate, rate)
	assert.Equal(t, int32(1), prober.calls.Load())
	assert.Equal(t, 1, store.fills())
}

func TestGetDetection_LazyFill_ConcurrentSameRow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Everything is built inside the bubble so the gate and the contexts the
		// handler derives are bubble-owned.
		prober := &fakeProber{rate: rateTestBatRate, gate: make(chan struct{})}
		e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), prober)
		rates := make([]int, rateTestConcur)
		errs := make([]error, rateTestConcur)
		var wg sync.WaitGroup
		for i := range rateTestConcur {
			wg.Go(func() { _, rates[i], errs[i] = fetchDetectionRate(t.Context(), e, h) })
		}
		synctest.Wait() // every request is waiting on the one shared probe
		close(prober.gate)
		wg.Wait()
		synctest.Wait()

		assert.Equal(t, int32(1), prober.calls.Load(), "one probe for the row")
		assert.Equal(t, 1, store.fills(), "one write for the row")
		for i, r := range rates {
			require.NoError(t, errs[i], "request %d", i)
			assert.Equal(t, rateTestBatRate, r, "request %d", i)
		}
	})
}

func TestGetDetection_LazyFill_Budget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Everything is built inside the bubble so the gate and the contexts the
		// handler derives are bubble-owned.
		prober := &fakeProber{rate: rateTestBatRate, gate: make(chan struct{})}
		e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), prober)
		h.clipRateWait = clipRateResponseBudget
		start := time.Now()
		status, rate := getDetectionRate(t, e, h)
		elapsed := time.Since(start)
		assert.Equal(t, http.StatusOK, status)
		assert.Zero(t, rate, "the field is omitted when the probe misses the budget")
		assert.GreaterOrEqual(t, elapsed, clipRateResponseBudget)
		assert.Less(t, elapsed, clipRateResponseBudget+rateTestMargin)

		// The shared work finishes in the background and stores the value.
		close(prober.gate)
		synctest.Wait()
		assert.Equal(t, rateTestBatRate, store.stored())

		_, rate = getDetectionRate(t, e, h)
		assert.Equal(t, rateTestBatRate, rate)
		assert.Equal(t, int32(1), prober.calls.Load(), "the next request does not probe")
	})
}

func TestGetDetection_LazyFill_ProbeErrors(t *testing.T) {
	const probeFailedLog = "Clip sample rate probe failed"
	tests := []struct {
		name      string
		err       error
		wantClass string
	}{
		{"missing file", os.ErrNotExist, clipRateErrMissing},
		{"permission denied", os.ErrPermission, clipRateErrPermission},
		{"unsupported codec", transcode.ErrUnsupportedSource, clipRateErrUnsupported},
		{"header unparsable", errors.NewStd("bad header"), clipRateErrOther},
		{"header with no frame", transcode.ErrUndecodableSource, clipRateErrNoAudio},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prober := &fakeProber{err: tt.err}
			e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), prober)
			rec := newLevelRecorder()
			h.APILogger = rec

			status, rate := getDetectionRate(t, e, h)
			assert.Equal(t, http.StatusOK, status)
			assert.Zero(t, rate)
			assert.Zero(t, store.fills(), "nothing is written")
			class, ok := rec.debugField(probeFailedLog, "error_class")
			require.True(t, ok, "the probe failure is logged with its class")
			assert.Equal(t, tt.wantClass, class)

			_, rate = getDetectionRate(t, e, h)
			assert.Zero(t, rate)
			assert.Equal(t, int32(2), prober.calls.Load(), "there is no negative cache")
		})
	}
}

// writeTruncatedWAV writes a mono 16-bit WAV whose header declares many more
// frames than the file holds.
func writeTruncatedWAV(t *testing.T, path string, rate int) {
	t.Helper()
	const (
		headerSize     = 44
		declaredFrames = 1_000_000
		heldFrames     = 64
		bytesPerFrame  = 2
		fmtChunkSize   = 16
		pcmFormat      = 1
		channels       = 1
		bitsPerSample  = 16
	)
	buf := make([]byte, headerSize+heldFrames*bytesPerFrame)
	copy(buf[0:], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], uint32(len(buf)-8+(declaredFrames-heldFrames)*bytesPerFrame)) //nolint:gosec // G115: test header, small constants
	copy(buf[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(buf[16:], fmtChunkSize)
	binary.LittleEndian.PutUint16(buf[20:], pcmFormat)
	binary.LittleEndian.PutUint16(buf[22:], channels)
	binary.LittleEndian.PutUint32(buf[24:], uint32(rate))               //nolint:gosec // G115: test rate
	binary.LittleEndian.PutUint32(buf[28:], uint32(rate*bytesPerFrame)) //nolint:gosec // G115: test rate
	binary.LittleEndian.PutUint16(buf[32:], bytesPerFrame)
	binary.LittleEndian.PutUint16(buf[34:], bitsPerSample)
	copy(buf[36:], "data")
	binary.LittleEndian.PutUint32(buf[40:], declaredFrames*bytesPerFrame)
	for i := headerSize; i < len(buf); i++ {
		buf[i] = byte(i)
	}
	require.NoError(t, os.WriteFile(path, buf, 0o600))
}

func TestGetDetection_LazyFill_TruncatedDataStillFills(t *testing.T) {
	const clip = "2026/01/trunc.wav"
	e, h, store := newRateHandler(t, batNote(clip, 0), nil)
	writeTruncatedWAV(t, filepath.Join(h.SFS.BaseDir(), filepath.FromSlash(clip)), rateTestBatRate)

	_, rate := getDetectionRate(t, e, h)
	assert.Equal(t, rateTestBatRate, rate, "the header's rate is the clip's rate")
	require.Eventually(t, func() bool { return store.stored() == rateTestBatRate }, time.Second, time.Millisecond)
}

// panickingProbe stands in for a decoder that panics on a malformed clip header.
func panickingProbe(context.Context, *os.File, string) (int, error) {
	panic("malformed header")
}

func TestGetDetection_LazyFill_NonPositiveRateIsNotStored(t *testing.T) {
	prober := &fakeProber{rate: 0}
	e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), prober)

	_, rate := getDetectionRate(t, e, h)
	assert.Zero(t, rate)
	assert.Zero(t, store.fills(), "a non-positive rate is never written")
}

func TestGetDetection_LazyFill_ProbePanicIsContained(t *testing.T) {
	e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), nil)
	h.probeClipRate = panickingProbe

	status, rate := getDetectionRate(t, e, h)
	assert.Equal(t, http.StatusOK, status, "the request still answers")
	assert.Zero(t, rate, "the field is omitted")
	assert.Zero(t, store.fills(), "nothing is written")

	// The panic left no flight behind: the next request runs the probe again.
	var probes atomic.Int32
	h.probeClipRate = func(context.Context, *os.File, string) (int, error) {
		probes.Add(1)
		return rateTestBatRate, nil
	}
	_, rate = getDetectionRate(t, e, h)
	assert.Equal(t, rateTestBatRate, rate)
	assert.Equal(t, int32(1), probes.Load())
}

func TestGetDetection_LazyFill_ClosesTheClipHandle(t *testing.T) {
	tests := []struct {
		name   string
		probe  func(*os.File) (int, error)
		writes bool
	}{
		{"probe succeeds", func(*os.File) (int, error) { return rateTestBatRate, nil }, true},
		{"probe fails", func(*os.File) (int, error) { return 0, errors.NewStd("probe failed") }, false},
		{"probe panics", func(*os.File) (int, error) { panic("malformed header") }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), nil)
			var handle *os.File
			probes := 0
			h.probeClipRate = func(_ context.Context, f *os.File, _ string) (int, error) {
				probes++
				handle = f
				return tt.probe(f)
			}
			store.onFill = func() {
				_, err := handle.Stat()
				assert.ErrorIs(t, err, os.ErrClosed, "the handle is closed before the write")
			}

			getDetectionRate(t, e, h)

			require.Equal(t, 1, probes, "the probe ran once")
			_, err := handle.Stat()
			require.ErrorIs(t, err, os.ErrClosed, "the handle is closed once the request has answered")
			if tt.writes {
				assert.Equal(t, 1, store.fills(), "the write ran, so the before-write check ran")
			}
		})
	}
}

func TestGetDetection_LazyFill_WriteFails(t *testing.T) {
	prober := &fakeProber{rate: rateTestBatRate}
	e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), prober)
	store.fillErr = errors.NewStd("attempt to write a readonly database")

	_, rate := getDetectionRate(t, e, h)
	assert.Equal(t, rateTestBatRate, rate, "the probed rate is still returned")
	assert.Zero(t, store.stored(), "the row stays NULL")

	_, rate = getDetectionRate(t, e, h)
	assert.Equal(t, rateTestBatRate, rate)
	assert.Equal(t, int32(2), prober.calls.Load(), "the next request probes again")
}

func TestGetDetection_LazyFill_Skips(t *testing.T) {
	birdNote := batNote(rateTestClipName, 0)
	birdNote.Model.ModelType = "bird"
	tests := []struct {
		name string
		note *datastore.Note
	}{
		{"bird row", birdNote},
		{"bat row with a rate", batNote(rateTestClipName, rateTestBatRate)},
		{"spectrogram-only bat row", func() *datastore.Note {
			n := batNote("", 0)
			n.SpectrogramClipName = rateTestClipName
			return n
		}()},
		{"traversal clip name", batNote("../escape.flac", 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prober := &fakeProber{rate: rateTestBatRate}
			e, h, store := newRateHandler(t, tt.note, prober)

			_, rate := getDetectionRate(t, e, h)
			assert.Equal(t, tt.note.ClipSampleRate, rate)
			assert.Zero(t, prober.calls.Load())
			assert.Zero(t, store.fills())
		})
	}
}

// TestGetDetection_LazyFill_ClipNotARegularFile verifies a clip that is missing or
// is a directory is never probed and the field is omitted.
func TestGetDetection_LazyFill_ClipNotARegularFile(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, abs string)
	}{
		{"missing file", func(t *testing.T, abs string) {
			t.Helper()
			require.NoError(t, os.Remove(abs))
		}},
		{"directory", func(t *testing.T, abs string) {
			t.Helper()
			require.NoError(t, os.Remove(abs))
			require.NoError(t, os.MkdirAll(abs, 0o750))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prober := &fakeProber{rate: rateTestBatRate}
			e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), prober)
			tt.setup(t, filepath.Join(h.SFS.BaseDir(), filepath.FromSlash(rateTestClipName)))

			status, rate := getDetectionRate(t, e, h)
			assert.Equal(t, http.StatusOK, status)
			assert.Zero(t, rate)
			assert.Zero(t, prober.calls.Load())
			assert.Zero(t, store.fills())
		})
	}
}

// TestGetDetection_LazyFill_SymlinkOutsideClipDirIsNotProbed verifies a clip that
// is a symlink to a real WAV outside the clip directory is never read. It uses
// the production probe, so a probe that follows the link would store the outside
// file's rate.
func TestGetDetection_LazyFill_SymlinkOutsideClipDirIsNotProbed(t *testing.T) {
	const clip = "2026/01/bat.wav"
	tests := []struct {
		name string
		// warmStatCache stats the regular clip first, so a cached entry would
		// outlive the swap to a symlink.
		warmStatCache bool
	}{
		{"symlink in place", false},
		{"symlink swapped in after a cached check", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, h, store := newRateHandler(t, batNote(clip, 0), nil)
			abs := filepath.Join(h.SFS.BaseDir(), filepath.FromSlash(clip))
			outside := filepath.Join(t.TempDir(), "outside.wav")
			writeTruncatedWAV(t, outside, rateTestBatRate)
			if tt.warmStatCache {
				_, err := h.SFS.StatRel(clip)
				require.NoError(t, err)
			}
			require.NoError(t, os.Remove(abs))
			if err := os.Symlink(outside, abs); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}

			_, rate := getDetectionRate(t, e, h)
			assert.Zero(t, rate, "a clip that resolves outside the clip directory is not probed")
			assert.Zero(t, store.fills())
		})
	}
}

func TestListAndSearch_NeverProbe(t *testing.T) {
	prober := &fakeProber{rate: rateTestBatRate}
	e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), prober)
	mockDS := store.MockInterface
	notes := []datastore.Note{*batNote(rateTestClipName, 0)}
	mockDS.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(notes, int64(len(notes)), nil)
	mockDS.On("GetLastDetections", mock.Anything).Return(notes, nil)
	mockDS.On("SearchDetections", mock.Anything).Return([]datastore.DetectionRecord{{ID: "1", ModelType: rateTestModelBat}}, 1, nil)

	rec := executeRequest(t, e, http.MethodGet, "/api/v2/detections", h.GetDetections)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "clipSampleRate")

	rec = executeRequest(t, e, http.MethodGet, "/api/v2/detections/recent", h.GetRecentDetections)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "clipSampleRate")

	req := httptest.NewRequest(http.MethodPost, "/api/v2/search", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/search")
	require.NoError(t, h.HandleSearch(c))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "clipSampleRate")

	assert.Zero(t, prober.calls.Load(), "list and search never read a clip")
	assert.Zero(t, store.fills())
}

func TestNoteToDetectionResponse_ClipSampleRate(t *testing.T) {
	_, _, h := setupTestEnvironment(t)

	resp := h.noteToDetectionResponse(&datastore.Note{ClipName: rateTestClipName, ClipSampleRate: rateTestBatRate}, false, nil)
	assert.Equal(t, rateTestBatRate, resp.ClipSampleRate)
	body, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"clipSampleRate":384000`)

	resp = h.noteToDetectionResponse(&datastore.Note{ClipName: rateTestClipName}, false, nil)
	assert.Zero(t, resp.ClipSampleRate)
	body, err = json.Marshal(resp)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "clipSampleRate", "an unknown rate is omitted")
}

// TestGetDetection_LazyFill_WorkTimeoutAndDetach verifies the shared work is
// bounded by clipRateWorkTimeout and does not follow the request context.
func TestGetDetection_LazyFill_WorkTimeoutAndDetach(t *testing.T) {
	t.Run("a hung probe is cancelled at the work timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var probeErr error
			prober := func(ctx context.Context, _ *os.File, _ string) (int, error) {
				<-ctx.Done()
				probeErr = ctx.Err()
				return 0, probeErr
			}
			e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), nil)
			h.probeClipRate = prober

			start := time.Now()
			_, rate := getDetectionRate(t, e, h)
			assert.Zero(t, rate)
			elapsed := time.Since(start)
			assert.GreaterOrEqual(t, elapsed, clipRateWorkTimeout)
			assert.Less(t, elapsed, clipRateWorkTimeout+rateTestMargin)
			require.ErrorIs(t, probeErr, context.DeadlineExceeded)
			assert.Zero(t, store.fills())
		})
	})

	t.Run("a stalled write is cancelled at the work timeout and the next request refills", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			prober := &fakeProber{rate: rateTestBatRate}
			e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), prober)
			store.stallNext = true

			// Request 1: the write blocks, so the shared work ends at the timeout.
			start := time.Now()
			_, rate := getDetectionRate(t, e, h)
			elapsed := time.Since(start)
			assert.GreaterOrEqual(t, elapsed, clipRateWorkTimeout)
			assert.Less(t, elapsed, clipRateWorkTimeout+rateTestMargin)
			assert.Equal(t, rateTestBatRate, rate, "the probed rate is returned even when the write fails")
			store.mu.Lock()
			stallErr := store.stallErr
			store.mu.Unlock()
			require.ErrorIs(t, stallErr, context.DeadlineExceeded)
			assert.Zero(t, store.stored())

			// Request 2: the cancelled write released the key, so this is a new
			// flight that probes again and fills the still-NULL row.
			_, rate = getDetectionRate(t, e, h)
			assert.Equal(t, rateTestBatRate, rate)
			assert.EqualValues(t, 2, prober.calls.Load())
			assert.Equal(t, rateTestBatRate, store.stored())
			assert.Equal(t, 2, store.fills())
		})
	})

	t.Run("a cancelled request does not cancel the work", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			gate := make(chan struct{})
			var ctxErr error
			prober := func(ctx context.Context, _ *os.File, _ string) (int, error) {
				<-gate
				ctxErr = ctx.Err()
				return rateTestBatRate, nil
			}
			e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), nil)
			h.probeClipRate = prober

			reqCtx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _, _ = fetchDetectionRate(reqCtx, e, h)
			}()
			synctest.Wait() // the request waits on the shared probe
			cancel()
			close(gate)
			<-done
			synctest.Wait()

			require.NoError(t, ctxErr, "the probe's context is not the request's")
			assert.Equal(t, rateTestBatRate, store.stored())
		})
	})
}
