// model_registry.go contains information about supported models and their properties
package birdnet

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// ModelInfo represents metadata about a BirdNET model
type ModelInfo struct {
	ID               string   // Unique identifier for the model
	Name             string   // User-friendly name
	Description      string   // Description of the model
	SupportedLocales []string // List of supported locale codes
	DefaultLocale    string   // Default locale if none is specified
	NumSpecies       int      // Number of species in the model
	CustomPath       string   // Path to custom model file, if any
}

// Predefined supported models
var supportedModels = map[string]ModelInfo{
	"BirdNET_GLOBAL_6K_V2.4": {
		ID:          "BirdNET_GLOBAL_6K_V2.4",
		Name:        "BirdNET GLOBAL 6K V2.4",
		Description: "Global model with 6523 species",
		SupportedLocales: []string{"af", "ar", "bg", "ca", "cs", "da", "de", "el", "en-uk", "en-us", "es",
			"et", "fi", "fr", "he", "hr", "hu", "id", "is", "it", "ja", "ko", "lt", "lv", "ml", "nl",
			"no", "pl", "pt", "pt-br", "pt-pt", "ro", "ru", "sk", "sl", "sr", "sv", "th", "tr", "uk", "zh"},
		DefaultLocale: conf.DefaultFallbackLocale,
		NumSpecies:    6523,
	},
	"sound_id": {
		ID:               "sound_id",
		Name:             "sound_id",
		Description:      "Merlin-style sound ID operating on a spectrogram",
		SupportedLocales: []string{"en-us"},
		DefaultLocale:    "en-us",
		NumSpecies:       2067,
	},
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
		for id, info := range supportedModels {
			if strings.Contains(baseName, id) {
				// Clone the model info but mark it as custom
				customInfo := info
				customInfo.CustomPath = modelPathOrID
				return customInfo, nil
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

func IsMerlinStyle(modelInfo *ModelInfo) bool {
	return strings.HasPrefix(modelInfo.Name, "sound_id")
}

func ShouldApplySigmoid(modelInfo *ModelInfo) bool {
	return !IsMerlinStyle(modelInfo)
}

func RequiresSpectrogramGeneration(modelInfo *ModelInfo) bool {
	return IsMerlinStyle(modelInfo)
}
