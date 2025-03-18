package myaudio

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// FuzzTestData contains the setup for a fuzz test
type FuzzTestData struct {
	NumConfiguredURLs    int      // Number of URLs configured
	NumRunningProcesses  int      // Number of processes currently running
	NumOrphanedProcesses int      // Number of orphaned processes (not in config)
	FailureRate          float64  // Rate of failures for IsProcessRunning (0.0-1.0)
	FindProcessError     bool     // Whether FindProcesses should return an error
	URLs                 []string // Generated URLs
	ProcessInfos         []ProcessInfo
}

// generateFuzzTestData creates randomized test data
func generateFuzzTestData(seed int64) FuzzTestData {
	r := rand.New(rand.NewSource(seed))

	data := FuzzTestData{
		NumConfiguredURLs:    r.Intn(50) + 1,    // 1-50 URLs
		NumRunningProcesses:  r.Intn(20) + 1,    // 1-20 processes
		NumOrphanedProcesses: r.Intn(10),        // 0-9 orphaned processes
		FailureRate:          r.Float64() * 0.5, // 0-50% failure rate
		FindProcessError:     r.Float64() < 0.1, // 10% chance of error
	}

	// Generate URLs
	data.URLs = make([]string, data.NumConfiguredURLs)
	for i := 0; i < data.NumConfiguredURLs; i++ {
		data.URLs[i] = generateRandomURL(r)
	}

	// Generate process infos
	totalProcesses := data.NumRunningProcesses + data.NumOrphanedProcesses
	data.ProcessInfos = make([]ProcessInfo, totalProcesses)
	for i := 0; i < totalProcesses; i++ {
		data.ProcessInfos[i] = ProcessInfo{
			PID:  1000 + i,
			Name: "ffmpeg",
		}
	}

	return data
}

// generateRandomURL creates a random RTSP URL
func generateRandomURL(r *rand.Rand) string {
	hosts := []string{"example.com", "test.com", "stream.org", "video.net", "media.io"}
	paths := []string{"live", "stream", "camera", "feed", "input", "output"}

	host := hosts[r.Intn(len(hosts))]
	path := paths[r.Intn(len(paths))]
	id := r.Intn(1000)

	return "rtsp://" + host + "/" + path + "/" + fmt.Sprintf("%d", id)
}

func TestFuzzCheckProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping fuzz test in short mode")
	}

	// Number of fuzz iterations
	iterations := 20
	if testing.Short() {
		iterations = 5
	}

	for i := 0; i < iterations; i++ {
		t.Run(fmt.Sprintf("FuzzSeed:%d", i), func(t *testing.T) {
			// Create test data with different seed each iteration
			data := generateFuzzTestData(int64(i))

			// Setup test context
			tc := NewTestContext(t)
			defer tc.Cleanup()

			// Configure URLs
			tc.WithConfiguredURLs(data.URLs)

			// Configure FindProcesses based on test data
			if data.FindProcessError {
				tc.ProcMgr.On("FindProcesses").Return([]ProcessInfo{}, assert.AnError).Maybe()
			} else {
				tc.ProcMgr.On("FindProcesses").Return(data.ProcessInfos, nil).Maybe()
			}

			// Add processes to repository
			expectedURLs := make(map[string]bool)

			// Add processes that are configured
			for i := 0; i < data.NumRunningProcesses && i < len(data.URLs); i++ {
				url := data.URLs[i]
				expectedURLs[url] = true
				process := NewMockFFmpegProcess(data.ProcessInfos[i].PID)
				tc.Repo.AddProcess(url, process)

				// Configure IsProcessRunning with random failures based on FailureRate
				isRunning := rand.Float64() >= data.FailureRate
				tc.ProcMgr.On("IsProcessRunning", data.ProcessInfos[i].PID).Return(isRunning).Maybe()
			}

			// Configure orphaned processes
			for i := data.NumRunningProcesses; i < len(data.ProcessInfos); i++ {
				tc.ProcMgr.On("IsProcessRunning", data.ProcessInfos[i].PID).Return(true).Maybe()
			}

			// Use a flexible matcher for any PID terminations rather than specific PIDs
			tc.ProcMgr.On("TerminateProcess", mock.AnythingOfType("int")).Return(nil).Maybe()

			// Setup ForEach matcher with expected URLs
			tc.Repo.On("ForEach", mock.AnythingOfType("func(interface {}, interface {}) bool")).Return().Maybe()

			// Run checks based on test conditions
			if !data.FindProcessError {
				err := tc.Monitor.checkProcesses()
				assert.NoError(t, err, "Check processes should not return error with valid input")

				err = tc.Monitor.cleanupOrphanedProcesses()
				assert.NoError(t, err, "Cleanup orphaned processes should not return error with valid input")
			} else {
				err := tc.Monitor.cleanupOrphanedProcesses()
				assert.Error(t, err, "Should return error when FindProcesses fails")
			}

			// Additional property-based assertions could be added here
			// For example, verifying that processes with failed IsProcessRunning were cleaned up
		})
	}
}

// TestFuzzMonitorLifecycle tests the monitor's lifecycle with randomized scenarios
func TestFuzzMonitorLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping fuzz test in short mode")
	}

	// Number of fuzz iterations
	iterations := 10
	if testing.Short() {
		iterations = 3
	}

	for i := 0; i < iterations; i++ {
		t.Run(fmt.Sprintf("FuzzSeed:%d", i), func(t *testing.T) {
			// Create test data with different seed each iteration
			seed := time.Now().UnixNano() + int64(i)
			r := rand.New(rand.NewSource(seed))

			// Create test context
			tc := NewTestContext(t)
			defer tc.Cleanup()

			// Random monitoring interval between 10ms and 100ms
			interval := time.Duration(r.Intn(90)+10) * time.Millisecond
			tc.Config.On("GetMonitoringInterval").Return(interval).Maybe()

			// Random number of URLs between 1 and 10
			numURLs := r.Intn(10) + 1
			urls := make([]string, numURLs)
			for i := 0; i < numURLs; i++ {
				urls[i] = generateRandomURL(r)
			}
			tc.WithConfiguredURLs(urls)

			// Setup notifications
			tickReceived := make(chan struct{}, 10)
			tc.Ticker.On("C").Return().Run(func(args mock.Arguments) {
				tickReceived <- struct{}{}
			}).Maybe()

			// Start the monitor
			tc.Monitor.Start()

			// Random number of operations between 1 and 5
			numOps := r.Intn(5) + 1
			for j := 0; j < numOps; j++ {
				// Wait for a tick
				select {
				case <-tickReceived:
					// Tick received
				case <-time.After(200 * time.Millisecond):
					// Skip if no tick received within timeout
					continue
				}

				// Perform a random operation
				opType := r.Intn(3)
				switch opType {
				case 0:
					// Add a process
					url := generateRandomURL(r)
					process := NewMockFFmpegProcess(2000 + j)
					tc.Repo.AddProcess(url, process)
				case 1:
					// Check processes
					tc.Monitor.checkProcesses()
				case 2:
					// Cleanup orphaned processes
					tc.ProcMgr.On("FindProcesses").Return([]ProcessInfo{}, nil).Maybe()
					tc.Monitor.cleanupOrphanedProcesses()
				}
			}

			// Stop the monitor
			tc.Monitor.Stop()

			// Verify monitor is stopped
			assert.False(t, tc.Monitor.IsRunning(), "Monitor should be stopped after Stop()")
		})
	}
}
