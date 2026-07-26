// model_registry.go contains the single source of truth for supported models.
package classifier

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
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
//
// BackendTFLite and BackendONNX double as the model file-type stored in
// ModelInfo.Backend (the static metadata). BackendOpenVINO is an execution
// provider, not a file type: OpenVINO executes an ONNX model file through the OV
// runtime, so it only ever appears as a live, per-instance backend reported by
// ModelInstance.RuntimeInfo(), never as a ModelInfo.Backend file-type value.
const (
	BackendTFLite   = "TFLite"
	BackendONNX     = "ONNX"
	BackendOpenVINO = "OpenVINO"
)

// Model file extensions (lowercase, including the leading dot).
const (
	extONNX   = ".onnx"
	extTFLite = ".tflite"
)

// Quantization is the numeric precision of a model's weights. It is orthogonal
// to Backend (a model can be TFLite or ONNX at any precision).
type Quantization string

const (
	QuantizationUnknown Quantization = ""     // unspecified / not applicable
	QuantizationFP32    Quantization = "FP32" // 32-bit float
	QuantizationFP16    Quantization = "FP16" // 16-bit float
	QuantizationINT8    Quantization = "INT8" // 8-bit integer quantized
)

// Registry ID constants for model identification across packages.
const (
	RegistryIDBirdNETV3 = "BirdNET_V3.0"
	RegistryIDBSG       = "BSG"
	RegistryIDBat       = "Bat"
	RegistryIDPerchV2   = "Perch_V2"
)

// defaultBirdNETClassifierARM64Arch is the GOARCH for which container images
// ship the INT8-ARM ONNX classifier as the memory-saving default classifier.
const defaultBirdNETClassifierARM64Arch = "arm64"

// Package-level compiled regexps for quantization token detection. Each pattern
// requires the token to be surrounded by a delimiter (start, end, or [_\-.]) so
// names like "sprint8" or "point8" do not false-positive.
var (
	reQuantINT8 = regexp.MustCompile(`(?i)(^|[_\-.])int8([_\-.]|$)`)
	reQuantFP16 = regexp.MustCompile(`(?i)(^|[_\-.])fp16([_\-.]|$)`)
	reQuantFP32 = regexp.MustCompile(`(?i)(^|[_\-.])fp32([_\-.]|$)`)
)

