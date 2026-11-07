// conf/consts.go hard coded constants
package conf

import "time"

const (
	// Model IDs identify the inference backends available for detection.
	ModelIDBirdNET   = "birdnet"
	ModelIDBirdNETV3 = "birdnet_v3.0"
	ModelIDPerchV2   = "perch_v2"
	ModelIDBat       = "bat"
	ModelIDBSG       = "bsg"
	ModelIDBSGBat    = "bsg_bat"

	// Catalog-style model IDs mirror the hyphenated model *catalog* entry IDs
	// (classifier/model_catalog.go) and are accepted as aliases for the canonical
	// underscore IDs above, so a config that carries the catalog-style spelling
	// still validates. Keep in sync with the secondary ConfigAliases in
	// classifier/model_registry.go. See Sentry BIRDNET-GO-2FZ (a config with
	// "perch-v2" was rejected as an unknown model ID).
	ModelIDBirdNETCatalog   = "birdnet-v2.4"
	ModelIDBirdNETV3Catalog = "birdnet-v3.0"
	ModelIDPerchV2Catalog   = "perch-v2"
	ModelIDBSGCatalog       = "bsg-finland"
	ModelIDBSGBatCatalog    = "bsg-bat"

	SampleRate     = 48000 // Sample rate of the audio fed to BirdNET Analyzer
	BitDepth       = 16    // Bit depth of the audio fed to BirdNET Analyzer
	NumChannels    = 1     // Number of channels of the audio fed to BirdNET Analyzer
	BytesPerSample = BitDepth / 8
	CaptureLength  = 3 // Length of audio data fed to BirdNET Analyzer in seconds

	SpeciesConfigCSV  = "species_config.csv"
	SpeciesActionsCSV = "species_actions.csv"

	// BufferSize is the size of the audio buffer in bytes, rounded up to the nearest 2048
	BufferSize = ((SampleRate*NumChannels*CaptureLength*BytesPerSample + 2047) / 2048) * 2048

	// DefaultCaptureBufferSeconds is the default ring buffer duration when extended capture is disabled.
	// Audio.Export.Length must not exceed this value or audio export will be truncated.
	DefaultCaptureBufferSeconds = 120

	// Extended capture defaults
	DefaultExtendedCaptureMaxDuration = 120  // Default max duration in seconds (2 minutes)
	MaxExtendedCaptureDuration        = 1200 // Absolute max (20 minutes)
	ExtendedCaptureBufferMargin       = 60   // Margin added to MaxDuration for buffer sizing
	ExtendedCaptureMinBufferMargin    = 30   // Minimum margin above MaxDuration + PreCapture

	// LiveStream defaults for webserver configuration.
	// Viper nested defaults can be lost when the parent key exists in the config
	// file but the child section is absent, so validation normalizes to these.
	DefaultLiveStreamBitRate        = 128
	MinLiveStreamBitRate            = 16
	MaxLiveStreamBitRate            = 320
	DefaultLiveStreamSegmentLength  = 2
	MinLiveStreamSegmentLength      = 1
	MaxLiveStreamSegmentLength      = 30
	DefaultLiveStreamSampleRate     = 48000
	MinLiveStreamSampleRate         = 8000
	MaxLiveStreamSampleRate         = 96000
	DefaultLiveStreamFFmpegLogLevel = "warning"

	// DefaultWeatherPollInterval is the default weather poll interval in minutes.
	DefaultWeatherPollInterval = 60

	// The viper defaults, named so defaults.go and the incomplete-feature
	// normalization cannot drift apart. Most are applied by normalizeIncompleteFeatures
	// when a feature is switched on and the field was left at its zero value;
	// DefaultNotificationSuppressionHours is the exception, because zero is a legal
	// value there rather than an unwritten one, so only viper uses it.
	DefaultWebServerPort                = "8080"
	DefaultTelemetryListen              = "0.0.0.0:8090"
	DefaultSoundLevelInterval           = 10
	DefaultDynamicThresholdValidHours   = 24
	DefaultNewSpeciesWindowDays         = 7
	DefaultSpeciesSyncIntervalMinutes   = 60
	DefaultYearlyTrackingResetMonth     = 1
	DefaultYearlyTrackingResetDay       = 1
	DefaultYearlyTrackingWindowDays     = 7
	DefaultSeasonalTrackingWindowDays   = 7
	DefaultNotificationSuppressionHours = 168
	DefaultAudioExportLength            = 15
	DefaultAudioExportBitrate           = "96k"
	DefaultNormalizationTargetLUFS      = -23.0
	DefaultNormalizationTruePeak        = -2.0
	DefaultRetentionMaxAge              = "30d"
	DefaultRetentionMaxUsage            = "80%"
	DefaultRetryBackoffMultiplier       = 2.0

	// DefaultLogDeduplicationHealthCheckSeconds is the default log deduplication health check
	// interval in seconds.
	DefaultLogDeduplicationHealthCheckSeconds = 60

	// The bat ultrasonic filter's STFT defaults. normalizeUltrasonicFilter resets an
	// invalid or unwritten value to them on load.
	DefaultUltrasonicFFTSize = 8192
	DefaultUltrasonicHopSize = 4096
	DefaultUltrasonicSplitHz = 20000

	// DefaultEQQFactor is the Q factor applied to an equalizer filter whose q was
	// never set. eqfilter_config.go offers it as the settings UI's default too, so a
	// filter created in the UI and one repaired here get the same value. 0.707 is the
	// conventional rounding of the Butterworth 1/sqrt(2).
	DefaultEQQFactor = 0.707

	// DefaultAlternateMinConfidence is the default lowest confidence, on the 0 to 1
	// scale, at which an alternate prediction of a detection is stored.
	// normalizeAlternateMinConfidence resets an invalid value to it on load.
	DefaultAlternateMinConfidence = 0.1

	// DefaultModelConsensusMinModels is the default number of agreeing models the
	// model consensus filter requires. normalizeModelConsensus resets an invalid
	// value to it on load.
	DefaultModelConsensusMinModels = 2

	// MinModelConsensusMinModels is the lowest accepted model consensus minmodels value.
	MinModelConsensusMinModels = 2

	// MaxModelConsensusMinModels is the highest accepted model consensus minmodels value.
	MaxModelConsensusMinModels = 4
)

