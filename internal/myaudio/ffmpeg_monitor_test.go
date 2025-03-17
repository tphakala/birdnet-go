package myaudio

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock implementations for testing

// MockClock is a mock implementation of the Clock interface
type MockClock struct {
	mock.Mock
}

// Make sure MockFFmpegProcess implements ProcessCleaner interface
var _ ProcessCleaner = &MockFFmpegProcess{}

func (m *MockClock) Now() time.Time {
	args := m.Called()
	return args.Get(0).(time.Time)
}

func (m *MockClock) NewTicker(duration time.Duration) Ticker {
	args := m.Called(duration)
	return args.Get(0).(Ticker)
}

func (m *MockClock) Sleep(duration time.Duration) {
	m.Called(duration)
}

// MockTicker is a mock implementation of the Ticker interface
type MockTicker struct {
	mock.Mock
	tickChan chan time.Time
}

func NewMockTicker() *MockTicker {
	return &MockTicker{
		tickChan: make(chan time.Time),
	}
}

func (m *MockTicker) C() <-chan time.Time {
	m.Called()
	return m.tickChan
}

func (m *MockTicker) Stop() {
	m.Called()
}

// SendTick simulates a tick event for testing
func (m *MockTicker) SendTick() {
	m.tickChan <- time.Now()
}

// MockProcessManager is a mock implementation of the ProcessManager interface
type MockProcessManager struct {
	mock.Mock
}

func (m *MockProcessManager) FindProcesses() ([]ProcessInfo, error) {
	args := m.Called()
	return args.Get(0).([]ProcessInfo), args.Error(1)
}

func (m *MockProcessManager) TerminateProcess(pid int) error {
	args := m.Called(pid)
	return args.Error(0)
}

func (m *MockProcessManager) IsProcessRunning(pid int) bool {
	args := m.Called(pid)
	return args.Bool(0)
}

// MockProcessRepository is a mock implementation of the ProcessRepository interface
type MockProcessRepository struct {
	mock.Mock
	processes map[string]interface{}
	mu        sync.RWMutex
}

func NewMockProcessRepository() *MockProcessRepository {
	return &MockProcessRepository{
		processes: make(map[string]interface{}),
	}
}

func (m *MockProcessRepository) ForEach(callback func(key, value any) bool) {
	m.Called(callback)

	// Actually execute the callback with the stored processes
	m.mu.RLock()
	defer m.mu.RUnlock()

	for key, value := range m.processes {
		if !callback(key, value) {
			break
		}
	}
}

// AddProcess adds a process to the mock repository for testing
func (m *MockProcessRepository) AddProcess(url string, process interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processes[url] = process
}

// ClearProcesses removes all processes from the mock repository
func (m *MockProcessRepository) ClearProcesses() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processes = make(map[string]interface{})
}

// MockConfigProvider is a mock implementation of the ConfigProvider interface
type MockConfigProvider struct {
	mock.Mock
}

func (m *MockConfigProvider) GetConfiguredURLs() []string {
	args := m.Called()
	return args.Get(0).([]string)
}

func (m *MockConfigProvider) GetMonitoringInterval() time.Duration {
	args := m.Called()
	return args.Get(0).(time.Duration)
}

func (m *MockConfigProvider) GetProcessCleanupSettings() CleanupSettings {
	args := m.Called()
	return args.Get(0).(CleanupSettings)
}

// MockCommandExecutor is a mock implementation of the CommandExecutor interface
type MockCommandExecutor struct {
	mock.Mock
}

func (m *MockCommandExecutor) ExecuteCommand(name string, args ...string) ([]byte, error) {
	// Call with the actual arguments separately instead of as a slice
	callArgs := make([]interface{}, 0, len(args)+1)
	callArgs = append(callArgs, name)
	for _, arg := range args {
		callArgs = append(callArgs, arg)
	}

	mockArgs := m.Called(callArgs...)
	return mockArgs.Get(0).([]byte), mockArgs.Error(1)
}

// Helper function to convert string args to interface{} for mock Calls
func convertArgsToInterface(args []string) []interface{} {
	result := make([]interface{}, len(args))
	for i, v := range args {
		result[i] = v
	}
	return result
}

