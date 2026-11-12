package processor

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/detection"
)

const (
	denoiseMarkNoteID   = 42
	denoiseMarkClipName = "clip.wav"
)

// markCall is one RecordClipDenoised call.
type markCall struct {
	noteID   uint
	clipName string
	denoised bool
}

// markRecorder captures RecordClipDenoised calls.
type markRecorder struct {
	mu    sync.Mutex
	calls []markCall
	err   error
}

func (r *markRecorder) RecordClipDenoised(_ context.Context, id uint, clipName string, denoised bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, markCall{id, clipName, denoised})
	return r.err
}

func (r *markRecorder) recorded() []markCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// newMarkedExportAction builds an export action wired to a shared detection
// context and a marker recorder. The database action has not saved yet, so the
// context carries no note ID.
func newMarkedExportAction(t *testing.T, denoiseOn bool, pcm []byte) (*SaveAudioAction, *DetectionContext, *markRecorder) {
	t.Helper()
	a, _ := newDenoiseAction(t, t.TempDir(), denoiseMarkClipName, pcm, conf.SampleRate, false, denoiseOn)
	detectionCtx := &DetectionContext{}
	marks := &markRecorder{}
	a.DetectionCtx = detectionCtx
	a.clipMarks = marks
	return a, detectionCtx, marks
}

// The export action records the marker after the clip is encoded: true only for
// a clip it denoised, false for a clip written as captured whatever the reason.
func TestSaveAudioAction_RecordsClipDenoised(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		denoiseOn bool
		pcm       func() []byte
		denoise   pcmDenoiser
		want      bool
	}{
		{
			name:      "denoised",
			denoiseOn: true,
			pcm:       func() []byte { return noisyPCM(conf.SampleRate, 1.5) },
			want:      true,
		},
		{
			name:      "setting off",
			denoiseOn: false,
			pcm:       func() []byte { return noisyPCM(conf.SampleRate, 1.5) },
			want:      false,
		},
		{
			name:      "denoise failed",
			denoiseOn: true,
			pcm:       func() []byte { return noisyPCM(conf.SampleRate, 1.5) },
			denoise: func(context.Context, []byte, int, int, string) ([]byte, error) {
				return nil, errTestEncodeFailed
			},
			want: false,
		},
		{
			name:      "over the frame budget",
			denoiseOn: true,
			pcm:       func() []byte { return make([]byte, (maxExportDenoiseFrames+1)*conf.BytesPerSample) },
			want:      false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, detectionCtx, marks := newMarkedExportAction(t, tc.denoiseOn, tc.pcm())
			if tc.denoise != nil {
				a.denoise = tc.denoise
			}
			detectionCtx.NoteID.Store(denoiseMarkNoteID)

			require.NoError(t, a.Execute(t.Context(), nil))

			assert.Equal(t, []markCall{{denoiseMarkNoteID, denoiseMarkClipName, tc.want}}, marks.recorded())
		})
	}
}

// A detection without a marker recorder (the legacy store) exports as before.
func TestSaveAudioAction_NilClipMarksIsNoOp(t *testing.T) {
	t.Parallel()
	a, detectionCtx, _ := newMarkedExportAction(t, true, noisyPCM(conf.SampleRate, 1.5))
	a.clipMarks = nil
	detectionCtx.NoteID.Store(denoiseMarkNoteID)

	require.NoError(t, a.Execute(t.Context(), nil))
}

