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
}

func (s *rateStore) Get(string) (datastore.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.note, nil
}

func (s *rateStore) FillClipSampleRate(_ uint, clipName string, rate int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fillCalls++
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

func (p *fakeProber) probe(context.Context, string) (int, error) {
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

// newRateHandler builds a handler over a rateStore holding note, with an (empty)
// clip file on disk when the note has a clip name.
func newRateHandler(t *testing.T, note *datastore.Note, prober *fakeProber) (*echo.Echo, *Handler, *rateStore) {
	t.Helper()
	e := echo.New()
	store := &rateStore{MockInterface: mocks.NewMockInterface(t), note: *note}
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithDatastore(store))
	h := buildTestHandler(t, core, map[string]string{}, map[string]string{})
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

// getDetectionRate requests GET /detections/:id and returns the status and the
// clipSampleRate in the body (0 when omitted).
func getDetectionRate(t *testing.T, e *echo.Echo, h *Handler) (status, rate int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/"+strconv.Itoa(int(rateTestNoteID)), http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(strconv.Itoa(int(rateTestNoteID)))
	require.NoError(t, h.GetDetection(c))
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	if v, ok := body["clipSampleRate"].(float64); ok {
		rate = int(v)
	}
	return rec.Code, rate
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
		var wg sync.WaitGroup
		for i := range rateTestConcur {
			wg.Go(func() { _, rates[i] = getDetectionRate(t, e, h) })
		}
		synctest.Wait() // every request is waiting on the one shared probe
		close(prober.gate)
		wg.Wait()
		synctest.Wait()

		assert.Equal(t, int32(1), prober.calls.Load(), "one probe for the row")
		assert.Equal(t, 1, store.fills(), "one write for the row")
		for i, r := range rates {
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
	tests := []struct {
		name string
		err  error
	}{
		{"missing file", os.ErrNotExist},
		{"permission denied", os.ErrPermission},
		{"unsupported codec", transcode.ErrUnsupportedSource},
		{"header unparsable", errors.NewStd("bad header")},
		{"header with no frame", transcode.ErrUndecodableSource},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prober := &fakeProber{err: tt.err}
			e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), prober)

			status, rate := getDetectionRate(t, e, h)
			assert.Equal(t, http.StatusOK, status)
			assert.Zero(t, rate)
			assert.Zero(t, store.fills(), "nothing is written")

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
func panickingProbe(context.Context, string) (int, error) {
	panic("malformed header")
}

func TestGetDetection_LazyFill_ProbePanicIsContained(t *testing.T) {
	e, h, store := newRateHandler(t, batNote(rateTestClipName, 0), nil)
	h.probeClipRate = panickingProbe
	h.clipRateWait = time.Minute // the flight ends by itself, so a long wait does not slow the test

	status, rate := getDetectionRate(t, e, h)
	assert.Equal(t, http.StatusOK, status, "the request still answers")
	assert.Zero(t, rate, "the field is omitted")
	assert.Zero(t, store.fills(), "nothing is written")

	// The panic left no flight behind: the next request runs the probe again.
	var probes atomic.Int32
	h.probeClipRate = func(context.Context, string) (int, error) {
		probes.Add(1)
		return rateTestBatRate, nil
	}
	_, rate = getDetectionRate(t, e, h)
	assert.Equal(t, rateTestBatRate, rate)
	assert.Equal(t, int32(1), probes.Load())
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
		{"traversal clip name", batNote("../../etc/passwd", 0)},
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

	t.Run("legacy store without the interface", func(t *testing.T) {
		prober := &fakeProber{rate: rateTestBatRate}
		e := echo.New()
		mockDS := mocks.NewMockInterface(t)
		note := batNote(rateTestClipName, 0)
		mockDS.On("Get", strconv.Itoa(int(rateTestNoteID))).Return(*note, nil)
		core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithDatastore(mockDS))
		h := buildTestHandler(t, core, map[string]string{}, map[string]string{})
		h.probeClipRate = prober.probe

		_, rate := getDetectionRate(t, e, h)
		assert.Zero(t, rate)
		assert.Zero(t, prober.calls.Load())
	})
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
