package ffmpeg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// ffmpegTimeoutParam is the FFmpeg flag name for the connection timeout parameter.
const ffmpegTimeoutParam = "-timeout"

// ffmpegStimeoutMaxMajor is the first FFmpeg major version that removed -stimeout.
// FFmpeg < 5 needs -stimeout for RTSP; 5.x+ uses -timeout.
const ffmpegStimeoutMaxMajor = 5

// ffmpegRTSPTimeoutParam is the RTSP timeout flag for FFmpeg 5.x+ and unknown versions.
const ffmpegRTSPTimeoutParam = "-timeout"

// ffmpegLegacyRTSPTimeoutParam is the RTSP timeout flag required by FFmpeg 4.x.
const ffmpegLegacyRTSPTimeoutParam = "-stimeout"

// ffmpegAllowedMediaTypesFlag restricts the RTSP demuxer to SETUP only the
// listed media types during the RTSP handshake. BirdNET-Go consumes audio only,
// so requesting audio alone avoids opening a video client slot on cameras with a
// limited number of RTSP sessions (issue #3798). Shared by every RTSP-opening
// path: live capture (buildInputArgs), stream inspection (buildProbeArgs), and
// channel-energy analysis (buildAnalysisArgs), so they stay in lockstep.
const ffmpegAllowedMediaTypesFlag = "-allowed_media_types"

// ffmpegAllowedMediaTypesAudio is the value paired with ffmpegAllowedMediaTypesFlag
// to accept audio tracks only.
const ffmpegAllowedMediaTypesAudio = "audio"

// ffmpegMajorCache stores detected FFmpeg major versions by binary path.
var ffmpegMajorCache sync.Map

// ffmpegMajorGroup coalesces concurrent first-time version probes for the same
// binary path, so a burst of streams starting at once spawns a single
// `ffmpeg -version` process instead of one per stream.
var ffmpegMajorGroup singleflight.Group

// rtspTimeoutParamForMajor returns the RTSP connection-timeout flag for the
// given FFmpeg major version: -stimeout below 5 (FFmpeg 4.x), -timeout on 5.x+
// and unknown versions. This is the single source of truth shared by the live
// capture path (timeoutParamForSource) and the channel-energy analysis path
// (buildAnalysisArgs), so both stay in lockstep.
func rtspTimeoutParamForMajor(ffmpegMajor int) string {
	if ffmpegMajor > 0 && ffmpegMajor < ffmpegStimeoutMaxMajor {
		return ffmpegLegacyRTSPTimeoutParam
	}
	return ffmpegRTSPTimeoutParam
}

// timeoutParamForSource returns the correct FFmpeg timeout flag for the given source type.
func timeoutParamForSource(st audiocore.SourceType, ffmpegMajor int) string {
	if st == audiocore.SourceTypeRTSP {
		return rtspTimeoutParamForMajor(ffmpegMajor)
	}
	return ffmpegTimeoutParam
}

// ffmpegVersionProbeTimeout bounds the one-time `ffmpeg -version` probe so a
// hung or wrapped binary cannot stall stream startup. On timeout the probe
// yields major 0, which falls back to the safe -timeout default.
const ffmpegVersionProbeTimeout = 5 * time.Second

// resolveFfmpegMajor returns the cached or detected FFmpeg major version for a binary path.
func resolveFfmpegMajor(ffmpegPath string) int {
	if ffmpegPath == "" {
		return 0
	}
	if cached, ok := ffmpegMajorCache.Load(ffmpegPath); ok {
		if major, ok := cached.(int); ok {
			return major
		}
	}

	// Coalesce concurrent cold-cache probes for the same path into one
	// `ffmpeg -version` execution.
	val, _, _ := ffmpegMajorGroup.Do(ffmpegPath, func() (any, error) {
		// Re-check the cache: another caller may have populated it while this
		// call was waiting for the singleflight slot.
		if cached, ok := ffmpegMajorCache.Load(ffmpegPath); ok {
			if major, ok := cached.(int); ok {
				return major, nil
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), ffmpegVersionProbeTimeout)
		defer cancel()

		_, major, _ := conf.GetFfmpegVersionFromContext(ctx, ffmpegPath)
		// Only cache a successful detection. Caching a failed probe (major 0)
		// would pin the safe -timeout fallback for the process lifetime, so a
		// transient first-probe failure on a real FFmpeg 4.x host would silently
		// suppress -stimeout until restart. Re-probing on the next start is cheap.
		if major > 0 {
			ffmpegMajorCache.Store(ffmpegPath, major)
		}
		return major, nil
	})

	if major, ok := val.(int); ok {
		return major
	}
	return 0
}

