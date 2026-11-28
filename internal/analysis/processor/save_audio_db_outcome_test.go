package processor

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// Submit implements PreRendererSubmit.
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

// Stop implements PreRendererSubmit.
func (r *recordingPreRenderer) Stop() {}

func (r *recordingPreRenderer) submittedNoteIDs() []uint {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.noteIDs)
}

// zeroIDSaveRepository succeeds on Save without assigning result.ID (defensive #2453 path).
type zeroIDSaveRepository struct {
	*MockDetectionRepository
}

func (r *zeroIDSaveRepository) Save(_ context.Context, _ *detection.Result, _ []detection.AdditionalResult) error {
	return nil
}

// unknownOutcomeConnectivityError simulates a connectivity failure during COMMIT
// (MySQL "invalid connection" without ErrDetectionSaveRetrySafe) where the row
// may already be persisted.
func unknownOutcomeConnectivityError() error {
	return fmt.Errorf("failed to commit detection: %w", errors.NewStd("invalid connection"))
}

// unmarkedCommitError is a COMMIT-phase failure that is neither a connectivity
// error nor marked ErrDetectionSaveRetrySafe: the row may exist, so the clip
// must be exported.
func unmarkedCommitError() error {
	return fmt.Errorf("failed to commit detection: %w", errors.NewStd("disk I/O error"))
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

func runRateLimitedDatabaseSkip(ctx context.Context, detCtx *DetectionContext, clipName string) error {
	tracker := NewEventTracker(testEventTrackerInterval)
	repo := NewMockDetectionRepository()

	first := newTestDatabaseAction(repo, tracker, &DetectionContext{}, "first.wav")
	if err := first.Execute(ctx, nil); err != nil {
		return fmt.Errorf("first save: %w", err)
	}

	second := newTestDatabaseAction(repo, tracker, detCtx, clipName)
	if err := second.Execute(ctx, nil); err != nil {
		return fmt.Errorf("second save: %w", err)
	}
	if !second.skipsLaterSteps() {
		return errors.NewStd("expected second DatabaseAction to skip via EventTracker")
	}
	return nil
}

func runTerminalDatabaseFailure(ctx context.Context, detCtx *DetectionContext, clipName, errMsg string) error {
	repo := &scriptedSaveRepository{
		MockDetectionRepository: NewMockDetectionRepository(),
		script:                  []error{fmt.Errorf("failed to save detection: %w: %w", datastore.ErrDetectionSaveRetrySafe, errors.NewStd(errMsg))},
	}
	db := newTestDatabaseAction(repo, NewEventTracker(testEventTrackerInterval), detCtx, clipName)
	if err := db.Execute(ctx, nil); err == nil {
		return errors.NewStd("expected terminal database save error, got nil")
	}
	return nil
}

// TestSaveAudioAction_AbortsWhenDatabaseSkippedOrFailed verifies that SaveAudioAction
// skips audio encoding, file creation, and spectrogram pre-rendering without waiting
// for noteIDWaitTimeout when DatabaseAction rate-limits or terminally fails.
// Not parallel because logtest.CaptureBuffer swaps logger.Global().
func TestSaveAudioAction_AbortsWhenDatabaseSkippedOrFailed(t *testing.T) {
	tests := []struct {
		name          string
		settleDB      func(ctx context.Context, detCtx *DetectionContext) error
		wantOperation string
	}{
		{
			name: "rate_limited_skip",
			settleDB: func(ctx context.Context, detCtx *DetectionContext) error {
				return runRateLimitedDatabaseSkip(ctx, detCtx, "skipped.wav")
			},
			wantOperation: "audio_export_db_skipped",
		},
		{
			name: "terminal_db_failure",
			settleDB: func(ctx context.Context, detCtx *DetectionContext) error {
				return runTerminalDatabaseFailure(ctx, detCtx, "failed.wav", "disk full")
			},
			wantOperation: "audio_export_db_failed",
		},
		{
			name:          "retry_exhausted_db_failure",
			wantOperation: "audio_export_db_failed",
			settleDB: func(ctx context.Context, detCtx *DetectionContext) error {
				repo := &scriptedSaveRepository{
					MockDetectionRepository: NewMockDetectionRepository(),
					script:                  []error{refusedRetrySafeError(), refusedRetrySafeError()},
				}
				db := newTestDatabaseAction(repo, NewEventTracker(testEventTrackerInterval), detCtx, "exhausted.wav")
				db.maxAttempts.Store(2)
				if err := db.Execute(ctx, nil); err == nil {
					return errors.NewStd("expected first retryable error, got nil")
				}
				if err := db.Execute(ctx, nil); err == nil {
					return errors.NewStd("expected retry-exhausted error, got nil")
				}
				return nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logs := logtest.CaptureBuffer(t)
			synctest.Test(t, func(t *testing.T) {
				detCtx := &DetectionContext{}
				require.NoError(t, tc.settleDB(t.Context(), detCtx))

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
				assert.Contains(t, logs.String(), tc.wantOperation, "the log names the verdict that stopped the export")
			})
		})
	}
}

// TestSaveAudioAction_WakesPromptlyOnConcurrentDatabaseOutcome verifies that when
// SaveAudioAction enters its wait before DatabaseAction settles, it wakes on the
// outcome channel instead of waiting out noteIDWaitTimeout. The verdict lands
// halfway through the wait, so a missed wake shows as the full timeout.
func TestSaveAudioAction_WakesPromptlyOnConcurrentDatabaseOutcome(t *testing.T) {
	const settleDelay = noteIDWaitTimeout / 2

	tests := []struct {
		name         string
		settleDB     func(ctx context.Context, detCtx *DetectionContext) (uint, error)
		wantExported bool
		presetNoteID uint
	}{
		{
			name: "saved_with_id",
			settleDB: func(ctx context.Context, detCtx *DetectionContext) (uint, error) {
				repo := NewMockDetectionRepository()
				db := newTestDatabaseAction(repo, NewEventTracker(testEventTrackerInterval), detCtx, "saved.wav")
				if err := db.Execute(ctx, nil); err != nil {
					return 0, err
				}
				if db.Result.ID == 0 {
					return 0, errors.NewStd("expected non-zero saved Result.ID")
				}
				return db.Result.ID, nil
			},
			wantExported: true,
		},
		{
			name: "saved_with_zero_id",
			settleDB: func(ctx context.Context, detCtx *DetectionContext) (uint, error) {
				repo := &zeroIDSaveRepository{MockDetectionRepository: NewMockDetectionRepository()}
				db := newTestDatabaseAction(repo, NewEventTracker(testEventTrackerInterval), detCtx, "zero_id.wav")
				if err := db.Execute(ctx, nil); err != nil {
					return 0, err
				}
				if db.Result.ID != 0 {
					return 0, fmt.Errorf("expected zero Result.ID, got %d", db.Result.ID)
				}
				return 0, nil
			},
			wantExported: true,
			presetNoteID: 77,
		},
		{
			name: "rate_limited_skip",
			settleDB: func(ctx context.Context, detCtx *DetectionContext) (uint, error) {
				return 0, runRateLimitedDatabaseSkip(ctx, detCtx, "skipped.wav")
			},
			wantExported: false,
		},
		{
			name: "terminal_failure",
			settleDB: func(ctx context.Context, detCtx *DetectionContext) (uint, error) {
				return 0, runTerminalDatabaseFailure(ctx, detCtx, "failed.wav", "constraint failed")
			},
			wantExported: false,
		},
		{
			name: "outcome_unknown_connectivity_error",
			settleDB: func(ctx context.Context, detCtx *DetectionContext) (uint, error) {
				repo := &scriptedSaveRepository{
					MockDetectionRepository: NewMockDetectionRepository(),
					script:                  []error{unknownOutcomeConnectivityError()},
				}
				db := newTestDatabaseAction(repo, NewEventTracker(testEventTrackerInterval), detCtx, "unknown.wav")
				if err := db.Execute(ctx, nil); err == nil {
					return 0, errors.NewStd("expected connectivity error, got nil")
				}
				return 0, nil
			},
			wantExported: true,
		},
		{
			name: "unmarked_terminal_error_exports",
			settleDB: func(ctx context.Context, detCtx *DetectionContext) (uint, error) {
				repo := &scriptedSaveRepository{
					MockDetectionRepository: NewMockDetectionRepository(),
					script:                  []error{unmarkedCommitError()},
				}
				db := newTestDatabaseAction(repo, NewEventTracker(testEventTrackerInterval), detCtx, "unmarked.wav")
				if err := db.Execute(ctx, nil); err == nil {
					return 0, errors.NewStd("expected commit error, got nil")
				}
				return 0, nil
			},
			wantExported: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				detCtx := &DetectionContext{}
				pre := &recordingPreRenderer{}
				action, tmpDir := newTestSaveAudioAction(t, "concurrent.wav", detCtx, pre)
				if tc.presetNoteID != 0 {
					action.NoteID = tc.presetNoteID
				}

				var savedID uint
				var settleErr error
				dbDone := make(chan struct{})
				go func() {
					defer close(dbDone)
					time.Sleep(settleDelay)
					savedID, settleErr = tc.settleDB(ctx, detCtx)
				}()

				start := time.Now()
				err := action.Execute(ctx, nil)
				elapsed := time.Since(start)
				<-dbDone

				require.NoError(t, settleErr)
				require.NoError(t, err)
				assert.Less(t, elapsed, noteIDWaitTimeout, "SaveAudioAction must wake on the database verdict, not the timeout")

				outputPath := filepath.Join(tmpDir, "concurrent.wav")
				if tc.wantExported {
					info, statErr := os.Stat(outputPath)
					require.NoError(t, statErr)
					assert.Positive(t, info.Size())
					assert.True(t, detCtx.ClipSaved.Load())

					wantID := cmp.Or(savedID, tc.presetNoteID)
					assert.Equal(t, wantID, action.NoteID)
					assert.Equal(t, []uint{wantID}, pre.submittedNoteIDs())
				} else {
					entries, readErr := os.ReadDir(tmpDir)
					require.NoError(t, readErr)
					assert.Empty(t, entries)
					assert.False(t, detCtx.ClipSaved.Load())
					assert.Empty(t, pre.submittedNoteIDs())
				}
			})
		})
	}
}

