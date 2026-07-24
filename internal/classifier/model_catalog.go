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

// CatalogEntry describes a downloadable model available in the model gallery.
//
// The snake_case JSON tags define the on-disk schema for the user-editable
// model-catalog.json file (see catalog_loader.go). They are intentionally
// stable and readable for hand-editing. The model-gallery API does NOT
// serialize this struct directly; it maps to a separate camelCase response
// type (see internal/api/v2/models.go), so these tags do not affect the API.
type CatalogEntry struct {
	ID              string        `json:"id"`                // unique catalog identifier (e.g., "battybirdnet-eu")
	Name            string        `json:"name"`              // user-facing display name
	Description     string        `json:"description"`       // short description of the model
	Author          string        `json:"author"`            // model author or organization
	License         string        `json:"license"`           // license identifier (e.g., "Apache-2.0")
	CommercialUse   bool          `json:"commercial_use"`    // whether commercial use is permitted
	Category        string        `json:"category"`          // "wildlife", "bird", "bat", or "geomodel"
	Region          string        `json:"region"`            // geographic region, or empty for global models
	SpeciesCount    int           `json:"species_count"`     // number of species the model can identify
	Version         string        `json:"version"`           // model version string
	GeomodelVersion string        `json:"geomodel_version"`  // geomodel range filter version (e.g., "v3"); empty if no geomodel
	RegistryID      string        `json:"registry_id"`       // maps to a ModelRegistry key; empty if loader not yet implemented
	Hidden          bool          `json:"hidden"`            // if true, entry is excluded from the gallery UI
	RequiresONNX    bool          `json:"requires_onnx"`     // if true, model needs ONNX Runtime (not just TFLite)
	UpstreamURL     string        `json:"upstream_url"`      // URL to the upstream project repository
	HuggingFaceRepo string        `json:"hugging_face_repo"` // HuggingFace repository path
	Files           []CatalogFile `json:"files"`             // files to download for this model
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

// EmbeddedCatalog is the built-in list of models available for download.
// Each entry provides enough metadata for the gallery UI and enough file
// information to drive the download process.
var EmbeddedCatalog = []CatalogEntry{
	// Wildlife models (multi-taxa classifiers)
	{
		ID:              "birdnet-v3.0",
		Name:            "BirdNET v3.0",
		Description:     "Global wildlife classifier using BirdNET v3.0 architecture",
		Author:          "Cornell Lab of Ornithology & Chemnitz University of Technology",
		License:         "TBD",
		CommercialUse:   false,
		Category:        CategoryWildlife,
		Region:          "",
		SpeciesCount:    0, // determined at runtime from label file
		Version:         "3.0",
		GeomodelVersion: "v3",
		RegistryID:      RegistryIDBirdNETV3,
		Hidden:          true,
		RequiresONNX:    true,
		UpstreamURL:     "https://github.com/birdnet-team/BirdNET-Analyzer",
		HuggingFaceRepo: "tphakala/BirdNET-v3.0",
		Files: slices.Concat([]CatalogFile{
			{RemotePath: "birdnet_v3.0.onnx", LocalName: "birdnet_v3.0.onnx", Role: RoleModel, SHA256: "", SizeBytes: 0},
			{RemotePath: "labels.txt", LocalName: "birdnet_v3.0_labels.txt", Role: RoleLabels, SHA256: "", SizeBytes: 0},
		}, geomodelFiles(), taxonomyFiles()),
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
		HuggingFaceRepo: "tphakala/Perch-v2",
		Files: slices.Concat([]CatalogFile{
			{RemotePath: "perch_v2.onnx", LocalName: "perch_v2.onnx", Role: RoleModel, SHA256: "bf0c8467a924cb074663970ca4a0ab1e143602121930209657d0dff5d5cefa1f", SizeBytes: 409148616},
			{RemotePath: "labels.txt", LocalName: "perch_v2_labels.txt", Role: RoleLabels, SHA256: "e4d5c0397d8fb08bf90c6b13a34810af53504faad927e472fcc567793c9de057", SizeBytes: 312716},
		}, geomodelFiles(), taxonomyFiles()),
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

// currentCatalogLocked returns the active runtime catalog, falling back to the
// built-in EmbeddedCatalog when no catalog has been loaded. Callers must hold
// catalogMu (read or write).
func currentCatalogLocked() []CatalogEntry {
	if activeCatalog == nil {
		return EmbeddedCatalog
	}
	return activeCatalog
}

// setActiveCatalog replaces the runtime catalog. It is called by LoadCatalog
// once at startup. Passing EmbeddedCatalog restores the built-in default, and
// passing nil restores the "use EmbeddedCatalog" sentinel. Test callers that use
// it to inject a catalog must run serially (no t.Parallel), since it mutates
// this package-global.
func setActiveCatalog(entries []CatalogEntry) {
	catalogMu.Lock()
	activeCatalog = entries
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
