// conf/validate_audio.go

package conf

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Audio gain limits in dB
const (
	MinAudioGain = -40.0 // Minimum allowed audio gain in dB
	MaxAudioGain = 40.0  // Maximum allowed audio gain in dB
)

// Playback gain limits in dB (UI preference, distinct from export gain)
const (
	MinPlaybackGain     = 0.0  // Minimum default playback gain
	MaxPlaybackGain     = 24.0 // Maximum default playback gain (matches AudioSettingsButton GAIN_MAX_DB)
	DefaultPlaybackGain = 0.0  // Safe default if invalid value provided
)

// Audio export type constants
const (
	AudioExportTypeWAV  = "wav"  // Lossless PCM audio
	AudioExportTypeFLAC = "flac" // Lossless compressed audio
	AudioExportTypeMP3  = "mp3"  // Lossy compressed audio
	AudioExportTypeAAC  = "aac"  // Lossy compressed audio
	AudioExportTypeOPUS = "opus" // Lossy compressed audio
)

// EBU R128 normalization limits
const (
	MinTargetLUFS    = -40.0 // Minimum target loudness in LUFS
	MaxTargetLUFS    = -10.0 // Maximum target loudness in LUFS
	MinLoudnessRange = 0.0   // Minimum loudness range in LU
	MaxLoudnessRange = 20.0  // Maximum loudness range in LU
	MinTruePeak      = -10.0 // Minimum true peak in dBTP
	MaxTruePeak      = 0.0   // Maximum true peak in dBTP
)

// Equalizer filter validation limits
const (
	MaxEQFrequency = 24000.0 // Maximum EQ frequency in Hz (Nyquist for 48 kHz)
	MaxEQQ         = 100.0   // Maximum Q factor for EQ filters
)

// Stream validation constants
const (
	MaxStreamNameLength      = 64
	MaxAudioSourceNameLength = 100
)

// Quiet hours validation constants
const (
	MaxQuietHoursOffset = 180  // Maximum offset in minutes from sun event
	MinQuietHoursOffset = -180 // Minimum offset in minutes from sun event
)

// Quiet hours mode constants
const (
	QuietHoursModeFixed = "fixed" // Fixed clock-based quiet hours
	QuietHoursModeSolar = "solar" // Solar event-based quiet hours
)

// Solar event constants
const (
	SolarEventSunrise = "sunrise" // Sunrise solar event
	SolarEventSunset  = "sunset"  // Sunset solar event
)

// ValidQuietHoursModes contains valid quiet hours mode values
var ValidQuietHoursModes = map[string]bool{
	QuietHoursModeFixed: true,
	QuietHoursModeSolar: true,
}

// ValidSolarEvents contains valid solar event names
var ValidSolarEvents = map[string]bool{
	SolarEventSunrise: true,
	SolarEventSunset:  true,
}

// ValidStreamTypes contains all supported stream types
var ValidStreamTypes = map[string]bool{
	StreamTypeRTSP: true,
	StreamTypeHTTP: true,
	StreamTypeHLS:  true,
	StreamTypeRTMP: true,
	StreamTypeUDP:  true,
}

// validSampleRates is the canonical list of supported capture sample rates (sorted ascending).
var validSampleRates = []int{48000, 96000, 192000, 256000, 384000}

// ValidSampleRates returns a copy of the canonical sample rate list.
// Returning a clone prevents callers from mutating the shared source of truth.
func ValidSampleRates() []int {
	return slices.Clone(validSampleRates)
}

