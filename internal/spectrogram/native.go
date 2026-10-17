package spectrogram

import (
	"bufio"
	"context"
	"fmt"
	"image/png"
	"os"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/spectrogram/render"
)

// This file is the native (pure Go) spectrogram path, selected per render by
// conf.NativeSpectrogramEnabled. It writes only the temp file the caller
// publishes with a rename, and never falls back to sox or FFmpeg.

const (
	// nativeOutputFilePerm is the permission mode of the temporary PNG.
	nativeOutputFilePerm = 0o644

	// nativeWriteBufferBytes is the buffer between the PNG encoder and the file.
	nativeWriteBufferBytes = 64 * 1024

	// nativeRendererName tags telemetry and logs from this path.
	nativeRendererName = "native"

	// Operation names of the two native entry points.
	nativeOperationFile = "generate_native_file"
	nativeOperationPCM  = "generate_native_pcm"

	// Dynamic range presets in dB, matching the sox -z values for the
	// conf.SpectrogramDynamicRange* settings.
	nativeRangeHighContrastDB = 80
	nativeRangeStandardDB     = 100
	nativeRangeExtendedDB     = 120
)

// nativeRangeDB maps the dynamic range preset strings getDynamicRange returns to
// dB; an unknown preset gives 0, which render rejects as invalid options.
func nativeRangeDB(preset string) int {
	switch preset {
	case conf.SpectrogramDynamicRangeHighContrast:
		return nativeRangeHighContrastDB
	case conf.SpectrogramDynamicRangeStandard:
		return nativeRangeStandardDB
	case conf.SpectrogramDynamicRangeExtended:
		return nativeRangeExtendedDB
	}
	return 0
}

// pcmSourceRate returns the capture rate for prerender PCM, falling back to the
// configured sample rate for a non-positive value (as generateWithSoxPCM does).
func pcmSourceRate(rate int) int {
	if rate <= 0 {
		return conf.SampleRate
	}
	return rate
}

// generateNativeFromFile renders the spectrogram of an audio file into tempPath.
func (g *Generator) generateNativeFromFile(ctx context.Context, settings *conf.Settings, audioPath, tempPath string, width int, raw bool, profile FrequencyProfile) error {
	src, err := transcode.DecodeMono(ctx, audioPath, profile.ResampleRate)
	if err != nil {
		return g.nativeError(ctx, err, nativeOperationFile)
	}
	return g.renderNative(ctx, settings, src, tempPath, width, raw, nativeOperationFile)
}

// generateNativeFromPCM renders the spectrogram of s16le mono PCM into tempPath.
func (g *Generator) generateNativeFromPCM(ctx context.Context, settings *conf.Settings, pcmData []byte, tempPath string, width int, raw bool, sampleRate int, profile FrequencyProfile) error {
	src, err := transcode.MonoFromPCM16(pcmData, pcmSourceRate(sampleRate), profile.ResampleRate)
	if err != nil {
		return g.nativeError(ctx, err, nativeOperationPCM)
	}
	return g.renderNative(ctx, settings, src, tempPath, width, raw, nativeOperationPCM)
}

// renderNative draws src and writes the PNG to tempPath, which must not exist.
func (g *Generator) renderNative(ctx context.Context, settings *conf.Settings, src render.Source, tempPath string, width int, raw bool, operation string) error {
	start := time.Now()
	opt := render.Options{
		Width:   width,
		Rows:    fftFriendlyHeight(width),
		Raw:     raw,
		RangeDB: nativeRangeDB(g.getDynamicRange(settings)),
		Style:   render.StyleFor(settings.Realtime.Dashboard.Spectrogram.Style),
	}
	img, cols, err := render.Render(ctx, src, opt)
	if err != nil {
		return g.nativeError(ctx, err, operation)
	}

	f, err := g.sfs.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, nativeOutputFilePerm)
	if err != nil {
		return g.nativeFileError(err, operation, "open")
	}
	// The PNG encoder writes many small chunks; buffer them rather than issue
	// a write call for each.
	buf := bufio.NewWriterSize(f, nativeWriteBufferBytes)
	err = png.Encode(buf, img)
	if err == nil {
		err = buf.Flush()
	}
	if err != nil {
		_ = f.Close()
		return g.nativeFileError(err, operation, "encode")
	}
	if err := f.Close(); err != nil {
		return g.nativeFileError(err, operation, "close")
	}

	g.log().Debug("Native spectrogram rendered",
		logger.String("renderer", nativeRendererName),
		logger.String("temp_path", g.relPath(tempPath)),
		logger.Int("width", width),
		logger.Int("columns", cols),
		logger.Int("sample_rate", src.SampleRate()),
		logger.Int64("elapsed_ms", time.Since(start).Milliseconds()))
	return nil
}

// nativeFileError builds the error for a failed temp file step.
func (g *Generator) nativeFileError(err error, operation, step string) error {
	return errors.New(err).
		Component("spectrogram").
		Category(errors.CategoryFileIO).
		Context("operation", operation).
		Context("renderer", nativeRendererName).
		Context("step", step).
		Build()
}

// nativeError classifies a decode or render failure and builds the enhanced
// error exactly once, since a Build reports to telemetry whenever reporting is
// active (see ErrorBuilder.Build in internal/errors). An error that is
// already enhanced (built by the transcode package) is only wrapped with the
// operation, even when the context has expired as well.
func (g *Generator) nativeError(ctx context.Context, err error, operation string) error {
	if _, ok := errors.AsType[*errors.EnhancedError](err); ok {
		// Already built (and reported) by the transcode package; building again
		// would report the same failure twice.
		return fmt.Errorf("native spectrogram %s: %w", operation, err)
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// Context-driven interruption is operational, like the sox path's
		// (IsOperationalError).
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Priority(errors.PriorityLow).
			Context("operation", operation).
			Context("renderer", nativeRendererName).
			Build()
	}
	category := errors.CategoryAudio
	if errors.Is(err, render.ErrInvalidOptions) || errors.Is(err, render.ErrEmptyClip) || errors.Is(err, render.ErrNoColumns) {
		category = errors.CategoryValidation
	}
	return errors.New(err).
		Component("spectrogram").
		Category(category).
		Context("operation", operation).
		Context("renderer", nativeRendererName).
		Build()
}
