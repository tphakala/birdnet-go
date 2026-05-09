// model_catalog.go defines the embedded catalog of downloadable models
// available in the model gallery UI. Each entry references a ModelRegistry
// key via RegistryID and provides download metadata for HuggingFace repos.
package classifier

// Catalog category constants.
const (
	CategoryBird = "bird"
	CategoryBat  = "bat"
)

// CatalogFile role constants.
const (
	RoleModel      = "model"
	RoleLabels     = "labels"
	RoleEmbeddings = "embeddings"
)

// CatalogEntry describes a downloadable model available in the model gallery.
type CatalogEntry struct {
	ID                string        // unique catalog identifier (e.g., "battybirdnet-eu")
	Name              string        // user-facing display name
	Description       string        // short description of the model
	Author            string        // model author or organization
	License           string        // license identifier (e.g., "Apache-2.0")
	CommercialUse     bool          // whether commercial use is permitted
	Category          string        // "bird" or "bat"
	Region            string        // geographic region, or empty for global models
	SpeciesCount      int           // number of species the model can identify
	Version           string        // model version string
	RegistryID        string        // maps to a ModelRegistry key; empty if loader not yet implemented
	RequiredBuildTags []string      // build tags required for this model (e.g., ["onnx"])
	HuggingFaceRepo   string        // HuggingFace repository path
	Files             []CatalogFile // files to download for this model
}

// CatalogFile describes a single file within a model's HuggingFace repository.
type CatalogFile struct {
	RemotePath string // path within the HuggingFace repo
	LocalName  string // filename to use on disk
	Role       string // file role: "model", "labels", or "embeddings"
	SHA256     string // hex-encoded SHA-256 checksum (placeholder for now)
	SizeBytes  int64  // file size in bytes
}

// EmbeddedCatalog is the built-in list of models available for download.
// Each entry provides enough metadata for the gallery UI and enough file
// information to drive the download process.
var EmbeddedCatalog = []CatalogEntry{
	// Bird models
	{
		ID:                "birdnet-v3.0",
		Name:              "BirdNET v3.0",
		Description:       "Global bird species classifier using BirdNET v3.0 architecture",
		Author:            "Cornell Lab of Ornithology & Chemnitz University of Technology",
		License:           "TBD",
		CommercialUse:     false,
		Category:          CategoryBird,
		Region:            "",
		SpeciesCount:      0, // determined at runtime from label file
		Version:           "3.0",
		RegistryID:        "BirdNET_V3.0",
		RequiredBuildTags: []string{"onnx"},
		HuggingFaceRepo:   "tphakala/BirdNET-v3.0",
		Files: []CatalogFile{
			{RemotePath: "birdnet_v3.0.onnx", LocalName: "birdnet_v3.0.onnx", Role: RoleModel, SHA256: "placeholder", SizeBytes: 0},
			{RemotePath: "labels.txt", LocalName: "birdnet_v3.0_labels.txt", Role: RoleLabels, SHA256: "placeholder", SizeBytes: 0},
		},
	},
	{
		ID:                "perch-v2",
		Name:              "Google Perch v2",
		Description:       "Google Perch v2 classifier with approximately 14,795 species (scientific names only)",
		Author:            "Google Research",
		License:           "Apache-2.0",
		CommercialUse:     true,
		Category:          CategoryBird,
		Region:            "",
		SpeciesCount:      14795,
		Version:           "2",
		RegistryID:        "Perch_V2",
		RequiredBuildTags: []string{"onnx"},
		HuggingFaceRepo:   "tphakala/Perch-v2",
		Files: []CatalogFile{
			{RemotePath: "perch_v2.onnx", LocalName: "perch_v2.onnx", Role: RoleModel, SHA256: "placeholder", SizeBytes: 0},
			{RemotePath: "labels.txt", LocalName: "perch_v2_labels.txt", Role: RoleLabels, SHA256: "placeholder", SizeBytes: 0},
		},
	},
	{
		ID:                "bsg-finland",
		Name:              "BSG Finland v4.4",
		Description:       "Regional bird classifier optimized for Finnish bird species",
		Author:            "University of Jyväskylä",
		License:           "Non-commercial",
		CommercialUse:     false,
		Category:          CategoryBird,
		Region:            "Finland",
		SpeciesCount:      0,
		Version:           "4.4",
		RegistryID:        "", // BSG loader not yet implemented
		RequiredBuildTags: []string{"onnx"},
		HuggingFaceRepo:   "tphakala/BSG",
		Files: []CatalogFile{
			{RemotePath: "bsg_finland_v4.4.onnx", LocalName: "bsg_finland_v4.4.onnx", Role: RoleModel, SHA256: "placeholder", SizeBytes: 0},
			{RemotePath: "labels.txt", LocalName: "bsg_finland_v4.4_labels.txt", Role: RoleLabels, SHA256: "placeholder", SizeBytes: 0},
		},
	},

	// Bat models (BattyBirdNET family by rdz-oss)
	batCatalogEntry("battybirdnet-bavaria", "BattyBirdNET Bavaria", "Bavaria", 32, "Bavaria"),
	batCatalogEntry("battybirdnet-bavaria-high", "BattyBirdNET Bavaria (High)", "Bavaria", 24, "Bavaria-High"),
	batCatalogEntry("battybirdnet-eu", "BattyBirdNET EU", "Europe", 30, "EU"),
	batCatalogEntry("battybirdnet-scotland", "BattyBirdNET Scotland", "Scotland", 11, "Scotland"),
	batCatalogEntry("battybirdnet-southwales", "BattyBirdNET South Wales", "South Wales", 29, "SouthWales"),
	batCatalogEntry("battybirdnet-sweden", "BattyBirdNET Sweden", "Sweden", 23, "Sweden"),
	batCatalogEntry("battybirdnet-uk", "BattyBirdNET UK", "UK", 20, "UK"),
	batCatalogEntry("battybirdnet-usa", "BattyBirdNET USA", "USA", 38, "USA"),
	batCatalogEntry("battybirdnet-usa-east", "BattyBirdNET USA East", "USA East", 23, "USA-East"),
	batCatalogEntry("battybirdnet-usa-east-high", "BattyBirdNET USA East (High)", "USA East", 17, "USA-East-High"),
	batCatalogEntry("battybirdnet-usa-west", "BattyBirdNET USA West", "USA West", 28, "USA-West"),
}