// Validate validates a single stream configuration
func (s *StreamConfig) Validate() error {
	// Normalize fields in-place so downstream code sees trimmed values.
	s.Name = strings.TrimSpace(s.Name)
	s.URL = strings.TrimSpace(s.URL)

	// Name is required
	if s.Name == "" {
		return fmt.Errorf("stream name is required")
	}

	// Name length limit
	if len(s.Name) > MaxStreamNameLength {
		return fmt.Errorf("stream name '%s' exceeds maximum length of %d characters", s.Name, MaxStreamNameLength)
	}

	// URL is required
	if s.URL == "" {
		return fmt.Errorf("stream URL is required for '%s'", s.Name)
	}

	// Validate stream type
	if !ValidStreamTypes[s.Type] {
		return fmt.Errorf("invalid stream type '%s' for '%s': must be one of rtsp, http, hls, rtmp, udp", s.Type, s.Name)
	}

	// Validate transport (only tcp/udp allowed, empty defaults to tcp)
	if s.Transport != "" && s.Transport != "tcp" && s.Transport != "udp" {
		return fmt.Errorf("invalid transport '%s' for '%s': must be tcp or udp", s.Transport, s.Name)
	}

	// Validate channel mode (empty defaults to downmix, explicit values must be valid)
	if s.ChannelMode != "" && !ValidChannelModes[s.ChannelMode] {
		return fmt.Errorf("invalid channel mode '%s' for '%s': must be downmix, left, or right", s.ChannelMode, s.Name)
	}

	// Validate gain range (NaN/Inf bypass < and > comparisons). Mirrors the
	// AudioSourceConfig.Validate check so a hand-edited config.yaml or a non-UI
	// API client cannot push an out-of-range gain past the frontend clamp.
	if math.IsNaN(s.Gain) || math.IsInf(s.Gain, 0) || s.Gain < MinAudioGain || s.Gain > MaxAudioGain {
		return fmt.Errorf("stream '%s': gain %.1f dB out of range [%.0f, +%.0f]", s.Name, s.Gain, MinAudioGain, MaxAudioGain)
	}

	// Validate URL scheme matches type
	if err := s.validateURLScheme(); err != nil {
		return err
	}

	// Validate per-stream EQ if set
	if s.Equalizer != nil {
		if err := validateEQFilters(s.Equalizer.Filters, fmt.Sprintf("stream '%s'", s.Name)); err != nil {
			return err
		}
	}

	// Validate quiet hours if enabled
	if err := ValidateQuietHours(&s.QuietHours, fmt.Sprintf("stream '%s'", s.Name)); err != nil {
		return err
	}

	return nil
}

// ValidateQuietHours validates a quiet hours configuration
func ValidateQuietHours(qh *QuietHoursConfig, context string) error {
	if qh == nil || !qh.Enabled {
		return nil
	}

	// Validate mode
	if !ValidQuietHoursModes[qh.Mode] {
		return fmt.Errorf("%s: quiet hours mode must be 'fixed' or 'solar', got '%s'", context, qh.Mode)
	}

	switch qh.Mode {
	case QuietHoursModeFixed:
		// Validate start time format
		if _, err := time.Parse("15:04", qh.StartTime); err != nil {
			return fmt.Errorf("%s: quiet hours start time must be in HH:MM format, got '%s'", context, qh.StartTime)
		}
		// Validate end time format
		if _, err := time.Parse("15:04", qh.EndTime); err != nil {
			return fmt.Errorf("%s: quiet hours end time must be in HH:MM format, got '%s'", context, qh.EndTime)
		}

	case QuietHoursModeSolar:
		// Validate start event
		if !ValidSolarEvents[qh.StartEvent] {
			return fmt.Errorf("%s: quiet hours start event must be 'sunrise' or 'sunset', got '%s'", context, qh.StartEvent)
		}
		// Validate end event
		if !ValidSolarEvents[qh.EndEvent] {
			return fmt.Errorf("%s: quiet hours end event must be 'sunrise' or 'sunset', got '%s'", context, qh.EndEvent)
		}
		// Validate offsets
		if qh.StartOffset < MinQuietHoursOffset || qh.StartOffset > MaxQuietHoursOffset {
			return fmt.Errorf("%s: quiet hours start offset must be between %d and %d minutes, got %d", context, MinQuietHoursOffset, MaxQuietHoursOffset, qh.StartOffset)
		}
		if qh.EndOffset < MinQuietHoursOffset || qh.EndOffset > MaxQuietHoursOffset {
			return fmt.Errorf("%s: quiet hours end offset must be between %d and %d minutes, got %d", context, MinQuietHoursOffset, MaxQuietHoursOffset, qh.EndOffset)
		}
	}

	return nil
}

