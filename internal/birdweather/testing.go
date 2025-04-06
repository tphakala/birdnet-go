// testing.go provides BirdWeather connection and functionality testing capabilities
package birdweather

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Rate limiting constants
const (
	// Minimum time between test submissions (1 minute)
	minTestInterval = 1 * time.Minute
)

// DNS Fallback resolvers
var fallbackDNSResolvers = []string{
	"1.1.1.1:53", // Cloudflare
	"8.8.8.8:53", // Google
	"9.9.9.9:53", // Quad9
}

// resultContext is used to pass result IDs through context
type resultContext struct {
	ID string
}

// Define a custom type for the context key to avoid collisions
type contextKey int

// Define the key constant
const resultIDKey contextKey = iota

// Global rate limiter state
var (
	lastTestTime  time.Time
	rateLimiterMu sync.Mutex
)

// checkRateLimit returns error if tests are being run too frequently
func checkRateLimit() error {
	rateLimiterMu.Lock()
	defer rateLimiterMu.Unlock()

	// If this is the first test or enough time has passed, allow it
	if lastTestTime.IsZero() || time.Since(lastTestTime) >= minTestInterval {
		// Update the last test time and allow this test
		lastTestTime = time.Now()
		return nil
	}

	// Calculate time until next allowed test
	nextAllowedTime := lastTestTime.Add(minTestInterval)
	expiryTime := nextAllowedTime.Unix()

	return fmt.Errorf("rate limit exceeded: please wait before testing again|%d", expiryTime)
}

// resolveDNSWithFallback attempts to resolve a hostname using fallback DNS servers if the OS resolver fails
func resolveDNSWithFallback(hostname string) ([]net.IP, error) {
	// First try the standard resolver
	ips, err := net.LookupIP(hostname)
	if err == nil && len(ips) > 0 {
		return ips, nil
	}

	// If standard resolver fails, log the error
	log.Printf("Standard DNS resolution for %s failed: %v", hostname, err)
	log.Printf("Attempting to resolve %s using fallback DNS servers...", hostname)

	// Try each fallback resolver
	for _, resolver := range fallbackDNSResolvers {
		r := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: 5 * time.Second}
				return d.DialContext(ctx, "udp", resolver)
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		fallbackIPs, err := r.LookupIPAddr(ctx, hostname)
		cancel()

		if err == nil && len(fallbackIPs) > 0 {
			// Convert IPAddr to IP
			result := make([]net.IP, len(fallbackIPs))
			for i, addr := range fallbackIPs {
				result[i] = addr.IP
			}
			log.Printf("Successfully resolved %s using fallback DNS %s: %v", hostname, resolver, result)
			return result, nil
		}
	}

	return nil, fmt.Errorf("failed to resolve %s with all DNS resolvers", hostname)
}

// TestConfig encapsulates test configuration for artificial delays and failures
type TestConfig struct {
	// Set to true to enable artificial delays and random failures
	Enabled bool
	// Internal flag to enable random failures (for testing UI behavior)
	RandomFailureMode bool
	// Probability of failure for each stage (0.0 - 1.0)
	FailureProbability float64
	// Min and max artificial delay in milliseconds
	MinDelay int
	MaxDelay int
	// Thread-safe random number generator
	rng *rand.Rand
	mu  sync.Mutex
}

// Default test configuration instance
var testConfig = &TestConfig{
	Enabled:            false,
	RandomFailureMode:  false,
	FailureProbability: 0.5,
	MinDelay:           500,
	MaxDelay:           3000,
	rng:                rand.New(rand.NewSource(time.Now().UnixNano())),
}

// simulateDelay adds an artificial delay
func simulateDelay() {
	if !testConfig.Enabled {
		return
	}
	testConfig.mu.Lock()
	delay := testConfig.rng.Intn(testConfig.MaxDelay-testConfig.MinDelay) + testConfig.MinDelay
	testConfig.mu.Unlock()
	time.Sleep(time.Duration(delay) * time.Millisecond)
}