// TestSaveAudioAction_DeferredExtendedCaptureAbortsWhenDatabaseSettled verifies
// that a deferred Extended Capture job defers while DatabaseAction is pending,
// and aborts cleanly without returning errAudioExportDeferred once DatabaseAction
// settles as skipped or terminally failed.
func TestSaveAudioAction_DeferredExtendedCaptureAbortsWhenDatabaseSettled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settleDB func(ctx context.Context, detCtx *DetectionContext) error
	}{
		{
			name: "rate_limited_skip",
			settleDB: func(ctx context.Context, detCtx *DetectionContext) error {
				return runRateLimitedDatabaseSkip(ctx, detCtx, "deferred.wav")
			},
		},
		{
			name: "terminal_db_failure",
			settleDB: func(ctx context.Context, detCtx *DetectionContext) error {
				return runTerminalDatabaseFailure(ctx, detCtx, "deferred.wav", "disk full")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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

				// DatabaseAction settles as skipped or terminally failed.
				require.NoError(t, tc.settleDB(t.Context(), detCtx))

				// Attempt 2: even with readyAt still in the future (and no capture buffer allocated),
				// SaveAudioAction must abort cleanly instead of returning errAudioExportDeferred or reading bufferMgr.
				require.NoError(t, action.Execute(t.Context(), nil))
				assert.False(t, detCtx.ClipSaved.Load())
			})
		})
	}
}

