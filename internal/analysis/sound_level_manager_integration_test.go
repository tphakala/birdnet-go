package analysis

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
)

// testSettingsManager helps manage global settings for tests
type testSettingsManager struct {
	t             *testing.T
	mu            sync.Mutex
	originalDebug bool
	savedSettings *conf.Settings
}

func newTestSettingsManager(t *testing.T) *testSettingsManager {
	t.Helper()
	return &testSettingsManager{t: t}
}

func (m *testSettingsManager) setup() {
	m.t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Save current settings if they exist
	if settings := conf.Setting(); settings != nil {
		m.savedSettings = settings
		m.originalDebug = settings.Realtime.Audio.SoundLevel.Debug
	}
}

func (m *testSettingsManager) cleanup() {
	m.t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Restore original settings if they existed
	if m.savedSettings != nil && m.savedSettings.Realtime.Audio.SoundLevel.Debug != m.originalDebug {
		m.savedSettings.Realtime.Audio.SoundLevel.Debug = m.originalDebug
	}
}

// TestSoundLevelManagerFullLifecycle tests the complete lifecycle with real settings
func TestSoundLevelManagerFullLifecycle(t *testing.T) {
	// Cannot run in parallel due to global state
	settingsMgr := newTestSettingsManager(t)
	settingsMgr.setup()
	defer settingsMgr.cleanup()

	// Get baseline goroutine count
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	baselineGoroutines := runtime.NumGoroutine()

	// Create test components
	soundLevelChan := make(chan myaudio.SoundLevelData, 100)
	defer close(soundLevelChan)

	manager := NewSoundLevelManager(soundLevelChan, nil, nil, nil)
	require.NotNil(t, manager)

	// Test 1: Initial state
	assert.False(t, manager.IsRunning(), "Manager should not be running initially")

	// Test 2: Start when disabled (should be no-op)
	if settings := conf.Setting(); settings != nil {
		settings.Realtime.Audio.SoundLevel.Enabled = false
		err := manager.Start()
		assert.NoError(t, err, "Start with disabled should not error")
		assert.False(t, manager.IsRunning(), "Manager should not run when disabled")
	}

	// Test 3: Enable and start
	if settings := conf.Setting(); settings != nil {
		settings.Realtime.Audio.SoundLevel.Enabled = true
		settings.Realtime.Audio.SoundLevel.Interval = 5
		
		err := manager.Start()
		assert.NoError(t, err, "Start should succeed when enabled")
		assert.True(t, manager.IsRunning(), "Manager should be running")
		
		// Give time for goroutines to start
		time.Sleep(50 * time.Millisecond)
		
		// Test 4: Double start should be safe
		err = manager.Start()
		assert.NoError(t, err, "Double start should not error")
		assert.True(t, manager.IsRunning(), "Manager should still be running")
	}

	// Test 5: Stop
	manager.Stop()
	assert.False(t, manager.IsRunning(), "Manager should stop")
	
	// Test 6: Double stop should be safe
	manager.Stop()
	assert.False(t, manager.IsRunning(), "Manager should remain stopped")

	// Test 7: Restart cycle
	if settings := conf.Setting(); settings != nil {
		settings.Realtime.Audio.SoundLevel.Enabled = true
		
		err := manager.Restart()
		assert.NoError(t, err, "Restart should succeed")
		assert.True(t, manager.IsRunning(), "Manager should run after restart")
		
		// Change settings and restart
		settings.Realtime.Audio.SoundLevel.Interval = 10
		err = manager.Restart()
		assert.NoError(t, err, "Restart with new interval should succeed")
		assert.True(t, manager.IsRunning(), "Manager should still be running")
	}

	// Final cleanup
	manager.Stop()
	
	// Verify no goroutine leaks
	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	finalGoroutines := runtime.NumGoroutine()
	assert.LessOrEqual(t, finalGoroutines, baselineGoroutines+2,
		"Should not leak goroutines: baseline=%d, final=%d", baselineGoroutines, finalGoroutines)
}

