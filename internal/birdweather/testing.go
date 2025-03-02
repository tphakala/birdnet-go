// testing.go provides BirdWeather connection and functionality testing capabilities
package birdweather

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

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
	Success    bool   `json:"success"`
	Stage      string `json:"stage"`
	Message    string `json:"message"`
	Error      string `json:"error,omitempty"`
	IsProgress bool   `json:"isProgress,omitempty"`
	State      string `json:"state,omitempty"`     // Current state: running, completed, failed, timeout
	Timestamp  string `json:"timestamp,omitempty"` // ISO8601 timestamp of the result
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

	return TestResult{
		Success: true,
		Stage:   stage.String(),
		Message: fmt.Sprintf("Successfully completed %s", stage),
	}
}

// testAPIConnectivity tests basic connectivity to the BirdWeather API
func (b *BwClient) testAPIConnectivity(ctx context.Context) TestResult {
	apiCtx, apiCancel := context.WithTimeout(ctx, apiTimeout)
	defer apiCancel()

	return runTest(apiCtx, APIConnectivity, func(ctx context.Context) error {
		// Simple check if we can reach the BirdWeather API endpoint
		req, err := http.NewRequestWithContext(ctx, "HEAD", "https://app.birdweather.com/api/v1/health", http.NoBody)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "BirdNET-Go")

		// Create a temporary HTTP client with a shorter timeout for this test
		client := &http.Client{Timeout: apiTimeout}
		resp, err := client.Do(req)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return fmt.Errorf("API connectivity test timed out: %w", err)
			}
			return fmt.Errorf("failed to connect to BirdWeather API: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			return fmt.Errorf("API returned error status: %d", resp.StatusCode)
		}

		return nil
	})
}

// testAuthentication tests authentication with the BirdWeather API
func (b *BwClient) testAuthentication(ctx context.Context) TestResult {
	authCtx, authCancel := context.WithTimeout(ctx, authTimeout)
	defer authCancel()

	return runTest(authCtx, Authentication, func(ctx context.Context) error {
		// Check if the station ID is valid by attempting to retrieve station details
		stationURL := fmt.Sprintf("https://app.birdweather.com/api/v1/stations/%s", b.BirdweatherID)
		req, err := http.NewRequestWithContext(ctx, "GET", stationURL, http.NoBody)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "BirdNET-Go")

		resp, err := b.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to authenticate with BirdWeather: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return fmt.Errorf("authentication failed: invalid station ID")
		} else if resp.StatusCode >= 400 {
			return fmt.Errorf("authentication failed: server returned status code %d", resp.StatusCode)
		}

		return nil
	})
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
		_, err := b.UploadSoundscape(timestamp, testPCMData)
		if err != nil {
			return fmt.Errorf("failed to upload soundscape: %w", err)
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

		// Use a common test species - Whooper Swan
		commonName := "Whooper Swan"
		scientificName := "Cygnus cygnus"
		confidence := 0.95

		// Post the test detection
		err := b.PostDetection(soundscapeID, timestamp, commonName, scientificName, confidence)
		if err != nil {
			return fmt.Errorf("failed to post detection: %w", err)
		}

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

	// Helper function to run a test stage
	runStage := func(stage TestStage, test func() TestResult) bool {
		// Send progress message
		sendResult(TestResult{
			Success: true,
			Stage:   stage.String(),
			Message: fmt.Sprintf("Running %s test...", stage.String()),
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
			// Extract soundscape ID from success message
			if strings.Contains(result.Message, "ID:") {
				parts := strings.Split(result.Message, "ID:")
				if len(parts) > 1 {
					soundscapeID = strings.TrimSpace(parts[1])
				}
			}
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
			Message: fmt.Sprintf("Successfully uploaded test soundscape with ID: %s", result.id),
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

	// Use a common test species - Whooper Swan
	commonName := "Whooper Swan"
	scientificName := "Cygnus cygnus"
	confidence := 0.95

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
			Message: "Successfully posted test detection",
		}
	}
}
