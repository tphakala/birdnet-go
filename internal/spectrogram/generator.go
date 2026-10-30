// Package spectrogram provides core spectrogram generation logic.
// This file contains the Generator type that draws spectrograms in Go, used by
// both the pre-renderer (background mode) and API (on-demand mode).
package spectrogram

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

const (
	// defaultGenerationTimeout is the default timeout for spectrogram generation
	// Increased to 90s to accommodate slow storage (e.g., SD cards) under I/O pressure
	defaultGenerationTimeout = 90 * time.Second

	// defaultDynamicRange is the default dynamic range in dB of the spectrogram.
	// This is used as fallback when no valid setting is configured
	defaultDynamicRange = "100"

	// outputDirPermissions is the permission mode for creating output directories
	outputDirPermissions = 0o755
)

// GenerateOption configures optional parameters for spectrogram generation.
type GenerateOption func(*generateOptions)

type generateOptions struct {
	// freqProfile overrides the default bird frequency profile for spectrogram
	// generation. When nil, BirdProfile() is used.
	freqProfile *FrequencyProfile
	// source is the state of the audio file when the caller judged it ready.
	// When set, a render of a file that has since changed is not published.
	source *SourceState
	// beforePublish, when set, runs after the native render and before the source
	// is compared and the PNG published. It is a seam for tests that change the
	// file in that window.
	beforePublish func()
}

// ErrSourceChanged marks a render whose audio file differs from the state the
// caller supplied with WithSourceState: the clip was still being written or was
// replaced, so the image would not match the file as it stands. Nothing is
// published; a later request decides from the current bytes.
var ErrSourceChanged = errors.NewStd("audio file changed during spectrogram generation")

// SourceState is an audio file as the caller's readiness check judged it ready
// to render: its identity, size and modification time, plus the upper bound of
// the memory its decode needs.
type SourceState struct {
	info         os.FileInfo
	decodedBytes int64
}

// NewSourceState captures info, the os.FileInfo of the audio file at the moment
// it was judged ready, and decodedBytes, the most bytes its decode can hold
// (transcode.ProbeInfo.DecodedBytes), which sizes the render's memory budget.
func NewSourceState(info os.FileInfo, decodedBytes int64) SourceState {
	return SourceState{info: info, decodedBytes: decodedBytes}
}

// DecodedBytes returns the decoded-size bound the state was created with.
func (s SourceState) DecodedBytes() int64 { return s.decodedBytes }

// Matches reports whether now describes the same file content as the snapshot:
// the same file (os.SameFile), size and modification time.
func (s SourceState) Matches(now os.FileInfo) bool {
	return now != nil && os.SameFile(s.info, now) && s.info.Size() == now.Size() && s.info.ModTime().Equal(now.ModTime())
}

// WithSourceState makes GenerateFromFile compare the audio file with state before
// the render and again before publishing, and return ErrSourceChanged when it
// differs.
func WithSourceState(state SourceState) GenerateOption {
	return func(o *generateOptions) {
		o.source = &state
	}
}

// WithFrequencyProfile sets the frequency profile for spectrogram generation.
// BatProfile() keeps the clip's native rate (axis 0 to half the capture rate);
// BirdProfile() draws 0-12 kHz. When not set, defaults to BirdProfile().
func WithFrequencyProfile(fp FrequencyProfile) GenerateOption {
	return func(o *generateOptions) {
		o.freqProfile = &fp
	}
}

// fftFriendlyHeight returns the nearest 2^n + 1 value >= width/2 for optimal FFT performance.
// The analysis frame is 2*(height-1). When this is a power of 2, the FFT takes the fast
// path (O(n log n)) instead of a brute-force DFT (O(n²)).
func fftFriendlyHeight(width int) int {
	target := width / 2
	// Find smallest power of 2 where 2^n + 1 >= target
	n := 1
	for n+1 < target {
		n *= 2
	}
	return n + 1
}

