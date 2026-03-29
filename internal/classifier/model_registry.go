// model_registry.go contains information about supported models and their properties
package classifier

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// ModelInfo represents metadata about a BirdNET model
type ModelInfo struct {
	ID               string    // Unique identifier for the model
	Name             string    // User-friendly name
	Description      string    // Description of the model
	Spec             ModelSpec // Audio requirements (sample rate, clip length)
	SupportedLocales []string  // List of supported locale codes
	DefaultLocale    string    // Default locale if none is specified
	NumSpecies       int       // Number of species in the model
	CustomPath       string    // Path to custom model file, if any
}

// Predefined supported models
var supportedModels = map[string]ModelInfo{
	"BirdNET_GLOBAL_6K_V2.4": {
		ID:          "BirdNET_GLOBAL_6K_V2.4",
		Name:        "BirdNET GLOBAL 6K V2.4",
		Description: "Global model with 6523 species",
		Spec:        ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		SupportedLocales: []string{"af", "ar", "bg", "ca", "cs", "da", "de", "el", "en-uk", "en-us", "es",
			"et", "fi", "fr", "he", "hr", "hu", "id", "is", "it", "ja", "ko", "lt", "lv", "ml", "nl",
			"no", "pl", "pt", "pt-br", "pt-pt", "ro", "ru", "sk", "sl", "sr", "sv", "th", "tr", "uk", "zh"},
		DefaultLocale: conf.DefaultFallbackLocale,
		NumSpecies:    6523,
	},
	"Perch_V2": {
		ID:          "Perch_V2",
		Name:        "Google Perch V2",
		Description: "Perch v2 model with ~14,795 species (scientific names only)",
		Spec:        ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second},
		NumSpecies:  14795,
	},
}

// knownSpecs maps model family identifiers to their audio specifications.
// Used by the Orchestrator to determine buffer sizes and resampling needs.
var knownSpecs = map[string]ModelSpec{
	"BirdNET_V2.4": {SampleRate: 48000, ClipLength: 3 * time.Second},
	"BirdNET_V3.0": {SampleRate: 32000, ClipLength: 5 * time.Second},
	"Perch_V2":     {SampleRate: 32000, ClipLength: 5 * time.Second},
}

// DetermineModelInfo identifies the model type from a file path or model identifier
func DetermineModelInfo(modelPathOrID string) (ModelInfo, error) {
	// Check if it's a known model ID
	if info, exists := supportedModels[modelPathOrID]; exists {
		return info, nil
	}

	// If it's a path to a custom model file
	if strings.HasSuffix(modelPathOrID, ".tflite") {
		// Try to determine model type from filename
		baseName := filepath.Base(modelPathOrID)

		// Check if it matches known patterns
		for id := range supportedModels {
			if strings.Contains(baseName, id) {
				// Clone the model info but mark it as custom
				customInfo := supportedModels[id]
				customInfo.CustomPath = modelPathOrID
				return customInfo, nil
			}
		}

		// Check if filename matches a known spec pattern
		for specID, spec := range knownSpecs {
			if strings.Contains(strings.ToLower(baseName), strings.ToLower(specID)) {
				return ModelInfo{
					ID:          specID,
					Name:        specID + " (Custom)",
					Description: fmt.Sprintf("Custom model from %s", baseName),
					Spec:        spec,
					CustomPath:  modelPathOrID,
				}, nil
			}
		}

		// If we couldn't identify it, create a generic custom model entry
		return ModelInfo{
			ID:               "Custom",
			Name:             "Custom Model",
			Description:      fmt.Sprintf("Custom model from %s", baseName),
			CustomPath:       modelPathOrID,
			DefaultLocale:    "en",
			SupportedLocales: []string{},
		}, nil
	}

	return ModelInfo{}, fmt.Errorf("unrecognized model: %s", modelPathOrID)
}

// IsLocaleSupported checks if a locale is supported by the given model
func IsLocaleSupported(modelInfo *ModelInfo, locale string) bool {
	// If it's a custom model with no specified locales, assume all are supported
	if len(modelInfo.SupportedLocales) == 0 {
		return true
	}

	// Normalize the input locale to lowercase
	normalizedLocale := strings.ToLower(locale)

	// Also try with hyphen replaced by underscore and vice versa
	alternateLocale := normalizedLocale
	if strings.Contains(normalizedLocale, "-") {
		alternateLocale = strings.ReplaceAll(normalizedLocale, "-", "_")
	} else if strings.Contains(normalizedLocale, "_") {
		alternateLocale = strings.ReplaceAll(normalizedLocale, "_", "-")
	}

	// Create a set of all valid forms of the locale for efficient lookup
	validForms := map[string]bool{
		normalizedLocale: true,
		alternateLocale:  true,
	}

	// Check if any supported locale matches any of our valid forms
	for _, supported := range modelInfo.SupportedLocales {
		if validForms[strings.ToLower(supported)] {
			return true
		}
	}

	return false
}