// TestSaveAudioAction_FailOpenOnRetryableOutageAndNilContext verifies that
// retryable DB outages, unknown save outcomes, a nil DetectionCtx and a context
// cancelled while the verdict is pending all export the clip.
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
			db := newTestDatabaseAction(repo, NewEventTracker(testEventTrackerInterval), detCtx, "outage.wav")
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

	t.Run("outcome_unknown_connectivity_error_exports_immediately", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			detCtx := &DetectionContext{}
			repo := &scriptedSaveRepository{
				MockDetectionRepository: NewMockDetectionRepository(),
				script:                  []error{unknownOutcomeConnectivityError()},
			}
			db := newTestDatabaseAction(repo, NewEventTracker(testEventTrackerInterval), detCtx, "unknown.wav")
			require.Error(t, db.Execute(t.Context(), nil))

			pre := &recordingPreRenderer{}
			action, tmpDir := newTestSaveAudioAction(t, "unknown.wav", detCtx, pre)
			start := time.Now()
			require.NoError(t, action.Execute(t.Context(), nil))
			assert.Less(t, time.Since(start), time.Second)

			info, err := os.Stat(filepath.Join(tmpDir, "unknown.wav"))
			require.NoError(t, err)
			assert.Positive(t, info.Size())
			assert.True(t, detCtx.ClipSaved.Load())
			assert.Equal(t, []uint{0}, pre.submittedNoteIDs())
		})
	})

	t.Run("panicked_save_exports_immediately", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			detCtx := &DetectionContext{}
			repo := &panickingSaveRepository{MockDetectionRepository: NewMockDetectionRepository()}
			db := newTestDatabaseAction(repo, NewEventTracker(testEventTrackerInterval), detCtx, "panicked.wav")
			assert.Panics(t, func() {
				_ = db.Execute(t.Context(), nil)
			})

			pre := &recordingPreRenderer{}
			action, tmpDir := newTestSaveAudioAction(t, "panicked.wav", detCtx, pre)
			start := time.Now()
			require.NoError(t, action.Execute(t.Context(), nil))
			assert.Less(t, time.Since(start), time.Second)

			info, err := os.Stat(filepath.Join(tmpDir, "panicked.wav"))
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

	t.Run("context_cancelled_while_pending_exports", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			detCtx := &DetectionContext{}
			pre := &recordingPreRenderer{}
			action, tmpDir := newTestSaveAudioAction(t, "cancelled.wav", detCtx, pre)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			go func() {
				time.Sleep(noteIDWaitTimeout / 2)
				cancel()
			}()

			start := time.Now()
			require.NoError(t, action.Execute(ctx, nil))
			assert.Less(t, time.Since(start), noteIDWaitTimeout, "a cancelled context must end the wait")

			info, err := os.Stat(filepath.Join(tmpDir, "cancelled.wav"))
			require.NoError(t, err)
			assert.Positive(t, info.Size())
		})
	})
}
