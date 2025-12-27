// wikipedia.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/antonholmquist/jason"
	"github.com/google/uuid"
	"github.com/k3a/html2text"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"golang.org/x/net/html"
	"golang.org/x/time/rate"
)

const (
	wikiProviderName = "wikimedia"
	wikipediaAPIURL  = "https://en.wikipedia.org/w/api.php"

	// User-Agent constants following Wikimedia robot policy
	// https://foundation.wikimedia.org/wiki/Policy:Wikimedia_Foundation_User-Agent_Policy
	userAgentName    = "BirdNETGo"
	userAgentContact = "https://github.com/tphakala/birdnet-go"
	userAgentLibrary = "Go-HTTP-Client"

	// Circuit breaker timeout durations
	circuitBreakerRateLimitDuration      = 60 * time.Second // Rate limit circuit breaker duration
	circuitBreakerBlockedDuration        = 5 * time.Minute  // Access blocked circuit breaker duration
	circuitBreakerUserAgentDuration      = 10 * time.Minute // User-Agent violation circuit breaker duration
	circuitBreakerServiceUnavailDuration = 30 * time.Second // Service unavailable circuit breaker duration

	// HTTP client configuration
	httpClientTimeout         = 30 * time.Second
	httpClientIdleConnTimeout = 90 * time.Second
	httpClientTLSTimeout      = 10 * time.Second
	httpClientMaxIdleConns    = 10
	diagnosticRequestTimeout  = 10 * time.Second

	// Rate limiting configuration
	globalRateLimitPerSecond     = 1 // Requests per second for global rate limiter
	backgroundRateLimitPerSecond = 1 // Requests per second for background operations

	// Retry and delay configuration
	defaultMaxRetries   = 3
	retryMinDelay       = 2 * time.Second
	configWaitTimeout   = 10 * time.Second
	configCheckInterval = 100 * time.Millisecond

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
	errorStringRate        = "rate"
	errorStringLimit       = "limit"
	errorStringThrottle    = "throttl"
	errorStringBlocked     = "blocked"
	errorStringBanned      = "banned"
	errorStringDenied      = "denied"
	errorStringHTMLDoctype = "<!DOCTYPE"
	errorStringHTMLTag     = "<html"
)

// wikiMediaProvider implements the ImageProvider interface for Wikipedia.
type wikiMediaProvider struct {
	httpClient        *http.Client // Standard HTTP client for API requests
	userAgent         string       // User-Agent header value (cached for reuse)
	debug             bool
	globalLimiter     *rate.Limiter // Global rate limiter for ALL Wikipedia requests (1 req/sec)
	backgroundLimiter *rate.Limiter // Additional limiter for background operations
	maxRetries        int

	// Circuit breaker fields to prevent hammering when rate limited
	circuitMu        sync.RWMutex
	circuitOpenUntil time.Time // When the circuit breaker can be closed again
	circuitFailures  int       // Number of consecutive failures
	circuitLastError string    // Last error message for logging
}

// wikiMediaAuthor represents the author information for a Wikipedia image.
type wikiMediaAuthor struct {
	name        string
	URL         string
	licenseName string
	licenseURL  string
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
}