// stripTimeoutParams returns a copy of params with any -timeout/-stimeout key-value pairs removed.
func stripTimeoutParams(params []string) []string {
	out := make([]string, 0, len(params))
	skipNext := false
	for _, param := range params {
		if skipNext {
			skipNext = false
			continue
		}
		if param == ffmpegTimeoutParam || param == ffmpegRTSPTimeoutParam || param == ffmpegLegacyRTSPTimeoutParam {
			skipNext = true
			continue
		}
		out = append(out, param)
	}
	return out
}

// AudioFilters defines optional processing filters for clip extraction and preview.
type AudioFilters struct {
	// Denoise preset name: "", "light", "medium", or "heavy".
	Denoise string
	// Normalize enables EBU R128 loudnorm normalisation.
	Normalize bool
	// LoudnessStats holds measured stats from the analysis pass (nil = analysis mode).
	LoudnessStats *LoudnessStats
	// GainDB is the volume adjustment in dB (0 = no change).
	GainDB float64
}

// HasFilters returns true if any processing filter is active.
func (f AudioFilters) HasFilters() bool {
	return f.Denoise != "" || f.Normalize || f.GainDB != 0
}

// denoisePresets maps preset names to afftdn parameters (nr=noise reduction, nf=noise floor).
var denoisePresets = map[string][2]int{
	"light":  {6, -30},
	"medium": {12, -40},
	"heavy":  {20, -50},
}

// IsValidDenoisePreset returns true if the preset name is valid (including empty for "off").
func IsValidDenoisePreset(preset string) bool {
	if preset == "" {
		return true
	}
	_, ok := denoisePresets[preset]
	return ok
}

// MaxGainDB is the maximum allowed gain adjustment in dB.
const MaxGainDB = 60.0

// IsValidGainDB returns true if the gain value is within the allowed range and not NaN/Inf.
func IsValidGainDB(gainDB float64) bool {
	return !math.IsNaN(gainDB) && !math.IsInf(gainDB, 0) && gainDB >= -MaxGainDB && gainDB <= MaxGainDB
}

// Loudnorm default targets (EBU R128).
const (
	loudnormTargetI   = -23.0
	loudnormTargetTP  = -2.0
	loudnormTargetLRA = 7.0
)

// BuildProcessingFilterChain constructs an FFmpeg -af filter string from AudioFilters.
// Filter order: denoise -> normalize -> gain (per spec).
// Returns an empty string if no filters are active.
func BuildProcessingFilterChain(f AudioFilters) string {
	var filters []string

	// 1. Denoise (afftdn).
	if params, ok := denoisePresets[f.Denoise]; ok {
		filters = append(filters, fmt.Sprintf("afftdn=nr=%d:nf=%d", params[0], params[1]))
	}

	// 2. Normalize (loudnorm).
	if f.Normalize {
		if f.LoudnessStats != nil && f.LoudnessStats.isValid() {
			// Pass 2: apply with measured values using linear normalisation.
			filters = append(filters, fmt.Sprintf(
				"loudnorm=I=%.1f:LRA=%.1f:TP=%.1f:measured_I=%s:measured_LRA=%s:measured_TP=%s:measured_thresh=%s:linear=true:offset=%s",
				loudnormTargetI, loudnormTargetLRA, loudnormTargetTP,
				f.LoudnessStats.InputI, f.LoudnessStats.InputLRA,
				f.LoudnessStats.InputTP, f.LoudnessStats.InputThresh,
				f.LoudnessStats.TargetOffset,
			))
		} else {
			// Pass 1: analysis mode.
			filters = append(filters, fmt.Sprintf(
				"loudnorm=I=%.1f:LRA=%.1f:TP=%.1f:print_format=json",
				loudnormTargetI, loudnormTargetLRA, loudnormTargetTP,
			))
		}
	}

	// 3. Gain (volume).
	if f.GainDB != 0 && !math.IsNaN(f.GainDB) {
		sign := "+"
		if f.GainDB < 0 {
			sign = ""
		}
		filters = append(filters, fmt.Sprintf("volume=%s%.1fdB", sign, f.GainDB))
	}

	return strings.Join(filters, ",")
}

