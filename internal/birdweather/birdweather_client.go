// birdweather.go this code implements a BirdWeather API client for uploading soundscapes and detections.
package birdweather

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// GetLogger returns the birdweather package logger
func GetLogger() logger.Logger {
	return logger.Global().Module("birdweather")
}

// targetIntegratedLoudnessLUFS defines the target loudness for normalization.
// EBU R128 standard target is -23 LUFS.
const targetIntegratedLoudnessLUFS = -23.0

// HTTP and timeout constants
const (
	// httpClientTimeout is the default timeout for HTTP requests
	httpClientTimeout = 45 * time.Second

	// encodingTimeout is the timeout for audio encoding operations
	encodingTimeout = 30 * time.Second

	// detectionDurationSeconds is the duration added to timestamp for end time
	detectionDurationSeconds = 3
)

// BirdNET algorithm constants
const (
	// birdnetAlgorithmVersion is the BirdNET model version identifier for API submissions
	birdnetAlgorithmVersion = "2p4"
)

// Geographic constants
const (
	// metersPerDegree is the approximate number of meters in one degree of latitude
	metersPerDegree = 111000.0

	// coordinatePrecisionFactor is used to truncate coordinates to 4 decimal places
	coordinatePrecisionFactor = 10000.0

	// randomOffsetMultiplier is used in coordinate randomization
	randomOffsetMultiplier = 2.0

	// randomCenterOffset is used to center random values around zero (-0.5 to +0.5)
	randomCenterOffset = 0.5
)

// HTML/Response preview constants
const (
	// errorSnippetBefore is the number of characters to show before an error pattern
	errorSnippetBefore = 50

	// errorSnippetAfter is the number of characters to show after an error pattern
	errorSnippetAfter = 100

	// maxHTMLPreview is the maximum length of HTML preview in error messages
	maxHTMLPreview = 200

	// maxResponsePreview is the maximum length of response preview in logs
	maxResponsePreview = 500
)

// File permission constants
const (
	// dirPermission is the permission mode for created directories
	dirPermission = 0o750

	// filePermission is the permission mode for created files
	filePermission = 0o600
)

// SoundscapeResponse represents the JSON structure of the response from the Birdweather API when uploading a soundscape.
type SoundscapeResponse struct {
	Success    bool `json:"success"`
	Soundscape struct {
		ID        int     `json:"id"`
		StationID int     `json:"stationId"`
		Timestamp string  `json:"timestamp"`
		URL       *string `json:"url"` // Pointer to handle null
		Filesize  int     `json:"filesize"`
		Extension string  `json:"extension"`
		Duration  float64 `json:"duration"` // Duration in seconds
	} `json:"soundscape"`
}

// BwClient holds the configuration for interacting with the Birdweather API.
type BwClient struct {
	Settings      *conf.Settings
	BirdweatherID string
	Accuracy      float64
	Latitude      float64
	Longitude     float64
	HTTPClient    *http.Client
	lastGainWarn  atomic.Int64 // unix timestamp of last WARN-level gain log

	// circuitBreaker guards outbound HTTP calls to the BirdWeather API. When the
	// remote endpoint starts failing consistently the breaker opens and subsequent
	// calls return ErrCircuitBreakerOpen without hitting the network, protecting
	// both the remote service and our telemetry pipeline from a flood of retries.
	// May be nil in legacy construction paths (tests); see callWithCircuitBreaker.
	circuitBreaker *notification.PushCircuitBreaker
}

// maskURL masks sensitive BirdWeatherID tokens in URLs for safe logging.
// Uses a descriptive marker [BIRDWEATHER_ID] for consistency with privacy package patterns.
func (b *BwClient) maskURL(urlStr string) string {
	if b.BirdweatherID == "" {
		return urlStr
	}
	return strings.ReplaceAll(urlStr, b.BirdweatherID, "[BIRDWEATHER_ID]")
}

// logGainLimit logs gain limiting at WARN level at most once per 5 minutes,
// falling back to DEBUG for subsequent occurrences to avoid log spam.
func (b *BwClient) logGainLimit(log logger.Logger, msg, key1 string, val1 float64, key2 string, val2 float64) {
	const gainWarnInterval int64 = 300 // 5 minutes in seconds
	now := time.Now().Unix()
	last := b.lastGainWarn.Load()
	if now-last >= gainWarnInterval && b.lastGainWarn.CompareAndSwap(last, now) {
		log.Warn(msg, logger.Float64(key1, val1), logger.Float64(key2, val2))
	} else {
		log.Debug(msg, logger.Float64(key1, val1), logger.Float64(key2, val2))
	}
}

// closeResponseBody safely closes an HTTP response body and logs any errors
// This helper reduces code duplication across HTTP request handlers
func closeResponseBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	if err := resp.Body.Close(); err != nil {
		log := GetLogger()
		log.Debug("Failed to close response body", logger.Error(err))
	}
}

// BirdweatherClientInterface defines what methods a BirdweatherClient must have
type Interface interface {
	Publish(note *datastore.Note, pcmData []byte) error
	UploadSoundscape(timestamp string, pcmData []byte) (soundscapeID string, err error)
	PostDetection(soundscapeID, timestamp, commonName, scientificName string, confidence float64) error
	TestConnection(ctx context.Context, resultChan chan<- TestResult)
	Close()
}

// New creates and initializes a new BwClient with the given settings.
// The HTTP client is configured with httpClientTimeout to prevent hanging requests.
func New(settings *conf.Settings) (*BwClient, error) {
	log := GetLogger()
	log.Info("Creating new BirdWeather client")
	// We expect that Birdweather ID is validated before this function is called
	client := &BwClient{
		Settings:      settings,
		BirdweatherID: settings.Realtime.Birdweather.ID,
		Accuracy:      settings.Realtime.Birdweather.LocationAccuracy,
		Latitude:      settings.BirdNET.Latitude,
		Longitude:     settings.BirdNET.Longitude,
		HTTPClient:    &http.Client{Timeout: httpClientTimeout},
	}

	// Attach the circuit breaker. Metrics are intentionally nil for now — the
	// BirdWeather integration is not wired into the notification Prometheus
	// registry and we want to avoid reaching across package boundaries just to
	// surface state transitions. The breaker degrades gracefully when metrics
	// are nil (see internal/notification/circuit_breaker.go).
	client.circuitBreaker = notification.NewPushCircuitBreaker(
		defaultBirdWeatherCircuitBreakerConfig(),
		nil,
		bwCircuitBreakerProvider,
	)

	return client, nil
}

