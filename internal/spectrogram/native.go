package spectrogram

import (
	"bufio"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"sync"
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

	// nativePNGCompression is the deflate effort of the PNG encoder. The level
	// only trades file size for encode time; the decoded pixels and palette are
	// the same at every level.
	nativePNGCompression = png.BestSpeed

	// nativeRendererName tags telemetry and logs from this path.
	nativeRendererName = "native"

	// soxRendererName tags the completion logs of the sox and FFmpeg path, so
	// the two renderers' timings can be compared.
	soxRendererName = "sox"

	// Operation names of the two native entry points.
	nativeOperationFile = "generate_native_file"
	nativeOperationPCM  = "generate_native_pcm"

	// Dynamic range presets in dB, matching the sox -z values for the
	// conf.SpectrogramDynamicRange* settings.
	nativeRangeHighContrastDB = 80
	nativeRangeStandardDB     = 100
	nativeRangeExtendedDB     = 120
)

// selectedRenderer reads the renderer gate once and returns the name to log
// with whether the native path is selected, so the branch taken and the logged
// name cannot differ.
func selectedRenderer() (name string, native bool) {
	if conf.NativeSpectrogramEnabled() {
		return nativeRendererName, true
	}
	return soxRendererName, false
}

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
// The render first takes its share of the memory budget, from the readiness
// snapshot when the caller supplied one and from a header probe otherwise, then
// opens the clip and draws it at low priority, decoding while it draws for a clip
// that transcode.OpenMono streams.
func (g *Generator) generateNativeFromFile(ctx context.Context, settings *conf.Settings, audioPath, tempPath string, width int, raw bool, profile FrequencyProfile, source *SourceState) error {
	var decodedBytes int64
	if source != nil {
		decodedBytes = source.decodedBytes
	} else {
		info, err := transcode.ProbeFile(ctx, audioPath)
		if err != nil {
			return g.nativeError(ctx, err, nativeOperationFile)
		}
		decodedBytes = info.DecodedBytes
	}
	weight := nativeRenderWeight(decodedBytes)
	release, err := g.acquireRenderBudget(ctx, weight)
	if err != nil {
		return g.nativeError(ctx, err, nativeOperationFile)
	}
	defer release()

	return runAtLowPriority(func() error {
		start := time.Now()
		opened, err := transcode.OpenMono(ctx, audioPath, 0)
		if err != nil {
			return g.nativeError(ctx, err, nativeOperationFile)
		}
		err = g.renderMono(ctx, settings, opened, profile, tempPath, width, raw, time.Since(start), weight)
		if !errors.Is(err, transcode.ErrShorterThanDeclared) {
			return err
		}
		// The header declared more audio than the file delivered, so the span the
		// render used was too long. Draw the file again from memory, which sizes the
		// picture from what it holds. The failed attempt wrote no temp file.
		g.log().Debug("Streamed clip is shorter than its header declares, decoding it into memory",
			logger.String("operation", nativeOperationFile),
			logger.Error(err))
		start = time.Now()
		decoded, err := transcode.DecodeMono(ctx, audioPath, 0)
		if err != nil {
			return g.nativeError(ctx, err, nativeOperationFile)
		}
		return g.renderMono(ctx, settings, decoded, profile, tempPath, width, raw, time.Since(start), weight)
	})
}

// renderMono picks the analysis source and display rate for a clip opened or
// decoded from a file and draws it.
func (g *Generator) renderMono(ctx context.Context, settings *conf.Settings, mono *transcode.MonoSource, profile FrequencyProfile, tempPath string, width int, raw bool, prepElapsed time.Duration, weight int64) error {
	src, displayRate, err := analysisSource(mono, profile)
	if err != nil {
		return g.nativeError(ctx, err, nativeOperationFile)
	}
	return g.renderNative(ctx, settings, src, displayRate, tempPath, width, raw, nativeOperationFile, prepElapsed, mono.Streamed(), weight)
}

// generateNativeFromPCM renders the spectrogram of s16le mono PCM into tempPath
// at low priority. The PCM is already in memory, held by the caller, so it takes
// no share of the render budget.
func (g *Generator) generateNativeFromPCM(ctx context.Context, settings *conf.Settings, pcmData []byte, tempPath string, width int, raw bool, sampleRate int, profile FrequencyProfile) error {
	return runAtLowPriority(func() error {
		start := time.Now()
		decoded, err := transcode.MonoFromPCM16(pcmData, pcmSourceRate(sampleRate), 0)
		if err != nil {
			return g.nativeError(ctx, err, nativeOperationPCM)
		}
		src, displayRate, err := analysisSource(decoded, profile)
		if err != nil {
			return g.nativeError(ctx, err, nativeOperationPCM)
		}
		return g.renderNative(ctx, settings, src, displayRate, tempPath, width, raw, nativeOperationPCM, time.Since(start), false, 0)
	})
}