// Generator handles core spectrogram generation logic.
// It is used by both the pre-renderer (background mode) and API (auto/user-requested modes).
type Generator struct {
	settings *conf.Settings
	sfs      *securefs.SecureFS

	// renderBudget bounds the memory of native renders in flight, in the bytes
	// nativeRenderWeight assigns each; renderBudgetBytes is its size.
	renderBudget      *semaphore.Weighted
	renderBudgetBytes int64
}

// NewGenerator creates a new generator instance.
// The log parameter is accepted for API compatibility but is no longer used;
// the generator always fetches a fresh logger via GetLogger() to avoid
// stale logger instances that silently drop log messages (see #2025).
func NewGenerator(settings *conf.Settings, sfs *securefs.SecureFS, _ logger.Logger) *Generator {
	g := &Generator{
		settings: settings,
		sfs:      sfs,
	}
	g.initRenderBudget()
	return g
}

// log returns a fresh module logger for the spectrogram package.
// Called on each log statement to avoid stale cached loggers (see #2025).
func (g *Generator) log() logger.Logger {
	return GetLogger()
}

// currentSettings returns the latest settings snapshot so UI changes to
// spectrogram style, dynamic range, etc. take effect on
// the next render without restarting the service.
func (g *Generator) currentSettings() *conf.Settings {
	return conf.CurrentOrFallback(g.settings)
}

// getDynamicRange returns the configured dynamic range value in dB.
// Returns the default value ("100") if not configured or if an invalid value is set.
// The settings snapshot is threaded in from the public entry point so the
// whole generation sees a consistent view.
func (g *Generator) getDynamicRange(settings *conf.Settings) string {
	dr := settings.Realtime.Dashboard.Spectrogram.DynamicRange
	if dr == "" {
		return defaultDynamicRange
	}
	// Validate it's one of the known presets
	switch dr {
	case conf.SpectrogramDynamicRangeHighContrast,
		conf.SpectrogramDynamicRangeStandard,
		conf.SpectrogramDynamicRangeExtended:
		return dr
	default:
		return defaultDynamicRange
	}
}