// RandomizeLocation adds a random offset to the given latitude and longitude to fuzz the location
// within a specified radius in meters for privacy, truncating the result to 4 decimal places.
// radiusMeters - the maximum radius in meters to adjust the coordinates
func (b *BwClient) RandomizeLocation(radiusMeters float64) (latitude, longitude float64) {
	log := GetLogger()

	// Create a new local random generator seeded with current Unix time
	rnd := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(time.Now().UnixNano()))) //nolint:gosec // G404: weak randomness acceptable for upload retry jitter, not security-critical

	// Calculate the degree offset using metersPerDegree approximation
	degreeOffset := radiusMeters / metersPerDegree

	// Generate random offsets within +/- degreeOffset
	latOffset := (rnd.Float64() - randomCenterOffset) * randomOffsetMultiplier * degreeOffset
	lonOffset := (rnd.Float64() - randomCenterOffset) * randomOffsetMultiplier * degreeOffset

	// Apply the offsets to the original coordinates and truncate to 4 decimal places
	latitude = math.Floor((b.Latitude+latOffset)*coordinatePrecisionFactor) / coordinatePrecisionFactor
	longitude = math.Floor((b.Longitude+lonOffset)*coordinatePrecisionFactor) / coordinatePrecisionFactor

	log.Debug("Randomized location",
		logger.Float64("original_lat", b.Latitude),
		logger.Float64("original_lon", b.Longitude),
		logger.Float64("radius_meters", radiusMeters),
		logger.Float64("fuzzed_lat", latitude),
		logger.Float64("fuzzed_lon", longitude))

	return latitude, longitude
}

// handleNetworkError handles network errors and returns a more specific error message.
func handleNetworkError(err error, url string, timeout time.Duration, operation string) *errors.EnhancedError {
	log := GetLogger()

	if err == nil {
		return errors.Newf("nil error").
			Component("birdweather").
			Category(errors.CategoryGeneric).
			Build()
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		// Create descriptive error message with operation context
		descriptiveErr := fmt.Errorf("BirdWeather %s timeout: %w", operation, err)
		log.Warn("Network request timed out",
			logger.String("operation", operation),
			logger.Error(err))
		return errors.New(descriptiveErr).
			Component("birdweather").
			Category(errors.CategoryNetwork).
			NetworkContext(url, timeout).
			Context("error_type", "timeout").
			Context("operation", operation).
			Build()
	}
	var urlErr *neturl.Error
	if errors.As(err, &urlErr) {
		var dnsErr *net.DNSError
		if errors.As(urlErr.Err, &dnsErr) {
			descriptiveErr := fmt.Errorf("BirdWeather %s DNS resolution failed: %w", operation, err)
			// DNS failures are transient infrastructure issues, not code bugs
			log.Warn("DNS resolution failed",
				logger.String("operation", operation),
				logger.String("url", url),
				logger.Error(err))
			return errors.New(descriptiveErr).
				Component("birdweather").
				Category(errors.CategoryNetwork).
				NetworkContext(url, timeout).
				Context("error_type", "dns_resolution").
				Context("operation", operation).
				Build()
		}
	}
	descriptiveErr := fmt.Errorf("BirdWeather %s network error: %w", operation, err)
	// Generic network errors are transient infrastructure issues
	log.Warn("Network error occurred",
		logger.String("operation", operation),
		logger.Error(err))
	return errors.New(descriptiveErr).
		Component("birdweather").
		Category(errors.CategoryNetwork).
		NetworkContext(url, timeout).
		Context("error_type", "generic_network").
		Context("operation", operation).
		Build()
}

// isHTMLResponse checks if the response content type indicates HTML
func isHTMLResponse(resp *http.Response) bool {
	contentType := resp.Header.Get("Content-Type")
	return strings.Contains(strings.ToLower(contentType), "text/html")
}

// extractHTMLError attempts to extract error message from HTML response
// This handles common error page patterns from web servers and proxies
func extractHTMLError(htmlContent string) string {
	// Common patterns for error messages in HTML
	// Look for title tags first as they often contain the error summary
	titleStart := strings.Index(htmlContent, "<title>")
	titleEnd := strings.Index(htmlContent, "</title>")
	if titleStart != -1 && titleEnd != -1 && titleEnd > titleStart {
		title := htmlContent[titleStart+7 : titleEnd]
		title = strings.TrimSpace(title)
		if title != "" {
			return fmt.Sprintf("HTML error page: %s", title)
		}
	}

	// Look for common error patterns in body
	lowerHTML := strings.ToLower(htmlContent)
	errorPatterns := []string{
		"error",
		"not found",
		"unauthorized",
		"forbidden",
		"bad request",
		"internal server error",
		"service unavailable",
		"gateway timeout",
		"too many requests",
	}

	for _, pattern := range errorPatterns {
		if !strings.Contains(lowerHTML, pattern) {
			continue
		}
		// Try to extract a reasonable snippet around the error
		index := strings.Index(lowerHTML, pattern)
		start := max(index-errorSnippetBefore, 0)
		end := min(index+errorSnippetAfter, len(htmlContent))
		snippet := htmlContent[start:end]
		// Remove HTML tags for cleaner output
		snippet = strings.ReplaceAll(snippet, "<", " <")
		snippet = strings.ReplaceAll(snippet, ">", "> ")
		// Clean up whitespace
		fields := strings.Fields(snippet)
		snippet = strings.Join(fields, " ")
		return fmt.Sprintf("HTML error detected: %s", snippet)
	}

	// If no specific error found, return generic message with beginning of content
	maxLen := min(len(htmlContent), maxHTMLPreview)
	preview := strings.TrimSpace(htmlContent[:maxLen])
	return fmt.Sprintf("Unexpected HTML response (first %d chars): %s", maxLen, preview)
}

