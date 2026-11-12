package detections

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/analysis/noisebank"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// fakeNoise records what the handlers ask of the noise example service.
type fakeNoise struct {
	mu sync.Mutex

	markErr    error
	markStatus noisebank.Status
	leaveErr   error
	status     noisebank.Status
	statusErr  error
	// refuseGuard lists the detections whose delete guard is refused.
	refuseGuard map[uint]bool

	events []string
	marks  []noisebank.MarkRequest
	leaves map[uint]entities.VerificationStatus
	held   map[uint]bool
	heldAt map[uint]int // guards still held by other detections when this one was guarded
	// onRelease runs when a guard is released.
	onRelease func(id uint)
}

func newFakeNoise() *fakeNoise {
	return &fakeNoise{
		refuseGuard: map[uint]bool{},
		leaves:      map[uint]entities.VerificationStatus{},
		held:        map[uint]bool{},
		heldAt:      map[uint]int{},
	}
}

func (f *fakeNoise) record(event string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}

func (f *fakeNoise) Mark(_ context.Context, req noisebank.MarkRequest) (noisebank.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "mark")
	f.marks = append(f.marks, req)
	return f.markStatus, f.markErr
}

func (f *fakeNoise) LeaveNoise(_ context.Context, id uint, verified entities.VerificationStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "leave")
	f.leaves[id] = verified
	return f.leaveErr
}

func (f *fakeNoise) GuardDelete(id uint) (release func(), ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heldAt[id] = len(f.held)
	if f.refuseGuard[id] {
		f.events = append(f.events, "guard-refused")
		return nil, false
	}
	f.held[id] = true
	f.events = append(f.events, "guard")
	return func() {
		f.mu.Lock()
		delete(f.held, id)
		f.events = append(f.events, "release")
		hook := f.onRelease
		f.mu.Unlock()
		if hook != nil {
			hook(id)
		}
	}, true
}

func (f *fakeNoise) Status(context.Context, uint, noisebank.OpenClipFunc) (noisebank.Status, error) {
	f.record("status")
	return f.status, f.statusErr
}

func (f *fakeNoise) eventLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.events)
}

// postJSON runs a handler on a JSON request for detection id.
func postJSON(t *testing.T, e *echo.Echo, handler echo.HandlerFunc, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/"+id+"/review", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(id)
	require.NoError(t, handler(c))
	return rec
}

type errorBody struct {
	ErrorKey string `json:"error_key"`
}

func errorKeyOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body errorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.ErrorKey
}

// reviewFixture is a handler whose datastore knows one unlocked detection, with
// the legacy review and comment writes expected only when the test says so.
func reviewFixture(t *testing.T, noise *fakeNoise) (*echo.Echo, *mocks.MockInterface, *Handler) {
	t.Helper()
	e, mockDS, h := setupTestEnvironment(t)
	if noise != nil {
		h.noise = noise
	}
	mockDS.On("Get", "7").Return(datastore.Note{ID: 7}, nil)
	mockDS.On("IsNoteLocked", "7").Return(false, nil)
	return e, mockDS, h
}