// simulateFailure returns true if the test should fail
func simulateFailure() bool {
	if !testConfig.Enabled || !testConfig.RandomFailureMode {
		return false
	}
	testConfig.mu.Lock()
	defer testConfig.mu.Unlock()
	return testConfig.rng.Float64() < testConfig.FailureProbability
}

// TestResult represents the result of a BirdWeather test
type TestResult struct {
	Success         bool   `json:"success"`
	Stage           string `json:"stage"`
	Message         string `json:"message"`
	Error           string `json:"error,omitempty"`
	IsProgress      bool   `json:"isProgress,omitempty"`
	State           string `json:"state,omitempty"`           // Current state: running, completed, failed, timeout
	Timestamp       string `json:"timestamp,omitempty"`       // ISO8601 timestamp of the result
	ResultID        string `json:"resultId,omitempty"`        // Optional ID for test results like soundscapeID
	RateLimitExpiry int64  `json:"rateLimitExpiry,omitempty"` // Unix timestamp for when rate limit expires
}

// TestStage represents a stage in the BirdWeather test process
type TestStage int

const (
	APIConnectivity TestStage = iota
	Authentication
	SoundscapeUpload
	DetectionPost
)

// String returns the string representation of a test stage
func (s TestStage) String() string {
	switch s {
	case APIConnectivity:
		return "API Connectivity"
	case Authentication:
		return "Authentication"
	case SoundscapeUpload:
		return "Soundscape Upload"
	case DetectionPost:
		return "Detection Post"
	default:
		return "Unknown Stage"
	}
}

// Timeout constants for various test stages
const (
	apiTimeout    = 5 * time.Second
	authTimeout   = 5 * time.Second
	uploadTimeout = 10 * time.Second
	postTimeout   = 5 * time.Second
)

// networkTest represents a generic network test function
type birdweatherTest func(context.Context) error

// runTest executes a BirdWeather test with proper timeout and cleanup
func runTest(ctx context.Context, stage TestStage, test birdweatherTest) TestResult {
	// Add simulated delay if enabled
	simulateDelay()

	// Check for simulated failure
	if simulateFailure() {
		return TestResult{
			Success: false,
			Stage:   stage.String(),
			Error:   fmt.Sprintf("simulated %s failure", stage),
			Message: fmt.Sprintf("Failed to perform %s", stage),
		}
	}

	// Create buffered channel for test result
	resultChan := make(chan error, 1)

	// Create a context with a value to pass back the result ID
	ctx = context.WithValue(ctx, resultIDKey, &resultContext{})

	// Run the test in a goroutine
	go func() {
		resultChan <- test(ctx)
	}()

	// Wait for either test completion or context cancellation
	select {
	case <-ctx.Done():
		return TestResult{
			Success: false,
			Stage:   stage.String(),
			Error:   "operation timeout",
			Message: fmt.Sprintf("%s operation timed out", stage),
		}
	case err := <-resultChan:
		if err != nil {
			return TestResult{
				Success: false,
				Stage:   stage.String(),
				Error:   err.Error(),
				Message: fmt.Sprintf("Failed to perform %s", stage),
			}
		}
	}

	// Get any result ID from the context
	var resultID string
	if rc, ok := ctx.Value(resultIDKey).(*resultContext); ok && rc != nil {
		resultID = rc.ID
	}

	// Create appropriate success message based on the stage
	var message string
	switch stage {
	case SoundscapeUpload:
		message = fmt.Sprintf("Successfully uploaded test soundscape (0.5 second silent audio) to BirdWeather. This recording should appear on your BirdWeather station at %s.", time.Now().Format("Jan 2, 2006 at 15:04:05"))
	case DetectionPost:
		message = "Successfully posted test detection to BirdWeather: Whooper Swan (Cygnus cygnus) with unlikely confidence."
	default:
		message = fmt.Sprintf("Successfully completed %s", stage)
	}

	return TestResult{
		Success:  true,
		Stage:    stage.String(),
		Message:  message,
		ResultID: resultID,
	}
}

