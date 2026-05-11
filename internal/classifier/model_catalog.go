// model_catalog.go defines the embedded catalog of downloadable models
// available in the model gallery UI. Each entry references a ModelRegistry
// key via RegistryID and provides download metadata for HuggingFace repos.
package classifier

// Catalog category constants.
const (
	CategoryWildlife = "wildlife"
	CategoryBird     = "bird"
	CategoryBat      = "bat"
)

// CatalogFile role constants.
const (
	RoleModel      = "model"
	RoleLabels     = "labels"
	RoleEmbeddings = "embeddings"
	RoleData       = "data"
)

// CatalogEntry describes a downloadable model available in the model gallery.
type CatalogEntry struct {
	ID              string        // unique catalog identifier (e.g., "battybirdnet-eu")
	Name            string        // user-facing display name
	Description     string        // short description of the model
	Author          string        // model author or organization
	License         string        // license identifier (e.g., "Apache-2.0")
	CommercialUse   bool          // whether commercial use is permitted
	Category        string        // "wildlife", "bird", or "bat"
	Region          string        // geographic region, or empty for global models
	SpeciesCount    int           // number of species the model can identify
	Version         string        // model version string
	RegistryID      string        // maps to a ModelRegistry key; empty if loader not yet implemented
	Hidden          bool          // if true, entry is excluded from the gallery UI
	UpstreamURL     string        // URL to the upstream project repository
	HuggingFaceRepo string        // HuggingFace repository path
	Files           []CatalogFile // files to download for this model
}

// CatalogFile describes a single file within a model's HuggingFace repository.
type CatalogFile struct {
	RemotePath string // path within the HuggingFace repo
	LocalName  string // filename to use on disk
	Role       string // file role: "model", "labels", "embeddings", or "data"
	SHA256     string // hex-encoded SHA-256 checksum
	SizeBytes  int64  // file size in bytes
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
		RegistryID:      RegistryIDBirdNETV3,
		Hidden:          true,
		UpstreamURL:     "https://github.com/birdnet-team/BirdNET-Analyzer",
		HuggingFaceRepo: "tphakala/BirdNET-v3.0",
		Files: []CatalogFile{
			{RemotePath: "birdnet_v3.0.onnx", LocalName: "birdnet_v3.0.onnx", Role: RoleModel, SHA256: "placeholder", SizeBytes: 0},
			{RemotePath: "labels.txt", LocalName: "birdnet_v3.0_labels.txt", Role: RoleLabels, SHA256: "placeholder", SizeBytes: 0},
		},
	},
	{
		ID:              "perch-v2",
		Name:            "Google Perch v2",
		Description:     "Google Perch v2 classifier with approximately 14,795 species (scientific names only)",
		Author:          "Google Research",
		License:         "Apache-2.0",
		CommercialUse:   true,
		Category:        CategoryWildlife,
		Region:          "",
		SpeciesCount:    14795,
		Version:         "2",
		RegistryID:      RegistryIDPerchV2,
		UpstreamURL:     "https://www.kaggle.com/models/google/bird-vocalization-classifier/tensorFlow2/perch_v2",
		HuggingFaceRepo: "tphakala/Perch-v2",
		Files: []CatalogFile{
			{RemotePath: "perch_v2.onnx", LocalName: "perch_v2.onnx", Role: RoleModel, SHA256: "bf0c8467a924cb074663970ca4a0ab1e143602121930209657d0dff5d5cefa1f", SizeBytes: 409148616},
			{RemotePath: "labels.txt", LocalName: "perch_v2_labels.txt", Role: RoleLabels, SHA256: "e4d5c0397d8fb08bf90c6b13a34810af53504faad927e472fcc567793c9de057", SizeBytes: 312716},
		},
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
const (
	embeddingsSHA256          = "b6b8f24dc9c3d43f2deb14a6f2c7b5b233e7477b6baf1b52341291e714903fb0"
	embeddingsSizeBytes int64 = 66932350
)

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
				RemotePath: "birdnet-v24-embeddings.onnx",
				LocalName:  "birdnet-v24-embeddings.onnx",
				Role:       RoleEmbeddings,
				SHA256:     embeddingsSHA256,
				SizeBytes:  embeddingsSizeBytes,
			},
		},
	}
}

// GetCatalogEntry returns the catalog entry with the given ID and true,
// or a zero value and false if no entry matches.
func GetCatalogEntry(id string) (CatalogEntry, bool) {
	for i := range EmbeddedCatalog {
		if EmbeddedCatalog[i].ID == id {
			return EmbeddedCatalog[i], true
		}
	}
	return CatalogEntry{}, false
}

// VisibleCatalog returns catalog entries that are not hidden.
func VisibleCatalog() []CatalogEntry {
	visible := make([]CatalogEntry, 0, len(EmbeddedCatalog))
	for i := range EmbeddedCatalog {
		if !EmbeddedCatalog[i].Hidden {
			visible = append(visible, EmbeddedCatalog[i])
		}
	}
	return visible
}

// CatalogByCategory groups visible catalog entries by their Category field
// (e.g., "bird", "bat") and returns the resulting map.
func CatalogByCategory() map[string][]CatalogEntry {
	visible := VisibleCatalog()
	grouped := make(map[string][]CatalogEntry)
	for i := range visible {
		grouped[visible[i].Category] = append(grouped[visible[i].Category], visible[i])
	}
	return grouped
}
