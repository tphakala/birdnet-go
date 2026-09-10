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
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
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

	// imageDownloadBlockDuration is how long the byte-download path stops issuing
	// requests to a provider after it refuses one. Defined AS the MediaWiki API path's
	// User-Agent breaker rather than as a copy of its value, so a single policy block
	// backs both paths off for the same length of time and the two cannot drift.
	imageDownloadBlockDuration = circuitBreakerUserAgentDuration
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

// ImageFileCache manages disk-based image caching organized by provider.
type ImageFileCache struct {
	basePath    string
	downloadSem chan struct{}      // limits concurrent external downloads
	sfGroup     singleflight.Group // deduplicates concurrent fetches for same species
	// httpClient performs the image byte downloads. imageHTTPClient's DialContext
	// rejects loopback and private IPs as SSRF protection, so tests override this to
	// reach an httptest server, which binds 127.0.0.1.
	httpClient *http.Client
	// rejectionLogged suppresses repeat escalations of a permanent host rejection.
	// Scoped per cache and cleared by a successful download, mirroring how
	// wikiMediaProvider.networkErrorLogged is cleared by resetCircuit: a
	// never-reset process-global latch would hide a second block entirely.
	rejectionLogged atomic.Bool
	// blockedUntil holds a per-provider cooldown deadline (provider -> time.Time)
	// after the image host refuses a download. Unlike the MediaWiki API path, which
	// opens a circuit on a policy rejection, the byte-download path had no breaker
	// at all: a blanket 403 meant one guaranteed-failing request per uncached
	// species on every page load, forever, which is the traffic profile that earns
	// a block in the first place.
	blockedUntil sync.Map
}

// ErrImageDownloadBlocked is returned instead of issuing a request while the
// per-provider download cooldown is open.
var ErrImageDownloadBlocked = errors.Newf("image downloads are cooling down after a host rejection").
	Component("imageprovider").
	Category(errors.CategoryNetwork).
	Build()

// downloadBlockedUntil reports the open cooldown deadline for a provider, if any.
func (c *ImageFileCache) downloadBlockedUntil(provider string) (deadline time.Time, open bool) {
	val, ok := c.blockedUntil.Load(provider)
	if !ok {
		return time.Time{}, false
	}
	deadline, ok = val.(time.Time)
	if !ok || !time.Now().Before(deadline) {
		return time.Time{}, false
	}
	return deadline, true
}

// openDownloadCooldown suppresses further downloads for a provider after a refusal.
// Only statuses that indicate the host is refusing us rather than missing one file
// arm it: a 404 is about a single image and must not stop the rest.
func (c *ImageFileCache) openDownloadCooldown(statusCode int, provider string) {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
	default:
		return
	}
	c.blockedUntil.Store(provider, time.Now().Add(imageDownloadBlockDuration))
	GetLogger().Warn("Suppressing image downloads after a host refusal",
		logger.String("provider", provider),
		logger.Int("status", statusCode),
		logger.Duration("cooldown", imageDownloadBlockDuration))
}

// NewImageFileCache creates a new ImageFileCache rooted at basePath.
func NewImageFileCache(basePath string) *ImageFileCache {
	return &ImageFileCache{
		basePath:    basePath,
		downloadSem: make(chan struct{}, maxConcurrentDownloads),
		httpClient:  imageHTTPClient,
	}
}

// logPermanentImageRejection escalates a 401/403 from the image host to Error, at most
// once until the next successful download.
//
// A 401 or 403 is treated as a policy or credential rejection rather than a transient
// fault. Callers log download failures at Info on purpose, so that ordinary upstream
// errors (404s, throttling) do not inflate the diagnostics error count. The side effect
// was that a blanket condition such as a User-Agent block, which fails every species on
// every request, was reported at the level chosen for transient faults and so never
// stood out. Escalating once preserves the low-noise property while making a blanket
// rejection visible.
//
// Note this is a heuristic: Wikimedia's edge also answers 403 for transient bot
// challenges, so a single escalation is a signal to investigate, not proof of a block.
// userAgent must be the value actually sent on the rejected request, not a fresh
// lookup: re-deriving it could report a different string if the memoized value latched
// between the request and the log, which is exactly the wrong thing to do in a
// diagnostic about which User-Agent was refused.
func (c *ImageFileCache) logPermanentImageRejection(statusCode int, provider, scientificName, imageURL, userAgent string) {
	// Narrower than openDownloadCooldown's {401, 403, 429} on purpose: 429 is the
	// host asking us to slow down, which the cooldown handles quietly, while 401
	// and 403 are the host refusing us and are worth one escalated log.
	if statusCode != http.StatusForbidden && statusCode != http.StatusUnauthorized {
		return
	}
	if !c.rejectionLogged.CompareAndSwap(false, true) {
		return
	}
	GetLogger().Error("Image host rejected the download; treating as a permanent condition, not a transient failure",
		logger.String("provider", provider),
		logger.String("species", scientificName),
		logger.Int("status", statusCode),
		logger.String("user_agent", userAgent),
		logger.String("url", imageURL),
	)
}