// testAPIConnectivity tests basic connectivity to the BirdWeather API
func (b *BwClient) testAPIConnectivity(ctx context.Context) TestResult {
	apiCtx, apiCancel := context.WithTimeout(ctx, apiTimeout)
	defer apiCancel()

	return runTest(apiCtx, APIConnectivity, func(ctx context.Context) error {
		// Define the API endpoint URL
		apiEndpoint := "https://app.birdweather.com/api/v1"

		// Parse URL to extract the hostname
		parsedURL, err := url.Parse(apiEndpoint)
		if err != nil {
			return fmt.Errorf("invalid API endpoint URL: %w", err)
		}
		hostname := parsedURL.Hostname()

		// First attempt: Use standard HTTP client
		log.Printf("Testing connectivity to BirdWeather API at %s", apiEndpoint)
		err = tryAPIConnection(ctx, apiEndpoint)

		// If first attempt fails with DNS error, try fallback DNS resolution
		if err != nil {
			if isDNSError(err) {
				log.Printf("DNS resolution failed: %v, attempting fallback...", err)

				// Attempt DNS resolution with fallback resolvers
				ips, resolveErr := resolveDNSWithFallback(hostname)
				if resolveErr != nil {
					return fmt.Errorf("Failed to connect to BirdWeather API at %s: %w - Could not resolve the BirdWeather API hostname", apiEndpoint, err)
				}

				// If fallback DNS succeeded, it means the system DNS is incorrectly configured
				// We don't connect directly with IP as that would cause HTTPS certificate validation issues
				log.Printf("Fallback DNS successfully resolved %s while system DNS failed", hostname)
				log.Printf("This indicates your system DNS is incorrectly configured")

				// Log the resolved IPs for debugging
				ipStrings := make([]string, len(ips))
				for i, ip := range ips {
					ipStrings[i] = ip.String()
				}
				log.Printf("Resolved IPs using fallback DNS: %s", strings.Join(ipStrings, ", "))

				// Try connecting again with the original FQDN - this may work if the DNS
				// resolution failure was transient or if the fallback resolution affected DNS cache
				log.Printf("Retrying connection with original hostname after fallback DNS resolution")
				retryErr := tryAPIConnection(ctx, apiEndpoint)
				if retryErr == nil {
					log.Printf("✅ Successfully connected to BirdWeather API after fallback DNS resolution")
					return nil
				}

				// Both attempts failed
				return fmt.Errorf("Failed to connect to BirdWeather API at %s: %w - Failed to perform API Connectivity connection with system DNS. Fallback DNS resolved the hostname, indicating issue with your systems DNS resolver configuration. Please check your network settings.", apiEndpoint, err)
			}

			// Not a DNS error, return the original error
			return err
		}

		return nil
	})
}

// Helper function to check if an error is DNS-related
func isDNSError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	return strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "lookup ") ||
		strings.Contains(errStr, "dial tcp") ||
		strings.Contains(errStr, "DNS") ||
		strings.Contains(errStr, "dns") ||
		strings.Contains(errStr, "cannot resolve")
}

// Helper function to replace hostname with IP in URL
func replaceHostWithIP(urlStr, ip string) string {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return urlStr // return original if parsing fails
	}

	// Keep track of original port if specified
	port := parsedURL.Port()
	if port != "" {
		parsedURL.Host = ip + ":" + port
	} else {
		parsedURL.Host = ip
	}

	return parsedURL.String()
}