// makeAPIRequest performs a direct HTTP GET request to Wikipedia API with proper headers.
// This replaces the mwclient library to ensure proper User-Agent header handling.
// The context is used for rate limiting, cancellation, and deadlines.
func (l *wikiMediaProvider) makeAPIRequest(ctx context.Context, params map[string]string) (*jason.Object, error) {
	if err := l.waitForGlobalRateLimit(ctx); err != nil {
		return nil, err
	}

	if err := l.validateUserAgent(); err != nil {
		return nil, err
	}

	fullURL, err := l.buildRequestURL(params)
	if err != nil {
		return nil, err
	}

	req, err := l.createHTTPRequest(fullURL)
	if err != nil {
		return nil, err
	}

	body, statusCode, err := l.executeHTTPRequest(req)
	if err != nil {
		return nil, err
	}

	if statusCode != http.StatusOK {
		return nil, l.handleHTTPStatusError(statusCode, string(body))
	}

	return l.parseJSONResponse(body)
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

// validateUserAgent ensures the User-Agent is set.
func (l *wikiMediaProvider) validateUserAgent() error {
	if l.userAgent == "" {
		GetLogger().Error("User-Agent is empty! This will cause Wikipedia to reject the request",
			logger.String("provider", wikiProviderName))
		return errors.Newf("User-Agent not set for Wikipedia provider").
			Component("imageprovider").
			Category(errors.CategoryConfiguration).
			Context("provider", wikiProviderName).
			Context("operation", "missing_user_agent").
			Build()
	}
	return nil
}

// buildRequestURL constructs the full API URL with query parameters.
func (l *wikiMediaProvider) buildRequestURL(params map[string]string) (string, error) {
	u, err := url.Parse(wikipediaAPIURL)
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

// createHTTPRequest creates an HTTP request with proper headers.
func (l *wikiMediaProvider) createHTTPRequest(fullURL string) (*http.Request, error) {
	req, err := http.NewRequest("GET", fullURL, http.NoBody)
	if err != nil {
		return nil, errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("operation", "create_request").
			Context("url", fullURL).
			Build()
	}

	req.Header.Set("User-Agent", l.userAgent)
	req.Header.Set("Accept", "application/json")

	GetLogger().Info("Setting User-Agent for Wikipedia API request",
		logger.String("provider", wikiProviderName),
		logger.String("user_agent", l.userAgent),
		logger.Int("user_agent_length", len(l.userAgent)),
		logger.String("url", fullURL))

	if l.debug {
		GetLogger().Debug("Full request headers",
			logger.String("provider", wikiProviderName),
			logger.String("headers", fmt.Sprintf("%v", req.Header)))
	}

	return req, nil
}

// executeHTTPRequest executes the HTTP request and returns the body and status code.
func (l *wikiMediaProvider) executeHTTPRequest(req *http.Request) (body []byte, statusCode int, err error) {
	resp, err := l.httpClient.Do(req)
	if err != nil {
		return nil, 0, errors.New(err).
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

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("operation", "read_response").
			Context("status_code", resp.StatusCode).
			Build()
	}

	return body, resp.StatusCode, nil
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
	bodyLower := strings.ToLower(bodyStr)
	truncated := truncateResponseBody(bodyStr, responseBodyPreviewLimit)

	switch {
	case strings.Contains(bodyLower, errorStringUserAgent) || strings.Contains(bodyLower, errorStringRobotPolicy):
		l.openCircuit(circuitBreakerUserAgentDuration, fmt.Sprintf("User-Agent policy violation (HTTP 403): %s", truncated))
	case strings.Contains(bodyLower, errorStringRate) || strings.Contains(bodyLower, errorStringLimit):
		l.openCircuit(circuitBreakerRateLimitDuration, fmt.Sprintf("Rate limited (HTTP 403): %s", truncated))
	default:
		l.openCircuit(circuitBreakerBlockedDuration, fmt.Sprintf("Access blocked (HTTP 403): %s", truncated))
	}
}

// parseJSONResponse parses the response body as JSON.
func (l *wikiMediaProvider) parseJSONResponse(body []byte) (*jason.Object, error) {
	var jsonData any
	if err := json.Unmarshal(body, &jsonData); err != nil {
		return nil, l.handleJSONParseError(body, err)
	}

	jasonObj, err := jason.NewObjectFromBytes(body)
	if err != nil {
		return nil, errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("operation", "jason_convert").
			Build()
	}

	return jasonObj, nil
}

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
	once     sync.Once
	provider *wikiMediaProvider
	initErr  error
}

// NewLazyWikiMediaProvider creates a new lazy-initialized Wikipedia provider.
// The actual provider creation is deferred until first use.
func NewLazyWikiMediaProvider() *LazyWikiMediaProvider {
	return &LazyWikiMediaProvider{}
}

// ensureInitialized creates the actual provider on first use, with validation.
// It uses sync.Once to ensure thread-safe single initialization.
func (l *LazyWikiMediaProvider) ensureInitialized() error {
	l.once.Do(func() {
		log := GetLogger().With(logger.String("provider", wikiProviderName))
		// Wait for valid configuration (with timeout)
		if !l.waitForValidConfig(configWaitTimeout) {
			l.initErr = errors.Newf("configuration not available after timeout").
				Component("imageprovider").
				Category(errors.CategoryConfiguration).
				Context("provider", wikiProviderName).
				Context("operation", "lazy_init_timeout").
				Build()
			log.Error("LazyWikiMediaProvider: Configuration not available after timeout")
			return
		}

		// Create the actual provider with valid configuration
		l.provider, l.initErr = NewWikiMediaProvider()
		if l.initErr != nil {
			log.Error("LazyWikiMediaProvider: Failed to create provider", logger.Error(l.initErr))
		} else {
			log.Info("LazyWikiMediaProvider: Successfully initialized provider")
		}
	})
	return l.initErr
}

// waitForValidConfig waits until configuration is available with a valid version.
func (l *LazyWikiMediaProvider) waitForValidConfig(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(configCheckInterval)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		settings := conf.Setting()
		if settings != nil && settings.Version != "" {
			GetLogger().Debug("LazyWikiMediaProvider: Valid configuration detected",
				logger.String("provider", wikiProviderName),
				logger.String("version", settings.Version))
			return true
		}
		<-ticker.C
	}
	return false
}

// Fetch implements the ImageProvider interface with lazy initialization.
func (l *LazyWikiMediaProvider) Fetch(scientificName string) (BirdImage, error) {
	if err := l.ensureInitialized(); err != nil {
		return BirdImage{}, err
	}
	return l.provider.Fetch(scientificName)
}

// FetchWithContext implements context-aware fetching with lazy initialization.
func (l *LazyWikiMediaProvider) FetchWithContext(ctx context.Context, scientificName string) (BirdImage, error) {
	if err := l.ensureInitialized(); err != nil {
		return BirdImage{}, err
	}
	return l.provider.FetchWithContext(ctx, scientificName)
}