// handleHTTPResponse processes HTTP response and handles both JSON and HTML responses
func handleHTTPResponse(resp *http.Response, expectedStatus int, operation, maskedURL string) ([]byte, error) {
	log := GetLogger()

	// Check status code first
	if resp.StatusCode != expectedStatus {
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			log.Error("Failed to read response body after non-expected status",
				logger.String("operation", operation),
				logger.String("url", maskedURL),
				logger.Int("expected_status", expectedStatus),
				logger.Int("actual_status", resp.StatusCode),
				logger.Error(readErr))
			return nil, fmt.Errorf("%s failed with status %d, failed to read response: %w", operation, resp.StatusCode, readErr)
		}

		// Check if response is HTML
		if isHTMLResponse(resp) {
			htmlError := extractHTMLError(string(responseBody))
			log.Error("Received HTML error response instead of JSON",
				logger.String("operation", operation),
				logger.String("url", maskedURL),
				logger.Int("status_code", resp.StatusCode),
				logger.String("html_error", htmlError),
				logger.String("response_preview", string(responseBody[:min(len(responseBody), maxResponsePreview)])))

			// Determine category based on status code
			category := errors.CategoryNetwork
			if resp.StatusCode == 408 || resp.StatusCode == 504 || resp.StatusCode == 524 {
				// 408 Request Timeout, 504 Gateway Timeout, 524 Timeout (Cloudflare)
				category = errors.CategoryTimeout
			}

			return nil, errors.Newf("%s failed: %s (status %d)", operation, htmlError, resp.StatusCode).
				Component("birdweather").
				Category(category).
				Context("response_type", "html").
				Context("status_code", resp.StatusCode).
				Context("operation", operation).
				Build()
		}

		// Not HTML, return the raw response
		err := fmt.Errorf("%s failed with status %d: %s", operation, resp.StatusCode, string(responseBody))
		responseStr := string(responseBody)

		// 422 Unprocessable Entity with species-related errors is expected for non-bird species
		// and should not be logged at error level. Check for species/scientificName in response.
		if resp.StatusCode == http.StatusUnprocessableEntity &&
			(strings.Contains(responseStr, `"species"`) || strings.Contains(responseStr, `"scientificName"`)) {
			log.Debug("Request failed with species validation error (expected for unknown species)",
				logger.String("operation", operation),
				logger.String("url", maskedURL),
				logger.Int("status_code", resp.StatusCode),
				logger.String("response_body", responseStr))
			return nil, errors.New(err).
				Component("birdweather").
				Category(errors.CategoryNotFound).
				Context("status_code", resp.StatusCode).
				Context("operation", operation).
				Build()
		}

		log.Error("Request failed with non-expected status",
			logger.String("operation", operation),
			logger.String("url", maskedURL),
			logger.Int("expected_status", expectedStatus),
			logger.Int("actual_status", resp.StatusCode),
			logger.String("response_body", string(responseBody)))
		return nil, errors.New(err).
			Component("birdweather").
			Category(errors.CategoryNetwork).
			Context("status_code", resp.StatusCode).
			Context("operation", operation).
			Build()
	}

	// Status is OK, read the body
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error("Failed to read response body",
			logger.String("operation", operation),
			logger.String("url", maskedURL),
			logger.Int("status_code", resp.StatusCode),
			logger.Error(err))
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return responseBody, nil
}

// encodeFlacUsingFFmpeg converts PCM data to FLAC format using FFmpeg directly into a bytes buffer.
// It applies a simple gain adjustment instead of dynamic loudness normalization to avoid pumping effects.
// This avoids writing temporary files to disk.
// It accepts a context for timeout/cancellation control and the explicit path to the FFmpeg executable.
func (b *BwClient) encodeFlacUsingFFmpeg(ctx context.Context, pcmData []byte, ffmpegPath string, settings *conf.Settings) (*bytes.Buffer, error) {
	log := GetLogger()

	log.Debug("Starting FLAC encoding process")
	// Add check for empty pcmData
	if len(pcmData) == 0 {
		log.Error("FLAC encoding failed: PCM data is empty")
		return nil, fmt.Errorf("pcmData is empty")
	}

	// ffmpegPath is now passed directly
	log.Debug("Using ffmpeg path", logger.String("path", ffmpegPath))

	// --- Pass 1: Analyze Loudness ---
	// Use the provided context for the analysis
	log.Debug("Performing loudness analysis (Pass 1)")
	loudnessStats, err := ffmpeg.AnalyzePCMLoudness(ctx, pcmData, ffmpegPath, conf.SampleRate, conf.BitDepth)
	if err != nil {
		// Check if the error is due to context cancellation
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			log.Warn("Loudness analysis cancelled or timed out", logger.Error(err))
			return nil, err // Propagate context error
		}

		log.Warn("Loudness analysis (Pass 1) failed, falling back to fixed gain adjustment", logger.Error(err))
		// Fallback to a conservative fixed gain adjustment
		// A fixed gain of 15dB is a reasonable middle ground for bird call recordings
		gainValue := 15.0
		volumeArgs := fmt.Sprintf("volume=%.1fdB", gainValue)
		customArgs := []string{
			"-af", volumeArgs, // Simple gain adjustment
			"-c:a", "flac",
			"-f", "flac",
		}

		// Use the provided context for the fallback export operation
		log.Debug("Starting fallback FLAC export with fixed gain", logger.Float64("gain_db", gainValue))
		buffer, err := ffmpeg.ExportAudioToBuffer(ctx, pcmData, ffmpegPath, conf.SampleRate, conf.NumChannels, conf.BitDepth, customArgs)
		if err != nil {
			log.Error("Fallback FLAC export with fixed gain failed",
				logger.Float64("gain_db", gainValue),
				logger.Error(err))
			return nil, fmt.Errorf("fallback FLAC export with fixed gain failed: %w", err)
		}
		log.Info("Encoded PCM to FLAC using fixed gain (fallback)", logger.Float64("gain_db", gainValue))
		return buffer, nil
	}

	log.Debug("Loudness analysis results",
		logger.String("input_i", loudnessStats.InputI),
		logger.String("input_lra", loudnessStats.InputLRA),
		logger.String("input_tp", loudnessStats.InputTP),
		logger.String("input_thresh", loudnessStats.InputThresh))

	// --- Calculate gain needed to reach target loudness ---
	inputLUFS := parseDouble(loudnessStats.InputI, -70.0)
	gainNeeded := targetIntegratedLoudnessLUFS - inputLUFS

	// Apply safety limits to prevent excessive amplification or attenuation
	maxGain := 30.0 // Maximum gain in dB (absolute value)
	gainLimited := false
	if gainNeeded > maxGain {
		b.logGainLimit(log, "Limiting gain to prevent excessive amplification",
			"calculated_gain", gainNeeded, "max_gain", maxGain)
		gainNeeded = maxGain
		gainLimited = true
	} else if gainNeeded < -maxGain {
		b.logGainLimit(log, "Limiting gain to prevent excessive attenuation",
			"calculated_gain", gainNeeded, "min_gain", -maxGain)
		gainNeeded = -maxGain
		gainLimited = true
	}
	log.Debug("Calculated gain adjustment",
		logger.Float64("gain_db", gainNeeded),
		logger.Float64("target_lufs", targetIntegratedLoudnessLUFS),
		logger.Float64("measured_lufs", inputLUFS),
		logger.Bool("limited", gainLimited))

	// --- Pass 2: Apply simple gain adjustment and encode ---
	log.Debug("Applying gain adjustment and encoding to FLAC (Pass 2)", logger.Float64("gain_db", gainNeeded))

	// Use simple volume filter instead of loudnorm
	volumeArgs := fmt.Sprintf("volume=%.2fdB", gainNeeded)

	customArgs := []string{
		"-af", volumeArgs, // Simple gain adjustment filter
		"-c:a", "flac", // Output codec: FLAC
		"-f", "flac", // Output format: FLAC
	}

	// Use the provided context for the final encoding operation
	buffer, err := ffmpeg.ExportAudioToBuffer(ctx, pcmData, ffmpegPath, conf.SampleRate, conf.NumChannels, conf.BitDepth, customArgs)
	if err != nil {
		log.Error("FFmpeg FLAC encoding with gain adjustment failed",
			logger.Float64("gain_db", gainNeeded),
			logger.Error(err))
		return nil, fmt.Errorf("failed to export PCM to FLAC with gain adjustment: %w", err)
	}

	log.Info("Encoded PCM to FLAC with gain adjustment", logger.Float64("gain_db", gainNeeded))

	// Return the buffer containing the FLAC data
	return buffer, nil
}

