package processor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
)

const (
	driftBirdSourceID = "drift_bird_src"
	driftBatSourceID  = "drift_bat_src"
	driftBirdRate     = 48000
	driftBatRate      = 96000
	// driftPCMSeconds is long enough for every encoder to accept the clip and
	// short enough to keep the 500-row invariant table fast.
	driftPCMSeconds = 0.2
	driftSineHz     = 1000
	driftSineAmp    = 8000
	// driftRetryPCMSeconds is the PCM length for the invariant table, which only
	// resolves parameters and resamples.
	driftRetryPCMSeconds = 0.05
)

// driftExportSettings builds an export settings snapshot for the drift tests.
func driftExportSettings(dir, exportType, ultrasonicType, bitrate string) *conf.Settings {
	return &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Export: conf.ExportSettings{
					Enabled:        true,
					Path:           dir,
					Type:           exportType,
					UltrasonicType: ultrasonicType,
					Bitrate:        bitrate,
					Length:         3,
				},
			},
		},
	}
}

// newDriftProcessor returns a Processor whose registry knows a 48 kHz bird
// source and a 96 kHz bat source.
func newDriftProcessor(t *testing.T) *Processor {
	t.Helper()
	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	for _, src := range []struct {
		id   string
		rate int
	}{{driftBirdSourceID, driftBirdRate}, {driftBatSourceID, driftBatRate}} {
		_, err := reg.Register(&audiocore.SourceConfig{
			ID:               src.id,
			Type:             audiocore.SourceTypeRTSP,
			ConnectionString: "rtsp://cam/" + src.id,
			DisplayName:      src.id,
			SampleRate:       src.rate,
			BitDepth:         16,
			Channels:         1,
		})
		require.NoError(t, err)
	}
	p := &Processor{Settings: &conf.Settings{}}
	p.registry = reg
	return p
}

// TestSaveAudioAction_ExportTypeChangedAfterNaming_WritesStoredClipName names a
// detection under one export configuration and exports it under another, as
// happens when the user switches the export format while the detection is
// pending. The file must land at exactly the stored ClipName.
func TestSaveAudioAction_ExportTypeChangedAfterNaming_WritesStoredClipName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		modelID       string
		sourceID      string
		rate          int
		nameType      string
		nameUltra     string
		execType      string
		execUltra     string
		execBitrate   string
		wantExt       string
		wantMagicOff  int
		wantMagic     string
		wantKeepsRate bool
	}{
		{name: "bird aac then flac", modelID: classifier.RegistryIDBirdNETV3, sourceID: driftBirdSourceID, rate: driftBirdRate,
			nameType: "aac", nameUltra: "flac", execType: "flac", execUltra: "flac", execBitrate: "",
			wantExt: ".m4a", wantMagicOff: 4, wantMagic: "ftyp"},
		{name: "bird flac then aac", modelID: classifier.RegistryIDBirdNETV3, sourceID: driftBirdSourceID, rate: driftBirdRate,
			nameType: "flac", nameUltra: "flac", execType: "aac", execUltra: "flac", execBitrate: "96k",
			wantExt: ".flac", wantMagicOff: 0, wantMagic: "fLaC"},
		{name: "bat ultrasonic flac then wav", modelID: classifier.RegistryIDBat, sourceID: driftBatSourceID, rate: driftBatRate,
			nameType: "mp3", nameUltra: "flac", execType: "mp3", execUltra: "wav", execBitrate: "128k",
			wantExt: ".flac", wantMagicOff: 0, wantMagic: "fLaC", wantKeepsRate: true},
		{name: "bat ultrasonic wav then flac", modelID: classifier.RegistryIDBat, sourceID: driftBatSourceID, rate: driftBatRate,
			nameType: "mp3", nameUltra: "wav", execType: "mp3", execUltra: "flac", execBitrate: "128k",
			wantExt: ".wav", wantMagicOff: 0, wantMagic: "RIFF", wantKeepsRate: true},
		{name: "bat type aac then flac with ultrasonic flac unchanged", modelID: classifier.RegistryIDBat, sourceID: driftBatSourceID, rate: driftBatRate,
			nameType: "aac", nameUltra: "flac", execType: "flac", execUltra: "flac", execBitrate: "",
			wantExt: ".flac", wantMagicOff: 0, wantMagic: "fLaC", wantKeepsRate: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			p := newDriftProcessor(t)
			nameSettings := driftExportSettings(dir, tt.nameType, tt.nameUltra, "96k")
			execSettings := driftExportSettings(dir, tt.execType, tt.execUltra, tt.execBitrate)

			res := p.createDetectionResult(nameSettings, time.Now(), time.Now(), time.Now(),
				"Parus major", "Great tit", "parmaj", "Parus major_Great Tit", 0.9,
				datastore.AudioSource{ID: tt.sourceID}, "", 0, 0, tt.modelID, "Parus major")
			res.ClipName = p.resolveClipName(nameSettings, &res, "Parus major", 0.9)
			require.Equal(t, tt.wantExt, filepath.Ext(res.ClipName), "the name is built under the naming settings")

			det := Detections{Result: res, CorrelationID: "drift-test"}
			a := p.buildSaveAudioAction(&det, nil, execSettings)
			a.pcmData = sinePCMAtRate(tt.rate, driftPCMSeconds, driftSineHz, driftSineAmp)
			require.NoError(t, a.Execute(t.Context(), nil))

			out := filepath.Join(dir, res.ClipName)
			assertNonEmptyFileWithMagic(t, out, tt.wantMagicOff, tt.wantMagic)
			if tt.wantKeepsRate {
				switch tt.wantExt {
				case ".flac":
					assert.Equal(t, tt.rate, flacSampleRate(t, out))
				case ".wav":
					assert.Equal(t, tt.rate, wavSampleRate(t, out))
				}
			}

			var files []string
			require.NoError(t, filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					files = append(files, path)
				}
				return err
			}))
			assert.Equal(t, []string{out}, files, "only the file named by the stored ClipName may exist")
		})
	}
}

