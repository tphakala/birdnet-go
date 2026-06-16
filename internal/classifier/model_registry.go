// model_registry.go contains the single source of truth for supported models.
package classifier

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
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

// Registry ID constants for model identification across packages.
const (
	RegistryIDBirdNETV3      = "BirdNET_V3.0"
	RegistryIDBSG            = "BSG"
	RegistryIDBat            = "Bat"
	RegistryIDPerchV2        = "Perch_V2"
	RegistryIDBirdNETV24INT8 = "BirdNET_V2.4_INT8"
)

// defaultBirdNETClassifierARM64Arch is the GOARCH for which container images
// ship the INT8-ARM ONNX classifier as the memory-saving default classifier.
const defaultBirdNETClassifierARM64Arch = "arm64"

// DetectionNamePerch is the detection model name for Perch classifiers,
// matching the DetectionName field in the ModelRegistry.
const DetectionNamePerch = "Perch"

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
		ConfigAliases:    []string{conf.ModelIDBirdNET},
		SupportedLocales: []string{"af", "ar", "bg", "ca", "cs", "da", "de", "el", "en-uk", "en-us", "es",
			"et", "fi", "fr", "he", "hr", "hu", "id", "is", "it", "ja", "ko", "lt", "lv", "ml", "nl",
			"no", "pl", "pt", "pt-br", "pt-pt", "ro", "ru", "sk", "sl", "sr", "sv", "th", "tr", "uk", "zh"},
		DefaultLocale: "en-uk",
		NumSpecies:    6523,
	},
	RegistryIDBirdNETV3: {
		ID:               RegistryIDBirdNETV3,
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
	RegistryIDPerchV2: {
		ID:               RegistryIDPerchV2,
		Name:             ModelNamePerchV2,
		Backend:          BackendONNX,
		DetectionName:    DetectionNamePerch,
		DetectionVersion: "V2",
		Description:      "Perch v2 multi-taxa model with ~14,795 species including birds, insects, amphibians, and mammals (scientific names only)",
		Spec:             ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second},
		ConfigAliases:    []string{conf.ModelIDPerchV2},
		NumSpecies:       14795,
	},
	RegistryIDBat: {
		ID:               RegistryIDBat,
		Name:             "Bat Classifier",
		Backend:          BackendONNX,
		DetectionName:    "BattyBirdNET",
		DetectionVersion: "1.0",
		Description:      "Bat species detection using BirdNET v2.4 embeddings",
		Spec:             ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second, RawSampleRate: 256000, MinRawSampleRate: 96000, RecommendedSampleRate: 192000},
		ConfigAliases:    []string{conf.ModelIDBat},
	},
	RegistryIDBSG: {
		ID:               RegistryIDBSG,
		Name:             "BSG Finland",
		Backend:          BackendONNX,
		DetectionName:    "BSG",
		DetectionVersion: "4.4",
		Description:      "Regional bird classifier optimized for Finnish bird species",
		Spec:             ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		ConfigAliases:    []string{conf.ModelIDBSG},
	},
}

// registerINT8Variant derives the INT8-ARM ONNX classifier entry from the
// BirdNET v2.4 TFLite entry so the two stay in lockstep: identical species,
// labels, locales, audio spec, and detection name/version (detections must be
// attributed to "BirdNET 2.4" regardless of backend). Only the registry ID and
// inference backend differ. It is deliberately not user-selectable by config ID
// (ConfigAliases is cleared so it cannot collide with the "birdnet" alias); the
// entry is reached only via the arm64 container default or an explicit ONNX
// model path. Run as init() because it copies the v2.4 map entry.
func init() {
	base := ModelRegistry[DefaultModelVersion]
	base.ID = RegistryIDBirdNETV24INT8
	base.Backend = BackendONNX
	base.ConfigAliases = nil
	base.CustomPath = ""
	base.Description = "BirdNET v2.4 INT8-ARM quantized (ONNX); reduced-memory default for arm64 containers"
	ModelRegistry[RegistryIDBirdNETV24INT8] = base
}

// defaultClassifierModelInfo resolves the classifier used when no model is
// selected via config (the Tier 4 default). On arm64, if the INT8-ARM ONNX
// classifier is present in the standard model paths (shipped only in arm64
// container images), it is preferred to cut peak RSS; otherwise the embedded
// TFLite BirdNET v2.4 model is used. find reports the resolved on-disk path of a
// model filename within the standard search paths.
func defaultClassifierModelInfo(goarch string, find func(name string) (path string, ok bool)) ModelInfo {
	if goarch == defaultBirdNETClassifierARM64Arch {
		if path, ok := find(DefaultBirdNETINT8ONNXModelName); ok {
			info := ModelRegistry[RegistryIDBirdNETV24INT8]
			info.CustomPath = path
			return info
		}
	}
	return ModelRegistry[DefaultModelVersion]
}

// defaultRangeFilterONNXPath resolves the ONNX range filter (MData) model used
// when no range filter is configured. On arm64 (container images ship the ONNX
// range filter instead of the TFLite MData models) it returns the on-disk path
// when present; on other architectures it returns false so the TFLite range
// filter is used. find reports the resolved path of a model filename within the
// standard search paths.
func defaultRangeFilterONNXPath(goarch string, find func(name string) (path string, ok bool)) (string, bool) {
	if goarch != defaultBirdNETClassifierARM64Arch {
		return "", false
	}
	return find(DefaultRangeFilterV2ONNXModelName)
}

// birdnetVersionToRegistryID maps user-facing BirdNET version strings to registry IDs.
var birdnetVersionToRegistryID = map[string]string{
	"2.4": "BirdNET_V2.4",
	"3.0": RegistryIDBirdNETV3,
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

// ConfigAliasForRegistry returns the primary config alias for a registry ID.
// Returns "" if the registry ID is unknown or has no aliases.
func ConfigAliasForRegistry(registryID string) string {
	info, ok := ModelRegistry[registryID]
	if !ok || len(info.ConfigAliases) == 0 {
		return ""
	}
	return info.ConfigAliases[0]
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
	"int8_arm":               RegistryIDBirdNETV24INT8, // INT8-ARM ONNX classifier (arm64 container default)
	"int8-arm":               RegistryIDBirdNETV24INT8,
	"birdnet_global_v3.0":    RegistryIDBirdNETV3,
	"birdnet-v30":            RegistryIDBirdNETV3,
	"birdnet_v3.0":           RegistryIDBirdNETV3,
	"birdnet-v3.0":           RegistryIDBirdNETV3,
	"perch_v2":               RegistryIDPerchV2,
	"perch-v2":               RegistryIDPerchV2,
	"bsg_finland":            RegistryIDBSG,
	"bsg-finland":            RegistryIDBSG,
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
