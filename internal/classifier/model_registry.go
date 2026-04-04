// model_registry.go contains the single source of truth for supported models.
package classifier

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/detection"
)

// modelIDCustom is the sentinel ID used for user-supplied model files that
// cannot be matched to a known registry entry.
const modelIDCustom = "Custom"

// Model display names — single source of truth for user-facing model names.
const (
	ModelNameBirdNETv24 = "BirdNET v2.4"
	ModelNameBirdNETv30 = "BirdNET v3.0"
	ModelNamePerchV2    = "Google Perch v2"
)

// Inference backend identifiers.
const (
	BackendTFLite = "TFLite"
	BackendONNX   = "ONNX"
)

// ModelInfo represents metadata about a classifier model.
type ModelInfo struct {
	ID               string    // Unique registry identifier (e.g., "BirdNET_V2.4")
	Name             string    // User-friendly name (e.g., "BirdNET v2.4")
	Backend          string    // Inference backend: "TFLite" or "ONNX"
	DetectionName    string    // Database model name (e.g., "BirdNET", "Perch")
	DetectionVersion string    // Database model version (e.g., "2.4", "V2")
	Description      string    // Description of the model
	Spec             ModelSpec // Audio requirements (sample rate, clip length)
	ConfigAliases    []string  // User-facing config IDs (e.g., ["birdnet"])
	SupportedLocales []string  // List of supported locale codes
	DefaultLocale    string    // Default locale if none is specified
	NumSpecies       int       // Number of species in the model
	CustomPath       string    // Path to custom model file, if any
}

// DisplayName returns the user-facing name including the backend type, e.g. "BirdNET v2.4 (TFLite)".
func (m *ModelInfo) DisplayName() string {
	if m.Backend == "" {
		return m.Name
	}
	return m.Name + " (" + m.Backend + ")"
}

// ModelRegistry is the single source of truth for all supported models.
// All model identity lookups, config validation, and spec queries derive from this.
var ModelRegistry = map[string]ModelInfo{
	"BirdNET_V2.4": {
		ID:               "BirdNET_V2.4",
		Name:             ModelNameBirdNETv24,
		Backend:          BackendTFLite,
		DetectionName:    "BirdNET",
		DetectionVersion: "2.4",
		Description:      "Global model with 6523 species",
		Spec:             ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		ConfigAliases:    []string{"birdnet"},
		SupportedLocales: []string{"af", "ar", "bg", "ca", "cs", "da", "de", "el", "en-uk", "en-us", "es",
			"et", "fi", "fr", "he", "hr", "hu", "id", "is", "it", "ja", "ko", "lt", "lv", "ml", "nl",
			"no", "pl", "pt", "pt-br", "pt-pt", "ro", "ru", "sk", "sl", "sr", "sv", "th", "tr", "uk", "zh"},
		DefaultLocale: "en-uk",
		NumSpecies:    6523,
	},
	"BirdNET_V3.0": {
		ID:               "BirdNET_V3.0",
		Name:             ModelNameBirdNETv30,
		Backend:          BackendONNX,
		DetectionName:    "BirdNET",
		DetectionVersion: "3.0",
		Description:      "BirdNET v3.0 model (32kHz, 5s clips, embeddings)", // NumSpecies omitted: determined at runtime from label file
		Spec:             ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second},
		ConfigAliases:    []string{"birdnet_v3.0"},
		SupportedLocales: []string{"af", "ar", "bg", "ca", "cs", "da", "de", "el", "en-uk", "en-us", "es",
			"et", "fi", "fr", "he", "hr", "hu", "id", "is", "it", "ja", "ko", "lt", "lv", "ml", "nl",
			"no", "pl", "pt", "pt-br", "pt-pt", "ro", "ru", "sk", "sl", "sr", "sv", "th", "tr", "uk", "zh"},
		DefaultLocale: "en-uk",
	},
	"Perch_V2": {
		ID:               "Perch_V2",
		Name:             ModelNamePerchV2,
		Backend:          BackendONNX,
		DetectionName:    "Perch",
		DetectionVersion: "V2",
		Description:      "Perch v2 model with ~14,795 species (scientific names only)",
		Spec:             ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second},
		ConfigAliases:    []string{"perch_v2"},
		NumSpecies:       14795,
	},
}

// birdnetVersionToRegistryID maps user-facing BirdNET version strings to registry IDs.
var birdnetVersionToRegistryID = map[string]string{
	"2.4": "BirdNET_V2.4",
	"3.0": "BirdNET_V3.0",
}

// KnownConfigIDs collects all ConfigAliases from the registry.
// Exported for conf.ValidateModelConfig to use without importing classifier.
func KnownConfigIDs() map[string]bool {
	ids := make(map[string]bool, len(ModelRegistry))
	for id := range ModelRegistry {
		for _, alias := range ModelRegistry[id].ConfigAliases {
			ids[strings.ToLower(alias)] = true
		}
	}
	return ids
}

// GetModelSpec returns the ModelSpec for a registry ID.
func GetModelSpec(registryID string) (ModelSpec, bool) {
	info, ok := ModelRegistry[registryID]
	if !ok {
		return ModelSpec{}, false
	}
	return info.Spec, true
}

// ResolveBirdNETVersion maps a user-facing version string (e.g., "2.4") to a
// ModelInfo from the registry. Returns the ModelInfo and true if found.
func ResolveBirdNETVersion(version string) (ModelInfo, bool) {
	registryID, ok := birdnetVersionToRegistryID[version]
	if !ok {
		return ModelInfo{}, false
	}
	info, ok := ModelRegistry[registryID]
	return info, ok
}

