// avicommons.go: Implements an ImageProvider using the Avicommons dataset.
package imageprovider

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability"
)

const (
	aviCommonsProviderName = "avicommons"
	aviCommonsBaseURL      = "https://static.avicommons.org"
	aviCommonsDefaultSize  = "320" // Default image size (e.g., 240, 320, 480, 900)
)

// aviCommonsEntry represents a single entry in the Avicommons full JSON data.
type aviCommonsEntry struct {
	Code    string `json:"code"`    // eBird species code
	Name    string `json:"name"`    // Common Name
	SciName string `json:"sciName"` // Scientific Name
	License string `json:"license"` // License code (e.g., "cc-by-nc")
	Key     string `json:"key"`     // Photo ID
	By      string `json:"by"`      // Author Name
	Family  string `json:"family"`
}

// AviCommonsProvider fetches images from the pre-loaded Avicommons dataset.
type AviCommonsProvider struct {
	data       []aviCommonsEntry           // Holds the parsed data from latest.json
	sciNameMap map[string]*aviCommonsEntry // Map for quick lookup by scientific name
	mu         sync.RWMutex
	debug      bool
}

var (
	// loggedUnknownLicenses tracks unknown license codes to avoid repeated warnings.
	loggedUnknownLicenses sync.Map
)

// NewAviCommonsProvider creates a new provider instance using data from the provided filesystem.
// It expects the filesystem to contain 'data/latest.json'.
func NewAviCommonsProvider(dataFs fs.FS, debug bool) (*AviCommonsProvider, error) {
	logger := imageProviderLogger.With("provider", aviCommonsProviderName)
	logger.Info("Initializing AviCommons provider")
	filePath := "data/latest.json"
	altFilePath := "internal/imageprovider/data/latest.json"
	logger.Debug("Attempting to read Avicommons data file",
		"primary_path", filePath,
		"fallback_path", altFilePath)
	// First try the direct path
	jsonData, err := fs.ReadFile(dataFs, filePath)
	if err != nil {
		logger.Debug("Primary path not found, trying alternative path",
			"primary_path", filePath,
			"error", err.Error(),
			"alternative_path", altFilePath)
		// If that fails, try with the internal/imageprovider prefix
		jsonData, err = fs.ReadFile(dataFs, altFilePath)
		if err != nil {
			enhancedErr := errors.Newf("failed to read Avicommons data file from embedded filesystem: %v", err).
				Component("imageprovider").
				Category(errors.CategoryFileIO).
				Context("provider", aviCommonsProviderName).
				Context("primary_path", filePath).
				Context("alternative_path", altFilePath).
				Context("operation", "read_avicommons_data_file").
				Context("error_detail", err.Error()).
				Build()
			logger.Error("Failed to read Avicommons data file from both paths",
				"primary_path", filePath,
				"alternative_path", altFilePath,
				"error", enhancedErr)
			return nil, enhancedErr
		}
	}

	if len(jsonData) == 0 {
		enhancedErr := errors.Newf("avicommons JSON data is empty").
			Component("imageprovider").
			Category(errors.CategoryFileIO).
			Context("provider", aviCommonsProviderName).
			Context("operation", "validate_json_data").
			Build()
		logger.Error("Avicommons JSON data file is empty",
		"error", enhancedErr)
		return nil, enhancedErr
	}
	logger.Info("Successfully read Avicommons data file",
		"size_bytes", len(jsonData),
		"size_kb", len(jsonData)/1024)

	logger.Debug("Unmarshalling Avicommons JSON data")
	var data []aviCommonsEntry
	if err := json.Unmarshal(jsonData, &data); err != nil {
		enhancedErr := errors.Newf("failed to parse Avicommons JSON data: %v", err).
			Component("imageprovider").
			Category(errors.CategoryFileParsing).
			Context("provider", aviCommonsProviderName).
			Context("data_size_bytes", len(jsonData)).
			Context("operation", "unmarshal_json_data").
			Context("error_detail", err.Error()).
			Build()
		logger.Error("Failed to unmarshal Avicommons JSON data",
		"data_size_bytes", len(jsonData),
		"error", enhancedErr)
		return nil, enhancedErr
	}

	if len(data) == 0 {
		enhancedErr := errors.Newf("avicommons JSON data is empty or invalid").
			Component("imageprovider").
			Category(errors.CategoryFileParsing).
			Context("provider", aviCommonsProviderName).
			Context("data_size_bytes", len(jsonData)).
			Context("operation", "validate_unmarshalled_data").
			Build()
		logger.Error("Avicommons JSON data unmarshalled to empty slice",
		"data_size_bytes", len(jsonData),
		"error", enhancedErr)
		return nil, enhancedErr
	}

	// Build map for faster lookups
	logger.Debug("Building scientific name lookup map",
		"total_entries", len(data))
	sciNameMap := make(map[string]*aviCommonsEntry, len(data))
	for i := range data {
		// Normalize the scientific name for consistent matching
		normalizedSciName := strings.ToLower(data[i].SciName)
		// Store pointer to the entry in the map
		// If multiple entries exist for the same normalized name, the last one wins.
		// This seems acceptable for now, but could be revisited if needed.
		sciNameMap[normalizedSciName] = &data[i]
	}
	logger.Info("Avicommons provider initialized successfully",
		"total_entries", len(data),
		"unique_sci_names", len(sciNameMap))

	return &AviCommonsProvider{
		data:       data,
		sciNameMap: sciNameMap,
		debug:      debug, // Keep debug flag if needed elsewhere
	}, nil
}