// Mock FFmpegProcess for testing
type MockFFmpegProcess struct {
	cmd           *MockCmd
	cleanupCalled bool
	cleanupURLs   []string
	expectedPID   int
}

func NewMockFFmpegProcess(pid int) *MockFFmpegProcess {
	cmd := &MockCmd{pid: pid}
	return &MockFFmpegProcess{
		cmd:         cmd,
		expectedPID: pid,
	}
}

func (p *MockFFmpegProcess) Cleanup(url string) {
	p.cleanupCalled = true
	p.cleanupURLs = append(p.cleanupURLs, url)
}

// MockCmd mocks an exec.Cmd
type MockCmd struct {
	pid     int
	process *MockProcess
}

// MockProcess mocks os.Process
type MockProcess struct {
	pid int
}

func (p *MockProcess) Kill() error {
	return nil
}

// Test cases

func TestNewFFmpegMonitor(t *testing.T) {
	// Create mock dependencies
	mockConfig := new(MockConfigProvider)
	mockProcMgr := new(MockProcessManager)
	mockRepo := NewMockProcessRepository()
	mockClock := new(MockClock)

	// Create the monitor
	monitor := NewFFmpegMonitor(mockConfig, mockProcMgr, mockRepo, mockClock)

	// Verify the monitor was created with correct dependencies
	assert.NotNil(t, monitor, "Monitor should not be nil")
	assert.Equal(t, mockConfig, monitor.config, "Config should be correctly set")
	assert.Equal(t, mockProcMgr, monitor.processManager, "ProcessManager should be correctly set")
	assert.Equal(t, mockRepo, monitor.processRepo, "ProcessRepository should be correctly set")
	assert.Equal(t, mockClock, monitor.clock, "Clock should be correctly set")
	assert.NotNil(t, monitor.done, "Done channel should be initialized")
	assert.False(t, monitor.running.Load(), "Monitor should not be running initially")
}

func TestMonitorStartStop(t *testing.T) {
	// Create mock dependencies
	mockConfig := new(MockConfigProvider)
	mockProcMgr := new(MockProcessManager)
	mockRepo := NewMockProcessRepository()
	mockClock := new(MockClock)
	mockTicker := NewMockTicker()

	// Configure mocks
	mockConfig.On("GetMonitoringInterval").Return(30 * time.Second)
	mockClock.On("NewTicker", 30*time.Second).Return(mockTicker)
	// Use Maybe() so the test doesn't expect an exact number of calls to C()
	mockTicker.On("C").Return().Maybe()
	mockTicker.On("Stop").Return()

	// Create the monitor
	monitor := NewFFmpegMonitor(mockConfig, mockProcMgr, mockRepo, mockClock)

	// Start the monitor
	monitor.Start()

	// Give the goroutine a small amount of time to start
	time.Sleep(10 * time.Millisecond)

	// Verify the monitor is running
	assert.True(t, monitor.IsRunning(), "Monitor should be running after Start")
	assert.NotNil(t, monitor.monitorTicker, "Ticker should be created")

	// Stop the monitor
	monitor.Stop()

	// Verify the monitor is stopped
	assert.False(t, monitor.IsRunning(), "Monitor should not be running after Stop")
	assert.Nil(t, monitor.monitorTicker, "Ticker should be nil after Stop")

	// Verify expectations
	mockConfig.AssertExpectations(t)
	mockClock.AssertExpectations(t)
	mockTicker.AssertExpectations(t)
}

func TestMonitorDoubleStart(t *testing.T) {
	// Create mock dependencies
	mockConfig := new(MockConfigProvider)
	mockProcMgr := new(MockProcessManager)
	mockRepo := NewMockProcessRepository()
	mockClock := new(MockClock)
	mockTicker := NewMockTicker()

	// Configure mocks
	mockConfig.On("GetMonitoringInterval").Return(30 * time.Second).Once()
	mockClock.On("NewTicker", 30*time.Second).Return(mockTicker).Once()
	mockTicker.On("C").Return()
	mockTicker.On("Stop").Return()

	// Create the monitor
	monitor := NewFFmpegMonitor(mockConfig, mockProcMgr, mockRepo, mockClock)

	// Start the monitor
	monitor.Start()

	// Start again - should be a no-op
	monitor.Start()

	// Verify the monitor is running
	assert.True(t, monitor.IsRunning(), "Monitor should be running")

	// Stop the monitor
	monitor.Stop()

	// Verify expectations - especially that NewTicker was only called once
	mockConfig.AssertExpectations(t)
	mockClock.AssertExpectations(t)
}