// parseDouble safely parses a string to float64, returning defaultValue on error.
func parseDouble(s string, defaultValue float64) float64 {
	val, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return defaultValue
	}
	return val
}

// UploadSoundscape uploads a soundscape file to the Birdweather API and returns the soundscape ID if successful.
// It handles the PCM to WAV conversion, compresses the data, and manages HTTP request creation and response handling safely.
func (b *BwClient) UploadSoundscape(timestamp string, pcmData []byte) (soundscapeID string, err error) {
	log := GetLogger()

	// Track performance timing for telemetry
	// Note: Wrapped in closure so soundscapeID is captured at execution time, not registration time
	startTime := time.Now()
	defer func() {
		trackOperationTiming(&err, "soundscape_upload", startTime, "timestamp", timestamp, "soundscape_id", soundscapeID)()
	}()

	log.Info("Starting soundscape upload", logger.String("timestamp", timestamp))

	// Validate input
	if len(pcmData) == 0 {
		return "", errors.Newf("pcmData is empty").
			Component("birdweather").
			Category(errors.CategoryValidation).
			Context("timestamp", timestamp).
			Build()
	}

	// Encode PCM data to audio format (FLAC with FFmpeg, or WAV fallback)
	encodingResult, err := b.encodeAudioForUpload(b.Settings, pcmData, timestamp)
	if err != nil {
		return "", errors.New(err).
			Component("birdweather").
			Category(errors.CategoryAudio).
			Context("timestamp", timestamp).
			Build()
	}
	audioBuffer := encodingResult.buffer
	audioExt := encodingResult.ext

	// If debug is enabled, save the audio file locally
	if b.Settings.Realtime.Birdweather.Debug {
		saveDebugAudioFile(audioBuffer, audioExt, timestamp)
	}

	// Create and execute the POST request
	// Note: FLAC is already compressed, so we don't gzip it
	soundscapeURL := fmt.Sprintf("https://app.birdweather.com/api/v1/stations/%s/soundscapes?timestamp=%s&type=%s",
		b.BirdweatherID, neturl.QueryEscape(timestamp), audioExt)
	maskedURL := b.maskURL(soundscapeURL)
	log.Debug("Creating soundscape upload request",
		logger.String("url", maskedURL),
		logger.Int("audio_size", audioBuffer.Len()))
	req, err := http.NewRequest("POST", soundscapeURL, audioBuffer)
	if err != nil {
		log.Error("Failed to create soundscape POST request",
			logger.String("url", maskedURL),
			logger.Error(err))
		return "", fmt.Errorf("failed to create POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("User-Agent", "BirdNET-Go")

	// Execute the request through the circuit breaker. The breaker records
	// transport-level failures (timeouts, DNS, TLS) AND non-2xx responses so
	// a broken or rate-limited backend trips the breaker the same way a flaky
	// network does. Success/failure classification mirrors the legacy behaviour
	// the callers already expect.
	log.Info("Uploading soundscape",
		logger.String("url", maskedURL),
		logger.String("format", audioExt))
	var (
		responseBody []byte
		// nonTransientErr carries business-logic errors (e.g. CategoryNotFound
		// species validation 422s) out of the breaker closure without tripping
		// the breaker. These are expected operational outcomes, not failures
		// of the upstream service.
		nonTransientErr error
	)
	cbErr := b.callWithCircuitBreaker(req.Context(), func(ctx context.Context) error {
		// Use the breaker callback's ctx so cancellation propagates through
		// to the HTTP layer (e.g., the caller aborts mid-retry) rather than
		// stopping at the breaker boundary.
		resp, httpErr := b.HTTPClient.Do(req.WithContext(ctx))
		if httpErr != nil {
			// handleNetworkError logs at Warn with classified details
			// (timeout/DNS/connection), and the caller (Publish) logs the
			// final outcome — so no extra log.Error here.
			return handleNetworkError(httpErr, maskedURL, httpClientTimeout, "soundscape upload")
		}
		if resp == nil {
			// Defensive: Go's http.Client should not return nil resp with nil
			// err. If it happens, let the caller handle it — don't double-log.
			return errors.Newf("received nil response from soundscape upload").
				Component("birdweather").
				Category(errors.CategoryNetwork).
				Context("operation", "soundscape_upload").
				Context("url", maskedURL).
				Build()
		}
		defer closeResponseBody(resp)
		log.Debug("Received soundscape upload response",
			logger.String("url", maskedURL),
			logger.Int("status_code", resp.StatusCode))

		body, handleErr := handleHTTPResponse(resp, http.StatusCreated, "soundscape upload", maskedURL)
		if handleErr != nil {
			if errors.IsCategory(handleErr, errors.CategoryNotFound) {
				nonTransientErr = handleErr
				return nil
			}
			return handleErr
		}
		responseBody = body

		// Validate the response inside the closure so that malformed bodies
		// (HTML with 201, invalid JSON, success:false payloads) count as
		// failures against the circuit breaker — otherwise a degraded
		// upstream could silently pass the closure and never trip the
		// breaker.
		id, parseErr := parseSoundscapeResponse(body, maskedURL, resp.StatusCode)
		if parseErr != nil {
			return parseErr
		}
		soundscapeID = id
		return nil
	})
	if nonTransientErr != nil {
		return "", nonTransientErr
	}
	if cbErr != nil {
		if isCircuitBreakerOpen(cbErr) {
			// Debug-level only: Publish() already emits its own debug log for the
			// breaker-open case (see handlePublishError), and a warn here would
			// double-log a condition that is not actionable on a per-upload basis.
			log.Debug("Soundscape upload skipped: circuit breaker open",
				logger.String("url", maskedURL),
				logger.String("timestamp", timestamp))
		}
		return "", cbErr
	}

	if b.Settings.Realtime.Birdweather.Debug {
		log.Debug("Soundscape response body", logger.String("body", string(responseBody)))
	}

	log.Info("Soundscape uploaded successfully",
		logger.String("timestamp", timestamp),
		logger.String("soundscape_id", soundscapeID),
		logger.String("url", maskedURL))
	return soundscapeID, nil
}

// PostDetection posts a detection to the Birdweather API matching the specified soundscape ID.
func (b *BwClient) PostDetection(soundscapeID, timestamp, commonName, scientificName string, confidence float64) (err error) {
	log := GetLogger()

	// Track performance timing for telemetry
	defer trackOperationTiming(&err, "detection_post", time.Now(), "soundscape_id", soundscapeID)()

	log.Info("Starting detection post",
		logger.String("soundscape_id", soundscapeID),
		logger.String("timestamp", timestamp),
		logger.String("common_name", commonName),
		logger.String("scientific_name", scientificName),
		logger.Float64("confidence", confidence))

	// Simple input validation
	if soundscapeID == "" || timestamp == "" || commonName == "" || scientificName == "" {
		enhancedErr := errors.Newf("invalid input: all string parameters must be non-empty").
			Component("birdweather").
			Category(errors.CategoryValidation).
			Context("soundscape_id", soundscapeID).
			Context("timestamp", timestamp).
			Context("common_name", commonName).
			Context("scientific_name", scientificName).
			Build()
		log.Error("Detection post failed: Invalid input",
			logger.String("soundscape_id", soundscapeID),
			logger.String("timestamp", timestamp),
			logger.String("common_name", commonName),
			logger.String("scientific_name", scientificName),
			logger.Error(enhancedErr))
		return enhancedErr
	}

	detectionURL := fmt.Sprintf("https://app.birdweather.com/api/v1/stations/%s/detections", b.BirdweatherID)
	maskedDetectionURL := b.maskURL(detectionURL)

	// Fuzz location coordinates with user defined accuracy
	fuzzedLatitude, fuzzedLongitude := b.RandomizeLocation(b.Accuracy)

	// Convert timestamp to time.Time and calculate end time
	parsedTime, err := time.Parse("2006-01-02T15:04:05.000-0700", timestamp)
	if err != nil {
		log.Error("Failed to parse timestamp for detection post",
			logger.String("timestamp", timestamp),
			logger.Error(err))
		return fmt.Errorf("failed to parse timestamp: %w", err)
	}
	endTime := parsedTime.Add(detectionDurationSeconds * time.Second).Format("2006-01-02T15:04:05.000-0700") // Add detection duration to timestamp for endTime
	log.Debug("Calculated detection time range",
		logger.String("start_time", timestamp),
		logger.String("end_time", endTime))

	// Prepare JSON payload for POST request
	postData := struct {
		Timestamp           string  `json:"timestamp"`
		Latitude            float64 `json:"lat"`
		Longitude           float64 `json:"lon"`
		SoundscapeID        string  `json:"soundscapeId"`
		SoundscapeStartTime string  `json:"soundscapeStartTime"`
		SoundscapeEndTime   string  `json:"soundscapeEndTime"`
		CommonName          string  `json:"commonName"`
		ScientificName      string  `json:"scientificName"`
		Algorithm           string  `json:"algorithm"`
		Confidence          string  `json:"confidence"`
	}{
		Timestamp:           timestamp,
		Latitude:            fuzzedLatitude,
		Longitude:           fuzzedLongitude,
		SoundscapeID:        soundscapeID,
		SoundscapeStartTime: timestamp, // Assuming detection aligns with soundscape start
		SoundscapeEndTime:   endTime,   // Soundscape is 3s, so end time matches
		CommonName:          commonName,
		ScientificName:      scientificName,
		Algorithm:           birdnetAlgorithmVersion,
		Confidence:          fmt.Sprintf("%.2f", confidence),
	}

	// Marshal JSON data
	postDataBytes, err := json.Marshal(postData)
	if err != nil {
		log.Error("Failed to marshal detection JSON data", logger.Error(err))
		return fmt.Errorf("failed to marshal JSON data: %w", err)
	}

	if b.Settings.Realtime.Birdweather.Debug {
		log.Debug("Detection JSON Payload", logger.String("payload", string(postDataBytes)))
	}

	// Execute POST request through the circuit breaker. A persistent BirdWeather
	// outage used to produce one Sentry event per detection; the breaker now
	// short-circuits retries once consecutive failures cross the configured
	// threshold, letting the remote service recover without a retry storm.
	log.Info("Posting detection",
		logger.String("url", maskedDetectionURL),
		logger.String("soundscape_id", soundscapeID),
		logger.String("scientific_name", scientificName))
	// nonTransientErr carries business-logic errors (e.g. CategoryNotFound
	// species validation 422s from the detection post — the common case for
	// non-bird species) out of the breaker closure without tripping it.
	var nonTransientErr error
	cbErr := b.callWithCircuitBreaker(context.Background(), func(ctx context.Context) error {
		// Use http.NewRequestWithContext so the breaker callback's ctx
		// propagates to the HTTP layer and honours cancellation. The
		// previous HTTPClient.Post() call discarded ctx, leaving requests
		// to run to HTTPClient.Timeout even after the breaker or caller
		// signalled cancellation.
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, detectionURL, bytes.NewBuffer(postDataBytes))
		if reqErr != nil {
			return fmt.Errorf("build detection post request: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "BirdNET-Go")
		resp, httpErr := b.HTTPClient.Do(req)
		if httpErr != nil {
			// handleNetworkError + Publish already log; no extra log.Error.
			return handleNetworkError(httpErr, maskedDetectionURL, httpClientTimeout, "detection post")
		}
		if resp == nil {
			// Defensive; caller handles reporting.
			return errors.Newf("received nil response from detection post").
				Component("birdweather").
				Category(errors.CategoryNetwork).
				Context("operation", "detection_post").
				Context("soundscape_id", soundscapeID).
				Context("url", maskedDetectionURL).
				Build()
		}
		defer closeResponseBody(resp)
		log.Debug("Received detection post response",
			logger.String("url", maskedDetectionURL),
			logger.String("soundscape_id", soundscapeID),
			logger.Int("status_code", resp.StatusCode))

		_, handleErr := handleHTTPResponse(resp, http.StatusCreated, "detection post", maskedDetectionURL)
		if handleErr != nil {
			// Add detection-specific context regardless of classification.
			var enhancedErr *errors.EnhancedError
			if errors.As(handleErr, &enhancedErr) {
				enhancedErr.Context["soundscape_id"] = soundscapeID
				enhancedErr.Context["scientific_name"] = scientificName
			}
			if errors.IsCategory(handleErr, errors.CategoryNotFound) {
				nonTransientErr = handleErr
				return nil
			}
			return handleErr
		}
		return nil
	})
	if nonTransientErr != nil {
		return nonTransientErr
	}
	if cbErr != nil {
		if isCircuitBreakerOpen(cbErr) {
			// Debug-level only: Publish() already logs this at debug.
			log.Debug("Detection post skipped: circuit breaker open",
				logger.String("url", maskedDetectionURL),
				logger.String("soundscape_id", soundscapeID),
				logger.String("scientific_name", scientificName))
		}
		return cbErr
	}

	log.Info("Detection posted successfully",
		logger.String("soundscape_id", soundscapeID),
		logger.String("scientific_name", scientificName))
	return nil
}

// Publish function handles the uploading of detected clips and their details to Birdweather.
// It first parses the timestamp from the note, then uploads the soundscape, and finally posts the detection.
func (b *BwClient) Publish(note *datastore.Note, pcmData []byte) (err error) {
	log := GetLogger()

	// Track performance timing for telemetry
	defer trackOperationTiming(&err, "publish", time.Now(), "common_name", note.CommonName, "scientific_name", note.ScientificName)()

	log.Info("Starting publish process",
		logger.String("date", note.Date),
		logger.String("time", note.Time),
		logger.String("common_name", note.CommonName),
		logger.String("scientific_name", note.ScientificName),
		logger.Float64("confidence", note.Confidence))

	// Validate input
	if len(pcmData) == 0 {
		return errors.Newf("pcmData is empty").
			Component("birdweather").
			Category(errors.CategoryValidation).
			Context("common_name", note.CommonName).
			Context("scientific_name", note.ScientificName).
			Build()
	}

	// Use system's local timezone for timestamp parsing
	loc := time.Local

	// Combine date and time from note to form a full timestamp string
	dateTimeString := fmt.Sprintf("%sT%s", note.Date, note.Time)

	// Parse the timestamp using the given format and the system's local timezone
	parsedTime, err := time.ParseInLocation("2006-01-02T15:04:05", dateTimeString, loc)
	if err != nil {
		log.Error("Error parsing date/time for publish",
			logger.String("date", note.Date),
			logger.String("time", note.Time),
			logger.Error(err))
		return fmt.Errorf("error parsing date: %w", err)
	}

	// Format the parsed time to the required timestamp format with timezone information
	timestamp := parsedTime.Format("2006-01-02T15:04:05.000-0700")
	log.Debug("Formatted timestamp for publish", logger.String("timestamp", timestamp))

	// If debug is enabled, save the raw PCM data to help diagnose issues
	if b.Settings.Realtime.Birdweather.Debug {
		debugDir := filepath.Join("debug", "birdweather", "pcm")
		debugFilename := filepath.Join(debugDir, fmt.Sprintf("bw_pcm_debug_%s.raw",
			parsedTime.Format("20060102_150405")))

		// Create directory if it doesn't exist
		if err := createDebugDirectory(debugDir); err != nil {
			log.Warn("Could not create debug PCM directory",
				logger.String("directory", debugDir),
				logger.Error(err))
		} else {
			// Save raw PCM data
			if err := os.WriteFile(debugFilename, pcmData, filePermission); err != nil {
				log.Warn("Could not save debug PCM file",
					logger.String("filename", debugFilename),
					logger.Error(err))
			} else {
				log.Debug("Saved debug PCM file", logger.String("filename", debugFilename))
			}
		}
	}

	// Upload the soundscape to Birdweather and retrieve the soundscape ID
	log.Debug("Calling UploadSoundscape", logger.String("timestamp", timestamp))
	soundscapeID, err := b.UploadSoundscape(timestamp, pcmData)
	if err != nil {
		switch {
		case isCircuitBreakerOpen(err):
			// Breaker is open — the upstream BirdWeather API is still considered
			// unhealthy. This is an operational throttling state, not a code bug,
			// so we log at debug level and skip alerting (which would otherwise
			// fire once per detection during extended outages).
			log.Debug("BirdWeather soundscape upload skipped: circuit breaker open",
				logger.String("timestamp", timestamp),
				logger.Error(err))
		case errors.IsTransientNetworkError(err):
			// Transient network errors (DNS, timeout, connection issues) are expected
			// external failures, not code bugs. Log at warn level and skip alerting
			// to avoid Sentry noise and unnecessary user notifications.
			log.Warn("BirdWeather soundscape upload failed due to transient network issue",
				logger.String("timestamp", timestamp),
				logger.Error(err))
		default:
			log.Error("Publish failed: Error during soundscape upload",
				logger.String("timestamp", timestamp),
				logger.Error(err))
			alerting.TryPublish(&alerting.AlertEvent{
				ObjectType: alerting.ObjectTypeIntegration,
				EventName:  alerting.EventBirdWeatherFailed,
				Properties: map[string]any{
					alerting.PropertyError: err.Error(),
				},
			})
		}
		return fmt.Errorf("failed to upload soundscape to Birdweather: %w", err)
	}
	log.Debug("UploadSoundscape completed",
		logger.String("timestamp", timestamp),
		logger.String("soundscape_id", soundscapeID))

	// Post the detection details to Birdweather using the retrieved soundscape ID
	log.Debug("Calling PostDetection",
		logger.String("soundscape_id", soundscapeID),
		logger.String("timestamp", timestamp),
		logger.Any("note", note))
	err = b.PostDetection(soundscapeID, timestamp, note.CommonName, note.ScientificName, note.Confidence)
	if err != nil {
		switch {
		case errors.IsNotFound(err):
			// CategoryNotFound (e.g., invalid species on Birdweather)
			// Expected — not all BirdNET species exist in BirdWeather. Skip without error.
			log.Debug("Publish skipped: species not recognized by Birdweather",
				logger.String("soundscape_id", soundscapeID),
				logger.String("common_name", note.CommonName),
				logger.String("scientific_name", note.ScientificName),
				logger.Error(err))
			return nil
		case isCircuitBreakerOpen(err):
			// Breaker is open — treat as a short-circuited skip. No alerting,
			// no Sentry noise (handled by shouldReportToSentry), debug-level log.
			log.Debug("BirdWeather detection post skipped: circuit breaker open",
				logger.String("soundscape_id", soundscapeID),
				logger.String("timestamp", timestamp),
				logger.String("common_name", note.CommonName),
				logger.String("scientific_name", note.ScientificName),
				logger.Error(err))
		case errors.IsTransientNetworkError(err):
			// Transient network errors during detection post are expected
			// external failures. Log at warn level and skip alerting.
			log.Warn("BirdWeather detection post failed due to transient network issue",
				logger.String("soundscape_id", soundscapeID),
				logger.String("timestamp", timestamp),
				logger.String("common_name", note.CommonName),
				logger.String("scientific_name", note.ScientificName),
				logger.Error(err))
		default:
			log.Error("Publish failed: Error during detection post",
				logger.String("soundscape_id", soundscapeID),
				logger.String("timestamp", timestamp),
				logger.Any("note", note),
				logger.Error(err))
			alerting.TryPublish(&alerting.AlertEvent{
				ObjectType: alerting.ObjectTypeIntegration,
				EventName:  alerting.EventBirdWeatherFailed,
				Properties: map[string]any{
					alerting.PropertyError: err.Error(),
				},
			})
		}
		return fmt.Errorf("failed to post detection to Birdweather: %w", err)
	}
	log.Debug("PostDetection completed", logger.String("soundscape_id", soundscapeID))

	log.Info("Publish process completed successfully",
		logger.String("soundscape_id", soundscapeID),
		logger.String("scientific_name", note.ScientificName))
	return nil
}

// Close properly cleans up the BwClient resources
// Currently this just cancels any pending HTTP requests
func (b *BwClient) Close() {
	log := GetLogger()

	log.Info("Closing BirdWeather client")
	if b.HTTPClient != nil && b.HTTPClient.Transport != nil {
		// If the transport implements the CloseIdleConnections method, call it
		type transporter interface {
			CloseIdleConnections()
		}
		if transport, ok := b.HTTPClient.Transport.(transporter); ok {
			log.Debug("Closing idle HTTP connections")
			transport.CloseIdleConnections()
		}
		// Cancel any in-flight requests by using a new client
		b.HTTPClient = nil // Allow GC to collect the old client/transport
	}

	if b.Settings.Realtime.Birdweather.Debug {
		log.Info("BirdWeather client closed")
	}
}

// createDebugDirectory creates a directory for debug files and returns any error encountered
func createDebugDirectory(path string) error {
	if err := os.MkdirAll(path, dirPermission); err != nil {
		return fmt.Errorf("couldn't create debug directory: %w", err)
	}
	return nil
}

// audioEncodingResult holds the result of audio encoding
type audioEncodingResult struct {
	buffer *bytes.Buffer
	ext    string
}

// encodeAudioForUpload handles the PCM to FLAC encoding using FFmpeg
// FFmpeg is required as BirdWeather only accepts FLAC format
func (b *BwClient) encodeAudioForUpload(settings *conf.Settings, pcmData []byte, timestamp string) (*audioEncodingResult, error) {
	log := GetLogger()

	// Use the validated FFmpeg path from settings (validated at startup)
	// This avoids redundant exec.LookPath calls on every upload
	ffmpegPathForExec := settings.Realtime.Audio.FfmpegPath
	ffmpegAvailable := ffmpegPathForExec != ""
	log.Debug("Checking FFmpeg availability",
		logger.String("path", ffmpegPathForExec),
		logger.Bool("available", ffmpegAvailable))

	if !ffmpegAvailable {
		log.Error("FFmpeg not available, cannot encode to FLAC for BirdWeather",
			logger.String("timestamp", timestamp))
		return nil, fmt.Errorf("FFmpeg is required for BirdWeather uploads (FLAC encoding)")
	}

	return b.encodeWithFFmpeg(settings, pcmData, ffmpegPathForExec, timestamp)
}

// encodeWithFFmpeg encodes PCM to FLAC format using FFmpeg
func (b *BwClient) encodeWithFFmpeg(settings *conf.Settings, pcmData []byte, ffmpegPath, timestamp string) (*audioEncodingResult, error) {
	log := GetLogger()

	ctx, cancel := context.WithTimeout(context.Background(), encodingTimeout)
	defer cancel()

	audioBuffer, err := b.encodeFlacUsingFFmpeg(ctx, pcmData, ffmpegPath, settings)
	if err != nil {
		log.Error("FLAC encoding failed",
			logger.String("timestamp", timestamp),
			logger.Error(err))
		logFLACEncodingError(err)
		return nil, fmt.Errorf("FLAC encoding failed: %w", err)
	}
	log.Info("Encoded audio to FLAC format", logger.String("timestamp", timestamp))
	return &audioEncodingResult{buffer: audioBuffer, ext: "flac"}, nil
}

// logFLACEncodingError logs the appropriate message for FLAC encoding failures
func logFLACEncodingError(err error) {
	log := GetLogger()

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		log.Warn("FLAC encoding timed out or was cancelled", logger.Error(err))
	} else {
		log.Error("Failed to encode/normalize PCM to FLAC", logger.Error(err))
	}
}

// saveDebugAudioFile saves audio buffer to a debug file if debug mode is enabled
func saveDebugAudioFile(audioBuffer *bytes.Buffer, audioExt, timestamp string) {
	log := GetLogger()

	parsedTime, parseErr := time.Parse("2006-01-02T15:04:05.000-0700", timestamp)
	if parseErr != nil {
		log.Warn("Could not parse timestamp for debug file saving",
			logger.String("timestamp", timestamp),
			logger.String("format", audioExt),
			logger.Error(parseErr))
		return
	}

	debugDir := filepath.Join("debug", "birdweather", audioExt)
	debugFilename := filepath.Join(debugDir, fmt.Sprintf("bw_debug_%s.%s", parsedTime.Format("20060102_150405"), audioExt))
	endTime := parsedTime.Add(detectionDurationSeconds * time.Second)

	audioCopy := bytes.NewBuffer(audioBuffer.Bytes())
	if saveErr := saveBufferToFile(audioCopy, debugFilename, parsedTime, endTime); saveErr != nil {
		log.Warn("Could not save debug file",
			logger.String("filename", debugFilename),
			logger.Error(saveErr))
	} else {
		log.Debug("Saved debug file", logger.String("filename", debugFilename))
	}
}

// parseSoundscapeResponse parses the JSON response from soundscape upload
func parseSoundscapeResponse(responseBody []byte, maskedURL string, statusCode int) (string, error) {
	log := GetLogger()

	var sdata SoundscapeResponse
	if err := json.Unmarshal(responseBody, &sdata); err != nil {
		// Check if this might be HTML even though we got 200 OK
		if strings.Contains(string(responseBody), "<") && strings.Contains(string(responseBody), ">") {
			htmlError := extractHTMLError(string(responseBody))
			log.Error("Received HTML response with 200 OK status",
				logger.String("operation", "soundscape upload"),
				logger.String("url", maskedURL),
				logger.String("html_error", htmlError),
				logger.String("response_preview", string(responseBody[:min(len(responseBody), maxResponsePreview)])))
			return "", errors.Newf("soundscape upload failed: %s", htmlError).
				Component("birdweather").
				Category(errors.CategoryNetwork).
				Context("response_type", "html_with_200").
				Context("operation", "soundscape upload").
				Build()
		}
		log.Error("Failed to decode soundscape JSON response",
			logger.String("url", maskedURL),
			logger.Int("status_code", statusCode),
			logger.String("body", string(responseBody)),
			logger.Error(err))
		return "", fmt.Errorf("failed to decode JSON response: %w", err)
	}

	if !sdata.Success {
		log.Error("Soundscape upload was not successful according to API response",
			logger.String("url", maskedURL),
			logger.Int("status_code", statusCode),
			logger.Any("response", sdata))
		return "", fmt.Errorf("upload failed, response reported failure")
	}

	return fmt.Sprintf("%d", sdata.Soundscape.ID), nil
}

// trackOperationTiming creates a deferred timing tracker for operations
// Usage: defer trackOperationTiming(&err, "operation_name", time.Now(), contextFields...)()
//
//nolint:gocritic // errPtr must be a pointer to modify the error in the calling function's scope
func trackOperationTiming(errPtr *error, operation string, startTime time.Time, contextFields ...any) func() {
	return func() {
		log := GetLogger()

		duration := time.Since(startTime)
		if *errPtr != nil {
			// Circuit-breaker short-circuits are an operational throttle state,
			// not a failure of the individual call. The breaker state transition
			// is surfaced via dedicated telemetry; emitting a per-call WARN here
			// would undo the Sentry-noise reduction the breaker exists to provide.
			// Treat them like successful no-ops for timing-log purposes.
			if isCircuitBreakerOpen(*errPtr) {
				logArgs := append([]logger.Field{
					logger.String("operation", operation),
					logger.Int64("duration_ms", duration.Milliseconds()),
				}, convertToFields(contextFields)...)
				log.Debug(fmt.Sprintf("%s short-circuited: circuit breaker open", operation), logArgs...)
				return
			}
			// Add timing context to error
			var enhancedErr *errors.EnhancedError
			if errors.As(*errPtr, &enhancedErr) {
				// Initialize Context map if nil to prevent panic
				if enhancedErr.Context == nil {
					enhancedErr.Context = make(map[string]any)
				}
				enhancedErr.Context["operation_duration_ms"] = duration.Milliseconds()
				enhancedErr.Context["operation"] = operation
			} else {
				*errPtr = errors.New(*errPtr).
					Component("birdweather").
					Category(errors.CategoryNetwork).
					Timing(operation, duration).
					Build()
			}
			logArgs := append([]logger.Field{
				logger.String("operation", operation),
				logger.Int64("duration_ms", duration.Milliseconds()),
				logger.Error(*errPtr),
			}, convertToFields(contextFields)...)
			log.Warn(fmt.Sprintf("%s failed", operation), logArgs...)
		} else {
			logArgs := append([]logger.Field{
				logger.String("operation", operation),
				logger.Int64("duration_ms", duration.Milliseconds()),
			}, convertToFields(contextFields)...)
			log.Info(fmt.Sprintf("%s completed", operation), logArgs...)
		}
	}
}

// convertToFields converts variadic key-value pairs to logger.Field slice
func convertToFields(args []any) []logger.Field {
	fields := make([]logger.Field, 0, len(args)/2)
	for i := 0; i+1 < len(args); i += 2 {
		key, ok := args[i].(string)
		if !ok {
			continue
		}
		switch v := args[i+1].(type) {
		case string:
			fields = append(fields, logger.String(key, v))
		case int:
			fields = append(fields, logger.Int(key, v))
		case int64:
			fields = append(fields, logger.Int64(key, v))
		case float64:
			fields = append(fields, logger.Float64(key, v))
		case bool:
			fields = append(fields, logger.Bool(key, v))
		case error:
			fields = append(fields, logger.Error(v))
		default:
			fields = append(fields, logger.Any(key, v))
		}
	}
	return fields
}
