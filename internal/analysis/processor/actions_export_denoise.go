// processor/actions_export_denoise.go
// Export-stage denoise: the captured PCM is denoised in memory before the clip
// is encoded.

package processor

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// maxExportDenoiseFrames bounds the clip length the export stage denoises.
	// The budget is in frames because denoise cost scales with samples: 5.76 M
	// frames is 120 s at 48 kHz, 30 s at 192 kHz and 22.5 s at 256 kHz, so the
	// default 15 s bat capture (3.84 M frames at 256 kHz) is denoised. A longer
	// clip is saved as captured.
	maxExportDenoiseFrames = 5_760_000

	// exportDenoiseTimeout bounds one export denoise. The export action runs
	// under a 30 s job timeout; a Raspberry Pi 4 denoises 15 s at 256 kHz in
	// about 1.1 s, so this leaves room for the loudness measurement and the
	// slowest encode inside the job budget.
	exportDenoiseTimeout = 10 * time.Second

	// exportDenoiseOff is the denoise value the success log reports for a clip
	// that was saved as captured.
	exportDenoiseOff = "off"

	// exportDenoiseOperation tags the export denoise log lines.
	exportDenoiseOperation = "audio_export_denoise"
)

// pcmDenoiser denoises interleaved 16-bit PCM into a new slice.
type pcmDenoiser func(ctx context.Context, pcm []byte, sampleRate, channels int, preset string) ([]byte, error)

// clipEncoderFunc writes the clip with an already selected encoder; runEncoder
// is the production implementation.
type clipEncoderFunc func(ctx context.Context, encoder string, exportRate, bitrateKbps int, exportFormat, outputPath string, gainDB float64) error

// denoiseSkipLogged guards the over-budget warning per sample rate: the cause is
// the capture length and rate, which repeat for every clip of an install.
//
//nolint:gochecknoglobals // process-lifetime log-flood guard
var denoiseSkipLogged onceByKey

// applyExportDenoise replaces a.pcmData with a denoised copy when
// Export.Denoise is enabled. It runs only on the PCM held in memory, after
// resolveExportParams settled the rate and trimmed the buffer and before any
// encoder reads it, so the container, the rate and the single write of the clip
// file are unchanged and nothing is ever decoded back from a written file.
//
// It never fails the export: a skipped clip (over the frame budget), an error or
// a timeout keeps the captured PCM, which is then saved raw, and logs a warning.
// The setting is read from the per-detection snapshot, like every export
// setting, so a change applies to the next clip without a restart.
func (a *SaveAudioAction) applyExportDenoise(ctx context.Context, exportRate int) {
	cfg := a.Settings.Realtime.Audio.Export.Denoise
	// denoiseAttempted is set before the attempt: a deferred export retries and
	// re-runs Execute on this action, and a second pass over denoised PCM would
	// denoise it twice.
	if !cfg.Enabled || a.denoiseAttempted {
		return
	}
	a.denoiseAttempted = true

	frames := len(a.pcmData) / (conf.BytesPerSample * conf.NumChannels)
	if frames > maxExportDenoiseFrames {
		denoiseSkipLogged.do(formatRateKey(cfg.Strength, exportRate), func() {
			GetLogger().Warn("Clip is longer than the export denoise limit; saving it as captured",
				logger.String("component", "analysis.processor.actions"),
				logger.String("preset", cfg.Strength),
				logger.Int("frames", frames),
				logger.Int("max_frames", maxExportDenoiseFrames),
				logger.Int("sample_rate", exportRate),
				logger.String("operation", exportDenoiseOperation))
		})
		return
	}

	denoise := a.denoise
	if denoise == nil {
		denoise = transcode.DenoisePCM16
	}
	timeout := exportDenoiseTimeout
	if a.denoiseTimeout > 0 {
		timeout = a.denoiseTimeout
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	out, err := denoise(dctx, a.pcmData, exportRate, conf.NumChannels, cfg.Strength)
	if err != nil {
		GetLogger().Warn("Export denoise failed; saving the clip as captured",
			logger.String("component", "analysis.processor.actions"),
			logger.String("detection_id", a.CorrelationID),
			logger.String("preset", cfg.Strength),
			logger.Int("frames", frames),
			logger.Int("sample_rate", exportRate),
			logger.Int64("elapsed_ms", time.Since(start).Milliseconds()),
			logger.Error(err),
			logger.String("operation", exportDenoiseOperation))
		return
	}
	a.pcmData = out
	a.denoisePreset = cfg.Strength
}