// TestResolveExportParams_PathEqualsStoredNameAcrossSettingsChanges checks the
// path-identity invariant over every pairing of naming and export settings and a
// set of capture shapes: the path comes back unchanged, the format is the one
// the name's extension records, the rate is one the encoder accepts, ultrasonic
// bat audio with a lossless name keeps its rate, and a retried call agrees with
// the first.
func TestResolveExportParams_PathEqualsStoredNameAcrossSettingsChanges(t *testing.T) {
	t.Parallel()

	type settingsPair struct{ exportType, ultrasonic string }
	pairs := make([]settingsPair, 0, 10)
	for _, typ := range []string{audioformat.WAV, audioformat.FLAC, audioformat.MP3, audioformat.AAC, audioformat.Opus} {
		for _, ultra := range []string{audioformat.WAV, audioformat.FLAC} {
			pairs = append(pairs, settingsPair{typ, ultra})
		}
	}
	captures := []struct {
		name  string
		isBat bool
		rate  int
	}{
		{"bird22050", false, 22050}, {"bird48000", false, driftBirdRate}, {"bird96000", false, driftBatRate},
		{"bat22050", true, 22050}, {"bat96000", true, driftBatRate},
	}

	p := &Processor{Settings: &conf.Settings{}}
	dir := t.TempDir()
	for _, nameCfg := range pairs {
		for _, execCfg := range pairs {
			for _, c := range captures {
				label := fmt.Sprintf("name=%v exec=%v %s", nameCfg, execCfg, c.name)
				nameSettings := driftExportSettings(dir, nameCfg.exportType, nameCfg.ultrasonic, "96k")
				execSettings := driftExportSettings(dir, execCfg.exportType, execCfg.ultrasonic, "96k")

				res := detection.Result{AudioSource: detection.AudioSource{SampleRate: c.rate}}
				modelName := ""
				if c.isBat {
					res.Model = classifier.DetectionModelInfoForID(classifier.RegistryIDBat)
					modelName = res.Model.Name
				}
				name := p.applyExportFormatExtension(nameSettings,
					p.buildClipPath(nameSettings, "Parus major", 0.9, 0, time.Now()), &res)

				a := &SaveAudioAction{
					Settings:         execSettings,
					ClipName:         name,
					pcmData:          sinePCMAtRate(c.rate, driftRetryPCMSeconds, driftSineHz, driftSineAmp),
					sourceSampleRate: c.rate,
					modelName:        modelName,
					modelVersion:     res.Model.Version,
				}
				in := filepath.Join(dir, name)
				rate, format, path := a.resolveExportParams(in)

				wantFormat, ok := audioformat.FormatForExtension(filepath.Ext(name)[1:])
				require.True(t, ok, label)
				assert.Equal(t, in, path, label)
				assert.Equal(t, wantFormat, format, label)
				assert.True(t, transcode.EncoderAcceptsRate(format, rate, conf.BitDepth, conf.NumChannels), label)
				if c.isBat && c.rate > conf.SampleRate {
					assert.Equal(t, c.rate, rate, "ultrasonic lossless name keeps its rate: %s", label)
				}

				rate2, format2, path2 := a.resolveExportParams(in)
				assert.Equal(t, []any{rate, format, path}, []any{rate2, format2, path2}, "retry: %s", label)
			}
		}
	}
}

