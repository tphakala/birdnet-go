// filecache.go provides disk-based image caching for bird images organized by provider.
package imageprovider

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
	"golang.org/x/sync/singleflight"
)

const (
	// imageCacheDir is the default subdirectory for cached images.
	imageCacheDir = "cache/images"

	// maxConcurrentDownloads limits parallel image fetches (for later use).
	maxConcurrentDownloads = 5

	// defaultFileCacheTTL is the default time-to-live for cached image files.
	defaultFileCacheTTL = 30 * 24 * time.Hour
)

// knownExtensions lists the file extensions tried when looking up cached images.
var knownExtensions = []string{".jpg", ".png", ".gif", ".webp", ".svg"}

// imageHTTPClient is a shared HTTP client for downloading images with a 10-second timeout.
// Shared to avoid goroutine leaks from per-request clients creating new HTTP/2 connections.
var imageHTTPClient = &http.Client{Timeout: 10 * time.Second}

// ImageFileCache manages disk-based image caching organized by provider.
type ImageFileCache struct {
	basePath    string
	downloadSem chan struct{}      // limits concurrent external downloads
	sfGroup     singleflight.Group // deduplicates concurrent fetches for same species
}

// NewImageFileCache creates a new ImageFileCache rooted at basePath.
func NewImageFileCache(basePath string) *ImageFileCache {
	return &ImageFileCache{
		basePath:    basePath,
		downloadSem: make(chan struct{}, maxConcurrentDownloads),
	}
}

// normalizeSpeciesName converts a species name to a filesystem-safe form:
// lowercase with spaces replaced by underscores.
func normalizeSpeciesName(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), " ", "_")
}

// validatePathComponent rejects path components that could cause traversal or
// other filesystem issues. It checks for separator characters and uses
// filepath.IsLocal for comprehensive validation.
func validatePathComponent(component string) error {
	if strings.ContainsAny(component, "/\\") {
		return fmt.Errorf("path component contains separator: %q", component)
	}
	cleaned := filepath.Clean(component)
	if !filepath.IsLocal(cleaned) {
		return fmt.Errorf("path component is not local: %q", component)
	}
	return nil
}

// extensionFromContentType maps a MIME content type to a file extension.
// Unknown types default to ".jpg".
func extensionFromContentType(contentType string) string {
	switch {
	case strings.Contains(contentType, "image/png"):
		return ".png"
	case strings.Contains(contentType, "image/gif"):
		return ".gif"
	case strings.Contains(contentType, "image/webp"):
		return ".webp"
	case strings.Contains(contentType, "image/svg"):
		return ".svg"
	default:
		return ".jpg"
	}
}

// buildPath constructs the validated directory and filename prefix for a cached image.
// It returns the directory path and the base filename (without extension).
func (c *ImageFileCache) buildPath(provider, scientificName string) (dir, namePrefix string, err error) {
	if err := validatePathComponent(provider); err != nil {
		return "", "", fmt.Errorf("invalid provider: %w", err)
	}
	normalized := normalizeSpeciesName(scientificName)
	if err := validatePathComponent(normalized); err != nil {
		return "", "", fmt.Errorf("invalid species name: %w", err)
	}
	dir = filepath.Join(c.basePath, provider)
	namePrefix = normalized
	return dir, namePrefix, nil
}

// Store saves image data to the file cache using atomic write (temp file + rename).
// It detects the content type from the data and returns the final file path.
func (c *ImageFileCache) Store(provider, scientificName string, data []byte, sourceURL string) (string, error) {
	log := GetLogger().With(
		logger.String("provider", provider),
		logger.String("species", scientificName),
	)

	dir, namePrefix, err := c.buildPath(provider, scientificName)
	if err != nil {
		return "", fmt.Errorf("build path: %w", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create cache directory: %w", err)
	}

	contentType := http.DetectContentType(data)
	ext := extensionFromContentType(contentType)
	finalPath := filepath.Join(dir, namePrefix+ext)

	// Atomic write: write to temp file then rename.
	tmpFile, err := os.CreateTemp(dir, namePrefix+"-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, writeErr := tmpFile.Write(data); writeErr != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("write temp file: %w", writeErr)
	}
	if closeErr := tmpFile.Close(); closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temp file: %w", closeErr)
	}

	if renameErr := os.Rename(tmpPath, finalPath); renameErr != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("rename temp file: %w", renameErr)
	}

	log.Debug("Stored image in file cache",
		logger.String("path", finalPath),
		logger.String("content_type", contentType),
		logger.String("source_url", sourceURL),
	)

	return finalPath, nil
}

// Get looks up a cached image file for the given provider and species.
// It tries known extensions in order and returns the path, content type,
// and freshness (based on defaultFileCacheTTL). A cache miss returns empty
// strings and no error.
func (c *ImageFileCache) Get(provider, scientificName string) (path, contentType string, fresh bool, err error) {
	dir, namePrefix, err := c.buildPath(provider, scientificName)
	if err != nil {
		return "", "", false, fmt.Errorf("build path: %w", err)
	}

	for _, ext := range knownExtensions {
		candidate := filepath.Join(dir, namePrefix+ext)
		info, statErr := os.Stat(candidate)
		if statErr != nil {
			continue
		}

		ct := contentTypeFromExtension(ext)
		isFresh := c.IsFresh(candidate, defaultFileCacheTTL)

		GetLogger().Debug("File cache hit",
			logger.String("provider", provider),
			logger.String("species", scientificName),
			logger.String("path", candidate),
			logger.Bool("fresh", isFresh),
			logger.Int64("size", info.Size()),
		)

		return candidate, ct, isFresh, nil
	}

	// Cache miss: not an error.
	return "", "", false, nil
}

// IsFresh reports whether the file at the given path was modified within the TTL.
func (c *ImageFileCache) IsFresh(path string, ttl time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < ttl
}

// contentTypeFromExtension returns the MIME type for a file extension.
func contentTypeFromExtension(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	default:
		return "image/jpeg"
	}
}

// DownloadAndStore fetches image bytes from imageURL, stores to disk, deduplicating concurrent requests.
// Returns the cached file path.
func (fc *ImageFileCache) DownloadAndStore(provider, scientificName, imageURL string) (string, error) {
	key := provider + "/" + normalizeSpeciesName(scientificName)

	result, err, _ := fc.sfGroup.Do(key, func() (any, error) {
		// Acquire semaphore to limit concurrent downloads.
		fc.downloadSem <- struct{}{}
		defer func() { <-fc.downloadSem }()

		resp, err := imageHTTPClient.Get(imageURL)
		if err != nil {
			return nil, fmt.Errorf("failed to download image: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("non-200 status downloading image: %d", resp.StatusCode)
		}

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read image body: %w", err)
		}

		return fc.Store(provider, scientificName, data, imageURL)
	})

	if err != nil {
		return "", err
	}
	return result.(string), nil
}