// validateURLScheme checks URL scheme matches declared stream type
func (s *StreamConfig) validateURLScheme() error {
	urlLower := strings.ToLower(s.URL)

	switch s.Type {
	case StreamTypeRTSP:
		if !strings.HasPrefix(urlLower, "rtsp://") && !strings.HasPrefix(urlLower, "rtsps://") {
			return fmt.Errorf("stream '%s': RTSP type requires rtsp:// or rtsps:// URL", s.Name)
		}
	case StreamTypeHTTP:
		if !strings.HasPrefix(urlLower, "http://") && !strings.HasPrefix(urlLower, "https://") {
			return fmt.Errorf("stream '%s': HTTP type requires http:// or https:// URL", s.Name)
		}
	case StreamTypeHLS:
		if !strings.HasPrefix(urlLower, "http://") && !strings.HasPrefix(urlLower, "https://") {
			return fmt.Errorf("stream '%s': HLS type requires http:// or https:// URL", s.Name)
		}
	case StreamTypeRTMP:
		if !strings.HasPrefix(urlLower, "rtmp://") && !strings.HasPrefix(urlLower, "rtmps://") {
			return fmt.Errorf("stream '%s': RTMP type requires rtmp:// or rtmps:// URL", s.Name)
		}
	case StreamTypeUDP:
		if !strings.HasPrefix(urlLower, "udp://") && !strings.HasPrefix(urlLower, "rtp://") {
			return fmt.Errorf("stream '%s': UDP type requires udp:// or rtp:// URL", s.Name)
		}
	}

	return nil
}

// ApplyStreamDefaults sets default transport for RTSP/RTMP streams that have an empty
// transport field. This handles the case where users write the new streams: YAML format
// directly without specifying per-stream transport — the global RTSPSettings.Transport
// (defaulting to "tcp") is propagated to each applicable stream.
func (r *RTSPSettings) ApplyStreamDefaults() {
	globalTransport := r.Transport
	if globalTransport == "" {
		globalTransport = DefaultTransport
	}
	for _, stream := range r.AllStreams() {
		if stream.Transport == "" && (stream.Type == StreamTypeRTSP || stream.Type == StreamTypeRTMP) {
			stream.Transport = globalTransport
		}
	}
}

// ValidateStreams validates the streams collection for uniqueness and individual validity
func (r *RTSPSettings) ValidateStreams() error {
	names := make(map[string]bool)
	urls := make(map[string]bool)

	for i, stream := range r.AllStreams() {
		// Validate individual stream
		if err := stream.Validate(); err != nil {
			return fmt.Errorf("stream %d: %w", i+1, err)
		}

		// Check for duplicate names (case-insensitive)
		nameLower := strings.ToLower(stream.Name)
		if names[nameLower] {
			return fmt.Errorf("duplicate stream name: '%s'", stream.Name)
		}
		names[nameLower] = true

		// Check for duplicate URLs
		if urls[stream.URL] {
			return fmt.Errorf("stream '%s' has a duplicate URL: '%s'", stream.Name, stream.URL)
		}
		urls[stream.URL] = true
	}

	return nil
}