// LoudnessStats holds the measured loudness statistics from FFmpeg's loudnorm filter.
type LoudnessStats struct {
	InputI            string `json:"input_i"`
	InputTP           string `json:"input_tp"`
	InputLRA          string `json:"input_lra"`
	InputThresh       string `json:"input_thresh"`
	OutputI           string `json:"output_i"`      // Not used for 2-pass, but part of JSON.
	OutputTP          string `json:"output_tp"`     // Not used for 2-pass.
	OutputLRA         string `json:"output_lra"`    // Not used for 2-pass.
	OutputThresh      string `json:"output_thresh"` // Not used for 2-pass.
	NormalizationType string `json:"normalization_type"`
	TargetOffset      string `json:"target_offset"` // Not used for 2-pass.
}

// isValid returns true if the measured loudness stats contain valid numeric values.
// This prevents injection of malformed values into FFmpeg filter chains.
func (s *LoudnessStats) isValid() bool {
	for _, v := range []string{s.InputI, s.InputTP, s.InputLRA, s.InputThresh, s.TargetOffset} {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
	}
	return true
}

// SeekRange defines an optional time range for FFmpeg input seeking.
type SeekRange struct {
	// Start offset in seconds.
	Start float64
	// Duration in seconds.
	Duration float64
}

// AnalyzeFileLoudness runs FFmpeg loudnorm analysis pass on a file and returns measured stats.
// If seekRange is non-nil, analysis is limited to the specified time range.
// Any pre-filters (like denoise) from AudioFilters are prepended to the analysis chain.
func AnalyzeFileLoudness(ctx context.Context, filePath, ffmpegPath string, filters AudioFilters, seekRange *SeekRange) (*LoudnessStats, error) {
	// Build the analysis filter chain: [denoise,] loudnorm with print_format=json.
	analysisParts := make([]string, 0, 2)
	if params, ok := denoisePresets[filters.Denoise]; ok {
		analysisParts = append(analysisParts, fmt.Sprintf("afftdn=nr=%d:nf=%d", params[0], params[1]))
	}
	analysisParts = append(analysisParts, fmt.Sprintf(
		"loudnorm=I=%.1f:LRA=%.1f:TP=%.1f:print_format=json",
		loudnormTargetI, loudnormTargetLRA, loudnormTargetTP,
	))
	filterChain := strings.Join(analysisParts, ",")

	// No inner timeout — inherits deadline from parent context.
	args := []string{
		"-hide_banner",
	}

	// Add seek range before input if specified.
	if seekRange != nil {
		args = append(args, "-ss", fmt.Sprintf("%.6f", seekRange.Start))
	}

	args = append(args, "-i", filePath)

	if seekRange != nil {
		args = append(args, "-t", fmt.Sprintf("%.6f", seekRange.Duration))
	}

	args = append(args,
		"-af", filterChain,
		"-f", "null",
		"-",
	)

	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: ffmpegPath validated by caller
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("loudness analysis cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("loudness analysis failed: %w, stderr: %s", err, stderr.String())
	}

	return parseLoudnessJSON(stderr.String())
}

