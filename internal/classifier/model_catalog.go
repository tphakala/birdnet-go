// model_catalog.go defines the embedded catalog of downloadable models
// available in the model gallery UI. Each entry references a ModelRegistry
// key via RegistryID and provides download metadata for HuggingFace repos.
package classifier

import (
	"slices"
	"sync"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Catalog category constants.
const (
	CategoryWildlife = "wildlife"
	CategoryBird     = "bird"
	CategoryBat      = "bat"
	CategoryGeomodel = "geomodel"
)

// birdnetV24SpeciesCount is the number of species in the embedded BirdNET v2.4
// label set (data/labels/V2.4). The v2.4 entry reports this at the entry and
// per-variant level because no labels file is downloaded, so the runtime count is
// known statically. It matches the embedded label file's expected line count.
const birdnetV24SpeciesCount = 6522

// catalogBackendTFLite is the backend token used as a key in CatalogVariant.Backends
// for the TensorFlow Lite backend, matching the tokens in the embedded catalog data
// and the remote manifests. It equals hwprofile.CapTFLite but is kept local so the
// catalog's backend-token lookups do not couple to the hardware-capability package.
const catalogBackendTFLite = "tflite"

// CatalogFile role constants.
const (
	RoleModel          = "model"
	RoleLabels         = "labels"
	RoleEmbeddings     = "embeddings"
	RoleGeomodelModel  = "geomodel_model"
	RoleGeomodelLabels = "geomodel_labels"
	RoleData           = "data"
	RoleTaxonomy       = "taxonomy"
)

// Model release channels. An empty CatalogEntry.Channel means a stable (GA)
// release; ChannelPreview marks a developer-preview build the gallery labels as
// not the final GA build.
const (
	ChannelStable  = "stable"
	ChannelPreview = "preview"
)

// CatalogEntry describes a downloadable model available in the model gallery.
//
// The snake_case JSON tags define the on-disk schema for the user-editable
// model-catalog.json file (see catalog_loader.go). They are intentionally
// stable and readable for hand-editing. The model-gallery API does NOT
// serialize this struct directly; it maps to a separate camelCase response
// type (see internal/api/v2/models.go), so these tags do not affect the API.
type CatalogEntry struct {
	ID            string `json:"id"`             // unique catalog identifier (e.g., "battybirdnet-eu")
	Name          string `json:"name"`           // user-facing display name
	Description   string `json:"description"`    // short description of the model
	Author        string `json:"author"`         // model author or organization
	License       string `json:"license"`        // license identifier (e.g., "Apache-2.0")
	CommercialUse bool   `json:"commercial_use"` // whether commercial use is permitted
	Category      string `json:"category"`       // "wildlife", "bird", "bat", or "geomodel"
	Region        string `json:"region"`         // geographic region, or empty for global models
	SpeciesCount  int    `json:"species_count"`  // number of species the model can identify
	Version       string `json:"version"`        // model version string
	// Channel marks a non-stable release. "" (or "stable") is the normal GA build;
	// "preview" marks a developer-preview build the gallery flags as not the GA
	// build. omitempty keeps every stable entry's on-disk JSON byte-identical, so
	// adding this field does not shift catalogChecksum or force a schema-version bump.
	Channel string `json:"channel,omitempty"`
	// BuildLabel is a human-facing build tag shown next to Version for a non-stable
	// channel (e.g. "preview3.1"). Empty for stable releases. omitempty as above.
	BuildLabel      string        `json:"build_label,omitempty"`
	GeomodelVersion string        `json:"geomodel_version"`  // geomodel range filter version (e.g., "v3"); empty if no geomodel
	RegistryID      string        `json:"registry_id"`       // maps to a ModelRegistry key; empty if loader not yet implemented
	Hidden          bool          `json:"hidden"`            // if true, entry is excluded from the gallery UI
	RequiresONNX    bool          `json:"requires_onnx"`     // if true, model needs ONNX Runtime (not just TFLite)
	UpstreamURL     string        `json:"upstream_url"`      // URL to the upstream project repository
	HuggingFaceRepo string        `json:"hugging_face_repo"` // HuggingFace repository path
	Files           []CatalogFile `json:"files"`             // files to download for this model (the resolved default variant's files when Variants is set)
	// Variants lists hardware/regional variants of this model (fp32, int8-arm,
	// regional slices). When non-empty, the catalog layer resolves Files to the
	// default variant's files so every Files consumer keeps working unchanged;
	// Variants itself is read only by the recommender, the gallery API, and the
	// install path when the user picks a non-default variant. Omitted (nil) for
	// single-variant models, which keep their own Files. omitempty keeps the
	// on-disk JSON of existing single-variant entries byte-identical, so adding
	// this field does not shift catalogChecksum or force a schema-version bump.
	Variants []CatalogVariant `json:"variants,omitempty"`
}

// CatalogVariant describes one hardware or regional variant of a model: a
// concrete set of files selected for a class of hosts (e.g. fp32 for CPU, an
// int8-arm build for low-RAM ARM, or a region-sliced build). Exactly one variant
// per entry should set Default. Resolution picks the first Default-flagged
// variant, or the first variant when none is flagged (see defaultVariant).
type CatalogVariant struct {
	ID           string                    `json:"id"`                      // stable variant id (e.g. "fp32", "int8-arm", "fp32@nordic")
	Region       string                    `json:"region"`                  // geographic region, or empty for a global variant
	Precision    string                    `json:"precision"`               // numeric precision (e.g. "fp32", "fp16", "int8")
	SpeciesCount int                       `json:"species_count"`           // species this variant can identify (regional slices differ from global)
	Default      bool                      `json:"default"`                 // if true, the variant resolved into Files absent an explicit choice
	Requirements VariantRequirements       `json:"requirements"`            // host capabilities this variant needs
	Backends     map[string]BackendSupport `json:"backends,omitempty"`      // per-backend support, keyed by backend token (e.g. "onnxruntime-cpu")
	Benchmarks   []Benchmark               `json:"benchmarks,omitempty"`    // measured latency/memory per device
	Files        []CatalogFile             `json:"files"`                   // files to download for this variant
	Legacy       bool                      `json:"legacy"`                  // if true, hidden unless already installed (superseded build)
	SupersededBy string                    `json:"superseded_by,omitempty"` // id of the variant that replaces this one, if any
	// BuiltIn marks the embedded baseline variant of a permanent model (the
	// BirdNET v2.4 classifier shipped inside the binary). A BuiltIn variant carries
	// no files (nothing to download), is always reported installed by ScanInstalled,
	// and is exempt from the catalog's no-files / model-role validation. At most one
	// variant per entry may set it. omitempty keeps the on-disk JSON of every other
	// entry byte-identical, so adding this field does not shift catalogChecksum.
	BuiltIn bool `json:"built_in,omitempty"`
}

// VariantRequirements declares the host capabilities a variant needs. Arch and
// Backends are any-of: a host matches when it carries at least one listed token.
// An empty slice means "no constraint on this axis".
type VariantRequirements struct {
	Arch     []string `json:"arch,omitempty"`     // any-of capability tokens (e.g. "aarch64", "armv7l"); empty = any arch
	Backends []string `json:"backends,omitempty"` // any-of backend tokens (e.g. "onnxruntime-cpu"); empty = any backend
	MinRAMMB int      `json:"min_ram_mb"`         // minimum host RAM in MB; 0 = no floor
	Excludes []string `json:"excludes,omitempty"` // capability tokens that disqualify a host (e.g. a known-bad GPU generation)
}

// BackendSupport records whether an inference backend runs a variant and whether
// it is the recommended backend for it. Mirrors the manifest's per-backend map.
type BackendSupport struct {
	Supported   bool `json:"supported"`   // the backend can execute this variant
	Recommended bool `json:"recommended"` // the backend is the preferred way to run this variant
}

// Benchmark is a measured latency (and optional memory) figure for a variant on
// a specific device and backend. Used by the recommender and the gallery card.
type Benchmark struct {
	Device    string `json:"device"`               // device identifier (e.g. "rpi5-a76")
	Backend   string `json:"backend"`              // backend the figure was measured with
	LatencyMs int    `json:"latency_ms,omitempty"` // single-inference latency in milliseconds
	RSSMB     int    `json:"rss_mb,omitempty"`     // resident memory in MB, when measured
}

// CatalogFile describes a single file within a model's HuggingFace repository.
type CatalogFile struct {
	RemotePath      string `json:"remote_path"`       // path within the HuggingFace repo
	LocalName       string `json:"local_name"`        // filename to use on disk
	Role            string `json:"role"`              // file role: "model", "labels", "embeddings", "geomodel_model", "geomodel_labels", or "data"
	SHA256          string `json:"sha256"`            // hex-encoded SHA-256 checksum
	SizeBytes       int64  `json:"size_bytes"`        // file size in bytes
	HuggingFaceRepo string `json:"hugging_face_repo"` // override entry-level HuggingFace repo for this file (empty = use entry repo)
}

//go:generate go run ./gen

// EmbeddedCatalog is the built-in list of models available for download.
// Each entry provides enough metadata for the gallery UI and enough file
// information to drive the download process.
var EmbeddedCatalog = []CatalogEntry{
	// Wildlife models (multi-taxa classifiers)
	// BirdNET v3.0 acoustic classifier (developer preview). Global GPU-native model
	// (EfficientNetV2-S backbone, 11,560 species, 32kHz/5s), paired with the v3.0
	// geomodel range filter. The HuggingFace repo is public and the file
	// checksums/sizes below are pinned from it, so this entry's download path is
	// integrity-checked like every other entry. Now visible in the gallery: the
	// hardware recommender (internal/classifier/recommend) plus the per-variant
	// MinRAMMB floors below keep the heavy global fp32 (557 MB) and fp16 (279 MB)
	// builds off hosts that cannot run them. The two global variants plus the 39
	// regional tiles appended by birdnetV30RegionalVariants ship today. The entry stays
	// labelled a developer preview so users know it is not the GA build. The
	// backend loader is fully functional and v3.0 can also be enabled via config
	// (models.enabled + birdnetv3 model/label paths).
	{
		ID:            "birdnet-v3.0",
		Name:          "BirdNET v3.0",
		Description:   "Developer preview of the BirdNET v3.0 global wildlife classifier (11,560 species, birds and other fauna; scientific and common names). Not the GA build.",
		Author:        "Cornell Lab of Ornithology & Chemnitz University of Technology",
		License:       "CC-BY-SA-4.0",
		CommercialUse: true,
		Category:      CategoryWildlife,
		Region:        "",
		SpeciesCount:  11560,
		Version:       "3.0",
		// Developer-preview build: the gallery shows a PREVIEW badge and a not-GA
		// notice, and surfaces this tag (which otherwise lives only inside the
		// preview3.1 file paths below) so users know it is not the final release.
		Channel:         ChannelPreview,
		BuildLabel:      "preview3.1",
		GeomodelVersion: "v3",
		RegistryID:      RegistryIDBirdNETV3,
		Hidden:          false,
		RequiresONNX:    true,
		UpstreamURL:     "https://github.com/birdnet-team/BirdNET-Analyzer",
		HuggingFaceRepo: "tphakala/BirdNET-v3.0-Models",
		// Global GPU-native model published under full/. fp32 is the default
		// (byte-identical to the previously flat entry); fp16 is the OpenVINO-GPU /
		// CUDA / TensorRT build. Regional tiles under regional/ are appended from the
		// generated birdnetV30RegionalVariants. Each variant's Files is self-contained (model +
		// labels + geomodel + taxonomy) because resolveVariantDefaults sets
		// entry.Files = variant.Files without merging anything.
		Variants: slices.Concat([]CatalogVariant{
			{
				ID:           "fp32",
				Precision:    "fp32",
				SpeciesCount: 11560,
				Default:      true,
				// RAM floor and benchmarks sourced from the acoustic-models
				// BirdNET-v3.0-Models.models.json manifest.
				Requirements: VariantRequirements{MinRAMMB: 800},
				Backends: map[string]BackendSupport{
					"onnxruntime-cpu": {Supported: true, Recommended: true},
					"openvino-cpu":    {Supported: true, Recommended: true},
					"openvino-gpu":    {Supported: true},
					"cuda":            {Supported: true, Recommended: true},
					"tensorrt":        {Supported: true, Recommended: true},
				},
				Benchmarks: []Benchmark{
					{Device: "rpi5-a76", Backend: "openvino-cpu", LatencyMs: 168},
					{Device: "rpi5-a76", Backend: "onnxruntime-cpu", LatencyMs: 363, RSSMB: 685},
					{Device: "rpi4b-a72", Backend: "onnxruntime-cpu", LatencyMs: 874, RSSMB: 688},
					{Device: "x86-i7-1260P", Backend: "onnxruntime-cpu", LatencyMs: 70},
					{Device: "x86-i7-1260P", Backend: "openvino-gpu", LatencyMs: 95},
				},
				Files: slices.Concat([]CatalogFile{
					{RemotePath: "full/birdnet-v3.0-preview3.1-fp32-b1.onnx", LocalName: "birdnet_v3.0_fp32.onnx", Role: RoleModel, SHA256: "05535c3ef6ce3f9e523706dd3e144cb6db96bc202e9047f4973961256acbf997", SizeBytes: 557212256},
				}, birdnetV30LabelsFile(), geomodelFiles(), taxonomyFiles()),
			},
			{
				ID:           "fp16",
				Precision:    "fp16",
				SpeciesCount: 11560,
				// RAM floor, exclude token and benchmarks sourced from the
				// acoustic-models BirdNET-v3.0-Models.models.json manifest.
				Requirements: VariantRequirements{MinRAMMB: 1100, Excludes: []string{"openvino-gpu-intel-gen12"}},
				Backends: map[string]BackendSupport{
					"openvino-gpu":    {Supported: true, Recommended: true},
					"cuda":            {Supported: true, Recommended: true},
					"tensorrt":        {Supported: true, Recommended: true},
					"onnxruntime-cpu": {Supported: true},
					"openvino-cpu":    {Supported: true},
				},
				Benchmarks: []Benchmark{
					{Device: "rpi5-a76", Backend: "onnxruntime-cpu", LatencyMs: 381, RSSMB: 480},
					{Device: "rpi4b-a72", Backend: "onnxruntime-cpu", LatencyMs: 887, RSSMB: 929},
					{Device: "x86-i7-1260P", Backend: "openvino-gpu", LatencyMs: 81},
				},
				Files: slices.Concat([]CatalogFile{
					{RemotePath: "full/birdnet-v3.0-preview3.1-fp16-b1.onnx", LocalName: "birdnet_v3.0_fp16.onnx", Role: RoleModel, SHA256: "18fc932b9ac7478720ac8ca9077694b6ea62fb00675aa488e77b15e722244e67", SizeBytes: 278787557},
				}, birdnetV30LabelsFile(), geomodelFiles(), taxonomyFiles()),
			},
		}, birdnetV30RegionalVariants()),
	},
	{
		ID:              "perch-v2",
		Name:            "Google Perch v2",
		Description:     "Google Perch v2 multi-taxa classifier with approximately 14,795 species including birds, insects, amphibians, and mammals (scientific names only)",
		Author:          "Google Research",
		License:         "Apache-2.0",
		CommercialUse:   true,
		Category:        CategoryWildlife,
		Region:          "",
		SpeciesCount:    14795,
		Version:         "2",
		GeomodelVersion: "v3",
		RegistryID:      RegistryIDPerchV2,
		RequiresONNX:    true,
		UpstreamURL:     "https://www.kaggle.com/models/google/bird-vocalization-classifier/tensorFlow2/perch_v2",
		HuggingFaceRepo: "tphakala/Perch-v2-Models",
		// Global builds under full/. fp32 (with in-graph DFT) is the default and is
		// byte-identical to the previously flat entry, so existing installs stay
		// detected with no re-download (ScanInstalled keys on LocalName, not repo or
		// RemotePath). no-dft-fp32 is the OpenVINO/GPU build; int8-arm is the low-RAM
		// ARM build. Regional tiles under regional/ are appended from the generated
		// perchV2RegionalVariants. Each variant's Files is self-contained (model + labels + geomodel +
		// taxonomy) because resolveVariantDefaults does not merge companions.
		Variants: slices.Concat([]CatalogVariant{
			{
				ID:           "fp32",
				Precision:    "fp32",
				SpeciesCount: 14795,
				Default:      true,
				// RAM floors sourced from the acoustic-models
				// Perch-v2-Models.models.json manifest.
				Requirements: VariantRequirements{MinRAMMB: 700},
				Backends: map[string]BackendSupport{
					"onnxruntime-cpu": {Supported: true, Recommended: true},
					"cuda":            {Supported: true, Recommended: true},
					"tensorrt":        {Supported: true},
				},
				Files: slices.Concat([]CatalogFile{
					{RemotePath: "full/perch_v2_fp32.onnx", LocalName: "perch_v2.onnx", Role: RoleModel, SHA256: "bf0c8467a924cb074663970ca4a0ab1e143602121930209657d0dff5d5cefa1f", SizeBytes: 409148616},
				}, perchV2LabelsFile(), geomodelFiles(), taxonomyFiles()),
			},
			{
				ID:           "no-dft-fp32",
				Precision:    "fp32",
				SpeciesCount: 14795,
				Requirements: VariantRequirements{MinRAMMB: 750},
				Backends: map[string]BackendSupport{
					"openvino-cpu":    {Supported: true, Recommended: true},
					"openvino-gpu":    {Supported: true, Recommended: true},
					"cuda":            {Supported: true, Recommended: true},
					"onnxruntime-cpu": {Supported: true},
					"tensorrt":        {Supported: true},
				},
				Files: slices.Concat([]CatalogFile{
					{RemotePath: "full/perch_v2_no_dft_fp32.onnx", LocalName: "perch_v2_no_dft.onnx", Role: RoleModel, SHA256: "4dcf71c18a147198545944bb5149697e89e3ad2e16637fa8f0edf6d13035a017", SizeBytes: 413350933},
				}, perchV2LabelsFile(), geomodelFiles(), taxonomyFiles()),
			},
			{
				ID:           "int8-arm",
				Precision:    "int8",
				SpeciesCount: 14795,
				Requirements: VariantRequirements{Arch: []string{"aarch64"}, MinRAMMB: 350},
				Backends: map[string]BackendSupport{
					"onnxruntime-cpu": {Supported: true, Recommended: true},
				},
				Files: slices.Concat([]CatalogFile{
					{RemotePath: "full/perch_v2_int8_arm.onnx", LocalName: "perch_v2_int8_arm.onnx", Role: RoleModel, SHA256: "ff32ca8c57954a86e6023a915d018dc7573cfc5567dd7314899d1c947cc6d5c5", SizeBytes: 130856164},
				}, perchV2LabelsFile(), geomodelFiles(), taxonomyFiles()),
			},
		}, perchV2RegionalVariants()),
	},
	{
		ID:              "bsg-finland",
		Name:            "BSG Finland v4.4",
		Description:     "Regional bird classifier optimized for Finnish bird species",
		Author:          "University of Jyväskylä",
		License:         "Non-commercial",
		CommercialUse:   false,
		Category:        CategoryBird,
		Region:          "Finland",
		SpeciesCount:    0,
		Version:         "4.4",
		RegistryID:      RegistryIDBSG,
		Hidden:          true,
		RequiresONNX:    true,
		UpstreamURL:     "https://github.com/luomus/BSG",
		HuggingFaceRepo: "tphakala/BSG",
		Files: []CatalogFile{
			{RemotePath: "BSG_birds_Finland_v4_4_fused_fp32.onnx", LocalName: "BSG_birds_Finland_v4_4_fused_fp32.onnx", Role: RoleModel, SHA256: "dd2b6b21c6b3d8adc5d72954f9e33c48b3d692dbbc647758340a69d68b203300", SizeBytes: 45446250},
			{RemotePath: "BSG_birds_Finland_v4_4_labels_fi.txt", LocalName: "BSG_birds_Finland_v4_4_labels_fi.txt", Role: RoleLabels, SHA256: "01497fbec1bdba18625862ac8a5aedf372801eeb36dfde7a5dbce5353eeda308", SizeBytes: 7813},
			{RemotePath: "BSG_birds_Finland_v4_4_calibration.csv", LocalName: "BSG_birds_Finland_v4_4_calibration.csv", Role: RoleData, SHA256: "b248ca8dac8205b427604ccc2832afdc2ab4672653c7e35ca78f44cc36ee5b28", SizeBytes: 6800},
			{RemotePath: "BSG_birds_Finland_v4_4_distribution.bin", LocalName: "BSG_birds_Finland_v4_4_distribution.bin", Role: RoleData, SHA256: "0617f19f3eca7f7bc409e3d853d742a171a835464862dc3ced2f5b72ef3093f5", SizeBytes: 25828768},
			{RemotePath: "BSG_birds_Finland_v4_4_migration.csv", LocalName: "BSG_birds_Finland_v4_4_migration.csv", Role: RoleData, SHA256: "a3fdbfc744645f6945def7fbfa3ee19e347c31d1b46ae78fba75e7059b54a86b", SizeBytes: 17054},
		},
	},

	// BirdNET v2.4, the permanent primary classifier, wired into its own variant set.
	// The embedded default (shipped inside the binary) is represented as a BuiltIn
	// baseline variant; the two DFT-truncated ONNX builds are opt-in, faster drop-in
	// alternatives. DFT-bin truncation drops the mel-DFT bins the filterbank discards,
	// so the output is bit-exact (single classification head, unchanged labels) while
	// CPU/OpenVINO inference is about 1.4-2x faster. The ONNX files are published under
	// NEW HuggingFace filenames, so existing installs are untouched.
	//
	// The entry is visible so the gallery can offer an in-place "optimize" swap between
	// the builtin baseline and a compatible DFT-truncated build. The primary BirdNET
	// v2.4 classifier is resolved at startup from config and the standard model paths
	// (see NewBirdNET), NOT from a generic gallery loader, so the swap runs through a
	// dedicated primary-reload path (ModelManager.replacePrimaryVariant ->
	// Orchestrator.ReloadPrimaryForVariantSwap), not the generic replaceVariant flow.
	// RegistryID is the permanent BirdNET v2.4 ID: the model is always installed (the
	// BuiltIn baseline needs no files), it is never hot-loaded by loadInstalledModels
	// (there is no secondary loader for the primary), and Uninstall refuses the entry
	// via the permanent-model guard, so only its variant may change. Labels are the
	// embedded v2.4 set (data/labels/V2.4), so no labels file is downloaded.
	{
		ID:            "birdnet-v2.4",
		Name:          "BirdNET v2.4",
		Description:   "The built-in BirdNET v2.4 classifier. Optionally swap in a DFT-truncated build for bit-exact output at about 1.4-2x faster CPU and OpenVINO inference: FP32 for OpenVINO/CPU (A76/Pi5, amd64, Intel iGPU); INT8 for low-RAM ARM via ONNX Runtime (Pi4/Pi3).",
		Author:        "Cornell Lab of Ornithology & Chemnitz University of Technology",
		License:       "CC-BY-NC-SA-4.0",
		CommercialUse: false,
		Category:      CategoryBird,
		Region:        "",
		SpeciesCount:  birdnetV24SpeciesCount,
		Version:       "2.4",
		RegistryID:    permanentRegistryID,
		Hidden:        false,
		// RequiresONNX is now per-variant: the BuiltIn baseline runs on the embedded
		// TFLite model (no ONNX Runtime needed); the DFT-truncated builds are ONNX
		// (see VariantNeedsONNX, which the install ORT gate consults per variant).
		RequiresONNX:    false,
		UpstreamURL:     "https://github.com/birdnet-team/BirdNET-Analyzer",
		HuggingFaceRepo: "tphakala/BirdNET-v2.4",
		// No labels/companions: v2.4 uses the embedded label set. Variant Files are
		// the model file only (and none for the BuiltIn baseline).
		Variants: []CatalogVariant{
			{
				// BuiltIn baseline: the embedded v2.4 model that ships inside the
				// binary. No files (nothing to download), always installed. It does NOT
				// mark any ONNX backend Recommended: a 0-byte size tie-break would let
				// this file-less variant beat a real DFT build and suppress the optimize
				// offer, so it advertises only tflite as recommended (the embedded path)
				// with onnxruntime-cpu merely supported.
				ID:      "builtin",
				BuiltIn: true,
				Default: true,
				// The embedded model identifies the full v2.4 label set.
				SpeciesCount: birdnetV24SpeciesCount,
				Backends: map[string]BackendSupport{
					"tflite":          {Supported: true, Recommended: true},
					"onnxruntime-cpu": {Supported: true},
				},
			},
			{
				ID:           "fp32-dfttrunc",
				Precision:    "fp32",
				SpeciesCount: birdnetV24SpeciesCount,
				// RAM floors sourced from the acoustic-models
				// BirdNET-v2.4.models.json manifest.
				Requirements: VariantRequirements{MinRAMMB: 250},
				Backends: map[string]BackendSupport{
					"onnxruntime-cpu": {Supported: true, Recommended: true},
					"openvino-cpu":    {Supported: true, Recommended: true},
					"openvino-gpu":    {Supported: true},
					"cuda":            {Supported: true, Recommended: true},
					"tensorrt":        {Supported: true},
				},
				Files: []CatalogFile{
					{RemotePath: "BirdNET_v2.4_fp32_dfttrunc.onnx", LocalName: "BirdNET_v2.4_fp32_dfttrunc.onnx", Role: RoleModel, SHA256: "3b72e88b3ad0c310a41adabccf8cf75b1a05daeeb40884ebd38038c91d0e423d", SizeBytes: 54068648},
				},
			},
			{
				ID:           "int8-arm-dfttrunc",
				Precision:    "int8",
				SpeciesCount: birdnetV24SpeciesCount,
				Requirements: VariantRequirements{Arch: []string{"aarch64"}, MinRAMMB: 250},
				Backends: map[string]BackendSupport{
					"onnxruntime-cpu": {Supported: true, Recommended: true},
					"openvino-cpu":    {Supported: true},
				},
				Files: []CatalogFile{
					{RemotePath: "BirdNET_v2.4_int8_arm_dfttrunc.onnx", LocalName: "BirdNET_v2.4_int8_arm_dfttrunc.onnx", Role: RoleModel, SHA256: "7550498ba996064feca12005ff4133eb1d35741c4061376e7a987d8227518893", SizeBytes: 38727042},
				},
			},
		},
	},

	// Geomodels (spatiotemporal species occurrence prediction)
	{
		ID:              "birdnet-geomodel-v3",
		Name:            "BirdNET Geomodel v3.0",
		Description:     "Spatiotemporal species occurrence prediction for post-filtering acoustic detections. Predicts which species are likely at a given location and week of the year.",
		Author:          "Stefan Kahl, Cornell Lab of Ornithology",
		License:         "CC BY-SA 4.0",
		CommercialUse:   true,
		Category:        CategoryGeomodel,
		Region:          "",
		SpeciesCount:    12012,
		Version:         "3.0.2",
		GeomodelVersion: "v3",
		RegistryID:      "",
		RequiresONNX:    true,
		UpstreamURL:     "https://github.com/birdnet-team/geomodel",
		HuggingFaceRepo: geomodelHuggingFaceRepo,
		Files:           geomodelFiles(),
	},

	// Bat models (BattyBirdNET family by rdz-oss)
	batCatalogEntry("battybirdnet-bavaria", "BattyBirdNET Bavaria", "Bavaria", 32, "Bavaria", false,
		batFileChecksums{"7ee3936621d180b9fe42f3732703339662b154135ce205f711797bca7daa44ea", 131827, "ff4a3f9a351f202c8712c807c6bb8b29df0b1c75ddab48e543ec76a88a42715c", 966}),
	batCatalogEntry("battybirdnet-bavaria-high", "BattyBirdNET Bavaria (High)", "Bavaria", 24, "Bavaria", true,
		batFileChecksums{"3d1d5bc174ed70bfc22a53439fe468a2a4aa317b755600d1e193cefbae307a30", 99026, "26bc12ecf6c5ca9ce8837cd1bebe6e1cb2ce95f0261355a827383c85a0dd9d96", 904}),
	batCatalogEntry("battybirdnet-eu", "BattyBirdNET EU", "Europe", 30, "EU", false,
		batFileChecksums{"f316073482ab95f48d65ca76e8b2aaa572019b3d286ab07a68ba57cea52d12f7", 123626, "9ad705d4bcd93040929a059854df968acebefee9f7513e97a558871c3997e65e", 1081}),
	batCatalogEntry("battybirdnet-scotland", "BattyBirdNET Scotland", "Scotland", 11, "Scotland", false,
		batFileChecksums{"003e3da16d3607d52dd5c963d71eec89fdfd58224dccc02bc6a27d58d21cbd85", 45725, "3dc657a38f691c20f351fa19e36b9919927aec2e30dc32f61ae9fd9bb319331b", 356}),
	batCatalogEntry("battybirdnet-southwales", "BattyBirdNET South Wales", "South Wales", 29, "SouthWales", false,
		batFileChecksums{"14534d34fc54b0bc267ba07a6eaddc10e360195b11d0c4b5f47460a4f1d5aea4", 119526, "fc7ed8bd55c28b66cdcecc8d8acb8ea05850d9301aa65467fd5d192ee00e8214", 1072}),
	batCatalogEntry("battybirdnet-sweden", "BattyBirdNET Sweden", "Sweden", 23, "Sweden", false,
		batFileChecksums{"85fe47431c275b5370e0c8d0aa9b049f54d32035f736afcec4ac5d62c1adb591", 94926, "c43042ebd458eed4cc7258fcd6526e0299a61e27146e3ca989300f696d1f2e02", 737}),
	batCatalogEntry("battybirdnet-uk", "BattyBirdNET UK", "UK", 20, "UK", false,
		batFileChecksums{"aa9d45a5e3e64b6c28a131d16a98346ee1095c2d4c9f4785e2ff1d5a6e4b27b6", 82625, "4cc63b7cfd0a8e4380857fc3f5d576e8ec48d80cbdc9060873abb20c4ef78740", 649}),
	batCatalogEntry("battybirdnet-usa", "BattyBirdNET USA", "USA", 38, "USA", false,
		batFileChecksums{"9230fb49c87b9953f311fa1d408eac8359a1c8761264204f51b796406bcfcc63", 156427, "3cf597702b5f0f558b227df3a01648da7eb52cc632ec70a148fd159763ba4399", 1222}),
	batCatalogEntry("battybirdnet-usa-east", "BattyBirdNET USA East", "USA East", 23, "USA-EAST", false,
		batFileChecksums{"403901ce25c3daecdbd4d83017da8ff54c802f0feb78fc66355677f5c8905241", 94926, "db88ade98f2680af786911f1de49e5c29425335bbbc814c4a40c1e71ef888713", 663}),
	batCatalogEntry("battybirdnet-usa-east-high", "BattyBirdNET USA East (High)", "USA East", 17, "USA-EAST", true,
		batFileChecksums{"cb3fd538fb8adc87f775fad4fe5f9b3e1f56e78c5c7acd9abed4da7034e39772", 70325, "438b01d917b3833f707cdf9e9d13f0b13eee2318ad23eeb34089b76b9f22e566", 613}),
	batCatalogEntry("battybirdnet-usa-west", "BattyBirdNET USA West", "USA West", 28, "USA-WEST", false,
		batFileChecksums{"d1d3573a379e9e8561a66dc27ab768342d7d3823268440ec9ab624b8fb4640fa", 115426, "f01a993b749f455636de5811e6ab9de96537f05dc191ec1919acf4365a6e6386", 867}),
}

// Shared BirdNET v2.4 embeddings model, used by all BattyBirdNET classifiers.
// This is the DFT-truncated variant (remote birdnet-v2.4-embeddings-fp32-dfttrunc.onnx):
// bit-exact with the original 2-output backbone (embedding max|delta| = 0.0, identical
// output order, so no inference-code change) but about 2x faster on CPU and roughly 8 MB
// smaller. The local filename is deliberately kept as birdnet-v24-embeddings.onnx so
// existing installs keep their on-disk shared/birdnet-v24-embeddings.onnx: the startup
// scan only stats each bat model's own regional file and never inspects or re-verifies
// the shared embeddings file, so nothing is flagged, and installs upgrade transparently
// the next time a bat model is reinstalled or another bat region is installed.
const (
	embeddingsSHA256          = "b91139d3c63d55d742779a56531078bc88366a09bcc9bd6a9b703d425914c380"
	embeddingsSizeBytes int64 = 58763257
)

// Shared v3.0 geomodel, used as range filter companion by Perch v2 and BirdNET v3.0.
const (
	geomodelHuggingFaceRepo       = "tphakala/BirdNET-Geomodel"
	geomodelONNXSHA256            = "2bc5a9b1e7c24115730015a97dbb688e9e8cd49c02c34a011439182c65ef0017"
	geomodelONNXSizeBytes   int64 = 7483473
	geomodelLabelsSHA256          = "92cdca7ca95beb7ed16a0a39f4010fa9a8b468b854b6e8083f732647f136ee1c"
	geomodelLabelsSizeBytes int64 = 479350
)

// Shared taxonomy.csv from BirdNET v3.0, provides common names in 29 languages
// for ~13,361 species. Used as fallback name resolver for Perch v2 and other
// models whose labels contain only scientific names.
const (
	taxonomyHuggingFaceRepo       = "tphakala/BirdNET-Geomodel"
	taxonomySHA256                = "74e4b31d2f9c56fbd1a45d980591654f508c73fc4a153cab52f11367a078ddfd"
	taxonomySizeBytes       int64 = 9162669
)

// taxonomyFiles returns the shared taxonomy CatalogFile entry appended to
// classifiers that benefit from multilingual common name resolution.
func taxonomyFiles() []CatalogFile {
	return []CatalogFile{
		{
			RemotePath:      "taxonomy.csv",
			LocalName:       "taxonomy.csv",
			Role:            RoleTaxonomy,
			SHA256:          taxonomySHA256,
			SizeBytes:       taxonomySizeBytes,
			HuggingFaceRepo: taxonomyHuggingFaceRepo,
		},
	}
}

// perchV2LabelsFile returns the shared global Perch v2 labels file. Every Perch v2
// variant shares one label set, so each variant's Files includes it (variant Files
// must be self-contained because resolveVariantDefaults does not merge companions).
func perchV2LabelsFile() []CatalogFile {
	return []CatalogFile{
		{RemotePath: "full/perch_v2_labels.txt", LocalName: "perch_v2_labels.txt", Role: RoleLabels, SHA256: "e4d5c0397d8fb08bf90c6b13a34810af53504faad927e472fcc567793c9de057", SizeBytes: 312716},
	}
}

// birdnetV30LabelsFile returns the shared global BirdNET v3.0 labels file. Every
// BirdNET v3.0 variant shares one label set, so each variant's Files includes it.
func birdnetV30LabelsFile() []CatalogFile {
	return []CatalogFile{
		{RemotePath: "full/birdnet-v3.0-preview3.1-labels-b1.txt", LocalName: "birdnet_v3.0_labels.txt", Role: RoleLabels, SHA256: "4f4ef82f1704c66cf4da9f59757c12baa34ff98863fa2627e33c302fc92997aa", SizeBytes: 461605},
	}
}

// geomodelFiles returns the shared geomodel CatalogFile entries appended to
// classifiers that use the v3.0 range filter (Perch v2, BirdNET v3.0).
func geomodelFiles() []CatalogFile {
	return []CatalogFile{
		{
			RemotePath:      "BirdNET+_Geomodel_V3.0.2_Global_12K_FP16.onnx",
			LocalName:       conf.GeomodelONNXLocalName,
			Role:            RoleGeomodelModel,
			SHA256:          geomodelONNXSHA256,
			SizeBytes:       geomodelONNXSizeBytes,
			HuggingFaceRepo: geomodelHuggingFaceRepo,
		},
		{
			RemotePath:      "geomodel_v3.0.2_labels.txt",
			LocalName:       conf.GeomodelLabelsLocalName,
			Role:            RoleGeomodelLabels,
			SHA256:          geomodelLabelsSHA256,
			SizeBytes:       geomodelLabelsSizeBytes,
			HuggingFaceRepo: geomodelHuggingFaceRepo,
		},
	}
}

// isEmbeddingsRole reports whether the given file role is an embeddings role.
func isEmbeddingsRole(role string) bool { return role == RoleEmbeddings }

// isGeomodelRole reports whether the given file role is a geomodel role.
func isGeomodelRole(role string) bool {
	return role == RoleGeomodelModel || role == RoleGeomodelLabels
}

// isTaxonomyRole reports whether the given file role is a taxonomy role.
func isTaxonomyRole(role string) bool { return role == RoleTaxonomy }

// isSharedRole reports whether the given file role stores into models/shared/.
func isSharedRole(role string) bool {
	return role == RoleEmbeddings || role == RoleTaxonomy || isGeomodelRole(role)
}

// IsSharedOnly reports whether all files in a catalog entry use shared roles
// (stored in models/shared/ rather than a per-model subdirectory).
func IsSharedOnly(entry *CatalogEntry) bool {
	if entry == nil || len(entry.Files) == 0 {
		return false
	}
	for _, f := range entry.Files {
		if !isSharedRole(f.Role) {
			return false
		}
	}
	return true
}

// HasTaxonomyFiles reports whether a catalog entry includes shared taxonomy files.
func HasTaxonomyFiles(entry *CatalogEntry) bool {
	if entry == nil {
		return false
	}
	for _, f := range entry.Files {
		if f.Role == RoleTaxonomy {
			return true
		}
	}
	return false
}

// HasGeomodelFiles reports whether a catalog entry includes shared geomodel files.
func HasGeomodelFiles(entry *CatalogEntry) bool {
	if entry == nil {
		return false
	}
	for _, f := range entry.Files {
		if isGeomodelRole(f.Role) {
			return true
		}
	}
	return false
}

// HasEmbeddingsFiles reports whether a catalog entry includes shared embeddings files.
func HasEmbeddingsFiles(entry *CatalogEntry) bool {
	if entry == nil {
		return false
	}
	for _, f := range entry.Files {
		if f.Role == RoleEmbeddings {
			return true
		}
	}
	return false
}

// batFileChecksums holds SHA256 and size for a BattyBirdNET model and its labels file.
type batFileChecksums struct {
	modelSHA256  string
	modelSize    int64
	labelsSHA256 string
	labelsSize   int64
}

// batCatalogEntry constructs a CatalogEntry for a BattyBirdNET regional model.
// fileRegion is used to build HuggingFace file paths (e.g., "EU" produces
// "BattyBirdNET-EU-256kHz_fp32.onnx"). When highQuality is true, "-high" is
// appended after "256kHz" (e.g., "BattyBirdNET-Bavaria-256kHz-high_fp32.onnx").
func batCatalogEntry(id, name, region string, speciesCount int, fileRegion string, highQuality bool, checksums batFileChecksums) CatalogEntry {
	quality := ""
	if highQuality {
		quality = "-high"
	}
	modelFile := "BattyBirdNET-" + fileRegion + "-256kHz" + quality + "_fp32.onnx"
	labelsFile := "BattyBirdNET-" + fileRegion + "-256kHz" + quality + "_Labels.txt"

	return CatalogEntry{
		ID:              id,
		Name:            name,
		Description:     "Bat species detection for " + region + " using BirdNET v2.4 embeddings",
		Author:          "R.D. Zinck",
		License:         "CC-BY-NC-SA-4.0",
		CommercialUse:   false,
		Category:        CategoryBat,
		Region:          region,
		SpeciesCount:    speciesCount,
		Version:         "1.0",
		RegistryID:      RegistryIDBat,
		RequiresONNX:    true,
		UpstreamURL:     "https://github.com/rdz-oss/BattyBirdNET-Analyzer",
		HuggingFaceRepo: "tphakala/BattyBirdNET-onnx",
		Files: []CatalogFile{
			{
				RemotePath: "fp32/" + modelFile,
				LocalName:  modelFile,
				Role:       RoleModel,
				SHA256:     checksums.modelSHA256,
				SizeBytes:  checksums.modelSize,
			},
			{
				RemotePath: "labels/" + labelsFile,
				LocalName:  labelsFile,
				Role:       RoleLabels,
				SHA256:     checksums.labelsSHA256,
				SizeBytes:  checksums.labelsSize,
			},
			{
				// RemotePath is the DFT-truncated backbone; LocalName stays the
				// original filename for drop-in compatibility (see embeddingsSHA256).
				RemotePath: "birdnet-v2.4-embeddings-fp32-dfttrunc.onnx",
				LocalName:  "birdnet-v24-embeddings.onnx",
				Role:       RoleEmbeddings,
				SHA256:     embeddingsSHA256,
				SizeBytes:  embeddingsSizeBytes,
			},
		},
	}
}

// catalogMu guards activeCatalog. activeCatalog is the runtime source of truth
// for catalog reads. It is nil until LoadCatalog populates it; a nil value means
// "use EmbeddedCatalog", so behavior is unchanged when LoadCatalog is never
// called (e.g. in tests). The RWMutex makes a future hot-reload race-safe.
//
// A future hot-reload must publish a brand-new slice via setActiveCatalog; it
// must never mutate an existing entry (or an entry's Files slice) in place,
// because ActiveCatalog hands out a shallow snapshot that shares those backing
// arrays with readers.
var (
	catalogMu     sync.RWMutex
	activeCatalog []CatalogEntry
)

// defaultVariant returns the variant resolved into Files when no explicit choice
// is made: the one flagged Default, else the first. Returns nil for a
// single-variant entry (len(Variants) == 0).
func defaultVariant(entry *CatalogEntry) *CatalogVariant {
	if len(entry.Variants) == 0 {
		return nil
	}
	for i := range entry.Variants {
		if entry.Variants[i].Default {
			return &entry.Variants[i]
		}
	}
	return &entry.Variants[0]
}

// variantFilesByID returns the file list for the given variant of an entry. An
// empty variantID yields the entry's resolved (default) Files, preserving the
// pre-variant behaviour. A non-empty variantID selects the matching variant's
// files; if it matches no variant (including any non-empty id on an entry with no
// variants), it reports ok=false so callers can reject a stale or unknown
// selection rather than operate on the wrong files.
func variantFilesByID(entry *CatalogEntry, variantID string) (files []CatalogFile, ok bool) {
	if variantID == "" {
		return entry.Files, true
	}
	for i := range entry.Variants {
		if entry.Variants[i].ID == variantID {
			return entry.Variants[i].Files, true
		}
	}
	return nil, false
}

// VariantSelectable reports whether variantID names a valid selectable variant of
// entry. An empty variantID (meaning the default variant) is always valid. It is
// the exported form of the internal variant lookup, for API-layer validation of a
// requested variant before an install/switch is dispatched.
func VariantSelectable(entry *CatalogEntry, variantID string) bool {
	_, ok := variantFilesByID(entry, variantID)
	return ok
}

// IsPermanentEntry reports whether entry is the permanent built-in BirdNET v2.4
// classifier. The permanent entry is always installed, can only have its variant
// swapped (never uninstalled), and swaps through the dedicated primary-reload path
// rather than the generic variant-replace flow.
func IsPermanentEntry(entry *CatalogEntry) bool {
	return entry != nil && entry.RegistryID == permanentRegistryID
}

// builtInVariant returns the entry's BuiltIn baseline variant (the embedded
// primary model), or nil when the entry has none. Catalog validation guarantees at
// most one BuiltIn variant per entry.
func builtInVariant(entry *CatalogEntry) *CatalogVariant {
	if entry == nil {
		return nil
	}
	for i := range entry.Variants {
		if entry.Variants[i].BuiltIn {
			return &entry.Variants[i]
		}
	}
	return nil
}

// resolveVariant returns the variant of entry with the given id, resolving an empty
// id to the default variant. It returns nil for a flat entry (no variants) or when
// the id matches no variant.
func resolveVariant(entry *CatalogEntry, variantID string) *CatalogVariant {
	if entry == nil || len(entry.Variants) == 0 {
		return nil
	}
	if variantID == "" {
		return defaultVariant(entry)
	}
	for i := range entry.Variants {
		if entry.Variants[i].ID == variantID {
			return &entry.Variants[i]
		}
	}
	return nil
}

// VariantNeedsONNX reports whether running the given variant of entry requires the
// ONNX Runtime. A BuiltIn baseline (the embedded v2.4 TFLite model) never does. A
// variant that advertises support for the TFLite backend can also run without ORT.
// Otherwise the variant is an ONNX build and needs the runtime. For a flat entry or
// an unknown variant id it falls back to the entry-level RequiresONNX flag. This is
// the per-variant refinement of the entry-level ORT gate: a v2.4 entry is no longer
// RequiresONNX at the entry level, so the install path consults this to gate only
// the DFT-truncated ONNX variants, never the embedded baseline.
func VariantNeedsONNX(entry *CatalogEntry, variantID string) bool {
	v := resolveVariant(entry, variantID)
	if v == nil {
		return entry != nil && entry.RequiresONNX
	}
	if v.BuiltIn {
		return false
	}
	// Backends is keyed by backend token; catalogBackendTFLite matches the literal
	// tokens used throughout this file's catalog data and the remote manifest. A
	// variant only avoids the ONNX Runtime when it declares TFLite as an actually
	// SUPPORTED backend: a manifest may list "tflite" with Supported:false (the
	// Perch manifests do exactly this for unavailable backends), and key presence
	// alone would then wrongly skip the ORT requirement.
	if support, ok := v.Backends[catalogBackendTFLite]; ok && support.Supported {
		return false
	}
	return true
}

// VariantRegion returns the region slug of entry's variant with the given id, or
// "" when the id is empty (a flat entry), or no variant matches (a hardware/global
// variant, or an id dropped from the catalog). It is the shared lookup behind the
// gallery region endpoint and the coordinate-change staleness detector.
func VariantRegion(entry *CatalogEntry, variantID string) string {
	if entry == nil || variantID == "" {
		return ""
	}
	for i := range entry.Variants {
		if entry.Variants[i].ID == variantID {
			return entry.Variants[i].Region
		}
	}
	return ""
}

// resolveVariantDefaults returns entries with each variant entry's Files
// populated from its default variant, so every Files consumer sees the effective
// default variant's files. Single-variant entries (no Variants) are returned
// unchanged. When no entry carries variants (the common case) the input slice is
// returned as-is with no allocation; otherwise a shallow copy is made and only
// the variant entries' Files are replaced, so the input (which may be the
// EmbeddedCatalog global) is never mutated. Variants is preserved on every entry
// for the gallery API and the install path.
func resolveVariantDefaults(entries []CatalogEntry) []CatalogEntry {
	hasVariants := false
	for i := range entries {
		if len(entries[i].Variants) > 0 {
			hasVariants = true
			break
		}
	}
	if !hasVariants {
		return entries
	}
	out := slices.Clone(entries)
	for i := range out {
		if v := defaultVariant(&out[i]); v != nil {
			out[i].Files = v.Files
		}
	}
	return out
}

// resolvedEmbeddedCatalog caches the variant-resolved EmbeddedCatalog for the
// pre-LoadCatalog fallback below, so that fallback does not re-clone the catalog
// on every read once an embedded entry carries variants. The embedded catalog is
// immutable, so resolving it once is safe; when no entry has variants the value
// is EmbeddedCatalog itself (resolveVariantDefaults returns its input by identity).
var resolvedEmbeddedCatalog = sync.OnceValue(func() []CatalogEntry {
	return resolveVariantDefaults(EmbeddedCatalog)
})

// currentCatalogLocked returns the active runtime catalog, falling back to the
// built-in EmbeddedCatalog when no catalog has been loaded. In both cases each
// variant entry's Files is resolved to its default variant's files. Callers must
// hold catalogMu (read or write).
func currentCatalogLocked() []CatalogEntry {
	if activeCatalog == nil {
		return resolvedEmbeddedCatalog()
	}
	return activeCatalog
}

// setActiveCatalog replaces the runtime catalog. It is called by LoadCatalog
// once at startup. Passing EmbeddedCatalog restores the built-in default, and
// passing nil restores the "use EmbeddedCatalog" sentinel. Test callers that use
// it to inject a catalog must run serially (no t.Parallel), since it mutates
// this package-global.
func setActiveCatalog(entries []CatalogEntry) {
	resolved := resolveVariantDefaults(entries)
	catalogMu.Lock()
	activeCatalog = resolved
	catalogMu.Unlock()
}

// ActiveCatalog returns a snapshot copy of the active runtime catalog (all
// entries, including hidden ones). The copy is safe for callers to range over
// without holding any lock. Entries' Files slices are shared (read-only).
func ActiveCatalog() []CatalogEntry {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	cat := currentCatalogLocked()
	out := make([]CatalogEntry, len(cat))
	copy(out, cat)
	return out
}

// GetCatalogEntry returns the catalog entry with the given ID and true,
// or a zero value and false if no entry matches.
func GetCatalogEntry(id string) (CatalogEntry, bool) {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	cat := currentCatalogLocked()
	for i := range cat {
		if cat[i].ID == id {
			return cat[i], true
		}
	}
	return CatalogEntry{}, false
}

// VisibleCatalog returns catalog entries that are not hidden.
func VisibleCatalog() []CatalogEntry {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	cat := currentCatalogLocked()
	visible := make([]CatalogEntry, 0, len(cat))
	for i := range cat {
		if !cat[i].Hidden {
			visible = append(visible, cat[i])
		}
	}
	return visible
}

// CatalogByCategory groups visible catalog entries by their Category field
// (e.g., "wildlife", "bird", "bat", "geomodel") and returns the resulting map.
func CatalogByCategory() map[string][]CatalogEntry {
	visible := VisibleCatalog()
	grouped := make(map[string][]CatalogEntry)
	for i := range visible {
		grouped[visible[i].Category] = append(grouped[visible[i].Category], visible[i])
	}
	return grouped
}