// tryAPIConnection attempts to connect to the API endpoint
func tryAPIConnection(ctx context.Context, apiEndpoint string, hostHeader ...string) error {
	req, err := http.NewRequestWithContext(ctx, "HEAD", apiEndpoint, http.NoBody)
	if err != nil {
		return err
	}

	// Set User-Agent
	req.Header.Set("User-Agent", "BirdNET-Go")

	// If host header is provided (for IP direct connections), set it
	if len(hostHeader) > 0 && hostHeader[0] != "" {
		req.Host = hostHeader[0]
	}

	// Create a temporary HTTP client with a shorter timeout for this test
	client := &http.Client{
		Timeout: apiTimeout,
		// Add special transport to handle potential certificate issues with direct IP
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false, // Keep secure by default
			},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return fmt.Errorf("API connectivity test timed out while connecting to %s: %w", apiEndpoint, err)
		}
		// Check if this is a DNS error
		if isDNSError(err) {
			return fmt.Errorf("Failed to connect to BirdWeather API at %s: %w - Could not resolve the BirdWeather API hostname", apiEndpoint, err)
		}
		return fmt.Errorf("Failed to connect to BirdWeather API at %s: %w", apiEndpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		// Special handling for 404 Not Found errors
		if resp.StatusCode == 404 {
			log.Printf("BirdWeather API endpoint not found: %s returned 404", apiEndpoint)
			return fmt.Errorf("API endpoint not found (404): %s", apiEndpoint)
		}
		log.Printf("BirdWeather API returned error status: %d for URL %s", resp.StatusCode, apiEndpoint)
		return fmt.Errorf("API at %s returned error status: %d", apiEndpoint, resp.StatusCode)
	}

	// Successfully connected to the API
	log.Printf("✅ Successfully connected to BirdWeather API at %s (status: %d)", apiEndpoint, resp.StatusCode)
	return nil
}

// testAuthentication tests authentication with the BirdWeather API
func (b *BwClient) testAuthentication(ctx context.Context) TestResult {
	authCtx, authCancel := context.WithTimeout(ctx, authTimeout)
	defer authCancel()

	return runTest(authCtx, Authentication, func(ctx context.Context) error {
		// Check if the station ID is valid by attempting to retrieve station details
		stationURL := fmt.Sprintf("https://app.birdweather.com/api/v1/stations/%s", b.BirdweatherID)

		// Try primary authentication method
		err := tryAuthentication(ctx, b, stationURL)
		if err != nil {
			// If it's a DNS or network error, try with alternative methods
			if isDNSError(err) || isNetworkError(err) {
				log.Printf("Primary authentication failed with network error: %v", err)

				// Try to resolve the hostname with fallback DNS
				parsedURL, parseErr := url.Parse(stationURL)
				if parseErr != nil {
					return fmt.Errorf("invalid station URL: %w", parseErr)
				}

				hostname := parsedURL.Hostname()
				ips, resolveErr := resolveDNSWithFallback(hostname)
				if resolveErr != nil {
					return fmt.Errorf("all DNS resolution attempts failed during authentication: %w", resolveErr)
				}

				// Try each resolved IP
				for _, ip := range ips {
					ipEndpoint := replaceHostWithIP(stationURL, ip.String())
					log.Printf("Attempting authentication with fallback DNS: %s", ipEndpoint)

					// Use the original hostname for Host header
					authErr := tryAuthenticationWithHostOverride(ctx, b, ipEndpoint, hostname)
					if authErr == nil {
						log.Printf("✅ Successfully authenticated with BirdWeather via fallback DNS")
						return nil
					}
				}

				// All fallback attempts failed
				return fmt.Errorf("authentication failed using both standard and fallback methods: %w", err)
			}

			// Not a network error, return original error
			return err
		}

		return nil
	})
}

// tryAuthentication attempts to authenticate with the station URL
func tryAuthentication(ctx context.Context, b *BwClient, stationURL string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", stationURL, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "BirdNET-Go")

	resp, err := b.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to authenticate with BirdWeather at %s: %w", stationURL, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 401, 403:
		log.Printf("❌ BirdWeather authentication failed: invalid station ID (tried URL: %s)", stationURL)
		return fmt.Errorf("authentication failed: invalid station ID (tried URL: %s)", stationURL)
	case 404:
		log.Printf("❌ BirdWeather station not found: %s returned 404", stationURL)
		return fmt.Errorf("station not found (404): %s", stationURL)
	default:
		if resp.StatusCode >= 400 {
			log.Printf("❌ BirdWeather authentication failed: server returned status code %d for URL %s", resp.StatusCode, stationURL)
			return fmt.Errorf("authentication failed: server returned status code %d for URL %s", resp.StatusCode, stationURL)
		}
	}

	// Successfully authenticated - don't log the actual token
	log.Printf("✅ Successfully authenticated with BirdWeather (status: %d)", resp.StatusCode)
	return nil
}

