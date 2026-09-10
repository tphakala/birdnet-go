// wikipedia.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/httpclient"
	"github.com/tphakala/birdnet-go/internal/logger"
	"golang.org/x/net/html"
	"golang.org/x/time/rate"
)

const (
	wikiProviderName = "wikimedia"
	wikipediaAPIURL  = "https://en.wikipedia.org/w/api.php"

	// Circuit breaker timeout durations
	circuitBreakerRateLimitDuration      = 60 * time.Second // Rate limit circuit breaker duration
	circuitBreakerBlockedDuration        = 5 * time.Minute  // Access blocked circuit breaker duration
	circuitBreakerUserAgentDuration      = 10 * time.Minute // User-Agent violation circuit breaker duration
	circuitBreakerServiceUnavailDuration = 30 * time.Second // Service unavailable circuit breaker duration
	circuitBreakerNetworkDuration        = 2 * time.Minute  // Network/DNS failure circuit breaker duration

	// HTTP client configuration
	httpClientTimeout         = 30 * time.Second
	httpClientIdleConnTimeout = 90 * time.Second
	httpClientTLSTimeout      = 10 * time.Second
	httpClientMaxIdleConns    = 10

	// Rate limiting configuration
	globalRateLimitPerSecond     = 1 // Requests per second for global rate limiter
	backgroundRateLimitPerSecond = 1 // Requests per second for background operations

	// Retry and delay configuration
	defaultMaxRetries = 3
	retryMinDelay     = 2 * time.Second
	// reasonSettingsUnavailable is the refusal reason recorded when the
	// configuration itself could not be read, as distinct from a policy choice.
	reasonSettingsUnavailable = "settings unavailable"

	configWaitTimeout   = 10 * time.Second
	configCheckInterval = 100 * time.Millisecond
	// lazyInitRetryInterval bounds how often a failed lazy initialization is
	// retried, so a provider that lost the race with config loading at boot
	// recovers instead of staying dead for the process lifetime.
	lazyInitRetryInterval = 1 * time.Minute

	// Response body size limits
	responseBodyPreviewLimit = 200 // Bytes to show in error messages
	responseBodyDebugLimit   = 500 // Bytes to show in debug logs

	// Request ID configuration
	requestIDLength = 8 // Length of UUID prefix used for request tracking

	// Metadata fallback values
	unknownMetadataValue = "Unknown" // Default value when author/license metadata is unavailable

	// Error detection strings (lowercase for case-insensitive comparison)
	errorStringUserAgent   = "user-agent"
	errorStringRobotPolicy = "robot policy"
	errorStringBlocked     = "blocked"
	errorStringBanned      = "banned"
	errorStringDenied      = "denied"
	errorStringHTMLDoctype = "<!DOCTYPE"
	errorStringHTMLTag     = "<html"

	// Rate-limit phrases, matched against a lowercased response body.
	//
	// These used to be the bare tokens "rate" and "limit", which also match
	// ordinary prose in an HTML error page: "corporate", "moderate",
	// "unlimited". A hard block whose page happened to contain any of those was
	// classified as a throttle and got the 60s breaker instead of the 5-minute
	// block breaker, so the app resumed hammering a host that had just refused
	// it. Match phrases, not fragments of unrelated words.
	errorStringRateLimit   = "rate limit"        // prose form
	errorStringRateLimited = "ratelimit"         // MediaWiki API error code "ratelimited"
	errorStringTooManyReqs = "too many requests" // HTTP 429 prose
	errorStringThrottle    = "throttl"
)

// mentionsRateLimit reports whether a lowercased response body names a rate
// limit. See the errorString* block above for why this is phrase-based.
func mentionsRateLimit(bodyLower string) bool {
	return strings.Contains(bodyLower, errorStringRateLimit) ||
		strings.Contains(bodyLower, errorStringRateLimited) ||
		strings.Contains(bodyLower, errorStringTooManyReqs) ||
		strings.Contains(bodyLower, errorStringThrottle)
}

// classifyForbiddenBody classifies the body of an HTTP 403 from the Wikimedia
// edge. Both the circuit-breaker path (handleForbiddenError) and the
// error-reporting path (detectWikipediaErrorType) need this verdict, and they
// used to derive it independently with two copies of the same substring checks.
func classifyForbiddenBody(bodyLower string) wikiErrorType {
	switch {
	case strings.Contains(bodyLower, errorStringUserAgent) || strings.Contains(bodyLower, errorStringRobotPolicy):
		return wikiErrorUserAgent
	case mentionsRateLimit(bodyLower):
		return wikiErrorRateLimit
	default:
		return wikiErrorBlocked
	}
}

// wikiMediaProvider implements the ImageProvider interface for Wikipedia.
type wikiMediaProvider struct {
	httpClient *http.Client // Standard HTTP client for API requests
	// apiURL is the MediaWiki endpoint. It exists so tests can point the provider at
	// an httptest server: without a seam, every HTTP path in this file (retry loop,
	// circuit breaker, response classification) could only be exercised against the
	// live Wikipedia API, which is why that coverage was deferred.
	apiURL            string
	debug             bool
	globalLimiter     *rate.Limiter // Global rate limiter for ALL Wikipedia requests (1 req/sec)
	backgroundLimiter *rate.Limiter // Additional limiter for background operations
	maxRetries        int

	// Circuit breaker fields to prevent hammering when rate limited
	circuitMu        sync.RWMutex
	circuitOpenUntil time.Time // When the circuit breaker can be closed again
	circuitFailures  int       // Number of consecutive failures
	circuitLastError string    // Last error message for logging

	// Track whether a network error has been logged at Error level to avoid log spam
	networkErrorLogged atomic.Bool
}

// wikiMediaAuthor represents the author information for a Wikipedia image.
type wikiMediaAuthor struct {
	name        string
	URL         string
	licenseName string
	licenseURL  string
}

// wikiAPIResponse models the subset of the MediaWiki API JSON response that
// this package consumes (formatversion=2). Unknown fields are ignored by
// encoding/json. The raw field retains the original response bytes for
// diagnostic logging and is not populated by unmarshalling.
type wikiAPIResponse struct {
	Query *wikiQuery    `json:"query"`
	Error *wikiAPIError `json:"error"`
	raw   []byte        `json:"-"`
}

// wikiAPIError models a MediaWiki structured error object.
type wikiAPIError struct {
	Code string `json:"code"`
	Info string `json:"info"`
}

// wikiQuery models the "query" object of a MediaWiki response. Normalized is
// retained as raw messages because only its count is used, for diagnostic
// logging; Redirects is typed because the redirect target decides whether the
// answered page is still about the species we asked for.
type wikiQuery struct {
	Pages      []wikiPage        `json:"pages"`
	Redirects  []wikiRedirect    `json:"redirects"`
	Normalized []json.RawMessage `json:"normalized"`
}

// wikiRedirect models one hop of the "redirects" array: the title we asked for
// and the title the API answered with.
type wikiRedirect struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// wikiPage models a single page entry from a MediaWiki query response.
type wikiPage struct {
	Title string `json:"title"`
	// Missing is how formatversion=2 reports a title that has no article, and
	// modelling it gives that case a first-class verdict instead of inferring it
	// further down from an absent thumbnail. The separate defect it is often
	// confused with, a nil query object being read as "no such species" when the
	// API also produces one for a structured error on HTTP 200 (ratelimited,
	// maxlag, readonly), is fixed by the explicit error branch in
	// queryAndGetFirstPageWithLimiter, not by this field.
	Missing   bool            `json:"missing"`
	Thumbnail *wikiThumbnail  `json:"thumbnail"`
	PageImage string          `json:"pageimage"`
	ImageInfo []wikiImageInfo `json:"imageinfo"`
}

// wikiThumbnail models the pageimages thumbnail object.
type wikiThumbnail struct {
	Source string `json:"source"`
}

// wikiImageInfo models an imageinfo entry carrying extended metadata.
type wikiImageInfo struct {
	ExtMetadata map[string]wikiExtMetaValue `json:"extmetadata"`
}

// wikiExtMetaValue models a single extmetadata field, which wraps its payload
// in a "value" key. Value is typed any because MediaWiki extmetadata mixes
// value types: most fields are strings, but some (e.g. CommonsMetadataExtension)
// carry a JSON number. A string-typed field would make encoding/json reject the
// entire response. Read string fields via extMetaString.
type wikiExtMetaValue struct {
	Value any `json:"value"`
}

// extMetaString returns the named extmetadata field's value as a string, or ""
// if the key is absent or its value is not a string. This mirrors the tolerant
// behavior of the previous jason-based lookups.
func extMetaString(ext map[string]wikiExtMetaValue, key string) string {
	s, _ := ext[key].Value.(string)
	return s
}

// isCircuitOpen checks if the circuit breaker is open (blocking requests)
func (l *wikiMediaProvider) isCircuitOpen() (open bool, reason string) {
	l.circuitMu.RLock()
	defer l.circuitMu.RUnlock()

	if time.Now().Before(l.circuitOpenUntil) {
		return true, l.circuitLastError
	}
	return false, ""
}

// openCircuit opens the circuit breaker for a specified duration
func (l *wikiMediaProvider) openCircuit(duration time.Duration, errorMsg string) {
	l.circuitMu.Lock()
	defer l.circuitMu.Unlock()

	l.circuitOpenUntil = time.Now().Add(duration)
	l.circuitFailures++
	l.circuitLastError = errorMsg

	GetLogger().Error("Opening Wikipedia circuit breaker",
		logger.String("provider", wikiProviderName),
		logger.Duration("duration", duration),
		logger.String("reason", errorMsg),
		logger.Int("consecutive_failures", l.circuitFailures))
}