// Validate validates a single audio source configuration.
// It normalizes whitespace on Name, Device, and Model in-place.
func (a *AudioSourceConfig) Validate() error {
	// Normalize fields in-place so downstream code sees trimmed values.
	a.Name = strings.TrimSpace(a.Name)
	a.Device = strings.TrimSpace(a.Device)
	a.Model = strings.TrimSpace(a.Model)

	// Name is required
	if a.Name == "" {
		return fmt.Errorf("audio source name is required")
	}
	if len(a.Name) > MaxAudioSourceNameLength {
		return fmt.Errorf("audio source name '%s' exceeds maximum length of %d characters", a.Name, MaxAudioSourceNameLength)
	}

	// Device is required
	if a.Device == "" {
		return fmt.Errorf("audio source device is required for '%s'", a.Name)
	}

	// Reject device strings that look like GPS coordinates. Users have
	// pasted values like ":45.5,-120.5" into the device field (probably
	// intended for the location/coordinates setting) which bypasses
	// argument parsing in ALSA/pipewire/pulse and causes the audio
	// engine to fail forever on every startup. Catch it up front with
	// a clear error pointing at the right setting.
	if gpsCoordPattern.MatchString(a.Device) {
		return fmt.Errorf("audio source '%s': device %q looks like GPS coordinates; set latitude/longitude under birdnet.latitude and birdnet.longitude instead and pick a real audio device", a.Name, a.Device)
	}

	// Validate gain range (NaN/Inf bypass < and > comparisons)
	if math.IsNaN(a.Gain) || math.IsInf(a.Gain, 0) || a.Gain < MinAudioGain || a.Gain > MaxAudioGain {
		return fmt.Errorf("audio source '%s': gain %.1f dB out of range [%.0f, +%.0f]", a.Name, a.Gain, MinAudioGain, MaxAudioGain)
	}

	// Validate sample rate if specified (0 means use default 48000)
	if a.SampleRate != 0 {
		if !slices.Contains(validSampleRates, a.SampleRate) {
			rateStrs := make([]string, len(validSampleRates))
			for i, r := range validSampleRates {
				rateStrs[i] = strconv.Itoa(r)
			}
			return fmt.Errorf("audio source '%s': sample rate %d Hz is not a supported value (valid: %s)", a.Name, a.SampleRate, strings.Join(rateStrs, ", "))
		}
	}

	// Validate model identifier
	if !ValidAudioModels[a.Model] {
		return fmt.Errorf("audio source '%s': unknown model '%s'", a.Name, a.Model)
	}

	// Validate per-source EQ if set
	if a.Equalizer != nil {
		if err := validateEQFilters(a.Equalizer.Filters, fmt.Sprintf("audio source '%s'", a.Name)); err != nil {
			return err
		}
	}

	// Validate quiet hours
	if err := ValidateQuietHours(&a.QuietHours, fmt.Sprintf("audio source '%s'", a.Name)); err != nil {
		return err
	}

	return nil
}

// ValidateSources validates all audio source configurations, including
// duplicate name and device detection.
func (a *AudioSettings) ValidateSources() error {
	names := make(map[string]bool)
	devices := make(map[string]bool)

	for i := range a.Sources {
		src := &a.Sources[i]
		if err := src.Validate(); err != nil {
			return fmt.Errorf("audio source %d: %w", i+1, err)
		}

		// Check for duplicate names (case-insensitive)
		nameLower := strings.ToLower(src.Name)
		if names[nameLower] {
			return fmt.Errorf("duplicate audio source name: '%s'", src.Name)
		}
		names[nameLower] = true

		// Check for duplicate devices
		if devices[src.Device] {
			return fmt.Errorf("audio source '%s' has a duplicate device: '%s'", src.Name, src.Device)
		}
		devices[src.Device] = true
	}

	return nil
}

// clearFfmpegMetadata resets all FFmpeg-related fields when path validation fails.
func (s *AudioSettings) clearFfmpegMetadata() {
	s.FfmpegPath = ""
	s.FfmpegVersion = ""
	s.FfmpegMajor = 0
	s.FfmpegMinor = 0
	s.FfprobePath = ""
}

