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
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpclient"
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
// Clones DefaultTransport (per golang/go#26013) for sane dial/timeout defaults, but
// disables proxy support: this client's security model requires DialContext to dial
// validated IPs directly.
var imageHTTPClient = func() *http.Client {
	transport := httpclient.CloneDefaultTransport()
	// Disable proxy support. CloneDefaultTransport inherits Proxy=ProxyFromEnvironment,
	// but a proxy would defeat the SSRF guard below: the transport would dial the proxy
	// and let it resolve the target, so the target IP never passes through isSafeIP. With
	// a loopback/private proxy (e.g. HTTPS_PROXY=http://127.0.0.1:8080) DialContext would
	// also reject the proxy address itself and break all image downloads.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
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
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}()

// cachedImageUserAgent memoizes the image-download User-Agent once the app version is
// known. It is a pointer rather than a sync.OnceValue because conf.Setting() can return
// nil early: latching a version-less string for the whole process lifetime would send
// weaker identification to Wikimedia than the policy asks for.
var cachedImageUserAgent atomic.Pointer[string]

// currentAppVersion returns the running application version, or "" if settings cannot
// be loaded. conf.Setting() returns nil when the config fails to load, so the nil check
// is required rather than defensive.
func currentAppVersion() string {
	if settings := conf.Setting(); settings != nil {
		return settings.Version
	}
	return ""
}

// imageDownloadUserAgent returns a User-Agent for the image byte download that complies
// with the Wikimedia Foundation User-Agent policy.
//
// upload.wikimedia.org answers 403 to Go's default "Go-http-client/1.1" and to an empty
// User-Agent. Without this header the download failed for every species on every
// request, the disk cache stayed permanently empty, and the media proxy fell back to
// redirecting clients to the external URL, where non-browser clients hit the same 403.
//
// It reuses buildUserAgent so this path and the MediaWiki API path cannot drift apart.
// Note that httpclient's own default User-Agent ("BirdNET-Go", no version, no contact
// URL) does not satisfy the policy and would not lift the 403.
func imageDownloadUserAgent() string {
	if ua := cachedImageUserAgent.Load(); ua != nil {
		return *ua
	}
	version := currentAppVersion()
	ua := buildUserAgent(version)
	// Only memoize once a real version is available.
	if version != "" {
		cachedImageUserAgent.Store(&ua)
	}
	return ua
}

// imageRejectionLogged latches the first permanent image-host rejection so the
// escalated log below is emitted once per process rather than once per download.
var imageRejectionLogged atomic.Bool

// logPermanentImageRejection escalates a 401/403 from the image host to Error, once.
//
// A 401 or 403 is a policy or credential rejection: permanent, and identical on every
// subsequent request. The caller logs download failures at Info deliberately, so that
// transient upstream errors (404s, throttling) do not trip the diagnostics health
// check's elevated-error-count rule. That choice also meant a 100%-failure condition
// such as a User-Agent block was logged at the level chosen for transient faults and so
// never escalated: the cache stayed empty for every species indefinitely and nothing
// said why. Escalating once keeps the transient-noise property while making a blanket
// block visible the first time it happens.
func logPermanentImageRejection(statusCode int, provider, scientificName, imageURL string) {
	if statusCode != http.StatusForbidden && statusCode != http.StatusUnauthorized {
		return
	}
	if !imageRejectionLogged.CompareAndSwap(false, true) {
		return
	}
	GetLogger().Error("Image host rejected the download; this is a permanent condition, not a transient failure",
		logger.String("provider", provider),
		logger.String("species", scientificName),
		logger.Int("status", statusCode),
		logger.String("user_agent", imageDownloadUserAgent()),
		logger.String("url", imageURL),
	)
}

// ImageFileCache manages disk-based image caching organized by provider.
type ImageFileCache struct {
	basePath    string
	downloadSem chan struct{}      // limits concurrent external downloads
	sfGroup     singleflight.Group // deduplicates concurrent fetches for same species
	// httpClient performs the image byte downloads. It defaults to imageHTTPClient,
	// whose DialContext rejects loopback and private IPs as SSRF protection. Tests
	// override it to reach an httptest server, which binds 127.0.0.1 and is therefore
	// unreachable through the production client.
	httpClient *http.Client
}

// NewImageFileCache creates a new ImageFileCache rooted at basePath.
func NewImageFileCache(basePath string) *ImageFileCache {
	return &ImageFileCache{
		basePath:    basePath,
		downloadSem: make(chan struct{}, maxConcurrentDownloads),
		httpClient:  imageHTTPClient,
	}
}

// client returns the HTTP client used for image downloads, falling back to the
// shared SSRF-protected client so a zero-value ImageFileCache never nil-panics.
func (c *ImageFileCache) client() *http.Client {
	if c.httpClient != nil {
		return c.httpClient
	}
	return imageHTTPClient
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

	// Remove stale files with different extensions (e.g. old .png when new file is .jpg).
	for _, oldExt := range knownExtensions {
		if oldExt == ext {
			continue
		}
		stale := filepath.Join(dir, namePrefix+oldExt)
		if err := os.Remove(stale); err == nil {
			log.Debug("Removed stale cached image variant", logger.String("path", stale))
		}
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
// The provided context is used for the HTTP request and semaphore acquisition, enabling cancellation.
// Returns the cached file path and the resolved content type.
func (fc *ImageFileCache) DownloadAndStore(ctx context.Context, provider, scientificName, imageURL string) (filePath, contentType string, err error) {
	key := provider + "/" + normalizeSpeciesName(scientificName)

	result, err, _ := fc.sfGroup.Do(key, func() (any, error) {
		// Acquire semaphore to limit concurrent downloads, respecting context cancellation.
		select {
		case fc.downloadSem <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-fc.downloadSem }()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, http.NoBody) //nolint:gosec // URL comes from trusted DB entries, not user input
		if err != nil {
			return nil, fmt.Errorf("failed to create image request: %w", err)
		}
		// Required: upload.wikimedia.org rejects Go's default User-Agent with 403.
		req.Header.Set("User-Agent", imageDownloadUserAgent())
		resp, err := fc.client().Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to download image: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			logPermanentImageRejection(resp.StatusCode, provider, scientificName, imageURL)
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
