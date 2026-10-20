package processor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	rateBatModel     = "BattyBirdNET"
	rateBirdModel    = "BirdNET"
	rateTest384k     = 384000
	rateTest256k     = 256000
	rateTest192k     = 192000
	rateTest32k      = 32000
	testRateNoteID   = uint(42)
	testRateClipName = "2026/01/clip.wav"

	rateTestConnection = "rate-test-card"
	rateTestSourceID   = "rate-test-source"
)

// newRateRegistry returns a registry knowing one source at the given rate.
func newRateRegistry(t *testing.T, sampleRate int) *audiocore.SourceRegistry {
	t.Helper()
	registry := audiocore.NewSourceRegistry(audiocore.GetLogger())
	_, err := registry.Register(&audiocore.SourceConfig{
		ID:               rateTestSourceID,
		ConnectionString: rateTestConnection,
		DisplayName:      "Rate test",
		Type:             audiocore.SourceTypeAudioCard,
		SampleRate:       sampleRate,
	})
	require.NoError(t, err)
	return registry
}

func TestExportClipRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		isBat      bool
		sourceRate int
		format     string
		want       int
	}{
		{"bat above analysis rate keeps full rate", true, rateTest384k, "flac", rateTest384k},
		{"bat at 256 kHz keeps full rate", true, rateTest256k, "wav", rateTest256k},
		{"bird above analysis rate is downsampled", false, rateTest192k, "wav", conf.SampleRate},
		{"bird at analysis rate is unchanged", false, conf.SampleRate, "wav", conf.SampleRate},
		{"sub-48k wav is unchanged", false, rateTest32k, "wav", rateTest32k},
		{"sub-48k opus is upsampled", false, rateTest32k, "opus", conf.SampleRate},
		{"bat below analysis rate follows the encoder", true, rateTest32k, "opus", conf.SampleRate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, exportClipRate(tt.isBat, tt.sourceRate, tt.format))
		})
	}
}

// TestPlannedClipSampleRate_MatchesExportParams verifies the planned rate stored
// at save time equals the rate the exporter actually writes, for every branch of
// resolveExportParams.
func TestPlannedClipSampleRate_MatchesExportParams(t *testing.T) {
	t.Parallel()

	// The stored name decides the container when its extension is a known export
	// format; settings decide only for a name with no known extension.
	tests := []struct {
		name       string
		clipName   string
		model      string
		sourceRate int
		exportType string
		ultraType  string
	}{
		{"bat 384k flac name", "clip.flac", rateBatModel, rateTest384k, "mp3", "flac"},
		{"bat 256k wav name under opus settings", "clip.wav", rateBatModel, rateTest256k, "opus", "wav"},
		{"bird 192k wav name", "clip.wav", rateBirdModel, rateTest192k, "wav", "flac"},
		{"bird 48k mp3 name", "clip.mp3", rateBirdModel, conf.SampleRate, "mp3", "flac"},
		{"bird 32k wav name under opus settings", "clip.wav", rateBirdModel, rateTest32k, "opus", "flac"},
		{"bird 32k opus name under wav settings", "clip.opus", rateBirdModel, rateTest32k, "wav", "flac"},
		{"bird 32k extensionless name, opus settings", "clip", rateBirdModel, rateTest32k, "opus", "flac"},
		{"bird 32k extensionless name, wav settings", "clip", rateBirdModel, rateTest32k, "wav", "flac"},
		{"bird 32k unknown extension, opus settings", "clip.xyz", rateBirdModel, rateTest32k, "opus", "flac"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := conftest.NewTestSettings().
				WithAudioExport(t.TempDir(), tt.exportType, "128k").
				WithUltrasonicExportType(tt.ultraType).
				Build()
			export := &settings.Realtime.Audio.Export

			result := &detection.Result{
				ClipName:    tt.clipName,
				AudioSource: detection.AudioSource{SampleRate: tt.sourceRate},
				Model:       detection.ModelInfo{Name: tt.model},
			}
			planned := plannedClipSampleRate(result, export)

			action := &SaveAudioAction{
				Settings:         settings,
				pcmData:          make([]byte, tt.sourceRate/10*2),
				sourceSampleRate: tt.sourceRate,
				modelName:        tt.model,
			}
			actual, _, _ := action.resolveExportParams(tt.clipName)
			assert.Equal(t, actual, planned)
		})
	}

	t.Run("no clip plans no rate", func(t *testing.T) {
		t.Parallel()
		export := &conf.ExportSettings{Type: "wav"}
		assert.Zero(t, plannedClipSampleRate(&detection.Result{AudioSource: detection.AudioSource{SampleRate: rateTest384k}}, export))
	})

	t.Run("unknown source rate defaults like the exporter", func(t *testing.T) {
		t.Parallel()
		export := &conf.ExportSettings{Type: "wav"}
		assert.Equal(t, conf.SampleRate, plannedClipSampleRate(&detection.Result{ClipName: testRateClipName}, export))
	})
}

