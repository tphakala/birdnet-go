package processor

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

const (
	denoiseTestNoiseAmp = 0.03
	denoiseTestToneAmp  = 0.3
	denoiseTestToneHz   = 3000.0
	denoiseTestSeed     = 11
	// denoiseTestShortTimeout makes the blocking-denoiser tests end quickly.
	denoiseTestShortTimeout = 20 * time.Millisecond
	// flacStreamInfoRateOffset is where the 20-bit sample rate starts in a FLAC
	// file: "fLaC" (4), block header (4), then 10 bytes of STREAMINFO.
	flacStreamInfoRateOffset = 18
	flacMagic                = "fLaC"
	flacRateShift            = 4
)

var errTestEncodeFailed = errors.NewStd("encode failed on purpose")

// noisyPCM returns mono 16-bit PCM: noise for the first half second, then a tone
// in the noise, so the denoiser has a quiet window to learn from and a change to
// make.
func noisyPCM(rate int, seconds float64) []byte {
	frames := int(float64(rate) * seconds)
	toneStart := rate / 2
	rng := rand.New(rand.NewPCG(denoiseTestSeed, denoiseTestSeed))
	buf := make([]byte, frames*2)
	for i := range frames {
		v := denoiseTestNoiseAmp * rng.NormFloat64()
		if i >= toneStart {
			v += denoiseTestToneAmp * math.Sin(2*math.Pi*denoiseTestToneHz*float64(i)/float64(rate))
		}
		s := int16(math.Max(math.Min(math.Round(v*math.MaxInt16), math.MaxInt16), math.MinInt16))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s)) //nolint:gosec // G115: clamped to the int16 range above
	}
	return buf
}

// pcmRecorder records what the encoder seam and the pre-render queue receive.
type pcmRecorder struct {
	encoded   [][]byte
	prerender [][]byte
	// failFirstEncode makes the first encode call fail before writing anything.
	failFirstEncode bool
}

// Submit implements PreRendererSubmit.
func (r *pcmRecorder) Submit(job interface {
	GetPCMData() []byte
	GetSampleRate() int
	GetClipPath() string
	GetNoteID() uint
	GetTimestamp() time.Time
	GetModelType() string
}) error {
	r.prerender = append(r.prerender, bytes.Clone(job.GetPCMData()))
	return nil
}

// Stop implements PreRendererSubmit.
func (r *pcmRecorder) Stop() {}

// newDenoiseAction builds an action that Execute carries to a real encoder, with
// the encoder seam recording the PCM it receives before running the real encoder.
func newDenoiseAction(t *testing.T, dir, clipName string, pcm []byte, rate int, bat, denoiseOn bool) (*SaveAudioAction, *pcmRecorder) {
	t.Helper()
	rec := &pcmRecorder{}
	a := &SaveAudioAction{
		CorrelationID:    "export-denoise-test",
		ClipName:         clipName,
		pcmData:          bytes.Clone(pcm),
		sourceSampleRate: rate,
		PreRenderer:      rec,
		Settings: &conf.Settings{Realtime: conf.RealtimeSettings{
			Dashboard: conf.Dashboard{Spectrogram: conf.SpectrogramPreRender{Enabled: true}},
			Audio: conf.AudioSettings{Export: conf.ExportSettings{
				Enabled:        true,
				Path:           dir,
				Type:           audioformat.MP3,
				UltrasonicType: audioformat.FLAC,
				Bitrate:        "96k",
				Denoise:        conf.DenoiseSettings{Enabled: denoiseOn, Strength: conf.DenoisePresetLightMedium},
			}},
		}},
	}
	if bat {
		a.modelName = batModelName
	}
	a.encode = func(ctx context.Context, encoder string, exportRate, bitrateKbps int, exportFormat, outputPath string, gainDB float64) error {
		rec.encoded = append(rec.encoded, bytes.Clone(a.pcmData))
		if rec.failFirstEncode && len(rec.encoded) == 1 {
			return errTestEncodeFailed
		}
		return a.runEncoder(ctx, encoder, exportRate, bitrateKbps, exportFormat, outputPath, gainDB)
	}
	return a, rec
}

func mustDenoise(t *testing.T, pcm []byte, rate int) []byte {
	t.Helper()
	out, err := transcode.DenoisePCM16(t.Context(), pcm, rate, conf.NumChannels, conf.DenoisePresetLightMedium)
	require.NoError(t, err)
	require.NotEqual(t, pcm, out, "the fixture must be changed by the denoiser")
	return out
}