func TestCheckProcesses(t *testing.T) {
	// Create mock dependencies
	mockConfig := new(MockConfigProvider)
	mockProcMgr := new(MockProcessManager)
	mockRepo := NewMockProcessRepository()
	mockClock := new(MockClock)

	// Create mock processes
	activeProcess := NewMockFFmpegProcess(123)
	inactiveProcess := NewMockFFmpegProcess(456)

	// Add processes to repository
	mockRepo.AddProcess("rtsp://active.example.com", activeProcess)
	mockRepo.AddProcess("rtsp://inactive.example.com", inactiveProcess)

	// Configure mocks
	mockConfig.On("GetConfiguredURLs").Return([]string{"rtsp://active.example.com"})
	// Use mock.Anything to accept any function argument - fixes the type mismatch
	mockRepo.On("ForEach", mock.Anything).Return()
	mockProcMgr.On("FindProcesses").Return([]ProcessInfo{}, nil)

	// Create the monitor
	monitor := NewFFmpegMonitor(mockConfig, mockProcMgr, mockRepo, mockClock)

	// Call checkProcesses
	err := monitor.checkProcesses()

	// Verify results
	assert.NoError(t, err, "checkProcesses should not return an error")
	assert.False(t, activeProcess.cleanupCalled, "Active process should not be cleaned up")
	assert.True(t, inactiveProcess.cleanupCalled, "Inactive process should be cleaned up")
	assert.Contains(t, inactiveProcess.cleanupURLs, "rtsp://inactive.example.com", "Cleanup should be called with correct URL")

	// Verify expectations
	mockConfig.AssertExpectations(t)
	mockRepo.AssertExpectations(t)
	mockProcMgr.AssertExpectations(t)
}
func TestCleanupOrphanedProcesses(t *testing.T) {
	// Create mock dependencies
	mockConfig := new(MockConfigProvider)
	mockProcMgr := new(MockProcessManager)
	mockRepo := NewMockProcessRepository()
	mockClock := new(MockClock)

	// Create mock processes
	knownProcess := NewMockFFmpegProcess(123)

	// Add processes to repository
	mockRepo.AddProcess("rtsp://example.com", knownProcess)

	// Configure mocks
	mockProcMgr.On("FindProcesses").Return([]ProcessInfo{
		{PID: 123, Name: "ffmpeg"}, // Known process
		{PID: 456, Name: "ffmpeg"}, // Orphaned process
	}, nil)
	// Use mock.Anything instead of mock.AnythingOfType for the function argument
	mockRepo.On("ForEach", mock.Anything).Return()
	mockProcMgr.On("TerminateProcess", 456).Return(nil)

	// Create the monitor
	monitor := NewFFmpegMonitor(mockConfig, mockProcMgr, mockRepo, mockClock)

	// Call cleanupOrphanedProcesses
	err := monitor.cleanupOrphanedProcesses()

	// Verify results
	assert.NoError(t, err, "cleanupOrphanedProcesses should not return an error")

	// Verify expectations
	mockProcMgr.AssertExpectations(t)
	mockRepo.AssertExpectations(t)
}

func TestCleanupOrphanedProcessesError(t *testing.T) {
	// Create mock dependencies
	mockConfig := new(MockConfigProvider)
	mockProcMgr := new(MockProcessManager)
	mockRepo := NewMockProcessRepository()
	mockClock := new(MockClock)

	// Configure mocks
	mockProcMgr.On("FindProcesses").Return([]ProcessInfo{}, errors.New("command failed"))

	// Create the monitor
	monitor := NewFFmpegMonitor(mockConfig, mockProcMgr, mockRepo, mockClock)

	// Call cleanupOrphanedProcesses
	err := monitor.cleanupOrphanedProcesses()

	// Verify results
	assert.Error(t, err, "cleanupOrphanedProcesses should return an error")
	assert.Contains(t, err.Error(), "error finding FFmpeg processes", "Error message should be descriptive")

	// Verify expectations
	mockProcMgr.AssertExpectations(t)
}