// TestResolveExportParams_NameWithoutKnownExtensionFollowsSettings pins the
// fallback for a ClipName whose extension names no export format: the settings
// decide the container and the extension is rewritten, as before, and a second
// call gives the same answer.
func TestResolveExportParams_NameWithoutKnownExtensionFollowsSettings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"clip", "clip.ogg"} {
		for _, isBat := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_bat=%t", name, isBat), func(t *testing.T) {
				t.Parallel()
				a := &SaveAudioAction{
					Settings:         driftExportSettings(dir, audioformat.MP3, audioformat.FLAC, "96k"),
					ClipName:         name,
					pcmData:          sinePCMAtRate(driftBatRate, driftRetryPCMSeconds, driftSineHz, driftSineAmp),
					sourceSampleRate: driftBatRate,
				}
				wantRate, wantFormat := conf.SampleRate, audioformat.MP3
				if isBat {
					a.modelName = batModelName
					wantRate, wantFormat = driftBatRate, audioformat.FLAC
				}
				in := filepath.Join(dir, name)
				want := in[:len(in)-len(filepath.Ext(in))] + "." + wantFormat

				for range 2 {
					rate, format, path := a.resolveExportParams(in)
					assert.Equal(t, wantRate, rate)
					assert.Equal(t, wantFormat, format)
					assert.Equal(t, want, path)
				}
			})
		}
	}
}

// TestCreateDetection_ClipNameFollowsResultModelAndRate verifies createDetection
// names the clip from the model and sample rate it stores in the Result: a bat
// capture at 96 kHz is named in the ultrasonic format, a bird capture in
// Export.Type, and the Result holds the same name.
func TestCreateDetection_ClipNameFollowsResultModelAndRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, modelID, sourceID, wantExt string
	}{
		{"bat at 96kHz", classifier.RegistryIDBat, driftBatSourceID, ".flac"},
		{"bird at 48kHz", classifier.RegistryIDBirdNETV3, driftBirdSourceID, ".mp3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newDriftProcessor(t)
			p.Bn = &classifier.Orchestrator{}
			settings := driftExportSettings(t.TempDir(), "mp3", "flac", "96k")

			item := classifier.Results{
				StartTime: time.Now(),
				Source:    datastore.AudioSource{ID: tt.sourceID},
				ModelID:   tt.modelID,
			}
			res := datastore.Results{Species: "Parus major_Great Tit", Confidence: 0.9}
			det := p.createDetection(settings, item, res, "Parus major", "Great tit", "parmaj", "Parus major_Great Tit")

			assert.Equal(t, tt.wantExt, filepath.Ext(det.Result.ClipName))
		})
	}
}