// dirEntries lists the names in dir, so a test can assert the clip is the only
// file written.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestSaveAudioAction_DenoisesPCMBeforeEncoding pins where export denoise runs:
// on the captured PCM in memory, before any encoder, with the clip written once
// in the container and at the rate the stored name and capture dictate. A bat
// capture stays lossless FLAC at its source rate. The pre-render job reads the
// same denoised PCM, so the stored clip and its spectrogram agree.
func TestSaveAudioAction_DenoisesPCMBeforeEncoding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		clipName string
		rate     int
		bat      bool
		denoise  bool
	}{
		{"mp3 at 48 kHz, denoise on", "clip.mp3", conf.SampleRate, false, true},
		{"flac bat capture at 256 kHz, denoise on", "clip.flac", batSourceSampleRate, true, true},
		{"mp3 at 48 kHz, denoise off", "clip.mp3", conf.SampleRate, false, false},
		{"flac bat capture at 256 kHz, denoise off", "clip.flac", batSourceSampleRate, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			raw := noisyPCM(tc.rate, 1.5)
			a, rec := newDenoiseAction(t, dir, tc.clipName, raw, tc.rate, tc.bat, tc.denoise)

			require.NoError(t, a.Execute(t.Context(), nil))

			want := raw
			if tc.denoise {
				want = mustDenoise(t, raw, tc.rate)
			}
			require.Len(t, rec.encoded, 1, "the clip must be encoded exactly once")
			assert.Equal(t, want, rec.encoded[0], "the encoder must receive the PCM in the expected state")
			require.Len(t, rec.prerender, 1)
			assert.Equal(t, want, rec.prerender[0], "the pre-render job must read the stored PCM")
			assert.Equal(t, []string{tc.clipName}, dirEntries(t, dir), "the clip is written once, at the stored name")

			if tc.bat {
				data, err := os.ReadFile(filepath.Join(dir, tc.clipName)) //nolint:gosec // G304: t.TempDir path
				require.NoError(t, err)
				require.Equal(t, flacMagic, string(data[:len(flacMagic)]), "a bat capture stays lossless")
				// The 20-bit sample rate spans bytes 18 to 20 of the file.
				const o = flacStreamInfoRateOffset
				rate := int(data[o])<<12 | int(data[o+1])<<flacRateShift | int(data[o+2])>>flacRateShift
				assert.Equal(t, tc.rate, rate, "denoise must not change the stored rate")
			}
		})
	}
}

// A bird capture above 48 kHz is resampled to 48 kHz for export. The denoise
// runs after that conversion: it receives the resampled PCM at the export rate,
// and the encoder and the pre-render job receive exactly what it returned.
func TestSaveAudioAction_DenoisesAtTheExportRate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	raw := noisyPCM(rateTest192k, 1.5)
	a, rec := newDenoiseAction(t, dir, "clip.wav", raw, rateTest192k, false, true)
	var gotRate int
	var gotPCM, denoised []byte
	a.denoise = func(ctx context.Context, pcm []byte, rate, ch int, preset string) ([]byte, error) {
		gotRate, gotPCM = rate, bytes.Clone(pcm)
		out, err := transcode.DenoisePCM16(ctx, pcm, rate, ch, preset)
		denoised = bytes.Clone(out)
		return out, err
	}

	require.NoError(t, a.Execute(t.Context(), nil))

	resampled, err := resample.ResampleBytes(raw, rateTest192k, conf.SampleRate)
	require.NoError(t, err)
	assert.Equal(t, conf.SampleRate, gotRate, "the denoiser runs at the export rate")
	assert.Equal(t, resampled, gotPCM, "the denoiser receives the resampled PCM")
	require.NotEqual(t, resampled, denoised, "the fixture must be changed by the denoiser")
	require.Len(t, rec.encoded, 1)
	assert.Equal(t, denoised, rec.encoded[0], "the encoder receives the denoised PCM")
	require.Len(t, rec.prerender, 1)
	assert.Equal(t, denoised, rec.prerender[0], "the pre-render job reads the stored PCM")
	assert.Equal(t, []string{"clip.wav"}, dirEntries(t, dir))
}

// A deferred export retries Execute on the same action after a failed attempt.
// The retry must reuse the first denoise, not run a second pass over the
// already denoised PCM.
func TestSaveAudioAction_RetryDoesNotDenoiseTwice(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	raw := noisyPCM(conf.SampleRate, 1.5)
	a, rec := newDenoiseAction(t, dir, "clip.flac", raw, conf.SampleRate, false, true)
	rec.failFirstEncode = true
	var denoiseCalls int
	a.denoise = func(ctx context.Context, pcm []byte, rate, channels int, preset string) ([]byte, error) {
		denoiseCalls++
		return transcode.DenoisePCM16(ctx, pcm, rate, channels, preset)
	}

	require.ErrorIs(t, a.Execute(t.Context(), nil), errTestEncodeFailed)
	require.NoError(t, a.Execute(t.Context(), nil))

	want := mustDenoise(t, raw, conf.SampleRate)
	assert.Equal(t, 1, denoiseCalls)
	require.Len(t, rec.encoded, 2)
	assert.Equal(t, want, rec.encoded[0])
	assert.Equal(t, want, rec.encoded[1], "the retry encodes the single-pass PCM")
}