// filenamePatterns maps common filename substrings to registry IDs.
// This covers ONNX naming conventions and legacy filenames whose prefix
// no longer matches the (shortened) registry ID.
var filenamePatterns = map[string]string{
	"birdnet_global_6k_v2.4": "BirdNET_V2.4", // legacy TFLite/label filenames
	"birdnet-v24":            "BirdNET_V2.4",
	"birdnet_v2.4":           "BirdNET_V2.4",
	"birdnet-v2.4":           "BirdNET_V2.4",
	"birdnet-go_classifier":  "BirdNET_V2.4", // custom-named classifier builds
	"birdnet_global_v3.0":    "BirdNET_V3.0",
	"birdnet-v30":            "BirdNET_V3.0",
	"birdnet_v3.0":           "BirdNET_V3.0",
	"birdnet-v3.0":           "BirdNET_V3.0",
	"perch_v2":               "Perch_V2",
	"perch-v2":               "Perch_V2",
}

// DetermineModelInfo identifies the model type from a file path or model identifier.
// This is the fallback path — prefer passing ModelInfo directly from the orchestrator
// or resolving via config version field.
func DetermineModelInfo(modelPathOrID string) (ModelInfo, error) {
	// Check if it's a known registry ID
	if info, exists := ModelRegistry[modelPathOrID]; exists {
		return info, nil
	}

	// If it's a path to a model file
	ext := strings.ToLower(filepath.Ext(modelPathOrID))
	if ext == ".tflite" || ext == ".onnx" {
		baseName := filepath.Base(modelPathOrID)
		lowerBase := strings.ToLower(baseName)

		// Check against registry IDs in the filename
		for id := range ModelRegistry {
			if strings.Contains(lowerBase, strings.ToLower(id)) {
				customInfo := ModelRegistry[id]
				customInfo.CustomPath = modelPathOrID
				return customInfo, nil
			}
		}

		// Check known filename patterns (ONNX conventions, legacy names, etc.)
		for pattern, registryID := range filenamePatterns {
			if strings.Contains(lowerBase, pattern) {
				info := ModelRegistry[registryID]
				info.CustomPath = modelPathOrID
				return info, nil
			}
		}

		// Unrecognized model file — return Custom, let runtime figure it out
		return ModelInfo{
			ID:               modelIDCustom,
			Name:             "Custom Model",
			Description:      fmt.Sprintf("Custom model from %s", baseName),
			CustomPath:       modelPathOrID,
			DefaultLocale:    "en",
			SupportedLocales: []string{},
		}, nil
	}

	return ModelInfo{}, fmt.Errorf("unrecognized model: %s", modelPathOrID)
}

// ResolveConfigModelID maps a user-facing config model ID (e.g. "birdnet") to
// the internal registry ID (e.g. "BirdNET_V2.4") by iterating
// ModelRegistry.ConfigAliases. Returns the registry ID and true if found,
// or empty string and false if unknown. Case-insensitive.
func ResolveConfigModelID(configID string) (string, bool) {
	for registryID := range ModelRegistry {
		for _, alias := range ModelRegistry[registryID].ConfigAliases {
			if strings.EqualFold(alias, configID) {
				return registryID, true
			}
		}
	}
	return "", false
}

// ToDetectionModelInfo converts classifier model metadata to the detection domain
// ModelInfo used for database storage in the ai_models table.
// Returns DefaultModelInfo() if DetectionName is empty (e.g. custom models not in the registry).
func (m *ModelInfo) ToDetectionModelInfo() detection.ModelInfo {
	if m.DetectionName == "" {
		return detection.DefaultModelInfo()
	}
	variant := "default"
	var classifierPath *string
	if m.CustomPath != "" {
		classifierPath = &m.CustomPath
		variant = "custom"
	}
	return detection.ModelInfo{
		Name:           m.DetectionName,
		Version:        m.DetectionVersion,
		Variant:        variant,
		ClassifierPath: classifierPath,
	}
}

// DetectionModelInfoForID returns the detection.ModelInfo for a registry model ID.
// Returns DefaultModelInfo() if the ID is not found in the registry.
func DetectionModelInfoForID(modelID string) detection.ModelInfo {
	if info, ok := ModelRegistry[modelID]; ok {
		return info.ToDetectionModelInfo()
	}
	return detection.DefaultModelInfo()
}

// IsLocaleSupported checks if a locale is supported by the given model.
func IsLocaleSupported(modelInfo *ModelInfo, locale string) bool {
	// If it's a custom model with no specified locales, assume all are supported.
	// Registered models with empty SupportedLocales genuinely have no locale support.
	if len(modelInfo.SupportedLocales) == 0 {
		return modelInfo.ID == modelIDCustom
	}

	normalizedLocale := strings.ToLower(locale)

	alternateLocale := normalizedLocale
	if strings.Contains(normalizedLocale, "-") {
		alternateLocale = strings.ReplaceAll(normalizedLocale, "-", "_")
	} else if strings.Contains(normalizedLocale, "_") {
		alternateLocale = strings.ReplaceAll(normalizedLocale, "_", "-")
	}

	validForms := map[string]bool{
		normalizedLocale: true,
		alternateLocale:  true,
	}

	for _, supported := range modelInfo.SupportedLocales {
		if validForms[strings.ToLower(supported)] {
			return true
		}
	}

	return false
}