// applyFfmpegFormatFallback forces WAV export when FFmpeg is unavailable and
// export is enabled. Does nothing when export is disabled.
func (s *AudioSettings) applyFfmpegFormatFallback() {
	if s.Export.Enabled && s.FfmpegPath == "" && s.Export.Type != AudioExportTypeWAV {
		GetLogger().Warn("FFmpeg not available, forcing WAV format for audio export",
			logger.String("previous_type", s.Export.Type))
		s.Export.Type = AudioExportTypeWAV
	}
}

// ffmpegVersionDetector resolves the version of the ffmpeg binary at a validated
// path by executing it. It is a package variable so unit tests can replace it
// with a stub. validateAudioSettings runs across many parallel test cases that
// configure placeholder ffmpeg paths (stub files under t.TempDir()), and
// concurrently exec'ing such a non-executable stub crashes the Windows race
// detector with STATUS_STACK_BUFFER_OVERRUN. Production always uses the real
// detector; only tests override it (see validate_audio_export_test.go).
var ffmpegVersionDetector = GetFfmpegVersionFrom

// validateAudioSettings validates the audio settings and sets ffmpeg and sox paths
func validateAudioSettings(settings *AudioSettings) error {
	// Validate and determine the effective FFmpeg path
	validatedFfmpegPath, ffmpegErr := ValidateToolPath(settings.FfmpegPath, GetFfmpegBinaryName())
	if ffmpegErr != nil {
		logValidationWarning(ffmpegErr, "audio-tool-ffmpeg", "ffmpeg-not-available")
		settings.clearFfmpegMetadata()
	} else {
		settings.FfmpegPath = validatedFfmpegPath // Store the validated path (explicit or from PATH)

		// Detect FFmpeg version using the validated path (not PATH lookup)
		version, major, minor := ffmpegVersionDetector(validatedFfmpegPath)
		settings.FfmpegVersion = version
		settings.FfmpegMajor = major
		settings.FfmpegMinor = minor

		if major > 0 {
			GetLogger().Debug("Detected FFmpeg version", logger.String("version", version), logger.Int("major", major), logger.Int("minor", minor))
		} else {
			GetLogger().Warn("Could not detect FFmpeg version", logger.String("version_string", version))
		}

		// Derive ffprobe path from the validated ffmpeg path. FFprobe is a
		// sibling binary in the same directory (e.g. ffmpeg.exe -> ffprobe.exe).
		ffprobeDir := filepath.Dir(validatedFfmpegPath)
		ffprobePath := filepath.Join(ffprobeDir, GetFfprobeBinaryName())
		if info, statErr := os.Stat(ffprobePath); statErr == nil && !info.IsDir() {
			settings.FfprobePath = ffprobePath
			GetLogger().Debug("FFprobe path derived from FFmpeg", logger.String("ffprobe_path", ffprobePath))
		} else {
			if lookPath, lookErr := exec.LookPath(GetFfprobeBinaryName()); lookErr == nil {
				settings.FfprobePath = lookPath
				GetLogger().Debug("FFprobe found in system PATH", logger.String("ffprobe_path", lookPath))
			} else {
				settings.FfprobePath = ""
				GetLogger().Warn("FFprobe not found alongside FFmpeg or in PATH",
					logger.String("expected_path", ffprobePath),
					logger.String("impact", "Audio validation via FFprobe will be disabled"))
			}
		}
	}

	// Validate and determine the effective SoX path, using the same
	// ValidateToolPath pattern as FFmpeg (configured path first, then PATH).
	validatedSoxPath, soxErr := ValidateToolPath(settings.SoxPath, GetSoxBinaryName())
	if soxErr != nil {
		settings.SoxPath = ""
		settings.SoxAudioTypes = nil
		GetLogger().Warn("SoX not available", logger.Error(soxErr), logger.String("impact", "Spectrogram generation via SoX will be disabled"))
	} else {
		settings.SoxPath = validatedSoxPath
		settings.SoxAudioTypes = GetSoxFormats(validatedSoxPath)
	}

	// Validate audio sources
	if err := settings.ValidateSources(); err != nil {
		return errors.New(err).
			Category(errors.CategoryValidation).
			Context("validation_type", "audio-sources").
			Build()
	}

	// Validate global quiet hours (legacy fallback)
	if err := ValidateQuietHours(&settings.QuietHours, "sound card"); err != nil {
		return errors.New(err).
			Category(errors.CategoryValidation).
			Context("validation_type", "audio-quiet-hours").
			Build()
	}

	// Validate audio export settings.
	//
	// Normalize first, validate second. Type must be normalized even when
	// Export.Enabled is false, because the Enabled flag can be flipped back on
	// without triggering validation of the already-persisted Type value.
	// An empty Type previously produced extension-less clip_name rows in the
	// DB, causing audio download and spectrogram generation to 404
	// (GitHub #2810, #2814).
	if strings.TrimSpace(settings.Export.Type) == "" {
		GetLogger().Warn("audio export type is empty, normalizing to wav",
			logger.String("previous_type", settings.Export.Type),
			logger.String("action", "normalize_to_wav"))
		settings.Export.Type = AudioExportTypeWAV
	}

	// Reject unknown Type before any fallback runs. Doing this first means a
	// garbage value cannot be silently rewritten to WAV by the ffmpeg-missing
	// fallback below, which would hide the misconfiguration.
	switch settings.Export.Type {
	case AudioExportTypeWAV, AudioExportTypeFLAC,
		AudioExportTypeAAC, AudioExportTypeOPUS, AudioExportTypeMP3:
		// known format
	default:
		return errors.Newf("unsupported audio export type: %s", settings.Export.Type).
			Category(errors.CategoryValidation).
			Context("validation_type", "audio-export-type").
			Context("export_type", settings.Export.Type).
			Build()
	}

	settings.applyFfmpegFormatFallback()

	// Bitrate only matters for lossy formats and only when export is enabled.
	switch settings.Export.Type {
	case AudioExportTypeAAC, AudioExportTypeOPUS, AudioExportTypeMP3:
		if settings.Export.Enabled {
			if err := validateExportBitrate(settings.Export.Type, settings.Export.Bitrate); err != nil {
				return err
			}
		}
	}

	// Validate export path when export is enabled
	if settings.Export.Enabled {
		if err := validateExportPath(settings.Export.Path); err != nil {
			return err
		}
	}

	// Validate global EQ filters
	if settings.Equalizer.Enabled && len(settings.Equalizer.Filters) > 0 {
		if err := validateEQFilters(settings.Equalizer.Filters, "global equalizer"); err != nil {
			return errors.New(err).
				Category(errors.CategoryValidation).
				Context("validation_type", "audio-global-eq").
				Build()
		}
	}

	// Remaining checks (length, pre-capture, gain, normalization) only make
	// sense when export is actually enabled.
	if settings.Export.Enabled {
		// Validate capture length (10-60 seconds)
		if settings.Export.Length < 10 || settings.Export.Length > 60 {
			return errors.Newf("audio capture length must be between 10 and 60 seconds, got %d", settings.Export.Length).
				Category(errors.CategoryValidation).
				Context("validation_type", "audio-export-capture-length").
				Context("capture_length", settings.Export.Length).
				Build()
		}

		// Validate pre-capture (max 1/2 of capture length)
		maxPreCapture := settings.Export.Length / 2
		if settings.Export.PreCapture < 0 || settings.Export.PreCapture > maxPreCapture {
			return errors.Newf("audio pre-capture must be between 0 and %d seconds (1/2 of capture length), got %d", maxPreCapture, settings.Export.PreCapture).
				Category(errors.CategoryValidation).
				Context("validation_type", "audio-export-precapture").
				Context("precapture", settings.Export.PreCapture).
				Context("max_precapture", maxPreCapture).
				Context("capture_length", settings.Export.Length).
				Build()
		}

		// Validate gain setting (reasonable range for audio processing)
		if math.IsNaN(settings.Export.Gain) || math.IsInf(settings.Export.Gain, 0) || settings.Export.Gain < MinAudioGain || settings.Export.Gain > MaxAudioGain {
			return errors.Newf("audio gain must be between %.0f and +%.0f dB, got %.1f", MinAudioGain, MaxAudioGain, settings.Export.Gain).
				Category(errors.CategoryValidation).
				Context("validation_type", "audio-export-gain").
				Context("gain", settings.Export.Gain).
				Context("min_gain", MinAudioGain).
				Context("max_gain", MaxAudioGain).
				Build()
		}

		// Validate normalization settings if enabled
		if settings.Export.Normalization.Enabled {
			if err := validateNormalizationSettings(&settings.Export.Normalization, settings.Export.Gain); err != nil {
				return err
			}
		}
	}

	return nil
}

