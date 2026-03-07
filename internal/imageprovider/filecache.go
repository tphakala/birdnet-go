// filecache.go provides disk-based image caching for bird images organized by provider.
package imageprovider

import (
	"context"
	"fmt"
	"io"
	"net"
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

	// maxConcurrentDownloads limits parallel image fetches.
	maxConcurrentDownloads = 5

	// maxImageSize is the maximum allowed image download size (10 MB).
	maxImageSize = 10 << 20

	// defaultFileCacheTTL is the default time-to-live for cached image files.
	defaultFileCacheTTL = 30 * 24 * time.Hour
)

// knownExtensions lists the file extensions tried when looking up cached images.
var knownExtensions = []string{".jpg", ".png", ".gif", ".webp", ".svg"}

// isSafeIP reports whether ip is safe to connect to (not loopback, private, link-local, or unspecified).
func isSafeIP(ip net.IP) bool {
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsUnspecified()
}

// imageHTTPClient is a shared HTTP client for downloading images with SSRF protection.
// The custom DialContext resolves hostnames and dials validated IPs directly (not hostnames),
// preventing SSRF via DNS rebinding, localhost, or IP-literal redirects.
var imageHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("invalid address %q: %w", addr, err)
			}

			// Resolve the hostname to IPs and validate them.
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("DNS lookup failed for %q: %w", host, err)
			}

			// Dial the first safe resolved IP directly to prevent TOCTOU/DNS rebinding.
			dialer := &net.Dialer{Timeout: 5 * time.Second}
			var lastErr error
			for _, ipAddr := range ips {
				if !isSafeIP(ipAddr.IP) {
					continue
				}
				conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ipAddr.IP.String(), port))
				if dialErr != nil {
					lastErr = dialErr
					continue
				}
				return conn, nil
			}
			if lastErr != nil {
				return nil, fmt.Errorf("failed to connect to %q: %w", host, lastErr)
			}
			return nil, fmt.Errorf("no safe IP addresses for host %q", host)
		},
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

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
// It uses upstreamContentType (from the HTTP response) to determine the extension;
// if empty or generic, it falls back to http.DetectContentType on the data bytes.
// Returns the final file path and the resolved content type.
func (c *ImageFileCache) Store(provider, scientificName string, data []byte, sourceURL, upstreamContentType string) (filePath, resolvedContentType string, err error) {
	log := GetLogger().With(
		logger.String("provider", provider),
		logger.String("species", scientificName),
	)

	dir, namePrefix, err := c.buildPath(provider, scientificName)
	if err != nil {
		return "", "", fmt.Errorf("build path: %w", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("create cache directory: %w", err)
	}

	// Prefer upstream Content-Type; fall back to sniffing if missing or generic.
	contentType := upstreamContentType
	if contentType == "" || strings.HasPrefix(contentType, "application/octet-stream") || strings.HasPrefix(contentType, "text/") {
		contentType = http.DetectContentType(data)
	}
	ext := extensionFromContentType(contentType)
	finalPath := filepath.Join(dir, namePrefix+ext)

	// Atomic write: write to temp file then rename.
	tmpFile, err := os.CreateTemp(dir, namePrefix+"-*.tmp")
	if err != nil {
		return "", "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, writeErr := tmpFile.Write(data); writeErr != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("write temp file: %w", writeErr)
	}
	if closeErr := tmpFile.Close(); closeErr != nil {
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("close temp file: %w", closeErr)
	}

	if renameErr := os.Rename(tmpPath, finalPath); renameErr != nil {
		_ = os.Remove(tmpPath)
		return "", "", fmt.Errorf("rename temp file: %w", renameErr)
	}

	resolvedCT := contentTypeFromExtension(ext)
	log.Debug("Stored image in file cache",
		logger.String("path", finalPath),
		logger.String("content_type", resolvedCT),
		logger.String("source_url", sourceURL),
	)

	return finalPath, resolvedCT, nil
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

// downloadResult holds the path and content type returned by DownloadAndStore via singleflight.
type downloadResult struct {
	path        string
	contentType string
}

// DownloadAndStore fetches image bytes from imageURL, stores to disk, deduplicating concurrent requests.
// Returns the cached file path and the resolved content type.
func (fc *ImageFileCache) DownloadAndStore(provider, scientificName, imageURL string) (filePath, contentType string, err error) {
	key := provider + "/" + normalizeSpeciesName(scientificName)

	result, err, _ := fc.sfGroup.Do(key, func() (any, error) {
		// Acquire semaphore to limit concurrent downloads.
		fc.downloadSem <- struct{}{}
		defer func() { <-fc.downloadSem }()

		resp, err := imageHTTPClient.Get(imageURL) //nolint:gosec // URL comes from trusted DB entries, not user input
		if err != nil {
			return nil, fmt.Errorf("failed to download image: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("non-200 status downloading image: %d", resp.StatusCode)
		}

		// Limit read size to prevent OOM from malicious or malformed responses.
		limited := io.LimitReader(resp.Body, maxImageSize+1)
		data, err := io.ReadAll(limited)
		if err != nil {
			return nil, fmt.Errorf("failed to read image body: %w", err)
		}
		if len(data) > maxImageSize {
			return nil, fmt.Errorf("image exceeds maximum size of %d bytes", maxImageSize)
		}

		upstreamCT := resp.Header.Get("Content-Type")
		path, ct, storeErr := fc.Store(provider, scientificName, data, imageURL, upstreamCT)
		if storeErr != nil {
			return nil, storeErr
		}
		return &downloadResult{path: path, contentType: ct}, nil
	})

	if err != nil {
		return "", "", err
	}
	dr := result.(*downloadResult)
	return dr.path, dr.contentType, nil
}
