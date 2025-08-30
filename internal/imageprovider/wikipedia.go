// wikipedia.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"cgt.name/pkg/go-mwclient"
	"github.com/antonholmquist/jason"
	"github.com/google/uuid"
	"github.com/k3a/html2text"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"golang.org/x/net/html"
	"golang.org/x/time/rate"
)

const (
	wikiProviderName = "wikimedia"

	// User-Agent constants following Wikimedia robot policy
	// https://foundation.wikimedia.org/wiki/Policy:Wikimedia_Foundation_User-Agent_Policy
	userAgentName    = "BirdNET-Go"
	userAgentContact = "https://github.com/tphakala/birdnet-go"
	userAgentLibrary = "Go-HTTP-Client"
)

// wikiMediaProvider implements the ImageProvider interface for Wikipedia.
type wikiMediaProvider struct {
	client            *mwclient.Client
	debug             bool
	backgroundLimiter *rate.Limiter // For background refresh operations
	maxRetries        int
}

// wikiMediaAuthor represents the author information for a Wikipedia image.
type wikiMediaAuthor struct {
	name        string
	URL         string
	licenseName string
	licenseURL  string
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

// validateUserAgent logs the constructed user-agent for debugging purposes
func validateUserAgent(logger *slog.Logger, appVersion string) {
	userAgent := buildUserAgent(appVersion)
	logger.Info("Wikipedia user-agent validation",
		"user_agent", userAgent,
		"complies_with_policy", "https://foundation.wikimedia.org/wiki/Policy:User-Agent_policy",
		"contains_app_name", userAgentName,
		"contains_version", appVersion,
		"contains_contact", userAgentContact,
		"contains_library", userAgentLibrary,
		"go_version", runtime.Version())
}

// checkUserAgentPolicyViolation checks for Wikipedia user-agent policy violations and returns an error if detected
func checkUserAgentPolicyViolation(reqID string, statusCode int, responseBody []byte, userAgent string, logger *slog.Logger) error {
	if statusCode != 403 {
		return nil
	}

	bodyStr := string(responseBody)
	if !strings.Contains(bodyStr, "User-Agent") && !strings.Contains(bodyStr, "robot policy") {
		return nil
	}

	logger.Error("Wikipedia blocked request - User-Agent policy violation, stopping retries",
		"error_message", bodyStr,
		"user_agent", userAgent,
		"policy_url", "https://foundation.wikimedia.org/wiki/Policy:User-Agent_policy",
		"action_required", "User-Agent needs to be updated to comply with policy")

	// This is a permanent failure - return immediately without retrying
	return errors.Newf("Wikipedia user-agent policy violation: %s", bodyStr).
		Component("imageprovider").
		Category(errors.CategoryNetwork).
		Context("provider", wikiProviderName).
		Context("request_id", reqID).
		Context("operation", "user_agent_policy_violation").
		Context("status_code", statusCode).
		Context("response_body", bodyStr).
		Context("user_agent", userAgent).
		Context("permanent_failure", true).
		Build()
}

// handleJSONParsingError handles JSON parsing errors by making a direct HTTP request to diagnose the issue
func (l *wikiMediaProvider) handleJSONParsingError(reqID, fullURL string, err error, settings *conf.Settings, attemptLogger *slog.Logger, attempt int) error {
	// Make a direct HTTP request to capture the actual error response
	req, _ := http.NewRequest("GET", fullURL, http.NoBody)
	req.Header.Set("User-Agent", buildUserAgent(settings.Version))
	httpClient := &http.Client{Timeout: 10 * time.Second}

	debugResp, debugErr := httpClient.Do(req)
	if debugErr != nil {
		attemptLogger.Debug("Unable to diagnose API error",
			"diagnostic_error", debugErr.Error(),
			"original_error", err.Error())
		return nil // Continue with normal retry logic
	}

	defer func() {
		if closeErr := debugResp.Body.Close(); closeErr != nil {
			attemptLogger.Debug("Failed to close debug response body", "error", closeErr)
		}
	}()

	body, readErr := io.ReadAll(debugResp.Body)
	if readErr != nil {
		attemptLogger.Debug("Failed to read debug response body", "error", readErr)
		return nil // Continue with normal retry logic
	}

	// Log the full error response at WARN level
	if debugResp.StatusCode != 200 {
		attemptLogger.Warn("Wikipedia API error response",
			"status_code", debugResp.StatusCode,
			"content_type", debugResp.Header.Get("Content-Type"),
			"response_body", string(body),
			"requested_url", fullURL,
			"attempt", attempt+1,
			"max_attempts", l.maxRetries)

		// Check for user-agent policy violations - this is a permanent failure, don't retry
		if policyErr := checkUserAgentPolicyViolation(reqID, debugResp.StatusCode, body, req.Header.Get("User-Agent"), attemptLogger); policyErr != nil {
			return policyErr // Return immediately, don't retry
		}
	} else {
		attemptLogger.Debug("Wikipedia returned non-JSON content",
			"parsing_error", err.Error(),
			"attempt", attempt+1,
			"will_retry", attempt < l.maxRetries-1)
	}

	return nil // Continue with normal retry logic
}

// mwclientDebugWriter implements io.Writer to capture mwclient debug output
type mwclientDebugWriter struct {
	logger *slog.Logger
}

func (w *mwclientDebugWriter) Write(p []byte) (n int, err error) {
	w.logger.Debug("mwclient debug output", "data", string(p))
	return len(p), nil
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
func logAPIError(logger *slog.Logger, category apiErrorCategory, reqID, species string, params map[string]string, err error) {
	logger.Error("Wikipedia API error - categorized for diagnostics",
		"error_category", category.Type,
		"error_description", category.Description,
		"error_severity", category.Severity,
		"actionable", category.Actionable,
		"request_id", reqID,
		"species_query", species,
		"api_params", params,
		"original_error", err.Error(),
		"troubleshooting_hint", getTroubleshootingHint(category))
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
func logAPISuccess(logger *slog.Logger, reqID, species, operation string, params map[string]string, responseMetadata map[string]interface{}) {
	logger.Info("Wikipedia API success - operation completed normally",
		"success", true,
		"request_id", reqID,
		"species_query", species,
		"operation", operation,
		"api_params", params,
		"response_metadata", responseMetadata,
		"diagnostic_info", "normal_successful_operation_for_baseline_metrics")
}

// NewWikiMediaProvider creates a new Wikipedia media provider.
// It initializes a new mwclient for interacting with the Wikipedia API.
func NewWikiMediaProvider() (*wikiMediaProvider, error) {
	// Use the shared imageProviderLogger
	logger := imageProviderLogger.With("provider", wikiProviderName)
	logger.Info("Initializing WikiMedia provider")
	settings := conf.Setting()

	// Enable debug logging if configured
	if settings.Realtime.Dashboard.Thumbnails.Debug {
		SetDebugLogging(true)
		logger.Info("Debug mode enabled for WikiMedia provider", "debug", true)
	}

	apiURL := "https://en.wikipedia.org/w/api.php"
	userAgent := buildUserAgent(settings.Version)

	// Validate and log user-agent for debugging - using WARN level to ensure visibility
	validateUserAgent(logger, settings.Version)
	logger.Warn("WikiMedia provider initialization - user-agent constructed",
		"user_agent", buildUserAgent(settings.Version),
		"app_version", settings.Version)

	logger.Debug("Creating mwclient with API URL", "api_url", apiURL, "user_agent", userAgent)
	logger.Warn("WikiMedia mwclient created with user-agent", "user_agent", userAgent)
	client, err := mwclient.New(apiURL, userAgent)
	if err != nil {
		enhancedErr := errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("operation", "create_mwclient").
			Context("api_url", "https://en.wikipedia.org/w/api.php").
			Build()
		logger.Error("Failed to create mwclient for Wikipedia API", "error", enhancedErr)
		return nil, enhancedErr
	}

	// Rate limiting is only applied to background cache refresh operations
	// User requests are not rate limited to ensure UI responsiveness
	// Background operations: 2 requests per second to respect Wikipedia's rate limits
	backgroundLimiter := rate.NewLimiter(rate.Limit(2), 2)
	logger.Info("WikiMedia provider initialized", "user_rate_limit", "none", "background_rate_limit_rps", 2)

	// Enable debug output for mwclient if debug mode is on
	if settings.Realtime.Dashboard.Thumbnails.Debug {
		// Create a debug writer that logs to our logger
		debugWriter := &mwclientDebugWriter{logger: logger}
		client.SetDebug(debugWriter)
		logger.Debug("Enabled mwclient debug output")
	}

	return &wikiMediaProvider{
		client:            client,
		debug:             settings.Realtime.Dashboard.Thumbnails.Debug,
		backgroundLimiter: backgroundLimiter,
		maxRetries:        3,
	}, nil
}

// queryWithRetryAndLimiter performs a query with retry logic using the specified rate limiter.
func (l *wikiMediaProvider) queryWithRetryAndLimiter(reqID string, params map[string]string, limiter *rate.Limiter) (*jason.Object, error) {
	logger := imageProviderLogger.With("provider", wikiProviderName, "request_id", reqID, "api_action", params["action"])
	var lastErr error
	for attempt := 0; attempt < l.maxRetries; attempt++ {
		attemptLogger := logger.With("attempt", attempt+1, "max_attempts", l.maxRetries)
		attemptLogger.Debug("Attempting Wikipedia API request",
			"species", params["titles"])
		// Wait for rate limiter if one is provided (only for background operations)
		if limiter != nil {
			attemptLogger.Debug("Waiting for rate limiter")
			err := limiter.Wait(context.Background()) // Using Background context for limiter wait
			if err != nil {
				enhancedErr := errors.New(err).
					Component("imageprovider").
					Category(errors.CategoryNetwork).
					Context("provider", wikiProviderName).
					Context("request_id", reqID).
					Context("operation", "rate_limiter_wait").
					Build()
				attemptLogger.Error("Rate limiter error", "error", enhancedErr)
				// Don't retry on limiter error, return immediately
				return nil, enhancedErr
			}
		} else {
			attemptLogger.Debug("No rate limiting applied (user request)")
		}

		// Construct the full URL for debug logging with proper URL encoding
		queryParams := make([]string, 0, len(params))
		for k, v := range params {
			queryParams = append(queryParams, k+"="+url.QueryEscape(v))
		}
		fullURL := "https://en.wikipedia.org/w/api.php?" + strings.Join(queryParams, "&")
		attemptLogger.Debug("Sending GET request to Wikipedia API", "full_url", fullURL, "params", params)
		resp, err := l.client.Get(params)
		if err == nil {
			// Log successful response with debug details
			if respObj, errJson := resp.Object(); errJson == nil {
				responseStr := respObj.String()
				previewLen := 500
				if len(responseStr) < previewLen {
					previewLen = len(responseStr)
				}
				attemptLogger.Debug("API request successful - raw response received",
					"response_preview", responseStr[:previewLen],
					"response_size", len(responseStr))
			} else {
				attemptLogger.Debug("API request successful")
			}
			return resp, nil // Success
		}

		// Check if this is a JSON parsing error (Wikipedia returned HTML instead of JSON)
		if strings.Contains(err.Error(), "invalid character") && strings.Contains(err.Error(), "looking for beginning of value") {
			// Get settings to build proper user agent
			settings := conf.Setting()
			if policyErr := l.handleJSONParsingError(reqID, fullURL, err, settings, attemptLogger, attempt); policyErr != nil {
				return nil, policyErr // Return immediately for permanent failures
			}
		}

		lastErr = err
		attemptLogger.Warn("API request failed",
			"error", err,
			"attempted_url", fullURL,
			"attempt", attempt+1,
			"will_retry", attempt < l.maxRetries-1)
		// if l.debug {
		// 	log.Printf("Debug: API request attempt %d failed: %v", attempt+1, err)
		// }

		// Wait before retry (exponential backoff)
		waitDuration := time.Second * time.Duration(1<<attempt)
		attemptLogger.Debug("Waiting before retry", "duration", waitDuration)
		time.Sleep(waitDuration)
	}

	// Use categorized error logging for final failure
	logAPIError(logger, errorCategoryNetworkFailure, reqID, params["titles"], params, lastErr)

	enhancedErr := errors.New(lastErr).
		Component("imageprovider").
		Category(errors.CategoryNetwork).
		Context("provider", wikiProviderName).
		Context("request_id", reqID).
		Context("max_retries", l.maxRetries).
		Context("operation", "query_with_retry").
		Context("api_action", params["action"]).
		Context("species_query", params["titles"]).
		Context("error_category", errorCategoryNetworkFailure.Type).
		Context("error_severity", errorCategoryNetworkFailure.Severity).
		Context("actionable", errorCategoryNetworkFailure.Actionable).
		Context("final_error", lastErr.Error()).
		Build()
	return nil, enhancedErr
}

// queryAndGetFirstPageWithLimiter queries Wikipedia with given parameters using the specified rate limiter.
func (l *wikiMediaProvider) queryAndGetFirstPageWithLimiter(reqID string, params map[string]string, limiter *rate.Limiter) (*jason.Object, error) {
	logger := imageProviderLogger.With("provider", wikiProviderName, "request_id", reqID, "api_action", params["action"], "titles", params["titles"])
	// Construct URL for debug logging with proper URL encoding
	queryParams := make([]string, 0, len(params))
	for k, v := range params {
		queryParams = append(queryParams, k+"="+url.QueryEscape(v))
	}
	fullURL := "https://en.wikipedia.org/w/api.php?" + strings.Join(queryParams, "&")
	logger.Info("Querying Wikipedia API", "debug_full_url", fullURL)
	// if l.debug {
	// 	log.Printf("[%s] Debug: Querying Wikipedia API with params: %v", reqID, params)
	// }

	resp, err := l.queryWithRetryAndLimiter(reqID, params, limiter)
	if err != nil {
		// Error already logged and enhanced in queryWithRetry
		return nil, err
	}

	// Log raw response at Debug level for troubleshooting
	if logger.Enabled(context.Background(), slog.LevelDebug) {
		if respObj, errJson := resp.Object(); errJson == nil {
			responseStr := respObj.String()
			logger.Debug("Raw Wikipedia API response received",
				"response_full", responseStr,
				"response_length", len(responseStr),
				"request_url", fullURL)
		} else {
			logger.Debug("Failed to format raw response for logging", "error", errJson, "request_url", fullURL)
		}
	}

	logger.Debug("Parsing pages from API response")

	// First check if the response has a query field at all
	query, err := resp.GetObject("query")
	if err != nil {
		// Log the complete raw response when query field is missing
		if respObj, errJson := resp.Object(); errJson == nil {
			logger.Debug("Wikipedia response missing 'query' field - full response dump",
				"raw_response", respObj.String(),
				"request_url", fullURL)
		}
		// Enhanced logging for missing query field
		logger.Info("Wikipedia response missing 'query' field - analyzing response structure",
			"error", err.Error(),
			"request_params", params,
			"request_url", fullURL,
			"response_analysis", "checking_for_api_errors")

		// Check if there's an error field in the response
		if errorObj, errCheck := resp.GetObject("error"); errCheck == nil {
			if errorCode, errCode := errorObj.GetString("code"); errCode == nil {
				if errorInfo, errInfo := errorObj.GetString("info"); errInfo == nil {
					logger.Debug("Wikipedia API returned structured error response - normal for missing pages",
						"error_code", errorCode,
						"error_info", errorInfo,
						"error_type", "api_structured_error_expected",
						"species_query", params["titles"],
						"diagnostic_hint", "wikipedia_api_rejected_request_for_nonexistent_page")
				}
			}
		} else {
			// No structured error, likely malformed response - this might be more serious
			logger.Debug("Wikipedia response has no 'query' field and no structured 'error' field",
				"response_structure_error", err.Error(),
				"error_type", "malformed_api_response_expected",
				"species_query", params["titles"],
				"diagnostic_hint", "wikipedia_api_returned_unexpected_format_for_missing_page")
		}

		// This is likely a "not found" scenario, not an error worth reporting to telemetry
		return nil, ErrImageNotFound
	}

	// Try to get pages array from the query object
	pages, err := query.GetObjectArray("pages")
	if err != nil {
		// Log the query object structure when pages field is missing
		if queryObj, errJson := query.Object(); errJson == nil {
			logger.Debug("Wikipedia 'query' object structure when 'pages' field missing",
				"query_object", queryObj.String(),
				"request_url", fullURL)
		}
		// Enhanced logging for missing pages array
		logger.Info("No 'pages' field in Wikipedia query response - analyzing alternative response structures",
			"pages_error", err.Error(),
			"species_query", params["titles"],
			"request_url", fullURL,
			"response_analysis", "checking_redirects_and_normalized_titles")

		// Check for alternative structures that might indicate page issues
		// Check for redirects
		if redirects, redirectErr := query.GetObjectArray("redirects"); redirectErr == nil && len(redirects) > 0 {
			logger.Info("Wikipedia response contains redirects but no pages",
				"redirect_count", len(redirects),
				"error_type", "redirect_without_pages",
				"diagnostic_hint", "wikipedia_redirected_query_but_target_page_missing")
		}

		// Check for normalized titles
		if normalized, normalErr := query.GetObjectArray("normalized"); normalErr == nil && len(normalized) > 0 {
			logger.Info("Wikipedia response contains normalized titles but no pages",
				"normalized_count", len(normalized),
				"error_type", "normalized_title_without_pages",
				"diagnostic_hint", "wikipedia_normalized_species_name_but_no_page_found")
		}

		// This is a common scenario for species without Wikipedia pages
		// Enhanced diagnostic logging
		logger.Info("Wikipedia page structure analysis complete - no pages found",
			"error_type", "no_pages_in_response",
			"species_query", params["titles"],
			"diagnostic_hint", "species_likely_has_no_wikipedia_page")
		return nil, ErrImageNotFound
	}

	if len(pages) == 0 {
		// Enhanced logging for empty pages array - this is normal for missing species
		logger.Debug("Wikipedia returned empty pages array - normal for species without pages",
			"error_type", "empty_pages_array_expected",
			"species_query", params["titles"],
			"request_url", fullURL,
			"response_has_query_field", true,
			"pages_array_length", 0,
			"diagnostic_hint", "wikipedia_query_succeeded_but_species_has_no_page")

		// Always log full response structure for debugging
		if respObj, errJson := resp.Object(); errJson == nil {
			logger.Debug("Full Wikipedia response structure analysis (empty pages)",
				"response_json", respObj.String(),
				"request_url", fullURL,
				"analysis", "complete_api_response_for_debugging")
		} else {
			logger.Debug("Could not serialize response for debugging", "serialization_error", errJson, "request_url", fullURL)
		}

		// Return specific error indicating page not found
		return nil, ErrImageNotFound
	}

	// Log first page content at Debug level for troubleshooting
	if logger.Enabled(context.Background(), slog.LevelDebug) {
		if firstPageObj, errJson := pages[0].Object(); errJson == nil {
			logger.Debug("First page content from API response",
				"page_content", firstPageObj.String(),
				"request_url", fullURL)
		} else {
			logger.Debug("Could not format first page for logging", "error", errJson, "request_url", fullURL)
		}
	}

	// Use success logging function
	responseMetadata := map[string]interface{}{
		"pages_found":              len(pages),
		"response_has_query_field": true,
		"pages_array_length":       len(pages),
	}
	logAPISuccess(logger, reqID, params["titles"], "get_first_page", params, responseMetadata)

	return pages[0], nil
}

// FetchWithContext retrieves the bird image for a given scientific name using a context.
// If the context indicates a background operation, it uses the background rate limiter.
// User requests are not rate limited.
func (l *wikiMediaProvider) FetchWithContext(ctx context.Context, scientificName string) (BirdImage, error) {
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
	// For user requests, limiter remains nil (no rate limiting)

	return l.fetchWithLimiter(scientificName, limiter)
}

// Fetch retrieves the bird image for a given scientific name.
// It queries for the thumbnail and author information, then constructs a BirdImage.
// User requests through this method are not rate limited.
func (l *wikiMediaProvider) Fetch(scientificName string) (BirdImage, error) {
	return l.fetchWithLimiter(scientificName, nil) // No rate limiting for user requests
}

// fetchWithLimiter retrieves the bird image using the specified rate limiter.
func (l *wikiMediaProvider) fetchWithLimiter(scientificName string, limiter *rate.Limiter) (BirdImage, error) {
	reqID := uuid.New().String()[:8] // Using first 8 chars for brevity
	logger := imageProviderLogger.With("provider", wikiProviderName, "scientific_name", scientificName, "request_id", reqID)

	// Enhanced start logging with operation context
	rateLimitType := "none"
	if limiter != nil {
		rateLimitType = "background"
	}
	logger.Info("Starting Wikipedia image fetch - operation details",
		"operation", "fetch_image",
		"species_query", scientificName,
		"rate_limit_type", rateLimitType,
		"request_id", reqID,
		"provider", wikiProviderName,
		"diagnostic_info", "beginning_wikipedia_image_fetch_operation")

	thumbnailURL, thumbnailSourceFile, err := l.queryThumbnail(reqID, scientificName, limiter)
	if err != nil {
		// Error already logged in queryThumbnail
		// if l.debug {
		// 	log.Printf("[%s] Debug: Failed to fetch thumbnail for %s: %v", reqID, scientificName, err)
		// }
		return BirdImage{}, err // Pass through the user-friendly error from queryThumbnail
	}
	logger = logger.With("thumbnail_url", thumbnailURL, "source_file", thumbnailSourceFile)
	logger.Info("Thumbnail retrieved successfully")

	authorInfo, err := l.queryAuthorInfo(reqID, thumbnailSourceFile, limiter)
	if err != nil {
		// If it's just a "not found" error, continue with default author info
		// Only fail for actual errors (network issues, parsing failures)
		if errors.Is(err, ErrImageNotFound) {
			logger.Debug("Author info not available, using defaults")
			// Use default author info rather than failing
			authorInfo = &wikiMediaAuthor{
				name:        "Unknown",
				URL:         "",
				licenseName: "Unknown",
				licenseURL:  "",
			}
		} else {
			// This is a real error (network, API issues), so we should report it
			logger.Error("Failed to fetch author info", "error", err)
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
	logger = logger.With("author", authorInfo.name, "license", authorInfo.licenseName)
	logger.Info("Author info retrieved successfully")

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
	successMetadata := map[string]interface{}{
		"thumbnail_url":   thumbnailURL,
		"source_file":     thumbnailSourceFile,
		"author_name":     authorInfo.name,
		"license_name":    authorInfo.licenseName,
		"rate_limit_type": rateLimitType,
		"has_author_url":  authorInfo.URL != "",
		"has_license_url": authorInfo.licenseURL != "",
	}
	logAPISuccess(logger, reqID, scientificName, "complete_fetch_operation", map[string]string{"operation": "full_image_fetch"}, successMetadata)

	return result, nil
}

// queryThumbnail queries Wikipedia for the thumbnail image of the given scientific name.
// It returns the URL and file name of the thumbnail.
func (l *wikiMediaProvider) queryThumbnail(reqID, scientificName string, limiter *rate.Limiter) (thumbnailURL, fileName string, err error) {
	logger := imageProviderLogger.With("provider", wikiProviderName, "scientific_name", scientificName, "request_id", reqID)
	logger.Debug("Querying thumbnail",
		"scientific_name", scientificName)

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

	page, err := l.queryAndGetFirstPageWithLimiter(reqID, params, limiter)
	if err != nil {
		// Log based on error type
		if errors.Is(err, ErrImageNotFound) {
			logger.Warn("No Wikipedia page found for species")
		} else {
			logger.Error("Failed to query thumbnail page", "error", err)
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
		logger.Debug("No thumbnail URL found in page data", "error", err)
		// This is common for pages without images or with non-free images
		// Don't create telemetry noise - treat as "not found"
		return "", "", ErrImageNotFound
	}

	fileName, err = page.GetString("pageimage")
	if err != nil {
		logger.Debug("No pageimage filename found in page data", "error", err)
		// This is common for pages without proper image metadata
		// Don't create telemetry noise - treat as "not found"
		return "", "", ErrImageNotFound
	}

	logger.Debug("Successfully retrieved thumbnail URL and filename",
		"url", thumbnailURL,
		"filename", fileName)

	return thumbnailURL, fileName, nil
}

// queryAuthorInfo queries Wikipedia for the author information of the given thumbnail URL.
// It returns a wikiMediaAuthor struct containing the author and license information.
func (l *wikiMediaProvider) queryAuthorInfo(reqID, thumbnailFileName string, limiter *rate.Limiter) (*wikiMediaAuthor, error) {
	logger := imageProviderLogger.With("provider", wikiProviderName, "request_id", reqID, "filename", thumbnailFileName)
	logger.Debug("Querying author info",
		"filename", thumbnailFileName,
		"file_title", "File:"+thumbnailFileName)

	params := map[string]string{
		"action":        "query",
		"format":        "json",
		"formatversion": "2",
		"prop":          "imageinfo",
		"iiprop":        "extmetadata",
		"titles":        "File:" + thumbnailFileName, // Use filename here
		"redirects":     "",
	}

	page, err := l.queryAndGetFirstPageWithLimiter(reqID, params, limiter)
	if err != nil {
		// Log based on error type
		if errors.Is(err, ErrImageNotFound) {
			logger.Warn("No Wikipedia file page found for image filename")
		} else {
			logger.Error("Failed to query author info page", "error", err)
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
	logger.Debug("Extracting metadata from imageinfo response")
	imgInfo, err := page.GetObjectArray("imageinfo")
	if err != nil || len(imgInfo) == 0 {
		logger.Debug("No imageinfo found in file page", "error", err, "array_len", len(imgInfo))
		// This is common for files without metadata or processing issues
		// Don't create telemetry noise - treat as "not found"
		return nil, ErrImageNotFound
	}

	extMetadata, err := imgInfo[0].GetObject("extmetadata")
	if err != nil {
		logger.Debug("No extmetadata found in imageinfo", "error", err)
		// This is common for files without extended metadata
		// Don't create telemetry noise - treat as "not found"
		return nil, ErrImageNotFound
	}

	// Extract specific fields (Artist, LicenseShortName, LicenseUrl)
	artistHTML, _ := extMetadata.GetString("Artist", "value")
	licenseName, _ := extMetadata.GetString("LicenseShortName", "value")
	licenseURL, _ := extMetadata.GetString("LicenseUrl", "value")

	logger.Debug("Extracted raw metadata fields", "artist_html_len", len(artistHTML), "license_name", licenseName, "license_url", licenseURL)

	// Parse artist HTML to get name and URL
	authorName, authorURL := "", ""
	if artistHTML != "" {
		authorURL, authorName, err = extractArtistInfo(artistHTML)
		if err != nil {
			// Log error but continue, attribution might just be text
			logger.Warn("Failed to parse artist HTML, using plain text if available", "html", artistHTML, "error", err)
			// Fallback to plain text version if parsing failed
			authorName = html2text.HTML2Text(artistHTML)
		} else {
			logger.Debug("Parsed artist info from HTML", "name", authorName, "url", authorURL)
		}
	}

	// Handle cases where author might still be empty
	if authorName == "" {
		logger.Warn("Author name could not be extracted")
		authorName = "Unknown"
	}
	if licenseName == "" {
		logger.Warn("License name could not be extracted")
		licenseName = "Unknown"
	}

	logger.Debug("Final extracted author and license info", "author_name", authorName, "author_url", authorURL, "license_name", licenseName, "license_url", licenseURL)
	return &wikiMediaAuthor{
		name:        authorName,
		URL:         authorURL,
		licenseName: licenseName,
		licenseURL:  licenseURL,
	}, nil
}

// extractArtistInfo extracts the artist's name and URL from the HTML string.
func extractArtistInfo(htmlStr string) (href, text string, err error) {
	logger := imageProviderLogger.With("provider", wikiProviderName)
	logger.Debug("Attempting to extract artist info from HTML", "html_len", len(htmlStr))
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		logger.Error("Failed to parse artist HTML", "error", err)
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
		logger.Debug("Found Wikipedia user link for artist", "href", href, "text", text)
		return href, text, nil
	}

	// Fallback: Find the first link if no specific user link is found
	allLinks := findLinks(doc)
	if len(allLinks) > 0 {
		href = extractHref(allLinks[0])
		text = extractText(allLinks[0])
		logger.Debug("No user link found, falling back to first available link", "href", href, "text", text)
		return href, text, nil
	}

	// Fallback: No links found, return plain text
	text = html2text.HTML2Text(htmlStr)
	logger.Debug("No links found in artist HTML, returning plain text", "text", text)
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