// Bitrate constraints for lossy audio export formats (mp3, aac, opus).
// The suffix and kbps bounds are consumed by validateExportBitrate and
// documented in the frontend's getBitrateConfig (audioValidation.ts).
const (
	audioExportBitrateSuffix  = "k"
	minAudioExportBitrateKbps = 32
	maxAudioExportBitrateKbps = 320
)

// validateExportBitrate validates the bitrate setting for lossy audio export
// formats. The value must use the "k" suffix and fall within the supported
// kbps range.
func validateExportBitrate(exportType, bitrate string) error {
	if !strings.HasSuffix(bitrate, audioExportBitrateSuffix) {
		return errors.Newf("invalid bitrate format for %s: %s. Must end with %q (e.g., '64k')", exportType, bitrate, audioExportBitrateSuffix).
			Category(errors.CategoryValidation).
			Context("validation_type", "audio-export-bitrate-format").
			Context("export_type", exportType).
			Context("bitrate", bitrate).
			Build()
	}
	bitrateValue, err := strconv.Atoi(strings.TrimSuffix(bitrate, audioExportBitrateSuffix))
	if err != nil {
		return errors.Newf("invalid bitrate value for %s: %s", exportType, bitrate).
			Category(errors.CategoryValidation).
			Context("validation_type", "audio-export-bitrate-value").
			Context("export_type", exportType).
			Context("bitrate", bitrate).
			Build()
	}
	if bitrateValue < minAudioExportBitrateKbps || bitrateValue > maxAudioExportBitrateKbps {
		return errors.Newf("bitrate for %s must be between %dk and %dk, got %dk",
			exportType, minAudioExportBitrateKbps, maxAudioExportBitrateKbps, bitrateValue).
			Category(errors.CategoryValidation).
			Context("validation_type", "audio-export-bitrate-range").
			Context("export_type", exportType).
			Context("bitrate_value", bitrateValue).
			Build()
	}
	return nil
}