func TestReviewDetection_NoiseMark(t *testing.T) {
	t.Run("without a service nothing is written", func(t *testing.T) {
		e, mockDS, h := reviewFixture(t, nil)

		rec := postJSON(t, e, h.ReviewDetection, "7", `{"verified":"false_positive","reason":"no_animal","comment":"rain"}`)

		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.Equal(t, string(noisebank.ReasonLegacyDatabase), errorKeyOf(t, rec))
		mockDS.AssertNotCalled(t, "SaveNoteReview", mock.Anything)
		mockDS.AssertNotCalled(t, "SaveNoteComment", mock.Anything)
	})

	t.Run("without a service on the enhanced database the answer is not_ready", func(t *testing.T) {
		datastoreV2.SetEnhancedDatabaseMode()
		t.Cleanup(datastoreV2.ResetDatabaseMode)
		e, _, h := reviewFixture(t, nil)

		rec := postJSON(t, e, h.ReviewDetection, "7", `{"verified":"false_positive","reason":"no_animal"}`)

		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.Equal(t, string(noisebank.ReasonNotReady), errorKeyOf(t, rec))
	})

	t.Run("invalid requests write nothing and call nothing", func(t *testing.T) {
		for name, body := range map[string]string{
			"no_animal with correct":         `{"verified":"correct","reason":"no_animal"}`,
			"no_animal without a verdict":    `{"reason":"no_animal"}`,
			"empty reason without a verdict": `{"reason":"","comment":"edited"}`,
			"unknown reason":                 `{"verified":"false_positive","reason":"bird_was_loud"}`,
			"unknown verdict":                `{"verified":"maybe","reason":"no_animal"}`,
		} {
			t.Run(name, func(t *testing.T) {
				noise := newFakeNoise()
				e, mockDS, h := reviewFixture(t, noise)

				rec := postJSON(t, e, h.ReviewDetection, "7", body)

				assert.Equal(t, http.StatusBadRequest, rec.Code)
				assert.Empty(t, noise.eventLog())
				mockDS.AssertNotCalled(t, "SaveNoteReview", mock.Anything)
				mockDS.AssertNotCalled(t, "SaveNoteComment", mock.Anything)
			})
		}
	})

	t.Run("each refusal maps to its key and status and writes no comment", func(t *testing.T) {
		tests := []struct {
			err        error
			wantStatus int
			wantKey    string
			retryAfter bool
		}{
			{&noisebank.RefusalError{Reason: noisebank.ReasonModelUnsupported}, http.StatusConflict, "model_unsupported", false},
			{&noisebank.RefusalError{Reason: noisebank.ReasonModelNotLoaded}, http.StatusConflict, "model_not_loaded", false},
			{&noisebank.RefusalError{Reason: noisebank.ReasonClipMissing}, http.StatusConflict, "clip_missing", false},
			{&noisebank.RefusalError{Reason: noisebank.ReasonClipUnusable}, http.StatusConflict, "clip_unusable", false},
			{&noisebank.RefusalError{Reason: noisebank.ReasonClipDenoised}, http.StatusConflict, "clip_denoised", false},
			{&noisebank.RefusalError{Reason: noisebank.ReasonBankFull}, http.StatusConflict, "bank_full", false},
			{&noisebank.RefusalError{Reason: noisebank.ReasonDetectionBeingDeleted}, http.StatusConflict, "detection_being_deleted", false},
			{&noisebank.RefusalError{Reason: noisebank.ReasonClearing}, http.StatusServiceUnavailable, "clearing", true},
			{noisebank.ErrBusy, http.StatusServiceUnavailable, errorKeyNoiseQueueFull, true},
			{noisebank.ErrNotFound, http.StatusNotFound, "", false},
			{errors.NewStd("database is down"), http.StatusInternalServerError, "", false},
		}
		for _, tt := range tests {
			t.Run(tt.err.Error(), func(t *testing.T) {
				noise := newFakeNoise()
				noise.markErr = tt.err
				e, mockDS, h := reviewFixture(t, noise)

				rec := postJSON(t, e, h.ReviewDetection, "7", `{"verified":"false_positive","reason":"no_animal","comment":"rain"}`)

				assert.Equal(t, tt.wantStatus, rec.Code)
				assert.Equal(t, tt.wantKey, errorKeyOf(t, rec))
				assert.Equal(t, tt.retryAfter, rec.Header().Get(echo.HeaderRetryAfter) != "")
				mockDS.AssertNotCalled(t, "SaveNoteReview", mock.Anything)
				mockDS.AssertNotCalled(t, "SaveNoteComment", mock.Anything)
			})
		}
	})

	t.Run("a mark writes the review through the service, then the comment", func(t *testing.T) {
		noise := newFakeNoise()
		noise.markStatus = noisebank.Status{Available: true, Marked: true, State: noisebank.StateQueued}
		e, mockDS, h := reviewFixture(t, noise)
		mockDS.On("SaveNoteComment", mock.AnythingOfType("*datastore.NoteComment")).
			Run(func(mock.Arguments) { noise.record("comment") }).Return(nil)

		rec := postJSON(t, e, h.ReviewDetection, "7", `{"verified":"false_positive","reason":"no_animal","comment":"rain"}`)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, []string{"mark", "comment"}, noise.eventLog())
		mockDS.AssertNotCalled(t, "SaveNoteReview", mock.Anything)
		require.Len(t, noise.marks, 1)
		assert.Equal(t, uint(7), noise.marks[0].DetectionID)
		assert.NotNil(t, noise.marks[0].OpenClip)

		var body struct {
			Status string           `json:"status"`
			Noise  noisebank.Status `json:"noise"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "success", body.Status)
		assert.True(t, body.Noise.Marked)
		assert.Equal(t, noisebank.StateQueued, body.Noise.State)
	})
}

func TestReviewDetection_LeavingNoise(t *testing.T) {
	t.Run("correct goes through the service, not the legacy write", func(t *testing.T) {
		noise := newFakeNoise()
		e, mockDS, h := reviewFixture(t, noise)

		rec := postJSON(t, e, h.ReviewDetection, "7", `{"verified":"correct"}`)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, entities.VerificationCorrect, noise.leaves[7])
		mockDS.AssertNotCalled(t, "SaveNoteReview", mock.Anything)
		assert.NotContains(t, rec.Body.String(), "noise", "the response carries no noise status unless a mark was made")
	})

	t.Run("false_positive with an explicit empty reason goes through the service", func(t *testing.T) {
		noise := newFakeNoise()
		e, mockDS, h := reviewFixture(t, noise)

		rec := postJSON(t, e, h.ReviewDetection, "7", `{"verified":"false_positive","reason":""}`)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, entities.VerificationFalsePositive, noise.leaves[7])
		mockDS.AssertNotCalled(t, "SaveNoteReview", mock.Anything)
	})

	t.Run("a failing service answers 500 and no review is written by another path", func(t *testing.T) {
		noise := newFakeNoise()
		noise.leaveErr = errors.NewStd("transaction failed")
		e, mockDS, h := reviewFixture(t, noise)

		rec := postJSON(t, e, h.ReviewDetection, "7", `{"verified":"correct"}`)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		mockDS.AssertNotCalled(t, "SaveNoteReview", mock.Anything)
	})

	t.Run("false_positive without a reason only writes the review and leaves the mark", func(t *testing.T) {
		noise := newFakeNoise()
		e, mockDS, h := reviewFixture(t, noise)
		mockDS.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(nil)

		rec := postJSON(t, e, h.ReviewDetection, "7", `{"verified":"false_positive"}`)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, noise.eventLog(), "the noise service is not involved")
		mockDS.AssertCalled(t, "SaveNoteReview", mock.Anything)
	})

	t.Run("a failing legacy write answers 500", func(t *testing.T) {
		e, mockDS, h := reviewFixture(t, newFakeNoise())
		mockDS.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(errors.NewStd("database is down"))

		rec := postJSON(t, e, h.ReviewDetection, "7", `{"verified":"false_positive"}`)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("without a service every verdict is the legacy write", func(t *testing.T) {
		for _, body := range []string{`{"verified":"correct"}`, `{"verified":"false_positive","reason":""}`} {
			e, mockDS, h := reviewFixture(t, nil)
			mockDS.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(nil)

			rec := postJSON(t, e, h.ReviewDetection, "7", body)

			require.Equal(t, http.StatusOK, rec.Code, body)
			mockDS.AssertCalled(t, "SaveNoteReview", mock.Anything)
		}
	})
}

func TestBatchReviewDetections_Noise(t *testing.T) {
	setup := func(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Handler, *fakeNoise) {
		t.Helper()
		noise := newFakeNoise()
		e, mockDS, h := setupTestEnvironment(t)
		h.noise = noise
		for _, id := range []string{"1", "2"} {
			n := uint(1)
			if id == "2" {
				n = 2
			}
			mockDS.On("Get", id).Return(datastore.Note{ID: n}, nil)
		}
		return e, mockDS, h, noise
	}

	t.Run("correct leaves noise per ID", func(t *testing.T) {
		e, mockDS, h, noise := setup(t)

		rec := postJSON(t, e, h.BatchReviewDetections, "batch", `{"ids":["1","2"],"verified":"correct"}`)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, map[uint]entities.VerificationStatus{1: entities.VerificationCorrect, 2: entities.VerificationCorrect}, noise.leaves)
		mockDS.AssertNotCalled(t, "SaveNoteReview", mock.Anything)
	})

	t.Run("false_positive keeps the legacy write and leaves marks alone", func(t *testing.T) {
		e, mockDS, h, noise := setup(t)
		mockDS.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(nil)

		rec := postJSON(t, e, h.BatchReviewDetections, "batch", `{"ids":["1","2"],"verified":"false_positive"}`)

		require.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, noise.eventLog())
		mockDS.AssertNumberOfCalls(t, "SaveNoteReview", 2)
	})
}

// clipFixture writes a clip under the handler's clip directory and returns its
// name and path.
func clipFixture(t *testing.T, h *Handler) (name, path string) {
	t.Helper()
	dir := filepath.Join(h.SFS.BaseDir(), "2025", "01")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	name = filepath.Join("2025", "01", "noise.wav")
	path = filepath.Join(h.SFS.BaseDir(), name)
	require.NoError(t, os.WriteFile(path, []byte("fake-audio"), 0o600))
	return name, path
}

func deleteRequest(t *testing.T, e *echo.Echo, h *Handler, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/detections/"+id, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(id)
	require.NoError(t, h.DeleteDetection(c))
	return rec
}

func TestDeleteDetection_NoiseGuard(t *testing.T) {
	t.Run("the guard is released after the delete and the files, and no example is touched", func(t *testing.T) {
		noise := newFakeNoise()
		e, mockDS, h := setupTestEnvironment(t)
		h.noise = noise
		name, path := clipFixture(t, h)
		mockDS.On("Get", "5").Return(datastore.Note{ID: 5, ClipName: name}, nil)
		mockDS.On("Delete", "5").Run(func(mock.Arguments) { noise.record("delete") }).Return(nil)
		noise.onRelease = func(uint) {
			_, err := os.Stat(path)
			assert.True(t, os.IsNotExist(err), "the clip is removed before the guard is released")
		}

		rec := deleteRequest(t, e, h, "5")

		require.Equal(t, http.StatusNoContent, rec.Code)
		assert.Equal(t, []string{"guard", "delete", "release"}, noise.eventLog())
	})

	t.Run("the guard is released when the delete fails", func(t *testing.T) {
		noise := newFakeNoise()
		e, mockDS, h := setupTestEnvironment(t)
		h.noise = noise
		mockDS.On("Get", "5").Return(datastore.Note{ID: 5}, nil)
		mockDS.On("Delete", "5").Return(errors.NewStd("database is down"))

		rec := deleteRequest(t, e, h, "5")

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Equal(t, []string{"guard", "release"}, noise.eventLog())
	})

	t.Run("a refused guard answers 409 and deletes nothing", func(t *testing.T) {
		noise := newFakeNoise()
		noise.refuseGuard[5] = true
		e, mockDS, h := setupTestEnvironment(t)
		h.noise = noise
		_, path := clipFixture(t, h)
		mockDS.On("Get", "5").Return(datastore.Note{ID: 5, ClipName: filepath.Join("2025", "01", "noise.wav")}, nil)

		rec := deleteRequest(t, e, h, "5")

		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.Equal(t, errorKeyNoiseLearningInProgress, errorKeyOf(t, rec))
		mockDS.AssertNotCalled(t, "Delete", mock.Anything)
		_, err := os.Stat(path)
		require.NoError(t, err, "the clip stays")
	})

	t.Run("a batch counts a failed delete as skipped and still releases the guard", func(t *testing.T) {
		noise := newFakeNoise()
		e, mockDS, h := setupTestEnvironment(t)
		h.noise = noise
		mockDS.On("Get", "1").Return(datastore.Note{ID: 1}, nil)
		mockDS.On("Delete", "1").Return(errors.NewStd("database is down"))

		rec := postJSON(t, e, h.BatchDeleteDetections, "batch", `{"ids":["1"]}`)

		var result BatchResult
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		assert.Equal(t, BatchResult{Processed: 0, Skipped: 1}, result)
		assert.Equal(t, []string{"guard", "release"}, noise.eventLog())
	})

	t.Run("a batch skips the refused ID, releases each guard before the next and deletes the rest", func(t *testing.T) {
		noise := newFakeNoise()
		noise.refuseGuard[2] = true
		e, mockDS, h := setupTestEnvironment(t)
		h.noise = noise
		for _, id := range []string{"1", "2", "3"} {
			n := map[string]uint{"1": 1, "2": 2, "3": 3}[id]
			mockDS.On("Get", id).Return(datastore.Note{ID: n}, nil)
			if id != "2" {
				mockDS.On("Delete", id).Return(nil)
			}
		}

		rec := postJSON(t, e, h.BatchDeleteDetections, "batch", `{"ids":["1","2","3"]}`)

		require.Equal(t, http.StatusOK, rec.Code)
		var result BatchResult
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		assert.Equal(t, BatchResult{Processed: 2, Skipped: 1}, result)
		mockDS.AssertNotCalled(t, "Delete", "2")
		assert.Equal(t, []string{"guard", "release", "guard-refused", "guard", "release"}, noise.eventLog())
		for id, others := range noise.heldAt {
			assert.Zero(t, others, "no guard of an earlier ID is still held when ID %d is guarded", id)
		}
	})
}

func TestGetDetectionNoise(t *testing.T) {
	get := func(t *testing.T, e *echo.Echo, h *Handler, id string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/"+id+"/noise", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(id)
		require.NoError(t, h.GetDetectionNoise(c))
		return rec
	}

	t.Run("without a service the answer says why", func(t *testing.T) {
		for name, tt := range map[string]struct {
			enhanced bool
			want     noisebank.Reason
		}{
			"legacy database":      {false, noisebank.ReasonLegacyDatabase},
			"enhanced, not loaded": {true, noisebank.ReasonNotReady},
		} {
			t.Run(name, func(t *testing.T) {
				if tt.enhanced {
					datastoreV2.SetEnhancedDatabaseMode()
					t.Cleanup(datastoreV2.ResetDatabaseMode)
				}
				e, _, h := setupTestEnvironment(t)
				repo := mocks.NewMockDetectionRepository(t)
				h.Repo = repo
				repo.EXPECT().Get(mock.Anything, "5").Return(&detection.Result{ID: 5}, nil)

				rec := get(t, e, h, "5")

				require.Equal(t, http.StatusOK, rec.Code)
				var st noisebank.Status
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
				assert.False(t, st.Available)
				assert.Equal(t, tt.want, st.UnavailableReason)
				assert.Equal(t, noisebank.StateIdle, st.State)
			})
		}
	})

	t.Run("an unknown detection is a 404 even without a service", func(t *testing.T) {
		e, _, h := setupTestEnvironment(t)
		repo := mocks.NewMockDetectionRepository(t)
		h.Repo = repo
		repo.EXPECT().Get(mock.Anything, "5").Return(nil, repository.ErrDetectionNotFound)

		assert.Equal(t, http.StatusNotFound, get(t, e, h, "5").Code)
	})

	t.Run("the legacy bridge's not-found error is a 404 without a service", func(t *testing.T) {
		e, _, h := setupTestEnvironment(t)
		repo := mocks.NewMockDetectionRepository(t)
		h.Repo = repo
		notFound := errors.New(errors.NewStd("record not found")).Category(errors.CategoryNotFound).Build()
		repo.EXPECT().Get(mock.Anything, "5").Return(nil, notFound)

		assert.Equal(t, http.StatusNotFound, get(t, e, h, "5").Code)
	})

	t.Run("a database failure without a service is a 500", func(t *testing.T) {
		e, _, h := setupTestEnvironment(t)
		repo := mocks.NewMockDetectionRepository(t)
		h.Repo = repo
		repo.EXPECT().Get(mock.Anything, "5").Return(nil, errors.NewStd("database is down"))

		assert.Equal(t, http.StatusInternalServerError, get(t, e, h, "5").Code)
	})

	t.Run("the service status is returned as is", func(t *testing.T) {
		noise := newFakeNoise()
		noise.status = noisebank.Status{
			Available: true, Marked: true, State: noisebank.StateRunning, Examples: 4, InactiveExamples: 1,
			WindowsDone: 37, WindowsTotal: 105,
		}
		e, _, h := setupTestEnvironment(t)
		h.noise = noise

		rec := get(t, e, h, "5")

		require.Equal(t, http.StatusOK, rec.Code)
		var raw map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
		assert.Equal(t, map[string]any{
			"available": true, "unavailableReason": "", "marked": true, "state": "running",
			"examples": float64(4), "inactiveExamples": float64(1), "windowsDone": float64(37), "windowsTotal": float64(105),
			"failureReason": "", "clipDenoised": false,
		}, raw)
	})

	t.Run("an unknown detection is a 404 with a service too", func(t *testing.T) {
		noise := newFakeNoise()
		noise.statusErr = noisebank.ErrNotFound
		e, _, h := setupTestEnvironment(t)
		h.noise = noise

		assert.Equal(t, http.StatusNotFound, get(t, e, h, "5").Code)
	})

	t.Run("a bad ID is a 400", func(t *testing.T) {
		e, _, h := setupTestEnvironment(t)
		assert.Equal(t, http.StatusBadRequest, get(t, e, h, "abc").Code)
		assert.Equal(t, http.StatusBadRequest, get(t, e, h, "0").Code)
	})
}

// TestNoiseClipOpener pins that the opener the handler gives the service resolves
// the stored name inside the clip directory and refuses a name that escapes it.
func TestNoiseClipOpener(t *testing.T) {
	_, _, h := setupTestEnvironment(t)
	name, _ := clipFixture(t, h)
	open := h.noiseClipOpener()

	f, err := open(name)
	require.NoError(t, err)
	data := make([]byte, 4)
	_, err = f.Read(data)
	require.NoError(t, err)
	assert.Equal(t, "fake", string(bytes.TrimSpace(data)))
	require.NoError(t, f.Close())

	// A real file one level above the clip directory must stay unreachable.
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(h.SFS.BaseDir()), "outside.wav"), []byte("secret"), 0o600))
	_, err = open("../outside.wav")
	require.Error(t, err)
	_, err = open("2025/01/missing.wav")
	require.Error(t, err)
}

// TestNoiseExamples_ComesFromTheProcessor pins where production finds the service:
// the processor, and nothing at all when none was set. A nil service must not
// become a non-nil interface, or every handler would call into it.
func TestNoiseExamples_ComesFromTheProcessor(t *testing.T) {
	_, _, h := setupTestEnvironment(t)

	h.Processor = &processor.Processor{}
	assert.Nil(t, h.noiseExamples())

	svc := noisebank.NewService(noisebank.Config{})
	h.Processor.SetNoiseExamples(svc)
	assert.Same(t, svc, h.noiseExamples())

	h.Processor = nil
	assert.Nil(t, h.noiseExamples())
}