// resetCircuit resets the circuit breaker on successful request
func (l *wikiMediaProvider) resetCircuit() {
	l.circuitMu.Lock()
	defer l.circuitMu.Unlock()

	if l.circuitFailures > 0 {
		GetLogger().Info("Resetting Wikipedia circuit breaker after successful request",
			logger.String("provider", wikiProviderName),
			logger.Int("previous_failures", l.circuitFailures))
	}

	l.circuitOpenUntil = time.Time{}
	l.circuitFailures = 0
	l.circuitLastError = ""
	l.networkErrorLogged.Store(false)
}

// makeAPIRequest performs a direct HTTP GET request to Wikipedia API with proper headers.
// This replaces the mwclient library to ensure proper User-Agent header handling.
// The context is used for rate limiting, cancellation, and deadlines.
func (l *wikiMediaProvider) makeAPIRequest(ctx context.Context, params map[string]string) (*wikiAPIResponse, error) {
	if err := l.waitForGlobalRateLimit(ctx); err != nil {
		return nil, err
	}

	fullURL, err := l.buildRequestURL(params)
	if err != nil {
		return nil, err
	}

	req, err := l.createHTTPRequest(ctx, fullURL)
	if err != nil {
		return nil, err
	}

	body, statusCode, contentType, err := l.executeHTTPRequest(req)
	if err != nil {
		return nil, err
	}

	if statusCode != http.StatusOK {
		return nil, l.handleHTTPStatusError(statusCode, string(body))
	}

	return l.parseJSONResponse(body, statusCode, contentType)
}

// waitForGlobalRateLimit waits for the global rate limiter if configured.
func (l *wikiMediaProvider) waitForGlobalRateLimit(ctx context.Context) error {
	if l.globalLimiter == nil {
		return nil
	}
	if err := l.globalLimiter.Wait(ctx); err != nil {
		return errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("operation", "global_rate_limit_wait").
			Build()
	}
	GetLogger().Debug("Global rate limiter wait completed", logger.String("provider", wikiProviderName))
	return nil
}

// buildRequestURL constructs the full API URL with query parameters.
func (l *wikiMediaProvider) buildRequestURL(params map[string]string) (string, error) {
	u, err := url.Parse(l.endpoint())
	if err != nil {
		return "", errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("operation", "parse_api_url").
			Build()
	}

	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// endpoint returns the MediaWiki API URL this provider talks to, defaulting to
// Wikipedia so a zero-valued provider still behaves as before.
func (l *wikiMediaProvider) endpoint() string {
	if l.apiURL == "" {
		return wikipediaAPIURL
	}
	return l.apiURL
}

// createHTTPRequest creates an HTTP request with proper headers. The context is
// attached so caller cancellation and deadlines are honored by the transport.
func (l *wikiMediaProvider) createHTTPRequest(ctx context.Context, fullURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, http.NoBody)
	if err != nil {
		return nil, errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("operation", "create_request").
			Context("url", fullURL).
			Build()
	}

	// Resolved per request, not latched at construction: a provider built
	// before main.go publishes Version would otherwise send "BirdNETGo/unknown"
	// for the whole process lifetime, while the image-download path self-heals.
	userAgent := appUserAgent()
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	GetLogger().Debug("Setting User-Agent for Wikipedia API request",
		logger.String("provider", wikiProviderName),
		logger.String("user_agent", userAgent),
		logger.Int("user_agent_length", len(userAgent)),
		logger.String("url", fullURL))

	if l.debug {
		GetLogger().Debug("Full request headers",
			logger.String("provider", wikiProviderName),
			logger.String("headers", fmt.Sprintf("%v", req.Header)))
	}

	return req, nil
}

// executeHTTPRequest executes the HTTP request and returns the body and status code.
func (l *wikiMediaProvider) executeHTTPRequest(req *http.Request) (body []byte, statusCode int, contentType string, err error) {
	resp, err := l.httpClient.Do(req)
	if err != nil {
		return nil, 0, "", errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("operation", "http_request").
			Context("url", req.URL.String()).
			Build()
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil && l.debug {
			GetLogger().Debug("Failed to close response body", logger.Error(closeErr))
		}
	}()

	contentType = resp.Header.Get("Content-Type")

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, contentType, errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("operation", "read_response").
			Context("status_code", resp.StatusCode).
			Build()
	}

	return body, resp.StatusCode, contentType, nil
}

// handleHTTPStatusError processes non-200 HTTP status codes.
func (l *wikiMediaProvider) handleHTTPStatusError(statusCode int, bodyStr string) error {
	GetLogger().Warn("Wikipedia API error response",
		logger.String("provider", wikiProviderName),
		logger.Int("status_code", statusCode),
		logger.String("body", bodyStr))

	l.handleCircuitBreaker(statusCode, bodyStr)

	truncatedBody := truncateResponseBody(bodyStr, responseBodyPreviewLimit)
	return errors.Newf("Wikipedia API returned status %d: %s", statusCode, truncatedBody).
		Component("imageprovider").
		Category(errors.CategoryNetwork).
		Context("provider", wikiProviderName).
		Context("operation", "api_error").
		Context("status_code", statusCode).
		Context("response_body", truncatedBody).
		Build()
}

// handleCircuitBreaker opens the circuit breaker based on the error type.
func (l *wikiMediaProvider) handleCircuitBreaker(statusCode int, bodyStr string) {
	switch statusCode {
	case http.StatusForbidden:
		l.handleForbiddenError(bodyStr)
	case http.StatusTooManyRequests:
		l.openCircuit(circuitBreakerRateLimitDuration,
			fmt.Sprintf("Rate limited (HTTP 429): %s", truncateResponseBody(bodyStr, responseBodyPreviewLimit)))
	case http.StatusServiceUnavailable:
		l.openCircuit(circuitBreakerServiceUnavailDuration,
			fmt.Sprintf("Service unavailable (HTTP 503): %s", truncateResponseBody(bodyStr, responseBodyPreviewLimit)))
	}
}

// handleForbiddenError classifies and handles HTTP 403 errors.
func (l *wikiMediaProvider) handleForbiddenError(bodyStr string) {
	truncated := truncateResponseBody(bodyStr, responseBodyPreviewLimit)

	switch classifyForbiddenBody(strings.ToLower(bodyStr)) {
	case wikiErrorUserAgent:
		l.openCircuit(circuitBreakerUserAgentDuration, fmt.Sprintf("User-Agent policy violation (HTTP 403): %s", truncated))
	case wikiErrorRateLimit:
		l.openCircuit(circuitBreakerRateLimitDuration, fmt.Sprintf("Rate limited (HTTP 403): %s", truncated))
	default:
		l.openCircuit(circuitBreakerBlockedDuration, fmt.Sprintf("Access blocked (HTTP 403): %s", truncated))
	}
}

// parseJSONResponse parses the response body as JSON into the typed response.
func (l *wikiMediaProvider) parseJSONResponse(body []byte, statusCode int, contentType string) (*wikiAPIResponse, error) {
	var resp wikiAPIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		// Attach the response that failed to parse. The retry loop classifies it
		// from this copy; re-requesting the same URL to "diagnose" the failure
		// doubled outbound traffic at exactly the moment Wikipedia was throttling.
		return nil, &wikiParseFailureError{
			statusCode:  statusCode,
			contentType: contentType,
			body:        body,
			err:         l.handleJSONParseError(body, err),
		}
	}
	resp.raw = body
	return &resp, nil
}

// wikiParseFailureError carries the unparseable response alongside the parse error so the
// retry loop can classify it (rate limit, block, transient) without issuing a second
// request for the same URL.
type wikiParseFailureError struct {
	statusCode  int
	contentType string
	body        []byte
	err         error
}

func (e *wikiParseFailureError) Error() string { return e.err.Error() }
func (e *wikiParseFailureError) Unwrap() error { return e.err }

// handleJSONParseError handles JSON parsing errors with context.
func (l *wikiMediaProvider) handleJSONParseError(body []byte, parseErr error) error {
	if bytes.Contains(body, []byte(errorStringHTMLDoctype)) || bytes.Contains(body, []byte(errorStringHTMLTag)) {
		return errors.Newf("Wikipedia returned HTML instead of JSON (likely an error page)").
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("operation", "json_parse_html_detected").
			Context("response_preview", truncateResponseBody(string(body), responseBodyPreviewLimit)).
			Build()
	}

	return errors.New(parseErr).
		Component("imageprovider").
		Category(errors.CategoryNetwork).
		Context("provider", wikiProviderName).
		Context("operation", "json_parse").
		Context("response_preview", truncateResponseBody(string(body), responseBodyPreviewLimit)).
		Build()
}

// LazyWikiMediaProvider wraps the actual Wikipedia provider with lazy initialization.
// This ensures the provider is only created when configuration is properly available,
// preventing race conditions during startup where conf.Setting() might return nil
// or have an empty Version field.
type LazyWikiMediaProvider struct {
	// initSem is a one-slot semaphore rather than a sync.Mutex so that
	// acquisition is cancellable. Initialization waits up to configWaitTimeout
	// for the configuration, and a caller with a shorter deadline (the
	// new-species image warm allows 3s) must be able to give up rather than
	// block on a lock for another caller's full wait. Same reason the
	// per-species init lock is a buffered channel.
	initSem  chan struct{}
	provider *wikiMediaProvider
	// lastErr and nextRetry replace a sync.Once. The Once latched the first
	// initialization error for the whole process lifetime, so a config wait that
	// timed out at boot (10s, plausible on a slow Pi) left the provider
	// permanently dead, with no retry and no log after the first line.
	lastErr   error
	nextRetry time.Time
}

// NewLazyWikiMediaProvider creates a new lazy-initialized Wikipedia provider.
// The actual provider creation is deferred until first use.
func NewLazyWikiMediaProvider() *LazyWikiMediaProvider {
	return &LazyWikiMediaProvider{initSem: make(chan struct{}, 1)}
}