// TestCreateDetection_PlansClipSampleRate drives the real createDetection path,
// where the clip name is resolved after the result is built, and checks the
// result carries the planned export rate: the full rate for a bat capture, 48 kHz
// for a bird capture, and none when export is disabled.
func TestCreateDetection_PlansClipSampleRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		modelID       string
		sourceRate    int
		exportEnabled bool
		want          int
	}{
		{"bat 384 kHz keeps its rate", classifier.RegistryIDBat, rateTest384k, true, rateTest384k},
		{"bird 48 kHz wav", classifier.RegistryIDBirdNETV3, conf.SampleRate, true, conf.SampleRate},
		{"bird 192 kHz is stored at 48 kHz", classifier.RegistryIDBirdNETV3, rateTest192k, true, conf.SampleRate},
		{"export disabled has no clip and no rate", classifier.RegistryIDBat, rateTest384k, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := conftest.NewTestSettings().WithAudioExport(t.TempDir(), "wav", "128k").Build()
			settings.Realtime.Audio.Export.Enabled = tt.exportEnabled
			p := &Processor{Settings: settings, Bn: &classifier.Orchestrator{}}
			p.SetRegistry(newRateRegistry(t, tt.sourceRate))

			item := classifier.Results{
				StartTime: time.Now(),
				Source:    datastore.AudioSource{ID: rateTestConnection},
				ModelID:   tt.modelID,
			}
			res := datastore.Results{Species: "Myotis daubentonii_Daubenton's bat", Confidence: 0.9}
			det := p.createDetection(settings, item, res, "Myotis daubentonii", "Daubenton's bat", "", "")

			assert.Equal(t, tt.exportEnabled, det.Result.ClipName != "")
			assert.Equal(t, tt.want, det.Result.ClipSampleRate)
		})
	}
}

func TestNormalizeDetectionTimes_ExtendedCapture_RecomputesClipSampleRate(t *testing.T) {
	t.Parallel()

	firstDetected := time.Date(2026, 3, 7, 20, 0, 0, 0, time.UTC)
	p := &Processor{Settings: &conf.Settings{}}
	p.Settings.Realtime.Audio.Export = conf.ExportSettings{Enabled: true, Length: 60, PreCapture: 6, Type: "wav", UltrasonicType: "flac"}
	p.Settings.Realtime.ExtendedCapture = conf.ExtendedCaptureSettings{Enabled: true, MaxDuration: 600}
	item := &PendingDetection{
		Detection: Detections{Result: detection.Result{
			BeginTime:   firstDetected.Add(30 * time.Second),
			EndTime:     firstDetected.Add(84 * time.Second),
			Timestamp:   firstDetected,
			Species:     detection.Species{CommonName: "bat", ScientificName: "Myotis daubentonii"},
			AudioSource: detection.AudioSource{SampleRate: rateTest384k},
			Model:       detection.ModelInfo{Name: rateBatModel},
		}},
		Confidence:      0.92,
		Source:          "test_source",
		FirstDetected:   firstDetected,
		LastUpdated:     firstDetected.Add(3 * time.Minute),
		ExtendedCapture: true,
	}
	p.normalizeDetectionTimes(item, p.currentSettings())

	require.NotEmpty(t, item.Detection.Result.ClipName)
	assert.Equal(t, rateTest384k, item.Detection.Result.ClipSampleRate)
}

// recordingRateRecorder captures RecordClipSampleRate calls. It embeds the
// repository interface (nil) so it can stand in as Processor.Repo; only
// RecordClipSampleRate is implemented.
type recordingRateRecorder struct {
	datastore.DetectionRepository
	mu     sync.Mutex
	calls  []rateCall
	ctxVal []any
	// remaining is the time left to each call's context deadline; 0 means the
	// context had none.
	remaining []time.Duration
	err       error
}

type rateCall struct {
	noteID uint
	clip   string
	rate   int
}

// rateCtxKey keys the marker the correction test puts on the job context.
type rateCtxKey struct{}

func (r *recordingRateRecorder) RecordClipSampleRate(ctx context.Context, noteID uint, clipName string, rate int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, rateCall{noteID, clipName, rate})
	r.ctxVal = append(r.ctxVal, ctx.Value(rateCtxKey{}))
	var left time.Duration
	if deadline, ok := ctx.Deadline(); ok {
		left = time.Until(deadline)
	}
	r.remaining = append(r.remaining, left)
	return r.err
}

