package detection

// Default model constants.
const (
	DefaultModelName    = "BirdNET"
	DefaultModelVersion = "2.4"
)

// ModelInfo describes the AI model used for detection.
type ModelInfo struct {
	Name    string // e.g., "BirdNET"
	Version string // e.g., "2.4"
	Custom  bool   // true if user-provided custom model
}

// DefaultModelInfo returns the default BirdNET model info.
func DefaultModelInfo() ModelInfo {
	return ModelInfo{
		Name:    DefaultModelName,
		Version: DefaultModelVersion,
		Custom:  false,
	}
}
