package processor

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	audioBuffer "github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// recordingPreRenderer records PreRendererSubmit calls for audio export outcome tests.
type recordingPreRenderer struct {
	mu      sync.Mutex
	noteIDs []uint
}

func (r *recordingPreRenderer) Submit(job interface {
	GetPCMData() []byte
	GetSampleRate() int
	GetClipPath() string
	GetNoteID() uint
	GetTimestamp() time.Time
	GetModelType() string
}) error {
	r.mu.Lock()
	r.noteIDs = append(r.noteIDs, job.GetNoteID())
	r.mu.Unlock()
	return nil
}

func (r *recordingPreRenderer) Stop() {}

func (r *recordingPreRenderer) submittedNoteIDs() []uint {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]uint, len(r.noteIDs))
	copy(out, r.noteIDs)
	return out
}

// zeroIDSaveRepository succeeds on Save without assigning result.ID (defensive #2453 path).
type zeroIDSaveRepository struct {
	*MockDetectionRepository
}

func (r *zeroIDSaveRepository) Save(_ context.Context, _ *detection.Result, _ []detection.AdditionalResult) error {
	return nil
}

func newTestSaveAudioAction(t *testing.T, clipName string, detCtx *DetectionContext, preRenderer PreRendererSubmit) (action *SaveAudioAction, exportDir string) {
	t.Helper()
	tmpDir := t.TempDir()
	settings := conftest.NewTestSettings().
		WithAudioExport(tmpDir, "wav", "192k").
		Build()
	settings.Realtime.Dashboard.Spectrogram.Enabled = true

	// 0.1s of deterministic 16-bit mono PCM at 48kHz.
	pcm := make([]byte, (conf.SampleRate/10)*(conf.BitDepth/8))
	for i := range pcm {
		pcm[i] = byte(i % 251)
	}

	return &SaveAudioAction{
		Settings:         settings,
		ClipName:         clipName,
		CorrelationID:    "test-corr-id",
		DetectionCtx:     detCtx,
		PreRenderer:      preRenderer,
		pcmData:          pcm,
		sourceSampleRate: conf.SampleRate,
		species:          "european robin",
	}, tmpDir
}

func newTestDatabaseAction(repo datastore.DetectionRepository, tracker *EventTracker, detCtx *DetectionContext, clipName string) *DatabaseAction {
	det := testDetection()
	det.Result.ClipName = clipName
	return &DatabaseAction{
		Settings:      &conf.Settings{},
		Repo:          repo,
		Result:        det.Result,
		Results:       det.Results,
		EventTracker:  tracker,
		DetectionCtx:  detCtx,
		CorrelationID: "test-corr-id",
	}
}

// TestSaveAudioAction_AbortsWhenDatabaseSkippedOrFailed verifies that SaveAudioAction
// skips audio encoding, file creation, and spectrogram pre-rendering without waiting
// for noteIDWaitTimeout when DatabaseAction rate-limits or terminally fails.
// Not parallel because logtest.CaptureBuffer swaps logger.Global().
func TestSaveAudioAction_AbortsWhenDatabaseSkippedOrFailed(t *testing.T) {
	tests := []struct {
		name     string
		settleDB func(t *testing.T, detCtx *DetectionContext)
	}{
		{
			name: "rate_limited_skip",
			settleDB: func(t *testing.T, detCtx *DetectionContext) {
				t.Helper()
				tracker := NewEventTracker(15 * time.Second)
				repo := NewMockDetectionRepository()

				// First detection consumes the per-species DatabaseSave window.
				first := newTestDatabaseAction(repo, tracker, &DetectionContext{}, "first.wav")
				require.NoError(t, first.Execute(t.Context(), nil))

				// Second detection of the same species within interval is skipped.
				second := newTestDatabaseAction(repo, tracker, detCtx, "skipped.wav")
				require.NoError(t, second.Execute(t.Context(), nil))
				require.True(t, second.skipsLaterSteps())
			},
		},
		{
			name: "terminal_db_failure",
			settleDB: func(t *testing.T, detCtx *DetectionContext) {
				t.Helper()
				repo := &scriptedSaveRepository{
					MockDetectionRepository: NewMockDetectionRepository(),
					script:                  []error{errors.NewStd("disk full")},
				}
				db := newTestDatabaseAction(repo, NewEventTracker(15*time.Second), detCtx, "failed.wav")
				require.Error(t, db.Execute(t.Context(), nil))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logs := logtest.CaptureBuffer(t)
			synctest.Test(t, func(t *testing.T) {
				detCtx := &DetectionContext{}
				tc.settleDB(t, detCtx)

				pre := &recordingPreRenderer{}
				action, tmpDir := newTestSaveAudioAction(t, "orphan.wav", detCtx, pre)

				start := time.Now()
				require.NoError(t, action.Execute(t.Context(), nil))
				elapsed := time.Since(start)

				assert.Less(t, elapsed, time.Second, "SaveAudioAction must not stall for noteIDWaitTimeout")
				assert.False(t, detCtx.ClipSaved.Load(), "ClipSaved must remain false")
				assert.Empty(t, pre.submittedNoteIDs(), "PreRenderer.Submit must not be called")

				entries, err := os.ReadDir(tmpDir)
				require.NoError(t, err)
				assert.Empty(t, entries, "no orphaned clip file should be written")
				assert.NotContains(t, logs.String(), "audio_export_success")
			})
		})
	}
}