func TestMonitorLoopWithTick(t *testing.T) {
	// Create mock dependencies
	mockConfig := new(MockConfigProvider)
	mockProcMgr := new(MockProcessManager)
	mockRepo := NewMockProcessRepository()
	mockClock := new(MockClock)
	mockTicker := NewMockTicker()

	// Configure mocks
	mockConfig.On("GetMonitoringInterval").Return(30 * time.Second)
	mockClock.On("NewTicker", 30*time.Second).Return(mockTicker)
	mockTicker.On("C").Return()
	mockTicker.On("Stop").Return()
	mockConfig.On("GetConfiguredURLs").Return([]string{}).Maybe()
	// Use mock.Anything instead of mock.AnythingOfType for the function argument
	mockRepo.On("ForEach", mock.Anything).Return().Maybe()
	mockProcMgr.On("FindProcesses").Return([]ProcessInfo{}, nil).Maybe()

	// Create the monitor
	monitor := NewFFmpegMonitor(mockConfig, mockProcMgr, mockRepo, mockClock)

	// Start the monitor
	monitor.Start()

	// Wait a bit for goroutine to start
	time.Sleep(10 * time.Millisecond)

	// Send a tick to trigger checkProcesses
	mockTicker.SendTick()

	// Wait a bit for tick to be processed
	time.Sleep(10 * time.Millisecond)

	// Stop the monitor
	monitor.Stop()

	// Verify expectations
	mockConfig.AssertExpectations(t)
	mockClock.AssertExpectations(t)
	mockTicker.AssertExpectations(t)
}

func TestUnixProcessManager(t *testing.T) {
	// Create mock command executor
	mockExecutor := new(MockCommandExecutor)

	// Configure mock
	mockExecutor.On("ExecuteCommand", "pgrep", "ffmpeg").Return([]byte("123\n456\n"), nil)
	mockExecutor.On("ExecuteCommand", "kill", "-9", "123").Return([]byte{}, nil)
	mockExecutor.On("ExecuteCommand", "kill", "-0", "456").Return([]byte{}, nil)

	// Create process manager
	pm := &UnixProcessManager{cmdExecutor: mockExecutor}

	// Test FindProcesses
	processes, err := pm.FindProcesses()
	assert.NoError(t, err, "FindProcesses should not return an error")
	assert.Len(t, processes, 2, "Should find 2 processes")
	assert.Equal(t, 123, processes[0].PID, "First process should have PID 123")
	assert.Equal(t, 456, processes[1].PID, "Second process should have PID 456")

	// Test TerminateProcess
	err = pm.TerminateProcess(123)
	assert.NoError(t, err, "TerminateProcess should not return an error")

	// Test IsProcessRunning
	running := pm.IsProcessRunning(456)
	assert.True(t, running, "Process 456 should be running")

	// Verify expectations
	mockExecutor.AssertExpectations(t)
}

func TestWindowsProcessManager(t *testing.T) {
	// Create mock command executor
	mockExecutor := new(MockCommandExecutor)

	// Configure mock
	mockExecutor.On("ExecuteCommand", "tasklist", "/FI", "IMAGENAME eq ffmpeg.exe", "/NH", "/FO", "CSV").
		Return([]byte("\"ffmpeg.exe\",\"123\",\"Console\"\n\"ffmpeg.exe\",\"456\",\"Console\"\n"), nil)
	mockExecutor.On("ExecuteCommand", "taskkill", "/F", "/T", "/PID", "123").
		Return([]byte{}, nil)
	mockExecutor.On("ExecuteCommand", "tasklist", "/FI", "PID eq 456", "/NH").
		Return([]byte("ffmpeg.exe 456 Console"), nil)

	// Create process manager
	pm := &WindowsProcessManager{cmdExecutor: mockExecutor}

	// Test FindProcesses
	processes, err := pm.FindProcesses()
	assert.NoError(t, err, "FindProcesses should not return an error")
	assert.Len(t, processes, 2, "Should find 2 processes")
	assert.Equal(t, 123, processes[0].PID, "First process should have PID 123")
	assert.Equal(t, 456, processes[1].PID, "Second process should have PID 456")

	// Test TerminateProcess
	err = pm.TerminateProcess(123)
	assert.NoError(t, err, "TerminateProcess should not return an error")

	// Test IsProcessRunning
	running := pm.IsProcessRunning(456)
	assert.True(t, running, "Process 456 should be running")

	// Verify expectations
	mockExecutor.AssertExpectations(t)
}