func TestSaveAudioAction_CorrectsClipSampleRate(t *testing.T) {
	t.Parallel()

	const wantOpusRate = conf.SampleRate
	tests := []struct {
		name        string
		clipName    string
		sourceRate  int
		exportType  string
		plannedRate int
		noteID      uint
		recordErr   error
		wantCalls   []rateCall
	}{
		{"equal rates make no call", "clip.wav", rateTest256k, "wav", rateTest256k, testRateNoteID, nil, nil},
		// A resample that failed at planning time or an older plan leaves the stored
		// rate different from what the encoder writes for the named container.
		{"written rate differs from the plan", "clip.wav", rateTest32k, "opus", wantOpusRate, testRateNoteID, nil,
			[]rateCall{{testRateNoteID, "clip.wav", rateTest32k}}},
		// A name with no known extension takes its format from the settings at encode time.
		{"extensionless name follows the settings format", "clip", rateTest32k, "opus", rateTest32k, testRateNoteID, nil,
			[]rateCall{{testRateNoteID, "clip", wantOpusRate}}},
		{"note id 0 makes no call", "clip.wav", rateTest32k, "opus", wantOpusRate, 0, nil, nil},
		{"recorder error does not fail the action", "clip.wav", rateTest32k, "opus", wantOpusRate, testRateNoteID, errors.NewStd("db read-only"),
			[]rateCall{{testRateNoteID, "clip.wav", rateTest32k}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := conftest.NewTestSettings().WithAudioExport(t.TempDir(), tt.exportType, "128k").Build()
			rec := &recordingRateRecorder{err: tt.recordErr}
			action := &SaveAudioAction{
				Settings:         settings,
				ClipName:         tt.clipName,
				pcmData:          make([]byte, tt.sourceRate/10*2),
				sourceSampleRate: tt.sourceRate,
				modelName:        rateBirdModel,
				NoteID:           tt.noteID,
				clipRates:        rec,
				plannedRate:      tt.plannedRate,
				CorrelationID:    "rate-correction",
			}
			if tt.sourceRate == rateTest256k {
				action.modelName = rateBatModel
			}

			const jobMarker = "job-ctx"
			ctx := context.WithValue(t.Context(), rateCtxKey{}, jobMarker)
			require.NoError(t, action.Execute(ctx, nil))
			assert.Equal(t, tt.wantCalls, rec.calls)
			for _, v := range rec.ctxVal {
				assert.Equal(t, jobMarker, v, "the write runs under the job context")
			}
			for _, left := range rec.remaining {
				assert.NotZero(t, left, "the write carries its own deadline")
				assert.LessOrEqual(t, left, clipRateCorrectionTimeout, "the deadline is the correction bound")
			}
		})
	}
}

// TestBuildSaveAudioAction_CarriesClipSampleRateWiring verifies the save action
// gets the planned rate from the result and the processor's repository as its
// clip sample rate recorder.
func TestBuildSaveAudioAction_CarriesClipSampleRateWiring(t *testing.T) {
	t.Parallel()

	settings := conftest.NewTestSettings().WithAudioExport(t.TempDir(), "wav", "128k").Build()
	det := &Detections{Result: detection.Result{ClipName: testRateClipName, ClipSampleRate: rateTest384k}}

	rec := &recordingRateRecorder{}
	p := &Processor{Repo: rec}
	action := p.buildSaveAudioAction(det, nil, settings)
	assert.Equal(t, rateTest384k, action.plannedRate)
	assert.Same(t, rec, action.clipRates, "the action records through the processor's repository")
}

// TestSaveAudioAction_NilClipRatesSkipsCorrection verifies a processor without
// a repository (nil recorder) makes the correction a no-op even when the planned
// and written rates differ.
func TestSaveAudioAction_NilClipRatesSkipsCorrection(t *testing.T) {
	t.Parallel()

	settings := conftest.NewTestSettings().WithAudioExport(t.TempDir(), "opus", "128k").Build()
	action := &SaveAudioAction{
		Settings:         settings,
		ClipName:         "clip.wav",
		pcmData:          make([]byte, rateTest32k/10*2),
		sourceSampleRate: rateTest32k,
		modelName:        rateBirdModel,
		NoteID:           testRateNoteID,
		plannedRate:      conf.SampleRate, // differs from the 32 kHz the wav name writes
		CorrelationID:    "nil-recorder",
	}

	require.NotPanics(t, func() { require.NoError(t, action.Execute(t.Context(), nil)) })
}