// validateNormalizationSettings validates the EBU R128 normalization parameters
// (target LUFS, loudness range, and true peak) and warns if gain is also configured.
func validateNormalizationSettings(norm *NormalizationSettings, gain float64) error {
	if math.IsNaN(norm.TargetLUFS) || math.IsInf(norm.TargetLUFS, 0) || norm.TargetLUFS < MinTargetLUFS || norm.TargetLUFS > MaxTargetLUFS {
		return errors.Newf("normalization target LUFS must be between %.0f and %.0f, got %.1f", MinTargetLUFS, MaxTargetLUFS, norm.TargetLUFS).
			Category(errors.CategoryValidation).
			Context("validation_type", "audio-normalization-target").
			Context("target_lufs", norm.TargetLUFS).
			Context("min_target_lufs", MinTargetLUFS).
			Context("max_target_lufs", MaxTargetLUFS).
			Build()
	}
	if math.IsNaN(norm.LoudnessRange) || math.IsInf(norm.LoudnessRange, 0) || norm.LoudnessRange < MinLoudnessRange || norm.LoudnessRange > MaxLoudnessRange {
		return errors.Newf("normalization loudness range must be between %.0f and %.0f LU, got %.1f", MinLoudnessRange, MaxLoudnessRange, norm.LoudnessRange).
			Category(errors.CategoryValidation).
			Context("validation_type", "audio-normalization-range").
			Context("loudness_range", norm.LoudnessRange).
			Context("min_loudness_range", MinLoudnessRange).
			Context("max_loudness_range", MaxLoudnessRange).
			Build()
	}
	if math.IsNaN(norm.TruePeak) || math.IsInf(norm.TruePeak, 0) || norm.TruePeak < MinTruePeak || norm.TruePeak > MaxTruePeak {
		return errors.Newf("normalization true peak must be between %.0f and %.0f dBTP, got %.1f", MinTruePeak, MaxTruePeak, norm.TruePeak).
			Category(errors.CategoryValidation).
			Context("validation_type", "audio-normalization-peak").
			Context("true_peak", norm.TruePeak).
			Context("min_true_peak", MinTruePeak).
			Context("max_true_peak", MaxTruePeak).
			Build()
	}
	if gain != 0 {
		GetLogger().Warn("Both gain and normalization are configured", logger.String("action", "Normalization will take precedence, gain setting will be ignored"))
	}
	return nil
}