// tryAuthenticationWithHostOverride attempts to authenticate with a provided host override
func tryAuthenticationWithHostOverride(ctx context.Context, b *BwClient, stationURL, hostOverride string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", stationURL, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "BirdNET-Go")

	// Set host header for direct IP connection
	if hostOverride != "" {
		req.Host = hostOverride
	}

	// Create a client with custom transport to handle direct IP connection
	client := &http.Client{
		Timeout: authTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false, // Keep secure
			},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to authenticate with BirdWeather at %s (host: %s): %w", stationURL, hostOverride, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 401, 403:
		log.Printf("❌ BirdWeather authentication failed: invalid station ID (tried URL: %s)", stationURL)
		return fmt.Errorf("authentication failed: invalid station ID (tried URL: %s)", stationURL)
	case 404:
		log.Printf("❌ BirdWeather station not found: %s returned 404", stationURL)
		return fmt.Errorf("station not found (404): %s", stationURL)
	default:
		if resp.StatusCode >= 400 {
			log.Printf("❌ BirdWeather authentication failed: server returned status code %d for URL %s", resp.StatusCode, stationURL)
			return fmt.Errorf("authentication failed: server returned status code %d for URL %s", resp.StatusCode, stationURL)
		}
	}

	// Successfully authenticated
	log.Printf("✅ Successfully authenticated with BirdWeather (status: %d)", resp.StatusCode)
	return nil
}

// Helper function to check if error is network-related
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "network") ||
		strings.Contains(errStr, "unreachable") ||
		strings.Contains(errStr, "reset by peer") ||
		strings.Contains(errStr, "connection closed")
}

// testSoundscapeUpload tests uploading a small soundscape to BirdWeather
func (b *BwClient) testSoundscapeUpload(ctx context.Context) TestResult {
	uploadCtx, uploadCancel := context.WithTimeout(ctx, uploadTimeout)
	defer uploadCancel()

	return runTest(uploadCtx, SoundscapeUpload, func(ctx context.Context) error {
		// Generate a small test PCM data (500ms of silence)
		sampleRate := 48000
		numSamples := sampleRate / 2              // 0.5 seconds
		testPCMData := make([]byte, numSamples*2) // 16-bit samples = 2 bytes per sample

		// Generate current timestamp in the required format
		timestamp := time.Now().Format("2006-01-02T15:04:05.000-0700")

		// Attempt to upload the test soundscape
		soundscapeID, err := b.UploadSoundscape(timestamp, testPCMData)
		if err != nil {
			log.Printf("❌ BirdWeather soundscape upload failed: %s", err)
			return fmt.Errorf("failed to upload soundscape: %w", err)
		}

		// Successfully uploaded soundscape
		log.Printf("✅ Successfully uploaded test soundscape to BirdWeather with ID: %s", soundscapeID)

		// Store the soundscapeID in the context
		if rc, ok := ctx.Value(resultIDKey).(*resultContext); ok && rc != nil {
			rc.ID = soundscapeID
		}

		return nil
	})
}

// testDetectionPost tests posting a test detection to BirdWeather
func (b *BwClient) testDetectionPost(ctx context.Context, soundscapeID string) TestResult {
	postCtx, postCancel := context.WithTimeout(ctx, postTimeout)
	defer postCancel()

	return runTest(postCtx, DetectionPost, func(ctx context.Context) error {
		// Generate current timestamp in the required format
		timestamp := time.Now().Format("2006-01-02T15:04:05.000-0700")

		// Use a test detection with 0% confidence to avoid contaminating real data
		commonName := "Whooper Swan"
		scientificName := "Cygnus cygnus"
		confidence := 0.3 // 30% confidence to indicate this is not a real detection

		// Post the test detection
		err := b.PostDetection(soundscapeID, timestamp, commonName, scientificName, confidence)
		if err != nil {
			log.Printf("❌ BirdWeather detection post failed: %s", err)
			return fmt.Errorf("failed to post detection: %w", err)
		}

		// Successfully posted detection
		log.Printf("✅ Successfully posted test detection to BirdWeather: %s (%s) with confidence %.2f",
			commonName, scientificName, confidence)

		return nil
	})
}