// ensureInitialized creates the actual provider on first use, with validation.
// A mutex plus a retry deadline, rather than a sync.Once: the Once latched the
// first failure for the process lifetime (see the struct fields).
func (l *LazyWikiMediaProvider) ensureInitialized(ctx context.Context) (*wikiMediaProvider, error) {
	release, err := l.acquireInit(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	if l.provider != nil {
		return l.provider, nil
	}
	if l.lastErr != nil && time.Now().Before(l.nextRetry) {
		return nil, l.lastErr
	}

	log := GetLogger().With(logger.String("provider", wikiProviderName))

	// Wait for valid configuration (with timeout)
	if !l.waitForValidConfig(ctx, configWaitTimeout) {
		// THIS caller gave up, which says nothing about whether the
		// configuration will ever arrive. Recording it would arm the retry
		// deadline and refuse every other caller for a minute on the strength of
		// one short-lived context: warmSpeciesImage, for instance, allows only
		// 3s. Report the cancellation and leave the provider re-attemptable.
		if err := ctx.Err(); err != nil {
			log.Debug("LazyWikiMediaProvider: initialization abandoned by the caller",
				logger.Error(err))
			return nil, err
		}

		l.failInit(errors.Newf("configuration not available after timeout").
			Component("imageprovider").
			Category(errors.CategoryConfiguration).
			Context("provider", wikiProviderName).
			Context("operation", "lazy_init_timeout").
			Build())
		log.Error("LazyWikiMediaProvider: Configuration not available after timeout",
			logger.Duration("retry_after", lazyInitRetryInterval))
		return nil, l.lastErr
	}

	// Create the actual provider with valid configuration
	provider, err := NewWikiMediaProvider()
	if err != nil {
		l.failInit(err)
		log.Error("LazyWikiMediaProvider: Failed to create provider",
			logger.Error(err),
			logger.Duration("retry_after", lazyInitRetryInterval))
		return nil, err
	}

	l.provider = provider
	l.lastErr = nil
	log.Info("LazyWikiMediaProvider: Successfully initialized provider")
	return provider, nil
}

// acquireInit takes the one-slot init semaphore, abandoning the attempt if the
// caller's context ends first. A zero-valued provider (only tests build one)
// has no semaphore, so it falls back to an uncontended acquire.
func (l *LazyWikiMediaProvider) acquireInit(ctx context.Context) (release func(), err error) {
	if l.initSem == nil {
		return func() {}, nil
	}
	select {
	case l.initSem <- struct{}{}:
		return func() { <-l.initSem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// failInit records an initialization failure and schedules the next attempt.
// Callers must hold the init semaphore.
func (l *LazyWikiMediaProvider) failInit(err error) {
	l.lastErr = err
	l.nextRetry = time.Now().Add(lazyInitRetryInterval)
}

// waitForValidConfig waits until configuration is available with a valid version.
func (l *LazyWikiMediaProvider) waitForValidConfig(ctx context.Context, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(configCheckInterval)
	defer ticker.Stop()

	for {
		settings := conf.Setting()
		if settings != nil && settings.Version != "" {
			GetLogger().Debug("LazyWikiMediaProvider: Valid configuration detected",
				logger.String("provider", wikiProviderName),
				logger.String("version", settings.Version))
			return true
		}
		// Caller first: when both its context and our deadline have expired, the
		// caller giving up is the truthful answer, and it is the one that must
		// not be recorded as a configuration failure.
		if ctx.Err() != nil {
			return false
		}
		if !time.Now().Before(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// Fetch implements the ImageProvider interface with lazy initialization.
//
// A context-free Fetch is treated as a foreground fetch, matching
// wikiMediaProvider.Fetch: it passes the process-global limiter but not the
// more conservative background one.
func (l *LazyWikiMediaProvider) Fetch(scientificName string) (BirdImage, error) {
	return l.FetchWithContext(context.Background(), scientificName)
}

// FetchWithContext implements context-aware fetching with lazy initialization.
func (l *LazyWikiMediaProvider) FetchWithContext(ctx context.Context, scientificName string) (BirdImage, error) {
	// The configuration gate runs before initialization. Initializing first meant
	// a call that policy forbids still paid the config wait and emitted the full
	// "Initializing WikiMedia provider" log block, which is very likely what was
	// reported as Wikimedia activity despite `fallbackpolicy: none`.
	if allowed, reason := wikiFetchAllowed(); !allowed {
		logWikiFetchBlocked(scientificName, reason)
		return BirdImage{}, ErrProviderNotConfigured
	}

	provider, err := l.ensureInitialized(ctx)
	if err != nil {
		return BirdImage{}, err
	}
	return provider.FetchWithContext(ctx, scientificName)
}

// ShouldRefreshCache implements ProviderStatusChecker interface.
// It checks if WikiMedia provider should actively refresh cache based on current configuration,
// without requiring full provider initialization. This allows the provider to be registered
// for UI discovery while preventing unnecessary cache operations when disabled.
func (l *LazyWikiMediaProvider) ShouldRefreshCache() bool {
	// This is deliberately narrower than wikiFetchAllowed, which also permits
	// "auto" and an unset provider because auto may elect WikiMedia. Refreshing
	// is worth doing only when WikiMedia is actually in use, and under auto it
	// is elected only if no other provider registers.
	//
	// What it must never be is BROADER than the fetch gate, or the hourly sweep
	// schedules fetches that are all denied. It used to be, in two ways: it
	// lowercased and trimmed the policy while the fetch side compared verbatim,
	// so `fallbackpolicy: All` enabled the refresh while every fetch it
	// scheduled was rejected; and it accepted a provider name ("wikimedia") as a
	// value of the policy field, which is a category error with the same effect.
	// Both conditions below imply wikiFetchAllowed, by construction.
	if normalizedImageProvider() == wikiProviderName {
		return true
	}
	return normalizedFallbackPolicy() == fallbackPolicyAll
}

// truncateResponseBody truncates a response body string to a specified length for logging.
// This prevents excessive memory usage and log spam when logging error responses.
func truncateResponseBody(body string, maxLength int) string {
	if len(body) <= maxLength {
		return body
	}
	return body[:maxLength] + "..."
}

// htmlToText extracts the visible text content from an HTML fragment or
// document, collapsing all runs of whitespace to single spaces. The contents
// of <script> and <style> elements are ignored. It replaces the former
// github.com/k3a/html2text dependency for the small set of attribution and
// error-page stripping this package needs.
func htmlToText(htmlStr string) string {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		// html.Parse is lenient and effectively never errors on string input;
		// fall back to whitespace-collapsed raw text if it somehow does.
		return strings.Join(strings.Fields(htmlStr), " ")
	}

	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
			sb.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	return strings.Join(strings.Fields(sb.String()), " ")
}

// parseHTMLErrorMessage extracts meaningful error message from HTML error page
func parseHTMLErrorMessage(htmlContent []byte) string {
	// Try to parse HTML to validate it's valid HTML
	_, err := html.Parse(bytes.NewReader(htmlContent))
	if err != nil {
		// Fallback to simple string extraction if HTML parsing fails
		bodyStr := string(htmlContent)
		if idx := strings.Index(bodyStr, "<title>"); idx != -1 {
			if endIdx := strings.Index(bodyStr[idx:], "</title>"); endIdx != -1 {
				return strings.TrimSpace(bodyStr[idx+7 : idx+endIdx])
			}
		}
		return "HTML error page (unable to parse)"
	}

	// Extract text content for valid HTML
	return htmlToText(string(htmlContent))
}

// detectErrorType analyzes response to determine error type and appropriate action
type wikiErrorType int

const (
	wikiErrorUnknown wikiErrorType = iota
	wikiErrorRateLimit
	wikiErrorBlocked
	wikiErrorUserAgent
	wikiErrorTemporary
)

func detectWikipediaErrorType(statusCode int, responseBody []byte, contentType string) (errorType wikiErrorType, message string) {
	// Convert to lowercase once for efficient comparison
	bodyLower := strings.ToLower(string(responseBody))

	// Check status codes first
	switch statusCode {
	case http.StatusTooManyRequests:
		return wikiErrorRateLimit, "Rate limit exceeded (HTTP 429)"
	case http.StatusForbidden:
		switch classifyForbiddenBody(bodyLower) {
		case wikiErrorUserAgent:
			return wikiErrorUserAgent, "User-Agent policy violation"
		case wikiErrorRateLimit:
			return wikiErrorRateLimit, "Rate limit exceeded (403 with rate limit message)"
		default:
			return wikiErrorBlocked, "Access blocked (HTTP 403)"
		}
	case http.StatusServiceUnavailable:
		return wikiErrorTemporary, "Service temporarily unavailable (HTTP 503)"
	}

	// Check content type - HTML responses usually indicate errors
	if strings.Contains(contentType, "text/html") {
		errorMsg := parseHTMLErrorMessage(responseBody)
		errorMsgLower := strings.ToLower(errorMsg)

		// Check for rate limiting keywords in HTML content
		if mentionsRateLimit(errorMsgLower) {
			return wikiErrorRateLimit, "Rate limit detected in HTML response"
		}

		// Check for blocking keywords
		if strings.Contains(errorMsgLower, errorStringBlocked) ||
			strings.Contains(errorMsgLower, errorStringBanned) ||
			strings.Contains(errorMsgLower, errorStringDenied) {
			return wikiErrorBlocked, "Access blocked (detected in HTML)"
		}

		return wikiErrorTemporary, fmt.Sprintf("HTML error response: %s", errorMsg)
	}

	return wikiErrorUnknown, "Unknown error type"
}

// checkUserAgentPolicyViolation checks for Wikipedia user-agent policy violations and returns an error if detected
func checkUserAgentPolicyViolation(reqID string, statusCode int, responseBody []byte, userAgent string) error {
	if statusCode != http.StatusForbidden {
		return nil
	}

	// Single-sourced with the circuit-breaker and error-reporting paths. This
	// used to re-derive the verdict from raw substrings, which its only caller
	// had already obtained from classifyForbiddenBody; if the two ever diverged
	// this returned nil, no permanent failure was reported, and the retry loop
	// kept hammering a host that had just refused the User-Agent.
	bodyStr := string(responseBody)
	if classifyForbiddenBody(strings.ToLower(bodyStr)) != wikiErrorUserAgent {
		return nil
	}

	GetLogger().Error("Wikipedia blocked request - User-Agent policy violation, stopping retries",
		logger.String("provider", wikiProviderName),
		logger.String("request_id", reqID),
		logger.String("error_message", truncateResponseBody(bodyStr, responseBodyPreviewLimit)),
		logger.String("user_agent", userAgent),
		logger.String("policy_url", "https://foundation.wikimedia.org/wiki/Policy:User-Agent_policy"),
		logger.String("action_required", "User-Agent needs to be updated to comply with policy"))

	// This is a permanent failure - return immediately without retrying
	return errors.Newf("Wikipedia user-agent policy violation: %s", truncateResponseBody(bodyStr, responseBodyPreviewLimit)).
		Component("imageprovider").
		Category(errors.CategoryNetwork).
		Context("provider", wikiProviderName).
		Context("request_id", reqID).
		Context("operation", "user_agent_policy_violation").
		Context("status_code", statusCode).
		Context("response_body", truncateResponseBody(bodyStr, responseBodyPreviewLimit)).
		Context("user_agent", userAgent).
		Context("permanent_failure", true).
		Build()
}

// classifyParseFailure diagnoses a response that could not be parsed as JSON and
// decides whether it is a permanent condition that must abort the retry loop.
//
// It classifies the response the caller already has. The previous implementation
// re-issued the same GET to the same URL purely for diagnostics, which doubled
// outbound traffic in precisely the situation the diagnosis was for: Wikipedia
// throttling or blocking us. A returned non-nil error stops the retries.
func (l *wikiMediaProvider) classifyParseFailure(reqID, fullURL string, attempt int, failure *wikiParseFailureError) error {
	log := GetLogger().With(
		logger.String("provider", wikiProviderName),
		logger.String("request_id", reqID),
		logger.Int("attempt", attempt+1),
		logger.Int("max_attempts", l.maxRetries))

	body := failure.body
	errorType, errorMsg := detectWikipediaErrorType(failure.statusCode, body, failure.contentType)

	// Log error details based on severity
	logFields := []logger.Field{
		logger.Int("error_type", int(errorType)),
		logger.String("error_message", errorMsg),
		logger.Int("status_code", failure.statusCode),
		logger.String("content_type", failure.contentType),
		logger.String("requested_url", fullURL),
	}

	switch {
	case errorType == wikiErrorRateLimit || errorType == wikiErrorBlocked || errorType == wikiErrorUserAgent:
		log.Error("Wikipedia API error diagnosed", logFields...)
	case failure.statusCode != http.StatusOK:
		log.Warn("Wikipedia API error diagnosed", logFields...)
	default:
		log.Debug("Wikipedia API error diagnosed", logFields...)
	}

	// Log full response body in debug mode
	if l.debug && len(body) > 0 {
		bodyPreview := truncateResponseBody(string(body), responseBodyDebugLimit)
		log.Debug("Response body preview", logger.String("body", bodyPreview))
	}

	// Handle different error types
	switch errorType {
	case wikiErrorRateLimit:
		// Rate limiting - open circuit breaker
		l.openCircuit(circuitBreakerRateLimitDuration, fmt.Sprintf("Rate limited: %s", errorMsg))
		return errors.Newf("Wikipedia rate limit exceeded: %s", errorMsg).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("request_id", reqID).
			Context("operation", "rate_limit_exceeded").
			Context("status_code", failure.statusCode).
			Context("error_message", errorMsg).
			Context("permanent_failure", true).
			Context("retry_after", "60s"). // Suggest retry after 60 seconds
			Build()

	case wikiErrorBlocked:
		// Access blocked - open circuit breaker
		l.openCircuit(circuitBreakerBlockedDuration, fmt.Sprintf("Access blocked: %s", errorMsg))
		return errors.Newf("Wikipedia access blocked: %s", errorMsg).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("request_id", reqID).
			Context("operation", "access_blocked").
			Context("status_code", failure.statusCode).
			Context("error_message", errorMsg).
			Context("permanent_failure", true).
			Build()

	case wikiErrorUserAgent:
		// User-agent policy violation - open circuit breaker
		l.openCircuit(circuitBreakerUserAgentDuration, "User-Agent policy violation")
		return checkUserAgentPolicyViolation(reqID, failure.statusCode, body, appUserAgent())

	case wikiErrorTemporary:
		// Temporary error - continue with retry logic but with longer backoff
		log.Warn("Temporary Wikipedia error, will retry with backoff",
			logger.String("error_message", errorMsg),
			logger.Bool("will_retry", attempt < l.maxRetries-1))
		return nil

	default:
		// Unknown error - continue with normal retry logic
		return nil
	}
}

// Error categorization for enhanced diagnostics
type apiErrorCategory struct {
	Type        string
	Description string
	Severity    string
	Actionable  bool
}

var errorCategoryNetworkFailure = apiErrorCategory{
	Type:        "network_failure",
	Description: "Network connectivity or Wikipedia API unavailable",
	Severity:    "high",
	Actionable:  true,
}

// logAPIError logs API errors with enhanced diagnostics and categorization
func logAPIError(category apiErrorCategory, reqID, species string, err error) {
	GetLogger().Error("Wikipedia API error - categorized for diagnostics",
		logger.String("provider", wikiProviderName),
		logger.String("error_category", category.Type),
		logger.String("error_description", category.Description),
		logger.String("error_severity", category.Severity),
		logger.Bool("actionable", category.Actionable),
		logger.String("request_id", reqID),
		logger.String("species_query", species),
		logger.String("original_error", err.Error()),
		logger.String("troubleshooting_hint", getTroubleshootingHint(category)))
}

// logNetworkError logs network errors, downgrading to debug level after the first occurrence
// to avoid log spam when DNS/network is persistently broken.
func (l *wikiMediaProvider) logNetworkError(category apiErrorCategory, reqID, species string, err error) {
	fields := []logger.Field{
		logger.String("provider", wikiProviderName),
		logger.String("error_category", category.Type),
		logger.String("error_description", category.Description),
		logger.String("error_severity", category.Severity),
		logger.Bool("actionable", category.Actionable),
		logger.String("request_id", reqID),
		logger.String("species_query", species),
		logger.String("original_error", err.Error()),
		logger.String("troubleshooting_hint", getTroubleshootingHint(category)),
	}

	if l.networkErrorLogged.CompareAndSwap(false, true) {
		GetLogger().Error("Wikipedia API error - categorized for diagnostics", fields...)
	} else {
		GetLogger().Debug("Wikipedia API error (repeated, suppressed)", fields...)
	}
}

// getTroubleshootingHint provides actionable troubleshooting advice based on error category
func getTroubleshootingHint(category apiErrorCategory) string {
	switch category.Type {
	case "network_failure":
		return "Check network connectivity and Wikipedia API status. Consider implementing backoff or fallback providers."
	default:
		return "Review error details and consider checking Wikipedia API documentation for changes."
	}
}

// logAPISuccess logs successful API operations for baseline metrics
func logAPISuccess(reqID, species, operation string) {
	GetLogger().Debug("Wikipedia API success - operation completed normally",
		logger.String("provider", wikiProviderName),
		logger.Bool("success", true),
		logger.String("request_id", reqID),
		logger.String("species_query", species),
		logger.String("operation", operation),
		logger.String("diagnostic_info", "normal_successful_operation_for_baseline_metrics"))
}

// NewWikiMediaProvider creates a new Wikipedia media provider.
// It initializes a standard HTTP client for interacting with the Wikipedia API.
func NewWikiMediaProvider() (*wikiMediaProvider, error) {
	log := GetLogger().With(logger.String("provider", wikiProviderName))
	log.Info("Initializing WikiMedia provider")
	debug := thumbnailSettings().Debug
	appVersion := currentAppVersion()

	// Log debug mode if configured
	if debug {
		log.Info("Debug mode enabled for WikiMedia provider",
			logger.Bool("debug", true))
	}

	// Log the user-agent. It is resolved per request rather than stored here, so
	// this is a diagnostic snapshot rather than the value that will be sent.
	userAgent := buildUserAgent(appVersion)
	logUserAgentValidation(appVersion)
	log.Info("WikiMedia provider initialization - user-agent constructed",
		logger.String("user_agent", userAgent),
		logger.String("app_version", appVersion))

	// Clone DefaultTransport (per golang/go#26013) to inherit proxy support and
	// dial timeouts, then override pool/timeout settings for Wikipedia API usage.
	transport := httpclient.CloneDefaultTransport()
	transport.MaxIdleConns = httpClientMaxIdleConns
	transport.IdleConnTimeout = httpClientIdleConnTimeout
	transport.TLSHandshakeTimeout = httpClientTLSTimeout
	httpClient := &http.Client{
		Timeout:   httpClientTimeout,
		Transport: transport,
	}

	// Global rate limiting for ALL Wikipedia requests to respect their API limits
	// Wikipedia prefers conservative request rates
	globalLimiter := rate.NewLimiter(rate.Limit(globalRateLimitPerSecond), globalRateLimitPerSecond)

	// Additional rate limiting for background cache refresh operations
	backgroundLimiter := rate.NewLimiter(rate.Limit(backgroundRateLimitPerSecond), backgroundRateLimitPerSecond)

	log.Info("WikiMedia provider initialized with conservative rate limits",
		logger.Int("global_rate_limit_rps", 1),
		logger.Int("background_rate_limit_rps", 1),
		logger.String("http_timeout", "30s"),
		logger.String("info", "All requests limited to 1/sec to respect Wikipedia API"))

	return &wikiMediaProvider{
		httpClient:        httpClient,
		apiURL:            wikipediaAPIURL,
		debug:             debug,
		globalLimiter:     globalLimiter,
		backgroundLimiter: backgroundLimiter,
		maxRetries:        defaultMaxRetries,
	}, nil
}

// checkCircuitBreaker checks if the circuit breaker is open and returns an error if so.
func (l *wikiMediaProvider) checkCircuitBreaker(reqID string, params map[string]string) error {
	if open, reason := l.isCircuitOpen(); open {
		GetLogger().Warn("Wikipedia circuit breaker is open, rejecting request",
			logger.String("provider", wikiProviderName),
			logger.String("request_id", reqID),
			logger.String("species", params["titles"]),
			logger.String("reason", reason))
		return errors.Newf("Wikipedia API circuit breaker open: %s", reason).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("request_id", reqID).
			Context("circuit_breaker", "open").
			Context("reason", reason).
			Build()
	}
	return nil
}

// waitForRateLimiterRetry waits for the rate limiter and returns an error if the wait fails.
func (l *wikiMediaProvider) waitForRateLimiterRetry(ctx context.Context, limiter *rate.Limiter, reqID string) error {
	log := GetLogger().With(
		logger.String("provider", wikiProviderName),
		logger.String("request_id", reqID))

	if limiter == nil {
		log.Debug("No rate limiting applied (user request)")
		return nil
	}

	log.Debug("Waiting for rate limiter")
	if err := limiter.Wait(ctx); err != nil {
		enhancedErr := errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("request_id", reqID).
			Context("operation", "rate_limiter_wait").
			Build()
		log.Error("Rate limiter error", logger.Error(enhancedErr))
		return enhancedErr
	}
	return nil
}

// logSuccessfulAPIResponse logs the successful API response details.
func logSuccessfulAPIResponse(resp *wikiAPIResponse) {
	GetLogger().With(logger.String("provider", wikiProviderName)).
		Debug("API request successful - raw response received",
			logger.String("response_preview", truncateResponseBody(string(resp.raw), responseBodyDebugLimit)),
			logger.Int("response_size", len(resp.raw)))
}

// handleJSONParsingErrorIfNeeded classifies a JSON parse failure and returns a
// non-nil error when the response indicates a permanent condition that retrying would
// only make worse.
//
// The gate is the failure type rather than substrings of the error message: matching
// on "invalid character" and "looking for beginning of value" silently stopped
// applying whenever the wrapping text changed.
func (l *wikiMediaProvider) handleJSONParsingErrorIfNeeded(err error, reqID, fullURL string, attempt int) error {
	var failure *wikiParseFailureError
	if !errors.As(err, &failure) {
		return nil
	}
	return l.classifyParseFailure(reqID, fullURL, attempt, failure)
}

// calculateRetryDelay calculates the delay before the next retry using exponential backoff.
func calculateRetryDelay(attempt int) time.Duration {
	exponentialDelay := time.Second * time.Duration(1<<attempt)
	return max(retryMinDelay, exponentialDelay)
}

// buildRetryExhaustedError builds the enhanced error when all retries are exhausted.
func buildRetryExhaustedError(lastErr error, reqID string, params map[string]string, maxRetries int) error {
	return errors.New(lastErr).
		Component("imageprovider").
		Category(errors.CategoryNetwork).
		Context("provider", wikiProviderName).
		Context("request_id", reqID).
		Context("max_retries", maxRetries).
		Context("operation", "query_with_retry").
		Context("api_action", params["action"]).
		Context("species_query", params["titles"]).
		Context("error_category", errorCategoryNetworkFailure.Type).
		Context("error_severity", errorCategoryNetworkFailure.Severity).
		Context("actionable", errorCategoryNetworkFailure.Actionable).
		Context("final_error", lastErr.Error()).
		Build()
}

// isNetworkError checks if an error is a network-level failure (DNS, dial, connection refused).
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	var netErr *net.OpError
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) || errors.As(err, &netErr) {
		return true
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, "dial tcp") ||
		strings.Contains(errMsg, "no such host") ||
		strings.Contains(errMsg, "connection refused")
}

// queryWithRetryAndLimiter performs a query with retry logic using the specified rate limiter.
// The context is used for cancellation, deadlines, and rate limiting.
func (l *wikiMediaProvider) queryWithRetryAndLimiter(ctx context.Context, reqID string, params map[string]string, limiter *rate.Limiter) (*wikiAPIResponse, error) {
	log := GetLogger().With(
		logger.String("provider", wikiProviderName),
		logger.String("request_id", reqID),
		logger.String("api_action", params["action"]))

	// A non-positive maxRetries would skip the loop entirely and leave lastErr nil,
	// which the post-loop error construction dereferences.
	if l.maxRetries <= 0 {
		return nil, errors.Newf("invalid maxRetries %d for Wikipedia query", l.maxRetries).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("request_id", reqID).
			Build()
	}

	if err := l.checkCircuitBreaker(reqID, params); err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := range l.maxRetries {
		log.Debug("Attempting Wikipedia API request",
			logger.Int("attempt", attempt+1),
			logger.Int("max_attempts", l.maxRetries),
			logger.String("species", params["titles"]))

		if err := l.waitForRateLimiterRetry(ctx, limiter, reqID); err != nil {
			return nil, err
		}

		log.Debug("Sending GET request to Wikipedia API",
			logger.Int("attempt", attempt+1))
		resp, err := l.makeAPIRequest(ctx, params)
		if err == nil {
			logSuccessfulAPIResponse(resp)
			l.resetCircuit()
			return resp, nil
		}

		fullURL := l.buildDebugURL(params)
		if policyErr := l.handleJSONParsingErrorIfNeeded(err, reqID, fullURL, attempt); policyErr != nil {
			return nil, policyErr
		}

		lastErr = err
		log.Warn("API request failed",
			logger.Error(err),
			logger.String("attempted_url", fullURL),
			logger.Int("attempt", attempt+1),
			logger.Bool("will_retry", attempt < l.maxRetries-1))

		// This failure may itself have opened the circuit: handleCircuitBreaker does so
		// for 403, 429 and 503. Check before sleeping, so a refused endpoint costs
		// neither another request nor the backoff that would precede it. Checking only
		// at the top of the next iteration still burned the full delay first.
		//
		// This runs on the final attempt too, not just before a retry. The breaker
		// error deliberately keeps its own message because telemetry suppression
		// matches on it (internal/errors/telemetry_integration.go); returning the
		// retry-exhausted error instead, purely because the circuit happened to open on
		// the last attempt rather than an earlier one, would report a throttle as a
		// novel failure.
		if cbErr := l.checkCircuitBreaker(reqID, params); cbErr != nil {
			// Log the failure that tripped it, so the cause is not lost along with the
			// retry-exhausted path's diagnostics.
			log.Warn("Abandoning retries because the circuit breaker opened",
				logger.Error(lastErr),
				logger.Int("attempts_made", attempt+1))
			return nil, cbErr
		}

		// Nothing follows the final attempt, so the backoff has nothing to wait for.
		if attempt == l.maxRetries-1 {
			break
		}

		waitDuration := calculateRetryDelay(attempt)
		log.Debug("Waiting before retry", logger.Duration("duration", waitDuration))
		timer := time.NewTimer(waitDuration)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	if isNetworkError(lastErr) {
		l.openCircuit(circuitBreakerNetworkDuration,
			fmt.Sprintf("Network/DNS failure: %s", lastErr.Error()))
		l.logNetworkError(errorCategoryNetworkFailure, reqID, params["titles"], lastErr)
	} else {
		logAPIError(errorCategoryNetworkFailure, reqID, params["titles"], lastErr)
	}

	return nil, buildRetryExhaustedError(lastErr, reqID, params, l.maxRetries)
}

// buildDebugURL constructs a URL string for debug logging purposes.
func (l *wikiMediaProvider) buildDebugURL(params map[string]string) string {
	queryParams := make([]string, 0, len(params))
	for k, v := range params {
		queryParams = append(queryParams, k+"="+url.QueryEscape(v))
	}
	return l.endpoint() + "?" + strings.Join(queryParams, "&")
}

// logRawResponse logs the raw API response at debug level for troubleshooting.
func logRawResponse(resp *wikiAPIResponse, fullURL string) {
	GetLogger().With(logger.String("provider", wikiProviderName)).
		Debug("Raw Wikipedia API response received",
			logger.String("response_full", string(resp.raw)),
			logger.Int("response_length", len(resp.raw)),
			logger.String("request_url", fullURL))
}

// logQueryMissingError logs diagnostics when the 'query' field is missing from the response.
func logQueryMissingError(resp *wikiAPIResponse, params map[string]string, fullURL string) {
	log := GetLogger().With(logger.String("provider", wikiProviderName))
	log.Debug("Wikipedia response missing 'query' field - full response dump",
		logger.String("raw_response", string(resp.raw)),
		logger.String("request_url", fullURL))

	if resp.Error != nil {
		log.Debug("Wikipedia API returned structured error response - normal for missing pages",
			logger.String("error_code", resp.Error.Code),
			logger.String("error_info", resp.Error.Info),
			logger.String("error_type", "api_structured_error_expected"),
			logger.String("species_query", params["titles"]),
			logger.String("diagnostic_hint", "wikipedia_api_rejected_request_for_nonexistent_page"))
		return
	}
	log.Debug("Wikipedia response has no 'query' field and no structured 'error' field",
		logger.String("error_type", "malformed_api_response_expected"),
		logger.String("species_query", params["titles"]),
		logger.String("diagnostic_hint", "wikipedia_api_returned_unexpected_format_for_missing_page"))
}

// logNoPages logs diagnostics when the query returned no pages. With typed
// decoding a missing 'pages' key and an empty 'pages' array are
// indistinguishable, so the former separate "pages missing" and "pages empty"
// diagnostics are merged here. The user-facing result (ErrImageNotFound) is
// unchanged.
func logNoPages(resp *wikiAPIResponse, params map[string]string, fullURL string) {
	GetLogger().With(logger.String("provider", wikiProviderName)).
		Debug("Wikipedia query returned no pages - normal for species without pages",
			logger.String("error_type", "no_pages_in_response"),
			logger.String("species_query", params["titles"]),
			logger.String("request_url", fullURL),
			logger.Int("redirect_count", len(resp.Query.Redirects)),
			logger.Int("normalized_count", len(resp.Query.Normalized)),
			logger.String("diagnostic_hint", "species_likely_has_no_wikipedia_page"))
}

// logFirstPageContent logs the first page content at debug level for troubleshooting.
func logFirstPageContent(page *wikiPage, fullURL string) {
	log := GetLogger().With(logger.String("provider", wikiProviderName))
	data, err := json.Marshal(page)
	if err != nil {
		log.Debug("Could not format first page for logging",
			logger.Error(err),
			logger.String("request_url", fullURL))
		return
	}
	log.Debug("First page content from API response",
		logger.String("page_content", string(data)),
		logger.String("request_url", fullURL))
}

// queryAndGetFirstPageWithLimiter queries Wikipedia with given parameters using the specified rate limiter.
func (l *wikiMediaProvider) queryAndGetFirstPageWithLimiter(ctx context.Context, reqID string, params map[string]string, limiter *rate.Limiter) (page *wikiPage, redirects []wikiRedirect, err error) {
	log := GetLogger().With(
		logger.String("provider", wikiProviderName),
		logger.String("request_id", reqID),
		logger.String("api_action", params["action"]),
		logger.String("titles", params["titles"]))

	fullURL := l.buildDebugURL(params)
	log.Debug("Querying Wikipedia API", logger.String("debug_full_url", fullURL))

	resp, err := l.queryWithRetryAndLimiter(ctx, reqID, params, limiter)
	if err != nil {
		return nil, nil, err
	}

	logRawResponse(resp, fullURL)
	log.Debug("Parsing pages from API response")

	// A structured error object means the API refused the request, not that the
	// species has no page. MediaWiki returns these with HTTP 200 for
	// ratelimited, maxlag and readonly, so classifying them as "not found" is
	// what let a Wikipedia throttling window persist as durable __NOT_FOUND__
	// rows for every species queried during it.
	if resp.Error != nil {
		logQueryMissingError(resp, params, fullURL)
		return nil, nil, l.wikiAPIErrorFor(resp.Error, params["titles"], reqID)
	}

	if resp.Query == nil {
		// With formatversion=2 an absent page is reported as
		// pages:[{missing:true}], so a nil query with no error object is a
		// malformed response rather than a missing species. Report it as such:
		// a not-found verdict here would be cached as "this species has no
		// image" for the full negative TTL.
		logQueryMissingError(resp, params, fullURL)
		return nil, nil, errors.Newf("Wikipedia response contained neither a query nor an error object").
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("request_id", reqID).
			Context("scientific_name", params["titles"]).
			Context("operation", "wiki_query_missing").
			Build()
	}

	if len(resp.Query.Pages) == 0 {
		logNoPages(resp, params, fullURL)
		return nil, nil, imageNotFoundFor(params["titles"], wikiProviderName, "wiki_pages_empty")
	}

	first := &resp.Query.Pages[0]
	if first.Missing {
		log.Debug("Wikipedia reports no such page", logger.String("title", first.Title))
		return nil, nil, imageNotFoundFor(params["titles"], wikiProviderName, "wiki_page_missing")
	}

	logFirstPageContent(first, fullURL)
	logAPISuccess(reqID, params["titles"], "get_first_page")

	return first, resp.Query.Redirects, nil
}

// wikiAPIErrorFor converts a MediaWiki structured error object into an error.
//
// Only codes meaning "this title can never resolve" are reported as
// ErrImageNotFound, because that is the verdict the cache persists as a
// negative entry. Everything else is a provider-side failure that must not be
// recorded as "this species has no image". A rate-limit code additionally opens
// the circuit breaker: these arrive with HTTP 200, so the status-code path that
// normally trips the breaker never sees them.
func (l *wikiMediaProvider) wikiAPIErrorFor(apiErr *wikiAPIError, title, reqID string) error {
	code := strings.ToLower(strings.TrimSpace(apiErr.Code))

	switch code {
	case "invalidtitle", "missingtitle", "nosuchpageid":
		return imageNotFoundFor(title, wikiProviderName, "wiki_api_"+code)
	}

	info := truncateResponseBody(apiErr.Info, responseBodyPreviewLimit)
	if mentionsRateLimit(code) || mentionsRateLimit(strings.ToLower(apiErr.Info)) {
		l.openCircuit(circuitBreakerRateLimitDuration,
			fmt.Sprintf("Rate limited (API error %s): %s", apiErr.Code, info))
	}

	return errors.Newf("Wikipedia API error %s: %s", apiErr.Code, info).
		Component("imageprovider").
		Category(errors.CategoryNetwork).
		Context("provider", wikiProviderName).
		Context("request_id", reqID).
		Context("scientific_name", title).
		Context("api_error_code", apiErr.Code).
		Context("operation", "wiki_api_error").
		Build()
}

// wikiFetchAllowed reports whether the WikiMedia provider is allowed to make
// requests under the current configuration. This prevents unnecessary API calls
// to Wikipedia when the provider is not configured for use. It is a package
// function rather than a method so the lazy wrapper can consult it before
// paying for initialization.
func wikiFetchAllowed() (allowed bool, reason string) {
	return wikiFetchAllowedFor(conf.Setting())
}

// wikiFetchAllowedFor is wikiFetchAllowed's decision with the settings passed
// in. It exists as a seam: conf.Setting() lazy-loads from disk on a nil global,
// so the nil branch below cannot be reached by clearing the global in a test.
func wikiFetchAllowedFor(settings *conf.Settings) (allowed bool, reason string) {
	if settings == nil {
		// Fail closed. Settings are nil only before the config loads or when
		// loading failed, and permitting outbound Wikipedia traffic in that
		// window contradicts a configured `fallbackpolicy: none`. The sibling
		// refresh gate already failed closed on the same condition.
		return false, reasonSettingsUnavailable
	}

	thumbnails := settings.Realtime.Dashboard.Thumbnails

	// Cases 1 and 2: WikiMedia is the configured provider, or auto mode may
	// select it (it does when no other provider registers).
	switch normalizeProviderName(thumbnails.ImageProvider) {
	case wikiProviderName, providerAuto, "":
		return true, ""
	}

	// Case 3: WikiMedia can be used as a fallback. Read from the settings passed
	// in, not through normalizedFallbackPolicy, which re-reads the global: a
	// seam that decided half its answer from its argument and half from a global
	// would give a caller passing anything else a spliced verdict.
	if normalizeProviderName(thumbnails.FallbackPolicy) == fallbackPolicyAll {
		return true, ""
	}

	// WikiMedia is not configured and fallback is disabled
	reason = fmt.Sprintf("provider=%s, fallback=%s",
		thumbnails.ImageProvider, thumbnails.FallbackPolicy)
	return false, reason
}

// logWikiFetchBlocked records a fetch refused by configuration.
//
// The hint is derived from the reason rather than fixed: the settings-unavailable
// refusal is a config-load failure, not a thumbnail-settings choice, and pointing
// an operator at the thumbnail settings for it would misdirect the one person
// most likely to be reading this line.
func logWikiFetchBlocked(scientificName, reason string) {
	hint := "WikiMedia is not the configured provider and fallback is disabled"
	if reason == reasonSettingsUnavailable {
		hint = "the configuration could not be read; fetches stay disabled until it loads"
	}
	GetLogger().Debug("WikiMedia fetch blocked by configuration",
		logger.String("provider", wikiProviderName),
		logger.String("scientific_name", scientificName),
		logger.String("config_reason", reason),
		logger.String("hint", hint))
}

// FetchWithContext retrieves the bird image for a given scientific name using a context.
// All requests pass through the global 1 req/s limiter; background operations also
// use an additional background-specific limiter for more conservative rate limiting.
func (l *wikiMediaProvider) FetchWithContext(ctx context.Context, scientificName string) (BirdImage, error) {
	// Check if we're allowed to make requests to WikiMedia
	if allowed, reason := wikiFetchAllowed(); !allowed {
		logWikiFetchBlocked(scientificName, reason)
		return BirdImage{}, ErrProviderNotConfigured
	}

	// Check if this is a background operation
	isBackground := isBackgroundContext(ctx)

	// Only use rate limiter for background operations
	var limiter *rate.Limiter
	if isBackground {
		limiter = l.backgroundLimiter
	}

	return l.fetchWithLimiter(ctx, scientificName, limiter)
}

// Fetch retrieves the bird image for a given scientific name.
// It queries for the thumbnail and author information, then constructs a BirdImage.
//
// Every request made through this method still passes the process-global 1 req/s
// limiter; what it skips is the additional, more conservative background limiter that
// FetchWithContext applies to sweep operations. Prefer FetchWithContext: this method
// exists for the context-free ImageProvider interface, and its context.Background()
// means neither the retry backoff nor the limiter wait can be cancelled.
func (l *wikiMediaProvider) Fetch(scientificName string) (BirdImage, error) {
	// Check if we're allowed to make requests to WikiMedia
	if allowed, reason := wikiFetchAllowed(); !allowed {
		logWikiFetchBlocked(scientificName, reason)
		return BirdImage{}, ErrProviderNotConfigured
	}

	return l.fetchWithLimiter(context.Background(), scientificName, nil)
}

// fetchWithLimiter retrieves the bird image using the specified rate limiter.
func (l *wikiMediaProvider) fetchWithLimiter(ctx context.Context, scientificName string, limiter *rate.Limiter) (BirdImage, error) {
	reqID := uuid.New().String()[:requestIDLength]
	log := GetLogger().With(
		logger.String("provider", wikiProviderName),
		logger.String("scientific_name", scientificName),
		logger.String("request_id", reqID))

	// Enhanced start logging with operation context
	rateLimitType := "none"
	if limiter != nil {
		rateLimitType = "background"
	}
	log.Debug("Starting Wikipedia image fetch - operation details",
		logger.String("operation", "fetch_image"),
		logger.String("species_query", scientificName),
		logger.String("rate_limit_type", rateLimitType),
		logger.String("diagnostic_info", "beginning_wikipedia_image_fetch_operation"))

	// A configured taxonomy synonym is the user asserting that the primary name
	// is the wrong one to ask Wikipedia for, so it goes first. Consulting it
	// only after a failure was backwards, and meant it could not fix the case it
	// is added for: a lookup that succeeds under the primary name and returns
	// the wrong image. The original name is still tried if the synonym misses,
	// so this costs no extra request in the common case.
	queryName := scientificName
	synonym, hasSynonym := GetTaxonomySynonym(scientificName)
	if hasSynonym {
		log.Debug("Querying Wikipedia with the configured taxonomy synonym first",
			logger.String("original_name", scientificName),
			logger.String("synonym", synonym))
		queryName = synonym
	}

	thumbnailURL, thumbnailSourceFile, err := l.queryThumbnail(ctx, reqID, queryName, limiter)
	if hasSynonym && errors.Is(err, ErrImageNotFound) {
		log.Debug("Synonym had no usable thumbnail, retrying with the original name",
			logger.String("original_name", scientificName),
			logger.String("synonym", synonym))
		thumbnailURL, thumbnailSourceFile, err = l.queryThumbnail(ctx, reqID, scientificName, limiter)
	}
	if err != nil {
		return BirdImage{}, err
	}
	log.Debug("Thumbnail retrieved successfully",
		logger.String("thumbnail_url", thumbnailURL),
		logger.String("source_file", thumbnailSourceFile))

	// Without a pageimage filename there is no file page to ask, so the
	// attribution query is skipped rather than failed. The thumbnail itself is
	// usable and is what the user came for.
	if thumbnailSourceFile == "" {
		log.Debug("No pageimage filename, using default attribution")
		return l.buildBirdImage(reqID, scientificName, thumbnailURL, unknownAuthorInfo()), nil
	}

	authorInfo, err := l.queryAuthorInfo(ctx, reqID, thumbnailSourceFile, limiter)
	if err != nil {
		// If it's just a "not found" error, continue with default author info
		// Only fail for actual errors (network issues, parsing failures)
		if errors.Is(err, ErrImageNotFound) {
			log.Debug("Author info not available, using defaults")
			// Use default author info rather than failing
			authorInfo = unknownAuthorInfo()
		} else {
			// This is a real error (network, API issues), so we should report it.
			//
			// Wrap the cause instead of replacing it. Building a fresh error here
			// dropped both the cause's CategoryNetwork tag and the "wikipedia api
			// circuit breaker open" text that internal/errors/telemetry_integration.go
			// matches on to suppress rate-limit noise, so a transient throttle was
			// reported to Sentry once per species. Same rationale as the wrapping at
			// imageprovider.go enhanceFetchError.
			log.Error("Failed to fetch author info", logger.Error(err))
			enhancedErr := errors.Newf("unable to retrieve image attribution for species %s: %w", scientificName, err).
				Component("imageprovider").
				Category(causeCategory(err, errors.CategoryImageFetch)).
				Context("provider", wikiProviderName).
				Context("request_id", reqID).
				Context("scientific_name", scientificName).
				Context("thumbnail_source_file", thumbnailSourceFile).
				Context("operation", "fetch_author_info").
				Build()
			return BirdImage{}, enhancedErr
		}
	}
	log.Debug("Author info retrieved successfully",
		logger.String("author", authorInfo.name),
		logger.String("license", authorInfo.licenseName))

	return l.buildBirdImage(reqID, scientificName, thumbnailURL, authorInfo), nil
}

// unknownAuthorInfo is the attribution used when Wikipedia has no usable
// metadata for an otherwise usable thumbnail.
func unknownAuthorInfo() *wikiMediaAuthor {
	return &wikiMediaAuthor{
		name:        unknownMetadataValue,
		URL:         "",
		licenseName: unknownMetadataValue,
		licenseURL:  "",
	}
}

// buildBirdImage assembles the result and emits the success log shared by the
// with-attribution and without-attribution paths.
func (l *wikiMediaProvider) buildBirdImage(reqID, scientificName, thumbnailURL string, authorInfo *wikiMediaAuthor) BirdImage {
	// Enhanced success logging with complete operation summary
	logAPISuccess(reqID, scientificName, "complete_fetch_operation")

	return BirdImage{
		URL:            thumbnailURL,
		ScientificName: scientificName,
		AuthorName:     authorInfo.name,
		AuthorURL:      authorInfo.URL,
		LicenseName:    authorInfo.licenseName,
		LicenseURL:     authorInfo.licenseURL,
		SourceProvider: wikiProviderName, // Set the provider name
	}
}

// queryThumbnail queries Wikipedia for the thumbnail image of the given scientific name.
// It returns the URL and file name of the thumbnail.
func (l *wikiMediaProvider) queryThumbnail(ctx context.Context, reqID, scientificName string, limiter *rate.Limiter) (thumbnailURL, fileName string, err error) {
	log := GetLogger().With(
		logger.String("provider", wikiProviderName),
		logger.String("scientific_name", scientificName),
		logger.String("request_id", reqID))
	log.Debug("Querying thumbnail")

	params := map[string]string{
		"action":        "query",
		"format":        "json",
		"formatversion": "2",
		"prop":          "pageimages",
		"piprop":        "thumbnail|name",
		"pilicense":     "free",
		"titles":        scientificName,
		"pithumbsize":   "400",
		// Redirect following stays on: most bird articles are titled by common
		// name, so a scientific name usually only resolves through its redirect.
		// The redirect target is validated below instead.
		"redirects": "",
	}

	page, redirects, err := l.queryAndGetFirstPageWithLimiter(ctx, reqID, params, limiter)
	if err != nil {
		// Log based on error type
		if errors.Is(err, ErrImageNotFound) {
			log.Warn("No Wikipedia page found for species")
		} else {
			log.Error("Failed to query thumbnail page", logger.Error(err))
		}
		// Return a consistent user-facing error
		// Check if it's already an enhanced error from queryAndGetFirstPage
		var enhancedErr *errors.EnhancedError
		if !errors.As(err, &enhancedErr) {
			enhancedErr = errors.Newf("no Wikipedia page found for species: %s", scientificName).
				Component("imageprovider").
				Category(errors.CategoryImageFetch).
				Context("provider", wikiProviderName).
				Context("request_id", reqID).
				Context("scientific_name", scientificName).
				Context("operation", "query_thumbnail").
				Build()
		}
		return "", "", enhancedErr
	}

	if target, left := redirectLeftSpecies(scientificName, redirects); left {
		log.Debug("Discarding thumbnail from a redirect to a broader taxon",
			logger.String("redirect_target", target))
		return "", "", imageNotFoundFor(scientificName, wikiProviderName, "wiki_redirect_left_species")
	}

	if page.Thumbnail == nil || page.Thumbnail.Source == "" {
		log.Debug("No thumbnail URL found in page data")
		// This is common for pages without images or with non-free images
		// Don't create telemetry noise - treat as "not found"
		return "", "", imageNotFoundFor(scientificName, wikiProviderName, "wiki_no_thumbnail")
	}
	thumbnailURL = page.Thumbnail.Source

	// An empty pageimage filename is not a reason to throw the thumbnail away.
	// It is only the key for the second, attribution query, and the caller
	// already falls back to "Unknown" attribution when that query fails.
	fileName = page.PageImage
	if fileName == "" {
		log.Debug("No pageimage filename in page data; serving the thumbnail without attribution metadata")
	}

	log.Debug("Successfully retrieved thumbnail URL and filename",
		logger.String("url", thumbnailURL),
		logger.String("filename", fileName))

	return thumbnailURL, fileName, nil
}

// supraGenericSuffixes are the endings of zoological names above genus rank:
// family, subfamily, superfamily and order. A word carrying one of these names
// a taxon containing many species, so an article titled with it is never a
// single species' article.
//
// The tribe suffix "-ini" is deliberately absent: it is short enough to end an
// ordinary English word, so it could discard a valid common-name article.
var supraGenericSuffixes = []string{"idae", "inae", "oidea", "iformes"}

// redirectLeftSpecies reports whether the API followed a redirect from the
// requested species up to an article about a broader taxon.
//
// Redirect following itself has to stay on: most bird articles are titled by
// common name, so a scientific name usually resolves only through its redirect.
// Measured against 550 species of the shipped label set on the live API, 548
// redirect. What must not pass is a redirect to a family or order article,
// whose pageimage is a different bird or a distribution map. That lookup
// succeeds, so the wrong image is written as a positive cache entry, and the
// taxonomy-synonym map cannot correct it: it used to be consulted only after a
// failure, and although the synonym is now tried first, a species absent from
// the map still has no second path.
//
// The test is deliberately narrow, because the rejection is reported as
// ErrImageNotFound and therefore persists as a negative cache entry: only a
// supra-generic rank name is rejected, and no species article can carry one.
// A target equal to the queried genus is NOT rejected, because a monotypic
// genus keeps one combined article at the bare genus title and that article's
// image is this species' image. In the same 550-species measurement every
// single-word redirect target was an English common name (Woodlark, Garganey,
// Weka) and none was a genus article, so that clause would have cost correct
// images for no observed benefit.
func redirectLeftSpecies(scientificName string, redirects []wikiRedirect) (target string, left bool) {
	if len(redirects) == 0 {
		return "", false
	}

	target = resolveRedirectTarget(scientificName, redirects)
	if target == "" || titlesEqual(target, scientificName) {
		return "", false
	}

	// Only a single-word target is tested. A supra-generic name is one word, and
	// a binomial is two, so this cannot reject a species article.
	//
	// Testing every word instead would: nine binomials in the shipped label set
	// carry a Latin genitive epithet that ends in one of these suffixes
	// (Pyrrhura molinae, Setophaga adelaidae, Crypturellus duidae and six more),
	// so a synonym redirect landing on one of them would be discarded and, since
	// the rejection is reported as ErrImageNotFound, cached as "no image". No
	// common name in that label set collides.
	if strings.ContainsAny(target, " _") {
		return "", false
	}

	targetLower := strings.ToLower(target)
	for _, suffix := range supraGenericSuffixes {
		if strings.HasSuffix(targetLower, suffix) {
			return target, true
		}
	}

	return "", false
}

// resolveRedirectTarget walks the redirect chain from the requested title.
// MediaWiki does not follow double redirects, so the chain is normally one hop;
// the loop is bounded anyway. If no hop starts at the requested title, the last
// reported target is used, which covers the case where the API normalized the
// title before redirecting it.
func resolveRedirectTarget(scientificName string, redirects []wikiRedirect) string {
	if len(redirects) == 0 {
		return ""
	}

	current := scientificName
	for range redirects {
		next := ""
		for i := range redirects {
			if titlesEqual(redirects[i].From, current) {
				next = redirects[i].To
				break
			}
		}
		if next == "" {
			break
		}
		current = next
	}

	if titlesEqual(current, scientificName) {
		return redirects[len(redirects)-1].To
	}
	return current
}

// titlesEqual compares two MediaWiki titles, which treat underscore and space
// as the same character and are case-insensitive on the first letter only.
// Case-insensitive comparison throughout is close enough for this purpose.
func titlesEqual(a, b string) bool {
	return strings.EqualFold(strings.ReplaceAll(a, "_", " "), strings.ReplaceAll(b, "_", " "))
}

// queryAuthorInfo queries Wikipedia for the author information of the given thumbnail URL.
// It returns a wikiMediaAuthor struct containing the author and license information.
func (l *wikiMediaProvider) queryAuthorInfo(ctx context.Context, reqID, thumbnailFileName string, limiter *rate.Limiter) (*wikiMediaAuthor, error) {
	log := GetLogger().With(
		logger.String("provider", wikiProviderName),
		logger.String("request_id", reqID),
		logger.String("filename", thumbnailFileName))
	log.Debug("Querying author info",
		logger.String("file_title", "File:"+thumbnailFileName))

	params := map[string]string{
		"action":        "query",
		"format":        "json",
		"formatversion": "2",
		"prop":          "imageinfo",
		"iiprop":        "extmetadata",
		"titles":        "File:" + thumbnailFileName, // Use filename here
		"redirects":     "",
	}

	page, _, err := l.queryAndGetFirstPageWithLimiter(ctx, reqID, params, limiter)
	if err != nil {
		// Log based on error type
		if errors.Is(err, ErrImageNotFound) {
			log.Warn("No Wikipedia file page found for image filename")
		} else {
			log.Error("Failed to query author info page", logger.Error(err))
		}
		// Return internal error, fetch will wrap it
		// Check if it's already an enhanced error from queryAndGetFirstPage
		var enhancedErr *errors.EnhancedError
		if !errors.As(err, &enhancedErr) {
			enhancedErr = errors.Newf("failed to query Wikipedia for image author information: %v", err).
				Component("imageprovider").
				Category(errors.CategoryImageFetch).
				Context("provider", wikiProviderName).
				Context("request_id", reqID).
				Context("thumbnail_filename", thumbnailFileName).
				Context("operation", "query_author_info").
				Context("error_detail", err.Error()).
				Build()
		}
		return nil, enhancedErr
	}

	// Extract metadata
	log.Debug("Extracting metadata from imageinfo response")
	if len(page.ImageInfo) == 0 {
		log.Debug("No imageinfo found in file page")
		// This is common for files without metadata or processing issues
		// Don't create telemetry noise - treat as "not found"
		// Note: thumbnailFileName used as lookup key since scientificName is not in scope here
		return nil, imageNotFoundFor(thumbnailFileName, wikiProviderName, "wiki_no_imageinfo")
	}

	extMetadata := page.ImageInfo[0].ExtMetadata
	if len(extMetadata) == 0 {
		log.Debug("No extmetadata found in imageinfo")
		// This is common for files without extended metadata
		// Don't create telemetry noise - treat as "not found"
		return nil, imageNotFoundFor(thumbnailFileName, wikiProviderName, "wiki_no_extmetadata") // thumbnailFileName as lookup key
	}

	// Extract specific fields (Artist, LicenseShortName, LicenseUrl).
	// These fields are optional; missing or non-string values yield "".
	artistHTML := extMetaString(extMetadata, "Artist")
	licenseName := extMetaString(extMetadata, "LicenseShortName")
	licenseURL := extMetaString(extMetadata, "LicenseUrl")

	log.Debug("Extracted raw metadata fields",
		logger.Int("artist_html_len", len(artistHTML)),
		logger.String("license_name", licenseName),
		logger.String("license_url", licenseURL))

	// Parse artist HTML to get name and URL using the helper function
	authorName, authorURL := parseAuthorFromHTML(artistHTML)
	log.Debug("Parsed author info",
		logger.String("name", authorName),
		logger.String("url", authorURL))

	// Handle license name fallback
	if licenseName == "" {
		log.Warn("License name could not be extracted")
		licenseName = unknownMetadataValue
	}

	log.Debug("Final extracted author and license info",
		logger.String("author_name", authorName),
		logger.String("author_url", authorURL),
		logger.String("license_name", licenseName),
		logger.String("license_url", licenseURL))
	return &wikiMediaAuthor{
		name:        authorName,
		URL:         authorURL,
		licenseName: licenseName,
		licenseURL:  licenseURL,
	}, nil
}

// causeCategory returns the error category already carried by err, falling back to
// fallback for a plain error. Preserving the cause's category matters because
// telemetry suppression and errors.Is matching both key on it: re-tagging a
// CategoryNetwork throttle as CategoryImageFetch turns a suppressed transient into a
// per-species Sentry event.
func causeCategory(err error, fallback errors.ErrorCategory) errors.ErrorCategory {
	if category := errors.CategoryOf(err); category != "" {
		return category
	}
	return fallback
}

// parseAuthorFromHTML extracts author name and URL from HTML, with fallbacks.
// Returns unknownMetadataValue for empty input or when extraction fails.
func parseAuthorFromHTML(artistHTML string) (authorName, authorURL string) {
	if artistHTML == "" {
		return unknownMetadataValue, ""
	}

	authorURL, authorName, err := extractArtistInfo(artistHTML)
	if err != nil {
		// Fallback to plain text version if parsing failed
		authorName = htmlToText(artistHTML)
	}

	// If author name is still empty after all attempts, use unknownMetadataValue
	if authorName == "" {
		authorName = unknownMetadataValue
	}

	return authorName, authorURL
}

// extractArtistInfo extracts the artist's name and URL from the HTML string.
func extractArtistInfo(htmlStr string) (href, text string, err error) {
	log := GetLogger().With(logger.String("provider", wikiProviderName))
	log.Debug("Attempting to extract artist info from HTML",
		logger.Int("html_len", len(htmlStr)))
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		log.Error("Failed to parse artist HTML", logger.Error(err))
		enhancedErr := errors.Newf("failed to parse Wikipedia artist attribution HTML: %v", err).
			Component("imageprovider").
			Category(errors.CategoryImageFetch).
			Context("provider", wikiProviderName).
			Context("html_length", len(htmlStr)).
			Context("operation", "parse_artist_html").
			Context("error_detail", err.Error()).
			Build()
		return "", "", enhancedErr
	}

	userLinks := findWikipediaUserLinks(findLinks(doc))
	if len(userLinks) > 0 {
		// Prefer the first valid Wikipedia user link
		href = extractHref(userLinks[0])
		text = extractText(userLinks[0])
		log.Debug("Found Wikipedia user link for artist",
			logger.String("href", href),
			logger.String("text", text))
		return href, text, nil
	}

	// Fallback: Find the first link if no specific user link is found
	allLinks := findLinks(doc)
	if len(allLinks) > 0 {
		href = extractHref(allLinks[0])
		text = extractText(allLinks[0])
		log.Debug("No user link found, falling back to first available link",
			logger.String("href", href),
			logger.String("text", text))
		return href, text, nil
	}

	// Fallback: No links found, return plain text
	text = htmlToText(htmlStr)
	log.Debug("No links found in artist HTML, returning plain text",
		logger.String("text", text))
	return "", text, nil // No error if no link, just return text
}

// findWikipediaUserLinks traverses the list of nodes and returns only Wikipedia user links.
func findWikipediaUserLinks(nodes []*html.Node) []*html.Node {
	var wikiUserLinks []*html.Node

	for _, node := range nodes {
		for _, attr := range node.Attr {
			if attr.Key == "href" && isWikipediaUserLink(attr.Val) {
				wikiUserLinks = append(wikiUserLinks, node)
				break
			}
		}
	}

	return wikiUserLinks
}

// isWikipediaUserLink checks if the given href is a link to a Wikipedia user page.
func isWikipediaUserLink(href string) bool {
	return strings.Contains(href, "/wiki/User:")
}

// findLinks traverses the HTML document and returns all anchor (<a>) tags.
func findLinks(doc *html.Node) []*html.Node {
	var linkNodes []*html.Node

	var traverse func(*html.Node)
	traverse = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" {
			linkNodes = append(linkNodes, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			traverse(child)
		}
	}

	traverse(doc)

	return linkNodes
}

// extractHref extracts the href attribute from an anchor tag.
func extractHref(link *html.Node) string {
	for _, attr := range link.Attr {
		if attr.Key == "href" {
			return attr.Val
		}
	}
	return ""
}

// extractText extracts the inner visible text from an anchor tag, including text
// split across multiple child nodes (e.g. "<a>John <b>Doe</b></a>"). Rendering
// the whole node and stripping tags via htmlToText avoids the previous bug where
// only the first child node was read, truncating multi-part author names.
func extractText(link *html.Node) string {
	var b bytes.Buffer
	if err := html.Render(&b, link); err != nil {
		return ""
	}
	return htmlToText(b.String())
}
