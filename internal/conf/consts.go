// conf/consts.go hard coded constants
package conf

const (
	SampleRate    = 48000 // Sample rate of the audio fed to BirdNET Analyzer
	BitDepth      = 16    // Bit depth of the audio fed to BirdNET Analyzer
	NumChannels   = 1     // Number of channels of the audio fed to BirdNET Analyzer
	CaptureLength = 3     // Length of audio data fed to BirdNET Analyzer in seconds

	SpeciesConfigCSV  = "species_config.csv"
	SpeciesActionsCSV = "species_actions.csv"
	DogBarkFilterCSV  = "dog_bark_filter.csv"
)