// TestSaveAudioAction_WakesPromptlyOnConcurrentDatabaseOutcome verifies that when
// SaveAudioAction enters its wait before DatabaseAction settles, it wakes via the
// outcome channel before the 50ms poll interval rather than polling or stalling 5s.
func TestSaveAudioAction_WakesPromptlyOnConcurrentDatabaseOutcome(t *testing.T) {
	const settleDelay = 25 * time.Millisecond
	const pollInterval = 50 * time.Millisecond

	tests := []struct {
		name           string
		settleDB       func(t *testing.T, detCtx *DetectionContext) uint
		wantExported   bool
		wantPreRender  bool
		checkNoteID    bool
		presetNoteID   uint
		wantFinalID    uint
		expectZeroPres bool
	}{
		{
			name: "saved_with_id",
			settleDB: func(t *testing.T, detCtx *DetectionContext) uint {
				t.Helper()
				repo := NewMockDetectionRepository()
				db := newTestDatabaseAction(repo, NewEventTracker(15*time.Second), detCtx, "saved.wav")
				require.NoError(t, db.Execute(t.Context(), nil))
				require.NotZero(t, db.Result.ID)
				return db.Result.ID
			},
			wantExported:  true,
			wantPreRender: true,
			checkNoteID:   true,
		},
		{
			name: "saved_with_zero_id",
			settleDB: func(t *testing.T, detCtx *DetectionContext) uint {
				t.Helper()
				repo := &zeroIDSaveRepository{MockDetectionRepository: NewMockDetectionRepository()}
				db := newTestDatabaseAction(repo, NewEventTracker(15*time.Second), detCtx, "zero_id.wav")
				require.NoError(t, db.Execute(t.Context(), nil))
				require.Zero(t, db.Result.ID)
				return 0
			},
			wantExported:   true,
			wantPreRender:  true,
			presetNoteID:   77,
			wantFinalID:    77,
			expectZeroPres: true,
		},
		{
			name: "rate_limited_skip",
			settleDB: func(t *testing.T, detCtx *DetectionContext) uint {
				t.Helper()
				tracker := NewEventTracker(15 * time.Second)
				repo := NewMockDetectionRepository()
				first := newTestDatabaseAction(repo, tracker, &DetectionContext{}, "first.wav")
				require.NoError(t, first.Execute(t.Context(), nil))

				second := newTestDatabaseAction(repo, tracker, detCtx, "skipped.wav")
				require.NoError(t, second.Execute(t.Context(), nil))
				return 0
			},
			wantExported:  false,
			wantPreRender: false,
		},
		{
			name: "terminal_failure",
			settleDB: func(t *testing.T, detCtx *DetectionContext) uint {
				t.Helper()
				repo := &scriptedSaveRepository{
					MockDetectionRepository: NewMockDetectionRepository(),
					script:                  []error{errors.NewStd("constraint failed")},
				}
				db := newTestDatabaseAction(repo, NewEventTracker(15*time.Second), detCtx, "failed.wav")
				require.Error(t, db.Execute(t.Context(), nil))
				return 0
			},
			wantExported:  false,
			wantPreRender: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				detCtx := &DetectionContext{}
				pre := &recordingPreRenderer{}
				action, tmpDir := newTestSaveAudioAction(t, "concurrent.wav", detCtx, pre)
				if tc.presetNoteID != 0 {
					action.NoteID = tc.presetNoteID
				}

				var savedID uint
				dbDone := make(chan struct{})
				go func() {
					defer close(dbDone)
					time.Sleep(settleDelay)
					savedID = tc.settleDB(t, detCtx)
				}()

				start := time.Now()
				err := action.Execute(t.Context(), nil)
				elapsed := time.Since(start)
				<-dbDone

				require.NoError(t, err)
				assert.Less(t, elapsed, pollInterval, "SaveAudioAction must wake via outcome channel before poll tick")

				outputPath := filepath.Join(tmpDir, "concurrent.wav")
				if tc.wantExported {
					info, statErr := os.Stat(outputPath)
					require.NoError(t, statErr)
					assert.Positive(t, info.Size())
					assert.True(t, detCtx.ClipSaved.Load())
				} else {
					entries, readErr := os.ReadDir(tmpDir)
					require.NoError(t, readErr)
					assert.Empty(t, entries)
					assert.False(t, detCtx.ClipSaved.Load())
				}

				if tc.wantPreRender {
					ids := pre.submittedNoteIDs()
					require.Len(t, ids, 1)
					if tc.checkNoteID {
						assert.Equal(t, savedID, action.NoteID)
						assert.Equal(t, savedID, ids[0])
					}
					if tc.expectZeroPres {
						assert.Equal(t, tc.wantFinalID, action.NoteID)
						assert.Equal(t, tc.wantFinalID, ids[0])
					}
				} else {
					assert.Empty(t, pre.submittedNoteIDs())
				}
			})
		})
	}
}