// ShouldRefreshCache implements ProviderStatusChecker interface.
// It checks if WikiMedia provider should actively refresh cache based on current configuration,
// without requiring full provider initialization. This allows the provider to be registered
// for UI discovery while preventing unnecessary cache operations when disabled.
func (l *LazyWikiMediaProvider) ShouldRefreshCache() bool {
	settings := conf.Setting()
	if settings == nil {
		return false
	}

	// Check if WikiMedia is configured as primary provider
	if settings.Realtime.Dashboard.Thumbnails.ImageProvider == wikiProviderName {
		return true
	}

	// Check if WikiMedia is configured as fallback provider
	fallback := strings.ToLower(strings.TrimSpace(settings.Realtime.Dashboard.Thumbnails.FallbackPolicy))
	return fallback == wikiProviderName || fallback == fallbackPolicyAll
}

// truncateResponseBody truncates a response body string to a specified length for logging.
// This prevents excessive memory usage and log spam when logging error responses.
func truncateResponseBody(body string, maxLength int) string {
	if len(body) <= maxLength {
		return body
	}
	return body[:maxLength] + "..."
}

// buildUserAgent constructs a user-agent string that complies with Wikimedia's robot policy.
// Format: <client name>/<version> (<contact information>) <library/framework name>/<version>
// Reference: https://foundation.wikimedia.org/wiki/Policy:Wikimedia_Foundation_User-Agent_Policy
func buildUserAgent(appVersion string) string {
	if appVersion == "" {
		appVersion = "unknown"
	}

	goVersion := runtime.Version()

	// Format: BirdNET-Go/1.0.0 (https://github.com/tphakala/birdnet-go) Go-HTTP-Client/go1.21.0
	return fmt.Sprintf("%s/%s (%s) %s/%s",
		userAgentName, appVersion, userAgentContact, userAgentLibrary, goVersion)
}