// TestConnection performs a multi-stage test of the BirdWeather connection and functionality
func (b *BwClient) TestConnection(ctx context.Context, resultChan chan<- TestResult) {
	// Helper function to send a result
	sendResult := func(result TestResult) {
		// Mark progress messages
		result.IsProgress = strings.Contains(strings.ToLower(result.Message), "running") ||
			strings.Contains(strings.ToLower(result.Message), "testing") ||
			strings.Contains(strings.ToLower(result.Message), "establishing") ||
			strings.Contains(strings.ToLower(result.Message), "initializing")

		// Set state based on result
		switch {
		case result.State != "":
			// Keep existing state if explicitly set
		case result.Error != "":
			result.State = "failed"
			result.Success = false
			result.IsProgress = false
		case result.IsProgress:
			result.State = "running"
		case result.Success:
			result.State = "completed"
		case strings.Contains(strings.ToLower(result.Error), "timeout") ||
			strings.Contains(strings.ToLower(result.Error), "deadline exceeded"):
			result.State = "timeout"
		default:
			result.State = "failed"
		}

		// Add timestamp
		result.Timestamp = time.Now().Format(time.RFC3339)

		// Log the result with emoji
		emoji := "❌"
		if result.Success {
			emoji = "✅"
		}

		// Format the log message
		logMsg := result.Message
		if !result.Success && result.Error != "" {
			logMsg = fmt.Sprintf("%s: %s", result.Message, result.Error)
		}
		log.Printf("%s %s: %s", emoji, result.Stage, logMsg)

		// Send result to channel
		select {
		case <-ctx.Done():
			return
		case resultChan <- result:
		}
	}

	// Check context before starting
	if err := ctx.Err(); err != nil {
		sendResult(TestResult{
			Success: false,
			Stage:   "Test Setup",
			Message: "Test cancelled",
			Error:   err.Error(),
			State:   "timeout",
		})
		return
	}

	// Start with the explicit "Starting Test" stage
	sendResult(TestResult{
		Success:    true,
		Stage:      "Starting Test",
		Message:    "Initializing BirdWeather Connection Test...",
		State:      "running",
		IsProgress: true,
	})

	// Check rate limiting
	if err := checkRateLimit(); err != nil {
		sendResult(TestResult{
			Success: false,
			Stage:   "Starting Test",
			Message: "Rate limit check failed",
			Error:   err.Error(),
			State:   "failed",
		})
		return
	}

	// Helper function to run a test stage
	runStage := func(stage TestStage, test func() TestResult) bool {
		// First, mark the "Starting Test" stage as completed if we're on the first real test
		if stage == APIConnectivity {
			sendResult(TestResult{
				Success:    true,
				Stage:      "Starting Test",
				Message:    "Initialization complete, starting tests",
				State:      "completed",
				IsProgress: false,
			})
		}

		// Send progress message for current stage
		sendResult(TestResult{
			Success:    true,
			Stage:      stage.String(),
			Message:    fmt.Sprintf("Running %s test...", stage.String()),
			State:      "running",
			IsProgress: true,
		})

		// Execute the test
		result := test()
		sendResult(result)
		return result.Success
	}

	// Stage 1: API Connectivity
	if !runStage(APIConnectivity, func() TestResult {
		return b.testAPIConnectivity(ctx)
	}) {
		return
	}

	// Stage 2: Authentication
	if !runStage(Authentication, func() TestResult {
		return b.testAuthentication(ctx)
	}) {
		return
	}

	// Stage 3: Soundscape Upload
	var soundscapeID string
	uploadResult := runStage(SoundscapeUpload, func() TestResult {
		result := b.testSoundscapeUpload(ctx)
		if result.Success {
			// Get the soundscape ID directly from the result
			soundscapeID = result.ResultID
		}
		return result
	})

	if !uploadResult || soundscapeID == "" {
		// If we couldn't get a soundscape ID, use a mock one for the detection test
		soundscapeID = "test123"
	}

	// Stage 4: Detection Post
	runStage(DetectionPost, func() TestResult {
		return b.testDetectionPost(ctx, soundscapeID)
	})
}