func TestSettingsBasedConfigProvider(t *testing.T) {
	// We can't easily test this without mocking conf.Setting
	// This is more of an integration test than a unit test

	cp := &SettingsBasedConfigProvider{}

	// Test GetMonitoringInterval
	interval := cp.GetMonitoringInterval()
	assert.Equal(t, 30*time.Second, interval, "Default monitoring interval should be 30 seconds")

	// Test GetProcessCleanupSettings
	settings := cp.GetProcessCleanupSettings()
	assert.True(t, settings.Enabled, "Process cleanup should be enabled by default")
	assert.Equal(t, 5*time.Minute, settings.Timeout, "Default cleanup timeout should be 5 minutes")
}

func TestBoundedBuffer(t *testing.T) {
	// Create a buffer with a small size
	bufSize := 10
	buf := NewBoundedBuffer(bufSize)

	// Write a small string that fits in the buffer
	smallData := "small"
	n, err := buf.Write([]byte(smallData))

	assert.NoError(t, err, "Write should not return an error")
	assert.Equal(t, len(smallData), n, "Should return correct number of bytes written")
	assert.Equal(t, smallData, buf.String(), "Buffer should contain the written data")

	// Write a large string that exceeds the buffer size
	largeData := "this is a very long string that exceeds the buffer size"
	n, err = buf.Write([]byte(largeData))

	assert.NoError(t, err, "Write should not return an error")
	// The implementation returns the number of bytes actually stored (buffer size), not the input length
	assert.Equal(t, bufSize, n, "Should return number of bytes actually written to the buffer")

	// The buffer should contain only the last 'bufSize' bytes of largeData
	expected := largeData[len(largeData)-bufSize:]
	assert.Equal(t, expected, buf.String(), "Buffer should contain only the last bytes")

	// Write another small string
	anotherSmall := "new"
	n, err = buf.Write([]byte(anotherSmall))

	assert.NoError(t, err, "Write should not return an error")
	assert.Equal(t, len(anotherSmall), n, "Should return correct number of bytes written")

	// The buffer should now only contain the new string, since the small string completely fits
	// and the buffer was reset before writing it
	assert.Equal(t, anotherSmall, buf.String(), "Buffer should only contain the new data")
}

func TestBackoffStrategy(t *testing.T) {
	// Create a backoff strategy with 3 max attempts, 1s initial delay, and 5s max delay
	maxAttempts := 3
	initialDelay := 1 * time.Second
	maxDelay := 5 * time.Second

	backoff := newBackoffStrategy(maxAttempts, initialDelay, maxDelay)

	// Test initial delay
	delay, canRetry := backoff.nextDelay()
	assert.True(t, canRetry, "Should allow retry on first attempt")
	assert.Equal(t, initialDelay, delay, "First delay should match initial delay")

	// Test second delay (should be 2x initial)
	delay, canRetry = backoff.nextDelay()
	assert.True(t, canRetry, "Should allow retry on second attempt")
	assert.Equal(t, initialDelay*2, delay, "Second delay should be 2x initial")

	// Test third delay (should be 4x initial, but capped at max)
	delay, canRetry = backoff.nextDelay()
	assert.True(t, canRetry, "Should allow retry on third attempt")
	expectedDelay := initialDelay * 4
	if expectedDelay > maxDelay {
		expectedDelay = maxDelay
	}
	assert.Equal(t, expectedDelay, delay, "Third delay should be 4x initial or max")

	// Test fourth delay (should not allow retry)
	delay, canRetry = backoff.nextDelay()
	assert.False(t, canRetry, "Should not allow retry after max attempts")
	assert.Equal(t, time.Duration(0), delay, "Delay should be 0 when max attempts reached")

	// Test reset
	backoff.reset()
	delay, canRetry = backoff.nextDelay()
	assert.True(t, canRetry, "Should allow retry after reset")
	assert.Equal(t, initialDelay, delay, "Delay should reset to initial value")
}