// TestSaveAudioAction_DeferredExtendedCaptureAbortsWhenDatabaseSettled verifies
// that a deferred Extended Capture job defers while DatabaseAction is pending,
// and aborts cleanly without returning errAudioExportDeferred once DatabaseAction skips.
func TestSaveAudioAction_DeferredExtendedCaptureAbortsWhenDatabaseSettled(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tmpDir := t.TempDir()
		settings := conftest.NewTestSettings().
			WithAudioExport(tmpDir, "wav", "192k").
			Build()

		mgr := audioBuffer.NewManager(GetLogger())
		detCtx := &DetectionContext{}
		action := &SaveAudioAction{
			Settings:     settings,
			ClipName:     "deferred.wav",
			DetectionCtx: detCtx,
			bufferMgr:    mgr,
			sourceID:     "missing-source",
			beginTime:    time.Now(),
			duration:     2,
			readyAt:      time.Now().Add(10 * time.Second),
		}

		// Attempt 1: DatabaseAction is still pending, so SaveAudioAction defers immediately.
		start := time.Now()
		err := action.Execute(t.Context(), nil)
		require.ErrorIs(t, err, errAudioExportDeferred)
		assert.Less(t, time.Since(start), time.Second)

		// DatabaseAction now rate-limits and skips the detection.
		tracker := NewEventTracker(15 * time.Second)
		repo := NewMockDetectionRepository()
		first := newTestDatabaseAction(repo, tracker, &DetectionContext{}, "first.wav")
		require.NoError(t, first.Execute(t.Context(), nil))
		second := newTestDatabaseAction(repo, tracker, detCtx, "deferred.wav")
		require.NoError(t, second.Execute(t.Context(), nil))
		require.True(t, second.skipsLaterSteps())

		// Attempt 2: even with readyAt still in the future (and no capture buffer allocated),
		// SaveAudioAction must abort cleanly instead of returning errAudioExportDeferred or reading bufferMgr.
		require.NoError(t, action.Execute(t.Context(), nil))
		assert.False(t, detCtx.ClipSaved.Load())
	})
}

// TestSaveAudioAction_FailOpenOnRetryableOutageAndNilContext verifies that
// retryable DB outages, nil DetectionCtx, and direct NoteID.Store callers continue
// to export clips as before.
func TestSaveAudioAction_FailOpenOnRetryableOutageAndNilContext(t *testing.T) {
	t.Parallel()

	t.Run("retryable_db_outage_fails_open", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			detCtx := &DetectionContext{}
			repo := &scriptedSaveRepository{
				MockDetectionRepository: NewMockDetectionRepository(),
				script:                  []error{refusedRetrySafeError()},
			}
			db := newTestDatabaseAction(repo, NewEventTracker(15*time.Second), detCtx, "outage.wav")
			db.maxAttempts.Store(5)
			require.Error(t, db.Execute(t.Context(), nil))

			pre := &recordingPreRenderer{}
			action, tmpDir := newTestSaveAudioAction(t, "outage.wav", detCtx, pre)
			require.NoError(t, action.Execute(t.Context(), nil))

			info, err := os.Stat(filepath.Join(tmpDir, "outage.wav"))
			require.NoError(t, err)
			assert.Positive(t, info.Size())
			assert.True(t, detCtx.ClipSaved.Load())
			assert.Equal(t, []uint{0}, pre.submittedNoteIDs())
		})
	})

	t.Run("nil_detection_context_encodes_immediately", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			pre := &recordingPreRenderer{}
			action, tmpDir := newTestSaveAudioAction(t, "nil_ctx.wav", nil, pre)

			start := time.Now()
			require.NoError(t, action.Execute(t.Context(), nil))
			assert.Less(t, time.Since(start), time.Second)

			info, err := os.Stat(filepath.Join(tmpDir, "nil_ctx.wav"))
			require.NoError(t, err)
			assert.Positive(t, info.Size())
		})
	})

	t.Run("direct_note_id_store_without_database_action", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			detCtx := &DetectionContext{}
			pre := &recordingPreRenderer{}
			action, tmpDir := newTestSaveAudioAction(t, "direct_id.wav", detCtx, pre)

			done := make(chan struct{})
			go func() {
				defer close(done)
				time.Sleep(25 * time.Millisecond)
				detCtx.NoteID.Store(99)
			}()

			require.NoError(t, action.Execute(t.Context(), nil))
			<-done

			assert.Equal(t, uint(99), action.NoteID)
			assert.Equal(t, []uint{99}, pre.submittedNoteIDs())
			info, err := os.Stat(filepath.Join(tmpDir, "direct_id.wav"))
			require.NoError(t, err)
			assert.Positive(t, info.Size())
		})
	})
}