// loudnessJSONPattern matches the JSON block output by loudnorm's print_format=json.
var loudnessJSONPattern = regexp.MustCompile(`(?s)\{[^}]*"input_i"\s*:[^}]*\}`)

// parseLoudnessJSON extracts LoudnessStats from FFmpeg stderr output.
func parseLoudnessJSON(stderr string) (*LoudnessStats, error) {
	match := loudnessJSONPattern.FindString(stderr)
	if match == "" {
		return nil, fmt.Errorf("no loudnorm JSON found in FFmpeg output")
	}

	var stats LoudnessStats
	if err := json.Unmarshal([]byte(match), &stats); err != nil {
		return nil, fmt.Errorf("failed to parse loudnorm JSON: %w", err)
	}

	if stats.InputI == "" {
		return nil, fmt.Errorf("loudnorm analysis returned empty input_i")
	}

	return &stats, nil
}

// AnalyzePCMLoudness analyzes the loudness of raw mono PCM audio data using
// FFmpeg's loudnorm filter. It writes pcmData to a temporary WAV file, runs
// loudness analysis via AnalyzeFileLoudness, and cleans up the temp file.
// sampleRate and bitDepth describe the PCM encoding (e.g. 48000, 16).
func AnalyzePCMLoudness(ctx context.Context, pcmData []byte, ffmpegPath string, sampleRate, bitDepth int) (*LoudnessStats, error) {
	if len(pcmData) == 0 {
		return nil, fmt.Errorf("empty PCM data provided for loudness analysis")
	}

	// Write PCM to a temporary WAV file so AnalyzeFileLoudness can process it.
	tempDir := os.TempDir()
	wavPath := filepath.Join(tempDir, fmt.Sprintf("birdnet-loudness-%d.wav", time.Now().UnixNano()))
	defer os.Remove(wavPath) //nolint:errcheck // best-effort cleanup

	if err := convert.SavePCMDataToWAV(wavPath, pcmData, sampleRate, bitDepth); err != nil {
		return nil, fmt.Errorf("failed to write temp WAV for loudness analysis: %w", err)
	}

	return AnalyzeFileLoudness(ctx, wavPath, ffmpegPath, AudioFilters{}, nil)
}

// processingTimeout is the maximum time allowed for the entire processing operation
// (includes both analysis and rendering passes for normalize).
const processingTimeout = 60 * time.Second

// MaxProcessDurationSec is the maximum allowed audio file duration for the process endpoint.
const MaxProcessDurationSec = 120

// ProcessAudioFile applies audio filters to an entire file and returns WAV output.
// For normalize: runs two-pass loudnorm (analysis then application).
// For denoise/gain without normalize: single-pass.
func ProcessAudioFile(ctx context.Context, filePath, ffmpegPath string, filters AudioFilters) (*bytes.Buffer, error) {
	if err := ValidateFFmpegPath(ffmpegPath); err != nil {
		return nil, fmt.Errorf("invalid FFmpeg path: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, processingTimeout)
	defer cancel()

	// Two-pass if normalize is requested.
	if filters.Normalize {
		stats, err := AnalyzeFileLoudness(ctx, filePath, ffmpegPath, filters, nil)
		if err != nil {
			return nil, fmt.Errorf("loudness analysis failed: %w", err)
		}
		filters.LoudnessStats = stats
	}

	filterChain := BuildProcessingFilterChain(filters)

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-i", filePath,
	}
	if filterChain != "" {
		args = append(args, "-af", filterChain)
	}
	args = append(args,
		"-c:a", "pcm_s16le",
		"-f", "wav",
		"pipe:1",
	)

	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: validated path
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("audio processing cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("audio processing failed: %w, stderr: %s", err, stderr.String())
	}

	if stdout.Len() == 0 {
		return nil, fmt.Errorf("FFmpeg produced empty output for %s", filepath.Base(filePath))
	}

	return &stdout, nil
}

// ProcessAudioToFile applies audio filters and writes WAV output directly to a file.
// Unlike ProcessAudioFile (which pipes to stdout producing broken WAV headers),
// this writes to a seekable file so ffmpeg can fix the header sizes.
func ProcessAudioToFile(ctx context.Context, filePath, ffmpegPath string, filters AudioFilters, outputPath string) error {
	if err := ValidateFFmpegPath(ffmpegPath); err != nil {
		return fmt.Errorf("invalid FFmpeg path: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, processingTimeout)
	defer cancel()

	// Two-pass if normalize is requested.
	if filters.Normalize {
		stats, err := AnalyzeFileLoudness(ctx, filePath, ffmpegPath, filters, nil)
		if err != nil {
			return fmt.Errorf("loudness analysis failed: %w", err)
		}
		filters.LoudnessStats = stats
	}

	filterChain := BuildProcessingFilterChain(filters)

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-y", // overwrite output file
		"-i", filePath,
	}
	if filterChain != "" {
		args = append(args, "-af", filterChain)
	}
	args = append(args,
		"-c:a", "pcm_s16le",
		outputPath,
	)

	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: validated path
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("audio processing cancelled: %w", ctx.Err())
		}
		return fmt.Errorf("audio processing failed: %w, stderr: %s", err, stderr.String())
	}

	return nil
}