// cacheFileError wraps a disk-cache filesystem failure, which is the one class
// of failure in this file worth reporting.
//
// A permanently empty image cache used to reach Sentry as nothing at all, since
// every error here was a bare fmt.Errorf and the only signal was a log line.
// The transient paths - DNS, dial, HTTP status, the download cooldown - are
// deliberately NOT built through this: ErrorBuilder.Build reports to telemetry
// whenever reporting is active, so doing so would emit one event per attempt for
// exactly the
// throttling and blanket-refusal conditions that already have a cooldown and a
// once-per-cache escalated log. It would also stack two or three reports on one
// failure, since a wrapped EnhancedError is reported again by each wrap.
func cacheFileError(err error, operation, provider, scientificName string) error {
	return errors.New(err).
		Component("imageprovider").
		Category(errors.CategoryImageCache).
		Context("provider", provider).
		Context("scientific_name", scientificName).
		Context("operation", operation).
		Build()
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
		return "", "", cacheFileError(err, "create_cache_directory", provider, scientificName)
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
		return "", "", cacheFileError(err, "create_temp_file", provider, scientificName)
	}
	tmpPath := tmpFile.Name()
	// Unconditional cleanup rather than one removal per error branch: after a
	// successful rename the path no longer exists and this is a no-op, while a
	// branch added later cannot silently start orphaning temp files.
	defer func() { _ = os.Remove(tmpPath) }()

	if _, writeErr := tmpFile.Write(data); writeErr != nil {
		_ = tmpFile.Close()
		return "", "", cacheFileError(writeErr, "write_temp_file", provider, scientificName)
	}
	if closeErr := tmpFile.Close(); closeErr != nil {
		return "", "", cacheFileError(closeErr, "close_temp_file", provider, scientificName)
	}

	if renameErr := os.Rename(tmpPath, finalPath); renameErr != nil {
		return "", "", cacheFileError(renameErr, "rename_temp_file", provider, scientificName)
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
//
// IMPORTANT: singleflight runs the shared work on the FIRST caller's context, so
// every waiter inherits that caller's cancellation. Only pass a context that outlives
// a single consumer: a detached background context, never a request context. If an
// HTTP handler ever calls this directly again, one browser aborting a thumbnail
// cancels the download for every other waiter on the same species.
func (fc *ImageFileCache) DownloadAndStore(ctx context.Context, provider, scientificName, imageURL string) (filePath, contentType string, err error) {
	key := provider + "/" + normalizeSpeciesName(scientificName)

	result, err, _ := fc.sfGroup.Do(key, func() (any, error) {
		// A refused host stays refused for the cooldown. Checked before anything
		// else so a blanket rejection costs neither a request nor a semaphore slot.
		if deadline, open := fc.downloadBlockedUntil(provider); open {
			return nil, fmt.Errorf("%w: retry after %s", ErrImageDownloadBlocked, deadline.UTC().Format(time.RFC3339))
		}

		// Build the User-Agent before taking a semaphore slot. It can reach
		// conf.Setting(), whose slow path takes a package-global lock and can read from
		// disk, and doing that while holding one of only five download slots would
		// serialize unrelated downloads behind it.
		userAgent := appUserAgent()

		// An already-cancelled context must not acquire a slot and issue a request.
		// With a free semaphore both select cases are ready and the choice is random,
		// so check first rather than relying on the select.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}

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
		// Required: Wikimedia rejects Go's default User-Agent with 403.
		req.Header.Set("User-Agent", userAgent)
		resp, err := fc.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to download image: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			fc.logPermanentImageRejection(resp.StatusCode, provider, scientificName, imageURL, userAgent)
			fc.openDownloadCooldown(resp.StatusCode, provider)
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

		// A success means any earlier rejection is over, so re-arm the escalated log
		// and lift the cooldown rather than waiting out its remaining time.
		fc.rejectionLogged.Store(false)
		fc.blockedUntil.Delete(provider)

		return &downloadResult{path: path, contentType: ct}, nil
	})

	if err != nil {
		return "", "", err
	}
	dr, ok := result.(*downloadResult)
	if !ok {
		return "", "", fmt.Errorf("unexpected download result type %T", result)
	}
	return dr.path, dr.contentType, nil
}