// validateExportPath rejects export paths that contain path traversal sequences
// or null bytes. Both relative and absolute paths are accepted: Docker
// containers and install.sh legitimately use absolute paths like /data/clips/.
func validateExportPath(path string) error {
	if path == "" {
		return nil
	}

	if strings.ContainsRune(path, '\x00') {
		return errors.Newf("audio export path must not contain null bytes: %q", path).
			Category(errors.CategoryValidation).
			Context("validation_type", "audio-export-path").
			Context("path", path).
			Build()
	}

	//nolint:gocritic // ruleguard suggests IsLocal alone, but IsLocal cleans "../x" to "x" (valid!); explicit ".." check is required for untrusted input per internal/CLAUDE.md
	if strings.Contains(path, "..") {
		return errors.Newf("audio export path must not contain path traversal (..): %q", path).
			Category(errors.CategoryValidation).
			Context("validation_type", "audio-export-path").
			Context("path", path).
			Build()
	}

	// For relative paths, also check filepath.IsLocal which rejects
	// reserved names on Windows (NUL, COM1, etc.).
	if !filepath.IsAbs(path) {
		cleanPath := filepath.Clean(path)
		if !filepath.IsLocal(cleanPath) {
			return errors.Newf("audio export path is not a safe local path: %q", path).
				Category(errors.CategoryValidation).
				Context("validation_type", "audio-export-path").
				Context("path", path).
				Build()
		}
	}

	return nil
}

// validateEQFilters validates a slice of equalizer filters. The context string
// is used for error messages (e.g. "global equalizer" or "audio source 'Mic'").
func validateEQFilters(filters []EqualizerFilter, context string) error {
	for i, f := range filters {
		// NaN comparisons always return false, so NaN would bypass range checks.
		// YAML supports .nan literals; reject them explicitly.
		if math.IsNaN(f.Frequency) || math.IsNaN(f.Q) {
			return fmt.Errorf("%s: filter %d has NaN value for frequency or Q (must be a valid number)", context, i+1)
		}
		if f.Frequency <= 0 {
			return fmt.Errorf("%s: filter %d has invalid frequency %.1f (must be positive)", context, i+1, f.Frequency)
		}
		if f.Frequency > MaxEQFrequency {
			return fmt.Errorf("%s: filter %d frequency %.1f exceeds maximum %.0f Hz", context, i+1, f.Frequency, MaxEQFrequency)
		}
		if f.Q <= 0 {
			return fmt.Errorf("%s: filter %d has invalid Q factor %.4f (must be positive)", context, i+1, f.Q)
		}
		if f.Q > MaxEQQ {
			return fmt.Errorf("%s: filter %d Q factor %.1f exceeds maximum %.0f", context, i+1, f.Q, MaxEQQ)
		}
	}
	return nil
}