// analysisSource picks what to analyse for a decoded clip and the display rate
// to render it at. A profile with a ResampleRate of zero or less (bat) keeps the
// clip rate, as newMonoSource does for such a target. A clip whose rate is an
// accepted multiple of the profile rate (render.FrameMultiple) is analysed at its
// own rate and drawn for the profile rate. Any other clip is resampled to the
// profile rate while streaming, which Render then draws without a multiple.
func analysisSource(src *transcode.MonoSource, profile FrequencyProfile) (render.Source, int, error) {
	if profile.ResampleRate <= 0 {
		return src, 0, nil
	}
	if _, ok := render.FrameMultiple(src.SampleRate(), profile.ResampleRate); ok {
		return src, profile.ResampleRate, nil
	}
	resampled, err := src.WithRate(profile.ResampleRate)
	if err != nil {
		return nil, 0, err
	}
	return resampled, profile.ResampleRate, nil
}

// renderNative draws src and writes the PNG to tempPath. A file already at
// tempPath is a stale temp from a crashed run (the name carries the pid and a
// per-process sequence) and is removed first; the file is then created
// exclusively, so a symlink or a file that appears in between still fails.
// displayRate is the rate the picture is drawn for (see
// render.Options.DisplayRate); zero draws at the rate of src. decodeElapsed is the
// time the caller spent preparing src: the whole decode for a source held in
// memory, only the header open for a streamed one (streamed is true), whose
// decode then runs inside Render's stream and counts as render time, as does
// resampling. It is only logged, with the render and write times and the streamed
// flag, so the renderers can be compared. budgetWeight is the render budget bytes
// the caller holds (zero when it holds none), also only logged.
func (g *Generator) renderNative(ctx context.Context, settings *conf.Settings, src render.Source, displayRate int, tempPath string, width int, raw bool, operation string, decodeElapsed time.Duration, streamed bool, budgetWeight int64) error {
	renderStart := time.Now()
	opt := render.Options{
		Width:       width,
		Rows:        fftFriendlyHeight(width),
		Raw:         raw,
		RangeDB:     nativeRangeDB(g.getDynamicRange(settings)),
		Style:       render.StyleFor(settings.Realtime.Dashboard.Spectrogram.Style),
		DisplayRate: displayRate,
	}
	img, cols, err := render.Render(ctx, src, opt)
	if err != nil {
		return g.nativeError(ctx, err, operation)
	}

	writeStart := time.Now()
	renderElapsed := writeStart.Sub(renderStart)
	if err := g.sfs.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return g.nativeFileError(err, operation, "remove")
	}
	f, err := g.sfs.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, nativeOutputFilePerm)
	if err != nil {
		return g.nativeFileError(err, operation, "open")
	}
	if err := encodePNG(f, img); err != nil {
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
		logger.Int("display_rate", displayRate),
		logger.Bool("streamed", streamed),
		logger.Int64("budget_weight_bytes", budgetWeight),
		logger.Int64("decode_ms", decodeElapsed.Milliseconds()),
		logger.Int64("render_ms", renderElapsed.Milliseconds()),
		logger.Int64("write_ms", time.Since(writeStart).Milliseconds()),
		logger.Int64("total_ms", (decodeElapsed+time.Since(renderStart)).Milliseconds()))
	return nil
}

// pngEncoderBufferPool implements png.EncoderBufferPool over a sync.Pool, so
// concurrent encodes each get their own buffer and reuse it for later images.
// Get returns nil when the pool is empty, which makes the encoder allocate a new
// buffer (image/png/writer.go:613-615, go1.27.3).
type pngEncoderBufferPool struct {
	pool sync.Pool
}

// Get returns a pooled encoder buffer, or nil when none is available.
func (p *pngEncoderBufferPool) Get() *png.EncoderBuffer {
	buf, _ := p.pool.Get().(*png.EncoderBuffer)
	return buf
}

// Put returns an encoder buffer to the pool.
func (p *pngEncoderBufferPool) Put(buf *png.EncoderBuffer) {
	p.pool.Put(buf)
}

// nativePNGEncoder encodes every native render at the fast compression level
// with pooled buffers. It is safe for concurrent use.
var nativePNGEncoder = &png.Encoder{
	CompressionLevel: nativePNGCompression,
	BufferPool:       &pngEncoderBufferPool{},
}

// encodePNG writes img to w as a PNG at the fast compression level, with pooled
// encoder buffers. The encoder issues many small writes, so they go through a
// buffer that is flushed before returning.
func encodePNG(w io.Writer, img image.Image) error {
	buf := bufio.NewWriterSize(w, nativeWriteBufferBytes)
	if err := nativePNGEncoder.Encode(buf, img); err != nil {
		return err
	}
	return buf.Flush()
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
// operation, even when the context has expired as well. An error wrapping
// transcode.ErrShorterThanDeclared is returned as it is: it is the signal to
// render the clip again from memory, not a failure to classify or report.
func (g *Generator) nativeError(ctx context.Context, err error, operation string) error {
	if errors.Is(err, transcode.ErrShorterThanDeclared) {
		return err
	}
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