// batCatalogEntry constructs a CatalogEntry for a BattyBirdNET regional model.
// The fileSuffix parameter is used to build HuggingFace file paths (e.g., "EU"
// produces "BattyBirdNET-EU-256kHz_fp32.onnx").
func batCatalogEntry(id, name, region string, speciesCount int, fileSuffix string) CatalogEntry {
	return CatalogEntry{
		ID:                id,
		Name:              name,
		Description:       "Bat species detection for " + region + " using BirdNET v2.4 embeddings",
		Author:            "R.D. Zinck",
		License:           "CC-BY-NC-SA-4.0",
		CommercialUse:     false,
		Category:          CategoryBat,
		Region:            region,
		SpeciesCount:      speciesCount,
		Version:           "1.0",
		RegistryID:        "Bat",
		RequiredBuildTags: []string{"onnx"},
		HuggingFaceRepo:   "tphakala/BattyBirdNET-onnx",
		Files: []CatalogFile{
			{
				RemotePath: "fp32/BattyBirdNET-" + fileSuffix + "-256kHz_fp32.onnx",
				LocalName:  "BattyBirdNET-" + fileSuffix + "-256kHz_fp32.onnx",
				Role:       RoleModel,
				SHA256:     "placeholder",
				SizeBytes:  0,
			},
			{
				RemotePath: "labels/BattyBirdNET-" + fileSuffix + "-256kHz_Labels.txt",
				LocalName:  "BattyBirdNET-" + fileSuffix + "-256kHz_Labels.txt",
				Role:       RoleLabels,
				SHA256:     "placeholder",
				SizeBytes:  0,
			},
			{
				RemotePath: "birdnet-v24-embeddings.onnx",
				LocalName:  "birdnet-v24-embeddings.onnx",
				Role:       RoleEmbeddings,
				SHA256:     "placeholder",
				SizeBytes:  0,
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

// CatalogByCategory groups all catalog entries by their Category field
// (e.g., "bird", "bat") and returns the resulting map.
func CatalogByCategory() map[string][]CatalogEntry {
	grouped := make(map[string][]CatalogEntry)
	for i := range EmbeddedCatalog {
		grouped[EmbeddedCatalog[i].Category] = append(grouped[EmbeddedCatalog[i].Category], EmbeddedCatalog[i])
	}
	return grouped
}