// BuildFFmpegArgs constructs the complete FFmpeg argument list for a streaming source.
// It is a pure function suitable for unit testing. It produces the same output
// arguments as the runtime path (Stream.startProcess) by sharing buildOutputArgs;
// only the input-argument timeout warning logging differs between the two.
//
// RTSP-specific flags like -rtsp_transport are only added for RTSP sources.
// A default -timeout is added unless the caller supplies one via ffmpegParameters.
func BuildFFmpegArgs(cfg *StreamConfig, ffmpegParameters []string) []string {
	args := buildInputArgs(cfg, ffmpegParameters)
	return buildOutputArgs(args, cfg)
}

// buildOutputArgs appends the post-input FFmpeg flags: the input URL, decode
// format, channel selection, conditional output resampling, and the stdout pipe.
// It is shared by BuildFFmpegArgs and Stream.startProcess so the unit-tested
// argument construction matches the runtime path exactly.
//
// -ac (channel count) is always emitted via appendChannelArgs so multi-channel
// sources are downmixed to mono. -ar (sample rate) is only emitted when the
// source rate is unknown or differs from the target, avoiding a needless
// resample when the source already matches.
func buildOutputArgs(args []string, cfg *StreamConfig) []string {
	sampleRate, numChannels, format := GetFFmpegFormat(cfg.SampleRate, cfg.Channels, cfg.BitDepth)

	logLevel := cfg.LogLevel
	if logLevel == "" {
		logLevel = "error"
	}

	args = append(args,
		"-i", cfg.URL,
		"-loglevel", logLevel,
		"-vn",
		"-f", format,
	)
	args = appendChannelArgs(args, cfg.ChannelMode, cfg.SourceChannels, numChannels)
	if cfg.needsOutputResampling() {
		args = append(args, "-ar", sampleRate)
	}
	args = append(args, "-hide_banner", "pipe:1")

	return args
}

// appendChannelArgs appends the appropriate FFmpeg channel selection flags.
// When channelMode is "left" or "right" and the source has >1 channel,
// it uses a pan filter to extract the selected channel. Falls back to
// simple -ac downmix when the source is mono or mode is "downmix"/empty.
func appendChannelArgs(args []string, channelMode string, sourceChannels int, numChannels string) []string {
	if sourceChannels > 1 {
		switch strings.ToLower(channelMode) {
		case "left":
			return append(args, "-af", "pan=mono|c0=c0", "-ac", "1")
		case "right":
			return append(args, "-af", "pan=mono|c0=c1", "-ac", "1")
		}
	}
	return append(args, "-ac", numChannels)
}

