// conf/consts.go hard coded constants
package conf

const (
	SampleRate    = 48000 // Sample rate of the audio fed to BirdNET Analyzer
	BitDepth      = 16    // Bit depth of the audio fed to BirdNET Analyzer
	NumChannels   = 1     // Number of channels of the audio fed to BirdNET Analyzer
	CaptureLength = 3     // Length of audio data fed to BirdNET Analyzer in seconds

	SpeciesConfigCSV  = "species_config.csv"
	SpeciesActionsCSV = "species_actions.csv"

	// BufferSize is the size of the audio buffer in bytes, rounded up to the nearest 2048
	BufferSize = ((SampleRate*NumChannels*CaptureLength*BitDepth/8 + 2047) / 2048) * 2048
)