// GenerateFromFile creates a spectrogram from an audio file path.
// Used by API on-demand and user-requested modes.
// The image is drawn in Go, with no external binary.
//
// The audioPath and outputPath must be absolute paths.
// Width is in pixels, raw controls whether to show axes/legends.
//
// Context Timeout Behavior:
// This function enforces a 90-second timeout for spectrogram generation regardless
// of any parent context timeout. If the parent context has a shorter deadline, that
// will take precedence (Go's context behavior). This ensures:
//   - Long-running generations are bounded to prevent resource exhaustion
//   - Callers can still impose stricter timeouts if needed
//   - HTTP request cancellations are still respected via parent context
func (g *Generator) GenerateFromFile(ctx context.Context, audioPath, outputPath string, width int, raw bool, opts ...GenerateOption) error {
	start := time.Now()

	// Apply options
	var options generateOptions
	for _, opt := range opts {
		opt(&options)
	}

	// Resolve frequency profile: explicit option > default bird
	profile := BirdProfile()
	if options.freqProfile != nil {
		profile = *options.freqProfile
	}

	g.log().Debug("Starting spectrogram generation from file",
		logger.String("audio_path", g.relPath(audioPath)),
		logger.String("output_path", g.relPath(outputPath)),
		logger.Int("width", width),
		logger.Bool("raw", raw),
		logger.Bool("bat_profile", ProfileSuffix(profile) != ""))

	// Validate inputs before filesystem operations
	if outputPath == "" {
		return errors.Newf("output path is empty").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "generate_from_file").
			Build()
	}
	if !filepath.IsAbs(outputPath) {
		return errors.Newf("output path must be absolute").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "generate_from_file").
			Context("output_path", g.relPath(outputPath)).
			Build()
	}
	if width <= 0 {
		return errors.Newf("width must be positive").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "generate_from_file").
			Context("width", width).
			Build()
	}

	// Verify the audio file is accessible before starting generation.
	// Checked here (not in each generator method) to produce a clear error
	// when the file simply does not exist.
	audioInfo, statErr := os.Stat(audioPath)
	if statErr != nil {
		return fmt.Errorf("audio file inaccessible: %w", statErr)
	}
	// The queue wait between the caller's readiness check and here can be long
	// enough for a clip that was still being written to grow or be replaced.
	if options.source != nil && !options.source.Matches(audioInfo) {
		return fmt.Errorf("%w: before render", ErrSourceChanged)
	}

	// Ensure output directory exists using SecureFS (validates path automatically)
	if err := g.ensureOutputDirectory(outputPath); err != nil {
		return err
	}

	// Generate into a process-unique temp file in the same directory, then
	// atomically rename it onto outputPath, so a crash/kill mid-write or two
	// concurrent generations of the same path can never leave a partial or
	// corrupt .png at the final path. The temp name reuses
	// audiotemp.UniquePath ("<outputPath>.<pid>.<seq>.temp"), a form
	// isExportTempFor already ignores so the audio-clip temp scan never mistakes
	// a spectrogram temp for an in-progress audio export.
	tempPath := audiotemp.UniquePath(outputPath)
	defer func() {
		// Best-effort cleanup. On success the temp was renamed away, so Remove
		// returns a not-exist error we intentionally ignore; on any failure path
		// (render fail, ctx cancel, rename fail) it deletes the partial.
		_ = g.sfs.Remove(tempPath)
	}()

	// Capture one settings snapshot for the whole generation so
	// Style, DynamicRange, etc. all read from the same view. A UI edit that
	// lands mid-generation cannot split the output across two snapshots.
	settings := g.currentSettings()

	// Create context with timeout (see function documentation for timeout layering behavior)
	renderCtx, renderCancel := context.WithTimeout(ctx, defaultGenerationTimeout)
	defer renderCancel()

	renderErr := g.generateNativeFromFile(renderCtx, settings, audioPath, tempPath, width, raw, profile, options.source)
	if options.beforePublish != nil {
		options.beforePublish()
	}
	if err := checkSourceUnchanged(renderCtx, options.source, audioPath, renderErr); err != nil {
		return err
	}
	if renderErr != nil {
		return renderErr
	}

	// Atomically publish the finished spectrogram. g.sfs.Rename keeps the move
	// inside the SecureFS sandbox (os.Root); FinalizeWith reuses audiotemp's
	// Windows rename-retry for transient sharing violations.
	if err := audiotemp.FinalizeWith(tempPath, outputPath, g.sfs.Rename); err != nil {
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategoryFileIO).
			Context("operation", "finalize_spectrogram").
			Context("output_path", g.relPath(outputPath)).
			Build()
	}

	g.log().Info("Spectrogram generation from file completed",
		logger.String("renderer", nativeRendererName),
		logger.String("audio_path", g.relPath(audioPath)),
		logger.Int("width", width),
		logger.Int64("total_ms", time.Since(start).Milliseconds()))

	return nil
}

// checkSourceUnchanged compares the audio file with the state readiness judged,
// after the render and before the PNG is published. A difference, or a file that
// can no longer be read, returns ErrSourceChanged, wrapping renderErr when the
// render failed too: a decode error on a file that changed under it is
// transient and outranks the render error. A nil source (no state supplied) and
// a render error under a context that is already done (an interruption, not a
// reading of the file) pass through.
func checkSourceUnchanged(ctx context.Context, source *SourceState, audioPath string, renderErr error) error {
	if source == nil {
		return nil
	}
	if renderErr != nil {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
	}
	if now, err := os.Stat(audioPath); err == nil && source.Matches(now) {
		return nil
	}
	if renderErr != nil {
		return fmt.Errorf("%w: after render: %w", ErrSourceChanged, renderErr)
	}
	return fmt.Errorf("%w: after render", ErrSourceChanged)
}

