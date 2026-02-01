package detection

// Default model constants.
const (
	DefaultModelName    = "BirdNET"
	DefaultModelVersion = "2.4"
	DefaultModelVariant = "default"
)

// ModelInfo describes the AI model used for detection.
type ModelInfo struct {
	Name           string  // e.g., "BirdNET"
	Version        string  // e.g., "2.4"
	Variant        string  // e.g., "default", "finland_birds"
	ClassifierPath *string // path to custom classifier file, nil for default
}

// DefaultModelInfo returns the default BirdNET model info.
func DefaultModelInfo() ModelInfo {
	return ModelInfo{
		Name:           DefaultModelName,
		Version:        DefaultModelVersion,
		Variant:        DefaultModelVariant,
		ClassifierPath: nil,
	}
}