// logUserAgentValidation logs the constructed user-agent for debugging purposes
func logUserAgentValidation(appVersion string) {
	userAgent := buildUserAgent(appVersion)
	GetLogger().Info("Wikipedia user-agent validation",
		logger.String("provider", wikiProviderName),
		logger.String("user_agent", userAgent),
		logger.String("complies_with_policy", "https://foundation.wikimedia.org/wiki/Policy:User-Agent_policy"),
		logger.String("contains_app_name", userAgentName),
		logger.String("contains_version", appVersion),
		logger.String("contains_contact", userAgentContact),
		logger.String("contains_library", userAgentLibrary),
		logger.String("go_version", runtime.Version()))
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

	// Extract text content using html2text for valid HTML
	return html2text.HTML2Text(string(htmlContent))
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
		if strings.Contains(bodyLower, errorStringUserAgent) || strings.Contains(bodyLower, errorStringRobotPolicy) {
			return wikiErrorUserAgent, "User-Agent policy violation"
		}
		if strings.Contains(bodyLower, errorStringRate) || strings.Contains(bodyLower, errorStringLimit) {
			return wikiErrorRateLimit, "Rate limit exceeded (403 with rate limit message)"
		}
		return wikiErrorBlocked, "Access blocked (HTTP 403)"
	case http.StatusServiceUnavailable:
		return wikiErrorTemporary, "Service temporarily unavailable (HTTP 503)"
	}

	// Check content type - HTML responses usually indicate errors
	if strings.Contains(contentType, "text/html") {
		errorMsg := parseHTMLErrorMessage(responseBody)
		errorMsgLower := strings.ToLower(errorMsg)

		// Check for rate limiting keywords in HTML content
		if strings.Contains(errorMsgLower, errorStringRate+" "+errorStringLimit) ||
			strings.Contains(errorMsgLower, "too many requests") ||
			strings.Contains(errorMsgLower, errorStringThrottle) {
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

	// Convert to lowercase once for efficient comparison
	bodyStr := string(responseBody)
	bodyLower := strings.ToLower(bodyStr)
	if !strings.Contains(bodyLower, errorStringUserAgent) && !strings.Contains(bodyLower, errorStringRobotPolicy) {
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

// makeRateLimitedRequest makes a rate-limited HTTP request to the given URL.
// This ensures all requests, including diagnostic requests, respect the global rate limiter.
// The context is used for rate limiting, cancellation, and deadlines.
func (l *wikiMediaProvider) makeRateLimitedRequest(ctx context.Context, requestURL string) (*http.Response, error) {
	// Apply global rate limiting - ALL requests must respect the 1 req/sec limit
	if l.globalLimiter != nil {
		if err := l.globalLimiter.Wait(ctx); err != nil {
			return nil, errors.New(err).
				Component("imageprovider").
				Category(errors.CategoryNetwork).
				Context("provider", wikiProviderName).
				Context("operation", "global_rate_limit_wait").
				Build()
		}
		GetLogger().Debug("Global rate limiter wait completed for diagnostic request",
			logger.String("provider", wikiProviderName))
	}

	// Create and execute request
	req, err := http.NewRequest("GET", requestURL, http.NoBody)
	if err != nil {
		return nil, errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("operation", "create_diagnostic_request").
			Build()
	}
	req.Header.Set("User-Agent", l.userAgent)

	httpClient := &http.Client{Timeout: diagnosticRequestTimeout}
	return httpClient.Do(req)
}

// handleJSONParsingError handles JSON parsing errors by making a rate-limited diagnostic request
// Always performs diagnostics to identify rate limiting and other error types
func (l *wikiMediaProvider) handleJSONParsingError(reqID, fullURL string, origErr error, attempt int) error {
	log := GetLogger().With(
		logger.String("provider", wikiProviderName),
		logger.String("request_id", reqID),
		logger.Int("attempt", attempt+1),
		logger.Int("max_attempts", l.maxRetries))

	// Always make a diagnostic request to identify the actual error type
	// This is important to detect rate limiting and blocking
	// Use rate-limited request to ensure all requests respect the global limiter

	debugResp, debugErr := l.makeRateLimitedRequest(context.Background(), fullURL)
	if debugErr != nil {
		log.Debug("Unable to diagnose API error",
			logger.String("diagnostic_error", debugErr.Error()),
			logger.String("original_error", origErr.Error()))
		return nil // Continue with normal retry logic
	}

	defer func() {
		if closeErr := debugResp.Body.Close(); closeErr != nil {
			log.Debug("Failed to close debug response body", logger.Error(closeErr))
		}
	}()

	body, readErr := io.ReadAll(debugResp.Body)
	if readErr != nil {
		log.Debug("Failed to read debug response body", logger.Error(readErr))
		return nil // Continue with normal retry logic
	}

	// Detect error type
	errorType, errorMsg := detectWikipediaErrorType(debugResp.StatusCode, body, debugResp.Header.Get("Content-Type"))

	// Log error details based on severity
	logFields := []logger.Field{
		logger.Int("error_type", int(errorType)),
		logger.String("error_message", errorMsg),
		logger.Int("status_code", debugResp.StatusCode),
		logger.String("content_type", debugResp.Header.Get("Content-Type")),
		logger.String("requested_url", fullURL),
	}

	switch {
	case errorType == wikiErrorRateLimit || errorType == wikiErrorBlocked || errorType == wikiErrorUserAgent:
		log.Error("Wikipedia API error diagnosed", logFields...)
	case debugResp.StatusCode != http.StatusOK:
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
			Context("status_code", debugResp.StatusCode).
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
			Context("status_code", debugResp.StatusCode).
			Context("error_message", errorMsg).
			Context("permanent_failure", true).
			Build()

	case wikiErrorUserAgent:
		// User-agent policy violation - open circuit breaker
		l.openCircuit(circuitBreakerUserAgentDuration, "User-Agent policy violation")
		return checkUserAgentPolicyViolation(reqID, debugResp.StatusCode, body, l.userAgent)

	case wikiErrorTemporary:
		// Temporary error - continue with retry logic but with longer backoff
		log.Info("Temporary Wikipedia error, will retry with backoff",
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

var (
	errorCategoryJSONParsing = apiErrorCategory{
		Type:        "json_parsing_failure",
		Description: "Wikipedia returned HTML error page instead of JSON",
		Severity:    "low",
		Actionable:  false,
	}
	errorCategoryNetworkFailure = apiErrorCategory{
		Type:        "network_failure",
		Description: "Network connectivity or Wikipedia API unavailable",
		Severity:    "high",
		Actionable:  true,
	}
	errorCategoryAPIStructuredError = apiErrorCategory{
		Type:        "api_structured_error",
		Description: "Wikipedia API returned structured error response",
		Severity:    "low",
		Actionable:  true,
	}
	errorCategoryMalformedResponse = apiErrorCategory{
		Type:        "malformed_response",
		Description: "Wikipedia API response format unexpected",
		Severity:    "low",
		Actionable:  true,
	}
)

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

// getTroubleshootingHint provides actionable troubleshooting advice based on error category
func getTroubleshootingHint(category apiErrorCategory) string {
	switch category.Type {
	case "json_parsing_failure":
		return "This usually means the species has no Wikipedia page. Check if scientific name is correct or if alternative names exist."
	case "network_failure":
		return "Check network connectivity and Wikipedia API status. Consider implementing backoff or fallback providers."
	case "api_structured_error":
		return "Wikipedia API rejected the request. Check API parameters, rate limits, or API changes."
	case "malformed_response":
		return "Wikipedia API response format unexpected. May indicate API changes or temporary service issues."
	default:
		return "Review error details and consider checking Wikipedia API documentation for changes."
	}
}

// logAPISuccess logs successful API operations for baseline metrics
func logAPISuccess(reqID, species, operation string) {
	GetLogger().Info("Wikipedia API success - operation completed normally",
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
	settings := conf.Setting()

	// Log debug mode if configured
	if settings.Realtime.Dashboard.Thumbnails.Debug {
		log.Info("Debug mode enabled for WikiMedia provider",
			logger.Bool("debug", true))
	}

	// Build and validate user-agent
	userAgent := buildUserAgent(settings.Version)
	logUserAgentValidation(settings.Version)
	log.Info("WikiMedia provider initialization - user-agent constructed",
		logger.String("user_agent", userAgent),
		logger.String("app_version", settings.Version))

	// Create HTTP client with reasonable timeouts
	httpClient := &http.Client{
		Timeout: httpClientTimeout,
		Transport: &http.Transport{
			MaxIdleConns:        httpClientMaxIdleConns,
			IdleConnTimeout:     httpClientIdleConnTimeout,
			DisableCompression:  false, // Allow gzip compression
			TLSHandshakeTimeout: httpClientTLSTimeout,
		},
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
		userAgent:         userAgent,
		debug:             settings.Realtime.Dashboard.Thumbnails.Debug,
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
func logSuccessfulAPIResponse(resp *jason.Object) {
	log := GetLogger().With(logger.String("provider", wikiProviderName))
	if respObj, errJson := resp.Object(); errJson == nil {
		responseStr := respObj.String()
		log.Debug("API request successful - raw response received",
			logger.String("response_preview", truncateResponseBody(responseStr, responseBodyDebugLimit)),
			logger.Int("response_size", len(responseStr)))
	} else {
		log.Debug("API request successful")
	}
}

// handleJSONParsingErrorIfNeeded checks for JSON parsing errors and handles them appropriately.
func (l *wikiMediaProvider) handleJSONParsingErrorIfNeeded(err error, reqID, fullURL string, attempt int) error {
	if !strings.Contains(err.Error(), "invalid character") || !strings.Contains(err.Error(), "looking for beginning of value") {
		return nil
	}

	if policyErr := l.handleJSONParsingError(reqID, fullURL, err, attempt); policyErr != nil {
		return policyErr
	}
	return nil
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

// queryWithRetryAndLimiter performs a query with retry logic using the specified rate limiter.
// The context is used for cancellation, deadlines, and rate limiting.
func (l *wikiMediaProvider) queryWithRetryAndLimiter(ctx context.Context, reqID string, params map[string]string, limiter *rate.Limiter) (*jason.Object, error) {
	log := GetLogger().With(
		logger.String("provider", wikiProviderName),
		logger.String("request_id", reqID),
		logger.String("api_action", params["action"]))

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

		fullURL := buildDebugURL(params)
		if policyErr := l.handleJSONParsingErrorIfNeeded(err, reqID, fullURL, attempt); policyErr != nil {
			return nil, policyErr
		}

		lastErr = err
		log.Warn("API request failed",
			logger.Error(err),
			logger.String("attempted_url", fullURL),
			logger.Int("attempt", attempt+1),
			logger.Bool("will_retry", attempt < l.maxRetries-1))

		waitDuration := calculateRetryDelay(attempt)
		log.Debug("Waiting before retry", logger.Duration("duration", waitDuration))
		time.Sleep(waitDuration)
	}

	logAPIError(errorCategoryNetworkFailure, reqID, params["titles"], lastErr)
	return nil, buildRetryExhaustedError(lastErr, reqID, params, l.maxRetries)
}

// buildDebugURL constructs a URL string for debug logging purposes.
func buildDebugURL(params map[string]string) string {
	queryParams := make([]string, 0, len(params))
	for k, v := range params {
		queryParams = append(queryParams, k+"="+url.QueryEscape(v))
	}
	return wikipediaAPIURL + "?" + strings.Join(queryParams, "&")
}

// logRawResponse logs the raw API response at debug level for troubleshooting.
func logRawResponse(resp *jason.Object, fullURL string) {
	log := GetLogger().With(logger.String("provider", wikiProviderName))
	if respObj, errJson := resp.Object(); errJson == nil {
		responseStr := respObj.String()
		log.Debug("Raw Wikipedia API response received",
			logger.String("response_full", responseStr),
			logger.Int("response_length", len(responseStr)),
			logger.String("request_url", fullURL))
	} else {
		log.Debug("Failed to format raw response for logging",
			logger.Error(errJson),
			logger.String("request_url", fullURL))
	}
}

// logQueryMissingError logs diagnostics when the 'query' field is missing from the response.
func logQueryMissingError(resp *jason.Object, params map[string]string, fullURL string, queryErr error) {
	log := GetLogger().With(logger.String("provider", wikiProviderName))

	// Log the complete raw response when query field is missing
	if respObj, errJson := resp.Object(); errJson == nil {
		log.Debug("Wikipedia response missing 'query' field - full response dump",
			logger.String("raw_response", respObj.String()),
			logger.String("request_url", fullURL))
	}

	log.Info("Wikipedia response missing 'query' field - analyzing response structure",
		logger.String("error", queryErr.Error()),
		logger.String("request_url", fullURL),
		logger.String("response_analysis", "checking_for_api_errors"))

	// Check if there's an error field in the response
	if errorObj, errCheck := resp.GetObject("error"); errCheck == nil {
		if errorCode, errCode := errorObj.GetString("code"); errCode == nil {
			if errorInfo, errInfo := errorObj.GetString("info"); errInfo == nil {
				log.Debug("Wikipedia API returned structured error response - normal for missing pages",
					logger.String("error_code", errorCode),
					logger.String("error_info", errorInfo),
					logger.String("error_type", "api_structured_error_expected"),
					logger.String("species_query", params["titles"]),
					logger.String("diagnostic_hint", "wikipedia_api_rejected_request_for_nonexistent_page"))
			}
		}
	} else {
		// No structured error, likely malformed response
		log.Debug("Wikipedia response has no 'query' field and no structured 'error' field",
			logger.String("response_structure_error", queryErr.Error()),
			logger.String("error_type", "malformed_api_response_expected"),
			logger.String("species_query", params["titles"]),
			logger.String("diagnostic_hint", "wikipedia_api_returned_unexpected_format_for_missing_page"))
	}
}

// logPagesMissingError logs diagnostics when the 'pages' field is missing from the query.
func logPagesMissingError(query *jason.Object, params map[string]string, fullURL string, pagesErr error) {
	log := GetLogger().With(logger.String("provider", wikiProviderName))

	// Log the query object structure
	if queryObj, errJson := query.Object(); errJson == nil {
		log.Debug("Wikipedia 'query' object structure when 'pages' field missing",
			logger.String("query_object", queryObj.String()),
			logger.String("request_url", fullURL))
	}

	log.Info("No 'pages' field in Wikipedia query response - analyzing alternative response structures",
		logger.String("pages_error", pagesErr.Error()),
		logger.String("species_query", params["titles"]),
		logger.String("request_url", fullURL),
		logger.String("response_analysis", "checking_redirects_and_normalized_titles"))

	// Check for redirects
	if redirects, redirectErr := query.GetObjectArray("redirects"); redirectErr == nil && len(redirects) > 0 {
		log.Info("Wikipedia response contains redirects but no pages",
			logger.Int("redirect_count", len(redirects)),
			logger.String("error_type", "redirect_without_pages"),
			logger.String("diagnostic_hint", "wikipedia_redirected_query_but_target_page_missing"))
	}

	// Check for normalized titles
	if normalized, normalErr := query.GetObjectArray("normalized"); normalErr == nil && len(normalized) > 0 {
		log.Info("Wikipedia response contains normalized titles but no pages",
			logger.Int("normalized_count", len(normalized)),
			logger.String("error_type", "normalized_title_without_pages"),
			logger.String("diagnostic_hint", "wikipedia_normalized_species_name_but_no_page_found"))
	}

	log.Info("Wikipedia page structure analysis complete - no pages found",
		logger.String("error_type", "no_pages_in_response"),
		logger.String("species_query", params["titles"]),
		logger.String("diagnostic_hint", "species_likely_has_no_wikipedia_page"))
}

// logEmptyPagesArray logs diagnostics when the pages array is empty.
func logEmptyPagesArray(resp *jason.Object, params map[string]string, fullURL string) {
	log := GetLogger().With(logger.String("provider", wikiProviderName))

	log.Debug("Wikipedia returned empty pages array - normal for species without pages",
		logger.String("error_type", "empty_pages_array_expected"),
		logger.String("species_query", params["titles"]),
		logger.String("request_url", fullURL),
		logger.Bool("response_has_query_field", true),
		logger.Int("pages_array_length", 0),
		logger.String("diagnostic_hint", "wikipedia_query_succeeded_but_species_has_no_page"))

	if respObj, errJson := resp.Object(); errJson == nil {
		log.Debug("Full Wikipedia response structure analysis (empty pages)",
			logger.String("response_json", respObj.String()),
			logger.String("request_url", fullURL),
			logger.String("analysis", "complete_api_response_for_debugging"))
	} else {
		log.Debug("Could not serialize response for debugging",
			logger.Error(errJson),
			logger.String("request_url", fullURL))
	}
}

// logFirstPageContent logs the first page content at debug level for troubleshooting.
func logFirstPageContent(pages []*jason.Object, fullURL string) {
	log := GetLogger().With(logger.String("provider", wikiProviderName))
	if firstPageObj, errJson := pages[0].Object(); errJson == nil {
		log.Debug("First page content from API response",
			logger.String("page_content", firstPageObj.String()),
			logger.String("request_url", fullURL))
	} else {
		log.Debug("Could not format first page for logging",
			logger.Error(errJson),
			logger.String("request_url", fullURL))
	}
}

// queryAndGetFirstPageWithLimiter queries Wikipedia with given parameters using the specified rate limiter.
func (l *wikiMediaProvider) queryAndGetFirstPageWithLimiter(ctx context.Context, reqID string, params map[string]string, limiter *rate.Limiter) (*jason.Object, error) {
	log := GetLogger().With(
		logger.String("provider", wikiProviderName),
		logger.String("request_id", reqID),
		logger.String("api_action", params["action"]),
		logger.String("titles", params["titles"]))

	fullURL := buildDebugURL(params)
	log.Info("Querying Wikipedia API", logger.String("debug_full_url", fullURL))

	resp, err := l.queryWithRetryAndLimiter(ctx, reqID, params, limiter)
	if err != nil {
		return nil, err
	}

	logRawResponse(resp, fullURL)
	log.Debug("Parsing pages from API response")

	query, err := resp.GetObject("query")
	if err != nil {
		logQueryMissingError(resp, params, fullURL, err)
		return nil, ErrImageNotFound
	}

	pages, err := query.GetObjectArray("pages")
	if err != nil {
		logPagesMissingError(query, params, fullURL, err)
		return nil, ErrImageNotFound
	}

	if len(pages) == 0 {
		logEmptyPagesArray(resp, params, fullURL)
		return nil, ErrImageNotFound
	}

	logFirstPageContent(pages, fullURL)
	logAPISuccess(reqID, params["titles"], "get_first_page")

	return pages[0], nil
}

// isAllowedToFetch checks if the WikiMedia provider is allowed to make requests
// based on the current configuration. This prevents unnecessary API calls to Wikipedia
// when the provider is not configured for use.
func (l *wikiMediaProvider) isAllowedToFetch() (allowed bool, reason string) {
	settings := conf.Setting()
	if settings == nil {
		// If settings are not available, allow for backward compatibility
		return true, ""
	}

	thumbnails := settings.Realtime.Dashboard.Thumbnails

	// Case 1: WikiMedia is explicitly configured as the provider
	if thumbnails.ImageProvider == wikiProviderName {
		return true, ""
	}

	// Case 2: Auto mode may select WikiMedia
	if thumbnails.ImageProvider == "auto" || thumbnails.ImageProvider == "" {
		return true, ""
	}

	// Case 3: WikiMedia can be used as a fallback
	if thumbnails.FallbackPolicy == fallbackPolicyAll {
		return true, ""
	}

	// WikiMedia is not configured and fallback is disabled
	reason = fmt.Sprintf("provider=%s, fallback=%s",
		thumbnails.ImageProvider, thumbnails.FallbackPolicy)
	return false, reason
}

// FetchWithContext retrieves the bird image for a given scientific name using a context.
// All requests pass through the global 1 req/s limiter; background operations also
// use an additional background-specific limiter for more conservative rate limiting.
func (l *wikiMediaProvider) FetchWithContext(ctx context.Context, scientificName string) (BirdImage, error) {
	// Check if we're allowed to make requests to WikiMedia
	if allowed, reason := l.isAllowedToFetch(); !allowed {
		GetLogger().Debug("WikiMedia fetch blocked by configuration",
			logger.String("provider", wikiProviderName),
			logger.String("scientific_name", scientificName),
			logger.String("config_reason", reason),
			logger.String("context", "background_operation"),
			logger.String("hint", "WikiMedia is not the configured provider and fallback is disabled"))

		return BirdImage{}, ErrProviderNotConfigured
	}

	// Check if this is a background operation
	isBackground := false
	if ctx != nil {
		if bg, ok := ctx.Value(backgroundOperationKey).(bool); ok && bg {
			isBackground = true
		}
	}

	// Only use rate limiter for background operations
	var limiter *rate.Limiter
	if isBackground {
		limiter = l.backgroundLimiter
	}

	return l.fetchWithLimiter(ctx, scientificName, limiter)
}

// Fetch retrieves the bird image for a given scientific name.
// It queries for the thumbnail and author information, then constructs a BirdImage.
// User requests through this method are not rate limited.
func (l *wikiMediaProvider) Fetch(scientificName string) (BirdImage, error) {
	// Check if we're allowed to make requests to WikiMedia
	if allowed, reason := l.isAllowedToFetch(); !allowed {
		GetLogger().Debug("WikiMedia fetch blocked by configuration",
			logger.String("provider", wikiProviderName),
			logger.String("scientific_name", scientificName),
			logger.String("config_reason", reason),
			logger.String("hint", "WikiMedia is not the configured provider and fallback is disabled"))

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
	log.Info("Starting Wikipedia image fetch - operation details",
		logger.String("operation", "fetch_image"),
		logger.String("species_query", scientificName),
		logger.String("rate_limit_type", rateLimitType),
		logger.String("diagnostic_info", "beginning_wikipedia_image_fetch_operation"))

	thumbnailURL, thumbnailSourceFile, err := l.queryThumbnail(ctx, reqID, scientificName, limiter)
	if err != nil {
		// Error already logged in queryThumbnail
		return BirdImage{}, err // Pass through the user-friendly error from queryThumbnail
	}
	log.Info("Thumbnail retrieved successfully",
		logger.String("thumbnail_url", thumbnailURL),
		logger.String("source_file", thumbnailSourceFile))

	authorInfo, err := l.queryAuthorInfo(ctx, reqID, thumbnailSourceFile, limiter)
	if err != nil {
		// If it's just a "not found" error, continue with default author info
		// Only fail for actual errors (network issues, parsing failures)
		if errors.Is(err, ErrImageNotFound) {
			log.Debug("Author info not available, using defaults")
			// Use default author info rather than failing
			authorInfo = &wikiMediaAuthor{
				name:        unknownMetadataValue,
				URL:         "",
				licenseName: unknownMetadataValue,
				licenseURL:  "",
			}
		} else {
			// This is a real error (network, API issues), so we should report it
			log.Error("Failed to fetch author info", logger.Error(err))
			enhancedErr := errors.Newf("unable to retrieve image attribution for species: %s", scientificName).
				Component("imageprovider").
				Category(errors.CategoryImageFetch).
				Context("provider", wikiProviderName).
				Context("request_id", reqID).
				Context("scientific_name", scientificName).
				Context("thumbnail_source_file", thumbnailSourceFile).
				Context("operation", "fetch_author_info").
				Build()
			return BirdImage{}, enhancedErr
		}
	}
	log.Info("Author info retrieved successfully",
		logger.String("author", authorInfo.name),
		logger.String("license", authorInfo.licenseName))

	result := BirdImage{
		URL:            thumbnailURL,
		ScientificName: scientificName,
		AuthorName:     authorInfo.name,
		AuthorURL:      authorInfo.URL,
		LicenseName:    authorInfo.licenseName,
		LicenseURL:     authorInfo.licenseURL,
		SourceProvider: wikiProviderName, // Set the provider name
	}

	// Enhanced success logging with complete operation summary
	logAPISuccess(reqID, scientificName, "complete_fetch_operation")

	return result, nil
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
		"redirects":     "",
	}

	page, err := l.queryAndGetFirstPageWithLimiter(ctx, reqID, params, limiter)
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

	thumbnailURL, err = page.GetString("thumbnail", "source")
	if err != nil {
		log.Debug("No thumbnail URL found in page data", logger.Error(err))
		// This is common for pages without images or with non-free images
		// Don't create telemetry noise - treat as "not found"
		return "", "", ErrImageNotFound
	}

	fileName, err = page.GetString("pageimage")
	if err != nil {
		log.Debug("No pageimage filename found in page data", logger.Error(err))
		// This is common for pages without proper image metadata
		// Don't create telemetry noise - treat as "not found"
		return "", "", ErrImageNotFound
	}

	log.Debug("Successfully retrieved thumbnail URL and filename",
		logger.String("url", thumbnailURL),
		logger.String("filename", fileName))

	return thumbnailURL, fileName, nil
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

	page, err := l.queryAndGetFirstPageWithLimiter(ctx, reqID, params, limiter)
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
	imgInfo, err := page.GetObjectArray("imageinfo")
	if err != nil || len(imgInfo) == 0 {
		log.Debug("No imageinfo found in file page",
			logger.Error(err),
			logger.Int("array_len", len(imgInfo)))
		// This is common for files without metadata or processing issues
		// Don't create telemetry noise - treat as "not found"
		return nil, ErrImageNotFound
	}

	extMetadata, err := imgInfo[0].GetObject("extmetadata")
	if err != nil {
		log.Debug("No extmetadata found in imageinfo", logger.Error(err))
		// This is common for files without extended metadata
		// Don't create telemetry noise - treat as "not found"
		return nil, ErrImageNotFound
	}

	// Extract specific fields (Artist, LicenseShortName, LicenseUrl)
	// These fields are optional - missing fields are expected and logged at debug level
	artistHTML, err := extMetadata.GetString("Artist", "value")
	if err != nil {
		log.Debug("Artist field not found in extmetadata", logger.Error(err))
	}
	licenseName, err := extMetadata.GetString("LicenseShortName", "value")
	if err != nil {
		log.Debug("LicenseShortName field not found in extmetadata", logger.Error(err))
	}
	licenseURL, err := extMetadata.GetString("LicenseUrl", "value")
	if err != nil {
		log.Debug("LicenseUrl field not found in extmetadata", logger.Error(err))
	}

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

// parseAuthorFromHTML extracts author name and URL from HTML, with fallbacks.
// Returns unknownMetadataValue for empty input or when extraction fails.
func parseAuthorFromHTML(artistHTML string) (authorName, authorURL string) {
	if artistHTML == "" {
		return unknownMetadataValue, ""
	}

	authorURL, authorName, err := extractArtistInfo(artistHTML)
	if err != nil {
		// Fallback to plain text version if parsing failed
		authorName = html2text.HTML2Text(artistHTML)
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
	text = html2text.HTML2Text(htmlStr)
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

// extractText extracts the inner text from an anchor tag.
func extractText(link *html.Node) string {
	if link.FirstChild != nil {
		var b bytes.Buffer
		err := html.Render(&b, link.FirstChild)
		if err != nil {
			return ""
		}
		return html2text.HTML2Text(b.String())
	}
	return ""
}
