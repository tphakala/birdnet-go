package detection

import "github.com/tphakala/birdnet-go/internal/datastore/v2/entities"

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

// ResolveModelType determines the entity ModelType from a model's name and version.
// BattyBirdNET models are bat, BirdNET v3.0+ and Perch are multi-taxa wildlife,
// and everything else (BirdNET v2.4, BSG, unknown) is bird.
func ResolveModelType(name, version string) entities.ModelType {
	switch {
	case name == "BattyBirdNET":
		return entities.ModelTypeBat
	case name == "Perch":
		return entities.ModelTypeMulti
	case name == "BirdNET" && version != "" && version != "2.4":
		return entities.ModelTypeMulti
	default:
		return entities.ModelTypeBird
	}
}