// Denoise preset names accepted wherever a clip denoise strength is chosen. They
// map to go-audio-dsp denoiser strengths in internal/audiocore/transcode, which
// imports this package, so the names live here.
const (
	// DenoisePresetLight is the gentlest denoise strength.
	DenoisePresetLight = "light"
	// DenoisePresetLightMedium sits between light and medium.
	DenoisePresetLightMedium = "light-medium"
	// DenoisePresetMedium is the medium denoise strength.
	DenoisePresetMedium = "medium"
	// DenoisePresetHeavy is the strongest denoise strength.
	DenoisePresetHeavy = "heavy"

	// DefaultDenoisePreset is the preset to fall back to when a setting turns
	// denoise on without naming a strength. It does not apply to clip and audio
	// processing requests, where an empty preset means denoise off.
	DefaultDenoisePreset = DenoisePresetLightMedium
)

// IsValidDenoisePresetName reports whether name is one of the denoise preset
// names. The empty string (denoise off) is not a preset name.
func IsValidDenoisePresetName(name string) bool {
	switch name {
	case DenoisePresetLight, DenoisePresetLightMedium, DenoisePresetMedium, DenoisePresetHeavy:
		return true
	default:
		return false
	}
}

// DefaultSessionDuration is the default session duration (7 days).
const DefaultSessionDuration = 168 * time.Hour

// The two runtime profiling sampling rates are NOT here, and not named Default*,
// because they are not what an unset key resolves to: unset means off. They live
// in profiling.go as RecommendedBlockProfileRate and
// RecommendedMutexProfileFraction. This pointer exists because consts.go is
// where a reader looks first.