// buildInputArgs constructs the pre-input FFmpeg flags (transport, timeout, extra parameters).
// This mirrors the logic in Stream.buildFFmpegInputArgs but accepts explicit parameters.
// RTSP streams use the timeout flag supported by the configured FFmpeg major version.
func buildInputArgs(cfg *StreamConfig, ffmpegParameters []string) []string {
	args := make([]string, 0, 8+len(ffmpegParameters))
	ffmpegMajor := resolveFfmpegMajor(cfg.FFmpegPath)
	timeoutFlag := timeoutParamForSource(cfg.sourceType(), ffmpegMajor)

	if cfg.sourceType() == audiocore.SourceTypeRTSP {
		args = append(args, "-rtsp_transport", cfg.Transport, ffmpegAllowedMediaTypesFlag, ffmpegAllowedMediaTypesAudio)
	}

	hasUserTimeout, userTimeoutValue := detectUserTimeout(ffmpegParameters)

	if !hasUserTimeout {
		args = append(args, timeoutFlag, strconv.FormatInt(defaultTimeoutMicroseconds, 10))
		if len(ffmpegParameters) > 0 {
			args = append(args, ffmpegParameters...)
		}
		return args
	}

	if err := validateTimeout(userTimeoutValue); err != nil {
		args = append(args, timeoutFlag, strconv.FormatInt(defaultTimeoutMicroseconds, 10))
	} else {
		args = append(args, timeoutFlag, userTimeoutValue)
	}
	args = append(args, stripTimeoutParams(ffmpegParameters)...)

	return args
}

// validateTimeout validates a user-provided timeout string value in microseconds.
// Returns an error if the value is not a valid integer or is below the minimum.
func validateTimeout(timeoutStr string) error {
	timeout, err := strconv.ParseInt(timeoutStr, 10, 64)
	if err != nil {
		return errors.Newf("invalid timeout format: %s (must be a number in microseconds)", timeoutStr).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "validate_timeout").
			Context("timeout_value", timeoutStr).
			Build()
	}

	if timeout < minTimeoutMicroseconds {
		return errors.Newf("timeout too short: %d microseconds (minimum: %d microseconds = 1 second)", timeout, minTimeoutMicroseconds).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "validate_timeout").
			Context("timeout_microseconds", timeout).
			Context("minimum_microseconds", minTimeoutMicroseconds).
			Build()
	}

	return nil
}

// CalculateBackoff computes the exponential backoff duration for a given restart count.
// It adds a random jitter of up to restartJitterPercentMax percent of the base backoff.
// The returned duration is always at least base and at most maxBackoff plus that jitter.
func CalculateBackoff(restartCount int, base, maxBackoff time.Duration) time.Duration {
	return applyBackoffJitter(computeBaseBackoff(restartCount, base, maxBackoff))
}

// computeBaseBackoff returns the capped exponential backoff for a restart count,
// without jitter. The exponent is clamped to [0, maxBackoffExponent] and the
// result is capped at maxBackoff.
func computeBaseBackoff(restartCount int, base, maxBackoff time.Duration) time.Duration {
	exponent := max(restartCount-1, 0)
	exponent = min(exponent, maxBackoffExponent)

	return min(base*time.Duration(1<<uint(exponent)), maxBackoff) //nolint:gosec // G115: exponent is capped by maxBackoffExponent
}

// applyBackoffJitter adds a random jitter of up to restartJitterPercentMax percent
// on top of backoff to prevent a thundering herd of simultaneous reconnects. A
// non-positive backoff (or jitter range) returns backoff unchanged.
func applyBackoffJitter(backoff time.Duration) time.Duration {
	if backoff <= 0 {
		return backoff
	}

	jitterRange := backoff * restartJitterPercentMax / 100
	if jitterRange <= 0 {
		return backoff
	}

	return backoff + time.Duration(rand.Int64N(jitterRange.Nanoseconds())) //nolint:gosec // G404: non-cryptographic jitter to spread out reconnects
}