// TestSoundLevelManagerDebugToggle tests debug setting changes
func TestSoundLevelManagerDebugToggle(t *testing.T) {
	// Cannot run in parallel due to global state
	settingsMgr := newTestSettingsManager(t)
	settingsMgr.setup()
	defer settingsMgr.cleanup()

	soundLevelChan := make(chan myaudio.SoundLevelData, 100)
	defer close(soundLevelChan)

	manager := NewSoundLevelManager(soundLevelChan, nil, nil, nil)

	if settings := conf.Setting(); settings != nil {
		// Start with debug off
		settings.Realtime.Audio.SoundLevel.Enabled = true
		settings.Realtime.Audio.SoundLevel.Debug = false
		
		err := manager.Start()
		require.NoError(t, err)
		assert.True(t, manager.IsRunning())
		
		// Enable debug and restart
		settings.Realtime.Audio.SoundLevel.Debug = true
		err = manager.Restart()
		assert.NoError(t, err)
		assert.True(t, manager.IsRunning())
		
		// The debug setting should be applied (we can't easily verify this without
		// implementation changes, but at least we verify it doesn't crash)
		
		// Disable debug
		settings.Realtime.Audio.SoundLevel.Debug = false
		err = manager.Restart()
		assert.NoError(t, err)
		
		manager.Stop()
	}
}

// TestSoundLevelManagerDataFlow tests that data flows through the channel
func TestSoundLevelManagerDataFlow(t *testing.T) {
	// Setup
	soundLevelChan := make(chan myaudio.SoundLevelData, 10)
	receivedCount := int32(0)
	
	// Create a consumer goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		timeout := time.After(2 * time.Second)
		for {
			select {
			case data := <-soundLevelChan:
				atomic.AddInt32(&receivedCount, 1)
				// Validate received data
				assert.NotZero(t, data.Timestamp)
				assert.NotEmpty(t, data.Source)
				assert.NotEmpty(t, data.Name)
				assert.Greater(t, len(data.OctaveBands), 0)
			case <-timeout:
				return
			}
		}
	}()
	
	// Send test data
	testData := myaudio.SoundLevelData{
		Timestamp: time.Now(),
		Source:    "test-source",
		Name:      "Test Device",
		Duration:  10,
		OctaveBands: map[string]myaudio.OctaveBandData{
			"1.0_kHz": {
				CenterFreq:  1000,
				Min:         -60.0,
				Max:         -30.0,
				Mean:        -45.0,
				SampleCount: 100,
			},
			"2.0_kHz": {
				CenterFreq:  2000,
				Min:         -65.0,
				Max:         -35.0,
				Mean:        -50.0,
				SampleCount: 100,
			},
		},
	}
	
	// Send multiple data points
	for i := 0; i < 5; i++ {
		soundLevelChan <- testData
		time.Sleep(10 * time.Millisecond)
	}
	
	// Wait for consumer to finish
	<-done
	close(soundLevelChan)
	
	// Verify data was received
	count := atomic.LoadInt32(&receivedCount)
	assert.Equal(t, int32(5), count, "Should receive all data points")
}

// TestSoundLevelManagerConcurrentOperations tests thread safety
func TestSoundLevelManagerConcurrentOperations(t *testing.T) {
	soundLevelChan := make(chan myaudio.SoundLevelData, 100)
	defer close(soundLevelChan)
	
	manager := NewSoundLevelManager(soundLevelChan, nil, nil, nil)
	
	// Run concurrent operations
	var wg sync.WaitGroup
	errors := make([]error, 0)
	var errorsMu sync.Mutex
	
	// Start multiple goroutines performing different operations
	operations := []func(){
		func() { manager.Start() },
		func() { manager.Stop() },
		func() { manager.Restart() },
		func() { _ = manager.IsRunning() },
	}
	
	// Run each operation multiple times concurrently
	for i := 0; i < 20; i++ {
		for _, op := range operations {
			wg.Add(1)
			operation := op // Capture loop variable
			go func() {
				defer wg.Done()
				
				// Run operation multiple times
				for j := 0; j < 5; j++ {
					func() {
						// Recover from any panic
						defer func() {
							if r := recover(); r != nil {
								errorsMu.Lock()
								errors = append(errors, fmt.Errorf("panic: %v", r))
								errorsMu.Unlock()
							}
						}()
						operation()
					}()
					time.Sleep(time.Millisecond)
				}
			}()
		}
	}
	
	// Wait for completion with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	
	select {
	case <-done:
		// Success
	case <-time.After(10 * time.Second):
		t.Fatal("Concurrent operations timed out")
	}
	
	// Check for errors
	assert.Empty(t, errors, "No panics should occur during concurrent operations")
	
	// Final cleanup
	manager.Stop()
}