// GenerateFromPCM creates a spectrogram from in-memory PCM data.
// Used by pre-renderer (background mode).
// PCM format: s16le, mono. sampleRate is the source capture rate in Hz;
// pass 0 to fall back to conf.SampleRate (48000 Hz).
// The image is drawn in Go, with no external binary.
//
// Context Timeout Behavior:
// This function enforces a 90-second timeout for spectrogram generation.
// The pre-renderer also sets its own timeout (generationTimeout in prerenderer.go), so the
// effective timeout will be whichever is shorter. This layered approach ensures
// both the pre-renderer and generator have safety limits.
func (g *Generator) GenerateFromPCM(ctx context.Context, pcmData []byte, outputPath string, width int, raw bool, sampleRate int, opts ...GenerateOption) error {
	start := time.Now()

	// Apply options
	var options generateOptions
	for _, opt := range opts {
		opt(&options)
	}

	// Resolve frequency profile: explicit option > default bird
	profile := BirdProfile()
	if options.freqProfile != nil {
		profile = *options.freqProfile
	}

	g.log().Debug("Starting spectrogram generation from PCM",
		logger.String("output_path", g.relPath(outputPath)),
		logger.Int("pcm_bytes", len(pcmData)),
		logger.Int("width", width),
		logger.Bool("raw", raw),
		logger.Bool("bat_profile", ProfileSuffix(profile) != ""))

	// Validate inputs before filesystem operations
	if outputPath == "" {
		return errors.Newf("output path is empty").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "generate_from_pcm").
			Build()
	}
	if !filepath.IsAbs(outputPath) {
		return errors.Newf("output path must be absolute").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "generate_from_pcm").
			Context("output_path", g.relPath(outputPath)).
			Build()
	}
	if width <= 0 {
		return errors.Newf("width must be positive").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "generate_from_pcm").
			Context("width", width).
			Build()
	}
	if len(pcmData) == 0 {
		return errors.Newf("PCM data is empty").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "generate_from_pcm").
			Build()
	}

	// Ensure output directory exists using SecureFS (validates path automatically)
	if err := g.ensureOutputDirectory(outputPath); err != nil {
		return err
	}

	// Generate into a process-unique temp file, then atomically rename it onto
	// outputPath, so an interrupted or concurrent render never leaves a partial
	// or corrupt .png at the final path. See GenerateFromFile.
	tempPath := audiotemp.UniquePath(outputPath)
	defer func() {
		// Best-effort: harmless not-exist error on success (temp already
		// renamed away); deletes the partial on any failure path.
		_ = g.sfs.Remove(tempPath)
	}()

	// Capture one settings snapshot for the whole generation so all reads
	// (Style, DynamicRange, Export.Length) are consistent even if
	// the user saves settings mid-render.
	settings := g.currentSettings()

	// Create context with timeout (see function documentation for timeout layering behavior)
	ctx, cancel := context.WithTimeout(ctx, defaultGenerationTimeout)
	defer cancel()

	if err := g.generateNativeFromPCM(ctx, settings, pcmData, tempPath, width, raw, sampleRate, profile); err != nil {
		return err
	}

	// Atomically publish the finished spectrogram (see GenerateFromFile).
	if err := audiotemp.FinalizeWith(tempPath, outputPath, g.sfs.Rename); err != nil {
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategoryFileIO).
			Context("operation", "finalize_spectrogram").
			Context("output_path", g.relPath(outputPath)).
			Build()
	}

	g.log().Info("Spectrogram generation from PCM completed",
		logger.String("renderer", nativeRendererName),
		logger.String("output_path", g.relPath(outputPath)),
		logger.Int("width", width),
		logger.Int("pcm_bytes", len(pcmData)),
		logger.Int64("total_ms", time.Since(start).Milliseconds()))

	return nil
}

// ensureOutputDirectory creates the output directory if it doesn't exist.
// Uses SecureFS for path validation.
func (g *Generator) ensureOutputDirectory(outputPath string) error {
	outputDir := filepath.Dir(outputPath)
	if err := g.sfs.MkdirAll(outputDir, outputDirPermissions); err != nil {
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategoryFileIO).
			Context("operation", "ensure_output_directory").
			Context("output_dir", g.relPath(outputDir)).
			Context("output_path", g.relPath(outputPath)).
			Build()
	}
	return nil
}