// UploadTestSoundscape uploads a test soundscape for testing purposes
func (b *BwClient) UploadTestSoundscape(ctx context.Context) TestResult {
	simulateDelay()

	if simulateFailure() {
		return TestResult{
			Success: false,
			Stage:   SoundscapeUpload.String(),
			Error:   "simulated soundscape upload failure",
			Message: "Failed to upload test soundscape",
		}
	}

	// Generate a small test PCM data (500ms of silence)
	sampleRate := 48000
	numSamples := sampleRate / 2              // 0.5 seconds
	testPCMData := make([]byte, numSamples*2) // 16-bit samples = 2 bytes per sample

	// Generate current timestamp in the required format
	timestamp := time.Now().Format("2006-01-02T15:04:05.000-0700")

	// Create a channel for the upload result
	resultChan := make(chan struct {
		id  string
		err error
	}, 1)

	go func() {
		id, err := b.UploadSoundscape(timestamp, testPCMData)
		resultChan <- struct {
			id  string
			err error
		}{id, err}
	}()

	// Wait for either the context to be done or the upload to complete
	select {
	case <-ctx.Done():
		return TestResult{
			Success: false,
			Stage:   SoundscapeUpload.String(),
			Error:   "Soundscape upload timeout",
			Message: "Soundscape upload timed out",
		}
	case result := <-resultChan:
		if result.err != nil {
			return TestResult{
				Success: false,
				Stage:   SoundscapeUpload.String(),
				Error:   result.err.Error(),
				Message: "Failed to upload test soundscape",
			}
		}
		return TestResult{
			Success: true,
			Stage:   SoundscapeUpload.String(),
			Message: fmt.Sprintf("Successfully uploaded test soundscape (0.5 second silent audio). <span class=\"text-info\">This recording should appear on your BirdWeather station at %s with ID: %s</span>", time.Now().Format("Jan 2, 2006 at 15:04:05"), result.id),
		}
	}
}

// PostTestDetection posts a test detection for testing purposes
func (b *BwClient) PostTestDetection(ctx context.Context, soundscapeID string) TestResult {
	simulateDelay()

	if simulateFailure() {
		return TestResult{
			Success: false,
			Stage:   DetectionPost.String(),
			Error:   "simulated detection post failure",
			Message: "Failed to post test detection",
		}
	}

	// Generate current timestamp in the required format
	timestamp := time.Now().Format("2006-01-02T15:04:05.000-0700")

	// Use a test detection with 0% confidence to avoid contaminating real data
	commonName := "Whooper Swan"
	scientificName := "Cygnus cygnus"
	confidence := 0.3 // 30% confidence to indicate this is not a real detection

	// Create a channel for the post result
	resultChan := make(chan error, 1)

	go func() {
		err := b.PostDetection(soundscapeID, timestamp, commonName, scientificName, confidence)
		resultChan <- err
	}()

	// Wait for either the context to be done or the post to complete
	select {
	case <-ctx.Done():
		return TestResult{
			Success: false,
			Stage:   DetectionPost.String(),
			Error:   "Detection post timeout",
			Message: "Detection post timed out",
		}
	case err := <-resultChan:
		if err != nil {
			return TestResult{
				Success: false,
				Stage:   DetectionPost.String(),
				Error:   err.Error(),
				Message: "Failed to post test detection",
			}
		}
		return TestResult{
			Success: true,
			Stage:   DetectionPost.String(),
			Message: "Successfully posted test detection: Whooper Swan (Cygnus cygnus) with unlikely confidence.",
		}
	}
}