// TestSoundLevelManagerWithMockPublishers tests with mock publishers
func TestSoundLevelManagerWithMockPublishers(t *testing.T) {
	// Setup mock components
	soundLevelChan := make(chan myaudio.SoundLevelData, 100)
	
	// Create mock processor with metrics
	mockMetrics := &observability.Metrics{
		SoundLevel: nil, // Would need proper mock, but nil is safe
	}
	mockProc := &processor.Processor{
		Metrics: mockMetrics,
	}
	
	// Create mock HTTP server
	mockHTTP := &httpcontroller.Server{
		APIV2: nil, // Would need proper mock, but nil is safe
	}
	
	// Create manager with mock components
	manager := NewSoundLevelManager(soundLevelChan, mockProc, mockHTTP, mockMetrics)
	
	// Test operations with mocks
	if settings := conf.Setting(); settings != nil {
		settings.Realtime.Audio.SoundLevel.Enabled = true
		settings.Realtime.MQTT.Enabled = false // Disable MQTT to avoid real connections
		
		err := manager.Start()
		assert.NoError(t, err)
		
		// The publishers will check for nil and handle gracefully
		time.Sleep(50 * time.Millisecond)
		
		manager.Stop()
	}
	
	close(soundLevelChan)
}

// TestControlMonitorIntegration tests control monitor hot reload
func TestControlMonitorIntegration(t *testing.T) {
	// Cannot run in parallel due to global state
	
	// Create control channels
	controlChan := make(chan string, 10)
	quitChan := make(chan struct{})
	soundLevelChan := make(chan myaudio.SoundLevelData, 100)
	notificationChan := make(chan handlers.Notification, 10)
	
	// Create control monitor
	cm := &ControlMonitor{
		controlChan:      controlChan,
		quitChan:         quitChan,
		soundLevelChan:   soundLevelChan,
		notificationChan: notificationChan,
		proc:             nil,
		httpServer:       nil,
		metrics:          nil,
	}
	
	// Start control monitor in background
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		cm.Start()
	}()
	
	// Give it time to start
	time.Sleep(50 * time.Millisecond)
	
	// Test sound level reconfigure signal
	if settings := conf.Setting(); settings != nil {
		settings.Realtime.Audio.SoundLevel.Enabled = true
		settings.Realtime.Audio.SoundLevel.Interval = 5
		
		// Send reconfigure signal
		select {
		case controlChan <- "reconfigure_sound_level":
			// Success
		case <-time.After(time.Second):
			t.Fatal("Failed to send control signal")
		}
		
		// Wait for notification
		select {
		case notification := <-notificationChan:
			assert.Contains(t, notification.Message, "Sound level monitoring")
		case <-time.After(2 * time.Second):
			t.Log("No notification received (may be expected if settings prevent start)")
		}
	}
	
	// Stop control monitor
	close(quitChan)
	
	// Wait for goroutine to finish
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	
	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Control monitor failed to stop")
	}
	
	// Cleanup
	close(controlChan)
	close(soundLevelChan)
	close(notificationChan)
}

// BenchmarkSoundLevelManagerOperations benchmarks manager operations
func BenchmarkSoundLevelManagerOperations(b *testing.B) {
	soundLevelChan := make(chan myaudio.SoundLevelData, 1000)
	defer close(soundLevelChan)
	
	manager := NewSoundLevelManager(soundLevelChan, nil, nil, nil)
	
	b.Run("IsRunning", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = manager.IsRunning()
		}
	})
	
	b.Run("Start-Stop", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			manager.Start()
			manager.Stop()
		}
	})
	
	b.Run("Restart", func(b *testing.B) {
		if settings := conf.Setting(); settings != nil {
			settings.Realtime.Audio.SoundLevel.Enabled = true
		}
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			manager.Restart()
		}
		manager.Stop()
	})
}