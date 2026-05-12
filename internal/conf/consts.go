// conf/consts.go hard coded constants
package conf

import "time"

const (
	// Model IDs identify the inference backends available for detection.
	ModelIDBirdNET = "birdnet"
	ModelIDPerchV2 = "perch_v2"
	ModelIDBat     = "bat"
	ModelIDBSG     = "bsg"

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
)

// DefaultSessionDuration is the default session duration (7 days).
const DefaultSessionDuration = 168 * time.Hour
