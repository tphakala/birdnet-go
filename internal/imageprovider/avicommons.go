// avicommons.go: Implements an ImageProvider using the Avicommons dataset.
package imageprovider

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"strings"
	"sync"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/telemetry"
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

// NewAviCommonsProvider creates a new provider instance using data from the provided filesystem.
// It expects the filesystem to contain 'data/latest.json'.
func NewAviCommonsProvider(dataFs fs.FS, debug bool) (*AviCommonsProvider, error) {
	// First try the direct path
	jsonData, err := fs.ReadFile(dataFs, "data/latest.json")
	if err != nil {
		// If that fails, try with the internal/imageprovider prefix
		jsonData, err = fs.ReadFile(dataFs, "internal/imageprovider/data/latest.json")
		if err != nil {
			return nil, fmt.Errorf("failed to read avicommons data file: %w", err)
		}
	}

	if len(jsonData) == 0 {
		return nil, fmt.Errorf("avicommons JSON data is empty")
	}

	var data []aviCommonsEntry
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Avicommons JSON data: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("avicommons JSON data is empty or invalid")
	}

	// Build map for faster lookups
	sciNameMap := make(map[string]*aviCommonsEntry, len(data))
	for i := range data {
		// Normalize the scientific name for consistent matching
		normalizedSciName := strings.ToLower(data[i].SciName)
		// Store pointer to the entry in the map
		// If multiple entries exist for the same normalized name, the last one wins.
		// This seems acceptable for now, but could be revisited if needed.
		sciNameMap[normalizedSciName] = &data[i]
	}

	if debug {
		log.Printf("Initialized AviCommonsProvider with %d entries, %d unique scientific names.", len(data), len(sciNameMap))
	}

	return &AviCommonsProvider{
		data:       data,
		sciNameMap: sciNameMap,
		debug:      debug,
	}, nil
}

// Fetch retrieves image information for a given scientific name from the Avicommons data.
func (p *AviCommonsProvider) Fetch(scientificName string) (BirdImage, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Normalize the input scientific name for lookup
	normalizedSciName := strings.ToLower(scientificName)
	entry, found := p.sciNameMap[normalizedSciName]

	if !found {
		if p.debug {
			log.Printf("Debug: [%s] Image not found for scientific name: %s (normalized: %s)", aviCommonsProviderName, scientificName, normalizedSciName)
		}
		// Consider returning a specific error type or using a sentinel error
		return BirdImage{}, fmt.Errorf("[%s] image not found for %s", aviCommonsProviderName, scientificName)
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

	if p.debug {
		log.Printf("Debug: [%s] Found image for %s: URL=%s, Author=%s, License=%s", aviCommonsProviderName, scientificName, imageURL, entry.By, entry.License)
	}

	return BirdImage{
		URL:            imageURL,
		ScientificName: entry.SciName, // Use original capitalization
		LicenseName:    licenseName,
		LicenseURL:     licenseURL,
		AuthorName:     entry.By,
		AuthorURL:      "", // Avicommons doesn't provide author URLs
		// CachedAt is set by the BirdImageCache
	}, nil
}

// mapAviCommonsLicense converts Avicommons license codes to names and URLs.
// This is a basic implementation and might need refinement.
func mapAviCommonsLicense(code string) (name, url string) {
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
		// Return the code itself if unknown, or consider logging a warning
		return code, ""
	}
}

// CreateAviCommonsCache creates a new BirdImageCache with the AviCommons image provider.
func CreateAviCommonsCache(dataFs fs.FS, metrics *telemetry.Metrics, store datastore.Interface) (*BirdImageCache, error) {
	settings := conf.Setting()
	debug := settings.Realtime.Dashboard.Thumbnails.Debug

	// Create the AviCommons provider using the embedded file system
	provider, err := NewAviCommonsProvider(dataFs, debug)
	if err != nil {
		return nil, fmt.Errorf("failed to create AviCommons provider: %w", err)
	}

	// Initialize the cache with the provider
	return InitCache(aviCommonsProviderName, provider, metrics, store), nil
}

// RegisterAviCommonsProvider creates and registers an AviCommons provider with the registry.
func RegisterAviCommonsProvider(registry *ImageProviderRegistry, dataFs fs.FS, metrics *telemetry.Metrics, store datastore.Interface) error {
	cache, err := CreateAviCommonsCache(dataFs, metrics, store)
	if err != nil {
		return fmt.Errorf("failed to create AviCommons cache: %w", err)
	}

	if err := registry.Register(aviCommonsProviderName, cache); err != nil {
		return fmt.Errorf("failed to register AviCommons provider: %w", err)
	}

	return nil
}