// detectQuantization infers weight precision from a model filename, matching
// delimiter-anchored tokens against filepath.Base(name) so unrelated names
// (sprint8, point8) do not false-positive. The extension's leading dot acts as
// a trailing delimiter. A name carrying more than one distinct precision token
// is ambiguous and returns QuantizationUnknown.
func detectQuantization(name string) Quantization {
	base := filepath.Base(name)
	matches := []Quantization{}
	if reQuantINT8.MatchString(base) {
		matches = append(matches, QuantizationINT8)
	}
	if reQuantFP16.MatchString(base) {
		matches = append(matches, QuantizationFP16)
	}
	if reQuantFP32.MatchString(base) {
		matches = append(matches, QuantizationFP32)
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return QuantizationUnknown
}

// DetectionNamePerch is the detection model name for Perch classifiers,
// matching the DetectionName field in the ModelRegistry.
const DetectionNamePerch = "Perch"

// ModelInfo represents metadata about a classifier model.
type ModelInfo struct {
	ID               string       // Unique registry identifier (e.g., "BirdNET_V2.4")
	Name             string       // User-friendly name (e.g., "BirdNET v2.4")
	Backend          string       // Inference backend: "TFLite" or "ONNX"
	DetectionName    string       // Database model name (e.g., "BirdNET", "Perch")
	DetectionVersion string       // Database model version (e.g., "2.4", "V2")
	Description      string       // Description of the model
	Spec             ModelSpec    // Audio requirements (sample rate, clip length)
	ConfigAliases    []string     // User-facing config IDs (e.g., ["birdnet"])
	SupportedLocales []string     // List of supported locale codes
	DefaultLocale    string       // Default locale if none is specified
	NumSpecies       int          // Number of species in the model
	CustomPath       string       // Path to custom model file, if any
	Quantization     Quantization // Precision of the loaded weights. Orthogonal to Backend.
	// IsStock marks the auto-resolved built-in default model. It is NOT set for
	// user-supplied models (birdnet.modelpath) or gallery models, so detection
	// attribution can treat the shipped default as "default" even when it loads
	// from a CustomPath. See ToDetectionModelInfo.
	IsStock bool
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
	"BirdNET_V2.4": { //nolint:goconst // registry data-table key; canonical model ID also named by DefaultModelVersion/BirdNET_V2_4/permanentRegistryID
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
		Quantization:  QuantizationFP32,
	},
	RegistryIDBirdNETV3: {
		ID:               RegistryIDBirdNETV3,
		Name:             ModelNameBirdNETv30,
		Backend:          BackendONNX,
		DetectionName:    "BirdNET",
		DetectionVersion: "3.0",
		Description:      "BirdNET v3.0 model (32kHz, 5s clips, embeddings)", // NumSpecies omitted: determined at runtime from label file
		Spec:             ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second},
		ConfigAliases:    []string{conf.ModelIDBirdNETV3},
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

// isBirdNETV24Family reports whether id is the BirdNET v2.4 classifier.
// Quantization (TFLite FP32 or ONNX INT8) is an orthogonal attribute on the
// unified entry; callers that special-case v2.4 behavior (the native range-filter
// fallback, the MData range-filter default) check the ID only.
func isBirdNETV24Family(id string) bool {
	return id == DefaultModelVersion
}

// remapV24ToONNXOnARM64 remaps a registry-resolved BirdNET v2.4 TFLite model to the
// INT8-ARM ONNX entry when ONNX is the appropriate stock backend and the ONNX model
// is present in the standard paths. That holds in two cases: on arm64 (where INT8-ARM
// ONNX is the reduced-memory stock default; arm64 container images also link
// libtensorflowlite_c so custom `.tflite` models still load), and on any build with
// no TFLite backend (the notflite tag), whose stub classifier cannot run TFLite and
// would otherwise fail to start. A normal non-arm64 build keeps its FP32 TFLite v2.4
// default even when the ONNX file happens to be present, so a stray copy never
// silently switches backends. An explicit model path (CustomPath set) is left
// untouched so a user-supplied model is never swapped.
func remapV24ToONNXOnARM64(info *ModelInfo, goarch string, tfliteAvailable bool, find func(name string) (path string, ok bool)) ModelInfo {
	if info.CustomPath != "" {
		return *info
	}
	if info.Backend != BackendTFLite || info.ID != DefaultModelVersion {
		return *info
	}
	// Remap to ONNX on arm64 (its stock default) or on a build with no TFLite
	// backend (notflite), where TFLite cannot run. A normal non-arm64 build keeps
	// FP32 TFLite even when the ONNX file is present, so nothing silently switches.
	if goarch != defaultBirdNETClassifierARM64Arch && tfliteAvailable {
		return *info
	}
	if path, ok := find(DefaultBirdNETINT8ONNXModelName); ok {
		return stockBirdNETV24ONNXVariant(path, QuantizationINT8)
	}
	return *info
}

// stockBirdNETV24ONNXVariant returns the canonical BirdNET_V2.4 entry adapted to
// load the given ONNX file at the given precision. The ID is unchanged
// (BirdNET_V2.4) so identity, metrics, labels, and attribution stay consistent
// across backends. IsStock is unconditionally set to true, so callers MUST only
// pass paths resolved from the standard model search paths
// (findModelPathInStandardPaths). It must NOT be called with a user-supplied
// birdnet.modelpath or gallery paths, which keep IsStock=false at their call site.
func stockBirdNETV24ONNXVariant(path string, q Quantization) ModelInfo {
	info := ModelRegistry[DefaultModelVersion]
	info.Backend = BackendONNX
	info.Quantization = q
	info.CustomPath = path
	info.IsStock = true
	info.SupportedLocales = slices.Clone(info.SupportedLocales)
	info.ConfigAliases = slices.Clone(info.ConfigAliases)
	return info
}

// customBirdNETV24ModelInfo returns the canonical BirdNET_V2.4 identity adapted
// to load a user-supplied model file configured in the birdnet config section
// (birdnet.modelpath). Any model placed in the birdnet slot is a BirdNET
// v2.4-type classifier (same 48kHz/3s I/O and 6522-class head), so it keeps the
// BirdNET_V2.4 ID regardless of filename. Keeping the ID canonical is required:
// the per-source model-set join in the analysis pipeline maps the config alias
// "birdnet" to BirdNET_V2.4 and looks the loaded model up by ID, so a divergent
// ID (e.g. the "Custom" sentinel for an unrecognized filename) would leave the
// primary classifier without an analysis buffer monitor and inference would
// never start. Backend is taken from the file extension and weight precision
// from the filename; IsStock stays false (user-supplied), so detection
// attribution records the "custom" variant. BirdNET v3.0 is selected via
// birdnet.version, never by a filename in this slot.
func customBirdNETV24ModelInfo(path string) ModelInfo {
	info := ModelRegistry[DefaultModelVersion]
	info.CustomPath = path
	info.SupportedLocales = slices.Clone(info.SupportedLocales)
	info.ConfigAliases = slices.Clone(info.ConfigAliases)
	switch strings.ToLower(filepath.Ext(path)) {
	case extONNX:
		info.Backend = BackendONNX
	case extTFLite:
		info.Backend = BackendTFLite
	}
	if q := detectQuantization(path); q != QuantizationUnknown {
		info.Quantization = q
	}
	return info
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
			return stockBirdNETV24ONNXVariant(path, QuantizationINT8)
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

// isAutoSelectRangeFilterModel reports whether the configured range-filter model
// requests automatic backend selection. Both "" and the "latest" sentinel (the
// config default in defaults.go) mean "pick the best available range filter": the
// v3 geomodel when its files are present, then the shipped ONNX MData model, then
// the classifier's embedded/shipped TFLite MData model.
//
// Without treating "latest" as auto-select, the default config dead-ends at the
// TFLite backend, which has no model file on ONNX-only (arm64) container images,
// leaving the instance with no range filter and every species unfiltered (#3932).
func isAutoSelectRangeFilterModel(model string) bool {
	return model == "" || model == conf.RangeFilterModelLatest
}

// shouldSelectDefaultONNXRangeFilter reports the ONNX MData range-filter model path
// to use as the arm64 default. It returns ("", false) unless the config requests
// auto-selection (isAutoSelectRangeFilterModel), no explicit range-filter ModelPath
// is set, the classifier is the BirdNET v2.4 family (whose labels match the MData V2
// output dimension), and defaultRangeFilterONNXPath locates the ONNX MData model
// (arm64 only). find resolves a model filename within the standard search paths.
func shouldSelectDefaultONNXRangeFilter(model, modelPath, classifierID, goarch string, find func(name string) (path string, ok bool)) (string, bool) {
	if !isAutoSelectRangeFilterModel(model) || modelPath != "" || !isBirdNETV24Family(classifierID) {
		return "", false
	}
	return defaultRangeFilterONNXPath(goarch, find)
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
	"birdnet-go_classifier":  "BirdNET_V2.4",      // custom-named classifier builds
	"int8_arm":               DefaultModelVersion, // INT8-ARM ONNX classifier (arm64 container default)
	"int8-arm":               DefaultModelVersion,
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
	if ext == extTFLite || ext == extONNX {
		baseName := filepath.Base(modelPathOrID)
		lowerBase := strings.ToLower(baseName)

		// Resolve by the LONGEST matching token so resolution is deterministic.
		// Both ModelRegistry and filenamePatterns are maps with randomized
		// iteration order, and some IDs/patterns are substrings of others (e.g.
		// "birdnet_v2.4" vs "birdnet_v2.4_int8"). Longest-match prevents a filename
		// from resolving to a different entry run-to-run; the more specific token
		// (e.g. the int8 marker) wins over its prefix.
		bestToken, bestID := "", ""
		consider := func(token, registryID string) {
			if !strings.Contains(lowerBase, strings.ToLower(token)) {
				return
			}
			// Longest token wins; on equal length break ties lexically so resolution stays
			// deterministic regardless of the randomized map iteration order above.
			if len(token) > len(bestToken) || (len(token) == len(bestToken) && token < bestToken) {
				bestToken, bestID = token, registryID
			}
		}
		for id := range ModelRegistry {
			consider(id, id)
		}
		for pattern, registryID := range filenamePatterns {
			consider(pattern, registryID)
		}
		if bestID != "" {
			info := ModelRegistry[bestID]
			info.CustomPath = modelPathOrID
			switch ext {
			case extONNX:
				info.Backend = BackendONNX
			case extTFLite:
				info.Backend = BackendTFLite
			}
			if q := detectQuantization(modelPathOrID); q != QuantizationUnknown {
				info.Quantization = q
			}
			return info, nil
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
		if !m.IsStock {
			variant = "custom"
		}
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