// A denoise that timed out is not attempted again by the retry: the clip is
// written from the captured PCM.
func TestSaveAudioAction_RetryAfterDenoiseTimeoutWritesRaw(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	raw := noisyPCM(conf.SampleRate, 1.5)
	a, rec := newDenoiseAction(t, dir, "clip.flac", raw, conf.SampleRate, false, true)
	rec.failFirstEncode = true
	a.denoiseTimeout = denoiseTestShortTimeout
	var denoiseCalls int
	a.denoise = func(ctx context.Context, _ []byte, _, _ int, _ string) ([]byte, error) {
		denoiseCalls++
		<-ctx.Done()
		return nil, ctx.Err()
	}

	require.ErrorIs(t, a.Execute(t.Context(), nil), errTestEncodeFailed)
	a.denoiseTimeout = 0 // a retry with a generous deadline must still not denoise
	require.NoError(t, a.Execute(t.Context(), nil))

	assert.Equal(t, 1, denoiseCalls)
	require.Len(t, rec.encoded, 2)
	assert.Equal(t, raw, rec.encoded[1], "the retry writes the captured PCM")
}

// Export denoise never costs the clip: a timeout, an error or a clip over the
// frame budget saves the captured PCM, returns no error and logs the reason.
//
// It stays serial, subtests included: it resets the package-level
// denoiseSkipLogged guard and logtest.CaptureBuffer swaps the process-global
// logger, so it must not run in parallel with other tests.
func TestSaveAudioAction_DenoiseFallsBackToRawPCM(t *testing.T) {
	denoiseSkipLogged.seen.Clear()
	tests := []struct {
		name       string
		pcm        func() []byte
		denoise    pcmDenoiser
		wantCalled bool
	}{
		{
			name: "timeout",
			pcm:  func() []byte { return noisyPCM(conf.SampleRate, 1.5) },
			denoise: func(ctx context.Context, _ []byte, _, _ int, _ string) ([]byte, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			wantCalled: true,
		},
		{
			name: "denoiser error",
			pcm:  func() []byte { return noisyPCM(conf.SampleRate, 1.5) },
			denoise: func(context.Context, []byte, int, int, string) ([]byte, error) {
				return nil, errTestEncodeFailed
			},
			wantCalled: true,
		},
		{
			// Exactly the budget is still denoised; the error stub shows the denoiser
			// ran, so a budget check that turned > into >= fails here.
			name: "at the frame budget, denoiser error",
			pcm:  func() []byte { return make([]byte, maxExportDenoiseFrames*conf.BytesPerSample) },
			denoise: func(context.Context, []byte, int, int, string) ([]byte, error) {
				return nil, errTestEncodeFailed
			},
			wantCalled: true,
		},
		{
			name:       "over the frame budget",
			pcm:        func() []byte { return make([]byte, (maxExportDenoiseFrames+1)*conf.BytesPerSample) },
			denoise:    nil,
			wantCalled: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logs := logtest.CaptureBuffer(t)
			dir := t.TempDir()
			raw := tc.pcm()
			a, rec := newDenoiseAction(t, dir, "clip.wav", raw, conf.SampleRate, false, true)
			a.denoiseTimeout = denoiseTestShortTimeout
			var called bool
			a.denoise = func(ctx context.Context, pcm []byte, rate, ch int, preset string) ([]byte, error) {
				called = true
				if tc.denoise == nil {
					return transcode.DenoisePCM16(ctx, pcm, rate, ch, preset)
				}
				return tc.denoise(ctx, pcm, rate, ch, preset)
			}

			require.NoError(t, a.Execute(t.Context(), nil))

			assert.Equal(t, tc.wantCalled, called)
			require.Len(t, rec.encoded, 1)
			assert.True(t, bytes.Equal(raw, rec.encoded[0]), "the captured PCM must be saved")
			assert.Equal(t, []string{"clip.wav"}, dirEntries(t, dir))
			assert.Contains(t, logs.String(), "operation="+exportDenoiseOperation)
			assert.Contains(t, logs.String(), "denoise="+exportDenoiseOff, "the success line reports the clip as not denoised")
		})
	}
}

// The success line names the denoise preset the clip was saved with, so a
// support log answers whether a clip was denoised at export.
func TestExecute_SuccessLogReportsDenoise(t *testing.T) {
	logs := logtest.CaptureBuffer(t)
	a, _ := newDenoiseAction(t, t.TempDir(), "clip.wav", noisyPCM(conf.SampleRate, 1.5), conf.SampleRate, false, true)

	require.NoError(t, a.Execute(t.Context(), nil))

	line := logLineContaining(t, logs.String(), "operation=audio_export_success")
	assert.Contains(t, line, "denoise="+conf.DenoisePresetLightMedium)
}