// A clip can be encoded before its row exists: the export action waits a bounded
// time for the note ID and the save can take longer. Whichever of the export and
// the database action finishes last must write the marker.
func TestClipDenoisedIsRecordedByTheLastWriter(t *testing.T) {
	t.Parallel()

	newDatabaseAction := func(detectionCtx *DetectionContext, marks *markRecorder) *DatabaseAction {
		det := testDetection()
		det.Result.ClipName = denoiseMarkClipName
		return &DatabaseAction{
			Settings:     &conf.Settings{},
			Ds:           NewActionMockDatastore(),
			Result:       det.Result,
			Results:      det.Results,
			EventTracker: NewEventTracker(testEventTrackerInterval),
			DetectionCtx: detectionCtx,
			clipMarks:    marks,
		}
	}

	t.Run("export finishes before the row exists", func(t *testing.T) {
		t.Parallel()
		a, detectionCtx, marks := newMarkedExportAction(t, true, noisyPCM(conf.SampleRate, 1.5))
		// The export waits out its bounded note ID wait before it encodes.
		require.NoError(t, a.Execute(t.Context(), nil))
		assert.Empty(t, marks.recorded(), "there is no row to mark yet")

		db := newDatabaseAction(detectionCtx, marks)
		require.NoError(t, db.Execute(t.Context(), nil))

		require.NotZero(t, db.Result.ID)
		assert.Equal(t, []markCall{{db.Result.ID, denoiseMarkClipName, true}}, marks.recorded())
	})

	t.Run("row exists before the export finishes", func(t *testing.T) {
		t.Parallel()
		a, detectionCtx, marks := newMarkedExportAction(t, true, noisyPCM(conf.SampleRate, 1.5))

		db := newDatabaseAction(detectionCtx, marks)
		require.NoError(t, db.Execute(t.Context(), nil))
		assert.Empty(t, marks.recorded(), "the clip has not been encoded yet")

		require.NoError(t, a.Execute(t.Context(), nil))

		assert.Equal(t, []markCall{{db.Result.ID, denoiseMarkClipName, true}}, marks.recorded())
	})
}

// recordClipDenoise writes only when both the export state and the note ID are
// present, and a failed write never fails the action that called it.
func TestDetectionContext_RecordClipDenoise(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		state    int32
		noteID   uint64
		clipName string
		nilRec   bool
		recErr   error
		want     []markCall
	}{
		{"state and note ID", clipDenoiseDenoised, denoiseMarkNoteID, denoiseMarkClipName, false, nil,
			[]markCall{{denoiseMarkNoteID, denoiseMarkClipName, true}}},
		{"raw clip", clipDenoiseRaw, denoiseMarkNoteID, denoiseMarkClipName, false, nil,
			[]markCall{{denoiseMarkNoteID, denoiseMarkClipName, false}}},
		{"state not set", clipDenoiseUnset, denoiseMarkNoteID, denoiseMarkClipName, false, nil, nil},
		{"no note ID", clipDenoiseDenoised, 0, denoiseMarkClipName, false, nil, nil},
		{"no clip name", clipDenoiseDenoised, denoiseMarkNoteID, "", false, nil, nil},
		{"nil recorder", clipDenoiseDenoised, denoiseMarkNoteID, denoiseMarkClipName, true, nil, nil},
		{"write error is only logged", clipDenoiseDenoised, denoiseMarkNoteID, denoiseMarkClipName, false, errTestEncodeFailed,
			[]markCall{{denoiseMarkNoteID, denoiseMarkClipName, true}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := &DetectionContext{}
			d.clipDenoise.Store(tc.state)
			d.NoteID.Store(tc.noteID)
			marks := &markRecorder{err: tc.recErr}
			var rec clipDenoiseRecorder = marks
			if tc.nilRec {
				rec = nil
			}

			d.recordClipDenoise(t.Context(), rec, tc.clipName)

			assert.Equal(t, tc.want, marks.recorded())
		})
	}
}

// The processor hands its marker recorder to both actions that write the marker,
// and to nothing when none is set.
func TestProcessor_WiresClipDenoiseRecorder(t *testing.T) {
	t.Parallel()
	settings := conftest.NewTestSettings().WithAudioExport(t.TempDir(), "wav", "128k").Build()
	settings.Output.SQLite.Enabled = true
	det := &Detections{Result: detection.Result{ClipName: denoiseMarkClipName}}

	for _, withRecorder := range []bool{true, false} {
		p := &Processor{}
		marks := &markRecorder{}
		if withRecorder {
			p.SetClipDenoiseRecorder(marks)
		}

		var save *SaveAudioAction
		var dbAction *DatabaseAction
		for _, action := range p.getDefaultActions(det, settings) {
			switch act := action.(type) {
			case *SaveAudioAction:
				save = act
			case *DatabaseAction:
				dbAction = act
			}
		}
		require.NotNil(t, save)
		require.NotNil(t, dbAction)

		if withRecorder {
			assert.Same(t, marks, save.clipMarks)
			assert.Same(t, marks, dbAction.clipMarks)
		} else {
			assert.Nil(t, save.clipMarks)
			assert.Nil(t, dbAction.clipMarks)
		}
	}
}