// Fetch retrieves image information for a given scientific name from the Avicommons data.
func (p *AviCommonsProvider) Fetch(scientificName string) (BirdImage, error) {
	logger := imageProviderLogger.With("provider", aviCommonsProviderName, "scientific_name", scientificName)
	logger.Debug("Fetching image from Avicommons data",
		"scientific_name", scientificName)
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Normalize the input scientific name for lookup
	normalizedSciName := strings.ToLower(scientificName)
	entry, found := p.sciNameMap[normalizedSciName]

	if !found {
		logger.Warn("Image not found in Avicommons data",
			"scientific_name", scientificName,
			"normalized_name", normalizedSciName)
		return BirdImage{}, ErrImageNotFound
	}

	// Construct the image URL
	// Format: https://static.avicommons.org/{code}-{key}-{size}.jpg
	imageURL := fmt.Sprintf("%s/%s-%s-%s.jpg",
		aviCommonsBaseURL,
		entry.Code,
		entry.Key,
		aviCommonsDefaultSize, // Use the default size for now
	)

	// Map license code to name and URL (basic mapping, can be expanded)
	licenseName, licenseURL := mapAviCommonsLicense(entry.License)

	logger.Debug("Image found in Avicommons data",
		"scientific_name", scientificName,
		"url", imageURL,
		"author", entry.By,
		"license", entry.License,
		"ebird_code", entry.Code,
		"photo_key", entry.Key)

	return BirdImage{
		URL:            imageURL,
		ScientificName: entry.SciName, // Use original capitalization
		LicenseName:    licenseName,
		LicenseURL:     licenseURL,
		AuthorName:     entry.By,
		AuthorURL:      "", // Avicommons doesn't provide author URLs
		SourceProvider: aviCommonsProviderName, // Set the provider name
		// CachedAt is set by the BirdImageCache
	}, nil
}

// mapAviCommonsLicense converts Avicommons license codes to names and URLs.
// This is a basic implementation and might need refinement.
func mapAviCommonsLicense(code string) (name, url string) {
	// No logging needed here as it's a pure function
	switch strings.ToLower(code) {
	case "cc-by":
		return "CC BY 4.0", "https://creativecommons.org/licenses/by/4.0/"
	case "cc-by-sa":
		return "CC BY-SA 4.0", "https://creativecommons.org/licenses/by-sa/4.0/"
	case "cc-by-nd":
		return "CC BY-ND 4.0", "https://creativecommons.org/licenses/by-nd/4.0/"
	case "cc-by-nc":
		return "CC BY-NC 4.0", "https://creativecommons.org/licenses/by-nc/4.0/"
	case "cc-by-nc-sa":
		return "CC BY-NC-SA 4.0", "https://creativecommons.org/licenses/by-nc-sa/4.0/"
	case "cc-by-nc-nd":
		return "CC BY-NC-ND 4.0", "https://creativecommons.org/licenses/by-nc-nd/4.0/"
	case "cc0":
		return "CC0 1.0 Universal", "https://creativecommons.org/publicdomain/zero/1.0/"
	default:
		// Log only once per unknown code
		if _, loaded := loggedUnknownLicenses.LoadOrStore(code, true); !loaded {
			imageProviderLogger.Warn("Unknown Avicommons license code encountered",
				"license_code", code,
				"action", "using_code_as_name")
		}
		return code, ""
	}
}

// CreateAviCommonsCache creates a new BirdImageCache with the AviCommons image provider.
func CreateAviCommonsCache(dataFs fs.FS, metrics *observability.Metrics, store datastore.Interface) (*BirdImageCache, error) {
	logger := imageProviderLogger.With("provider", aviCommonsProviderName)
	logger.Info("Creating AviCommons cache")
	settings := conf.Setting()
	debug := settings.Realtime.Dashboard.Thumbnails.Debug

	// Create the AviCommons provider using the embedded file system
	provider, err := NewAviCommonsProvider(dataFs, debug)
	if err != nil {
		// Error already enhanced in NewAviCommonsProvider
		logger.Error("Failed to create AviCommons provider",
			"error", err)
		return nil, err
	}

	// Initialize the cache with the provider
	logger.Info("Initializing cache with AviCommons provider")
	return InitCache(aviCommonsProviderName, provider, metrics, store), nil
}

// RegisterAviCommonsProvider creates and registers an AviCommons provider with the registry.
func RegisterAviCommonsProvider(registry *ImageProviderRegistry, dataFs fs.FS, metrics *observability.Metrics, store datastore.Interface) error {
	logger := imageProviderLogger.With("provider", aviCommonsProviderName)
	logger.Info("Registering AviCommons provider with registry")
	cache, err := CreateAviCommonsCache(dataFs, metrics, store)
	if err != nil {
		// Error already enhanced in CreateAviCommonsCache
		return err
	}

	if err := registry.Register(aviCommonsProviderName, cache); err != nil {
		// Check if it's already an enhanced error from Register method
		var enhancedErr *errors.EnhancedError
		if !errors.As(err, &enhancedErr) {
			enhancedErr = errors.Newf("failed to register Avicommons provider with registry: %v", err).
				Component("imageprovider").
				Category(errors.CategoryImageProvider).
				Context("provider", aviCommonsProviderName).
				Context("operation", "register_avicommons_provider").
				Context("error_detail", err.Error()).
				Build()
		}
		logger.Error("Failed to register AviCommons provider cache with registry",
			"error", enhancedErr)
		return enhancedErr
	}

	logger.Info("Successfully registered AviCommons provider")
	return nil
}
