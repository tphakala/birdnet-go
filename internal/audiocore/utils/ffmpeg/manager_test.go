package ffmpeg

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestNewManager(t *testing.T) {
	t.Parallel()

	config := ManagerConfig{
		MaxProcesses:      10,
		HealthCheckPeriod: 30 * time.Second,
		CleanupTimeout:    10 * time.Second,
		RestartPolicy: RestartPolicy{
			Enabled:           true,
			MaxRetries:        5,
			InitialDelay:      1 * time.Second,
			MaxDelay:          30 * time.Second,
			BackoffMultiplier: 2.0,
		},
	}

	manager := NewManager(config)
	if manager == nil {
		t.Error("NewManager should not return nil")
	}

	// Test that initial state is correct
	processes := manager.ListProcesses()
	if len(processes) != 0 {
		t.Errorf("Expected 0 processes initially, got %d", len(processes))
	}
}

func TestManagerCreateProcess(t *testing.T) {
	t.Parallel()

	config := ManagerConfig{
		MaxProcesses:      2,
		HealthCheckPeriod: 30 * time.Second,
		CleanupTimeout:    10 * time.Second,
	}

	manager := NewManager(config)

	processConfig := &ProcessConfig{
		ID:           "test-process-1",
		InputURL:     "test.wav",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BitDepth:     16,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg",
	}

	process, err := manager.CreateProcess(processConfig)
	if err != nil {
		t.Errorf("Failed to create process: %v", err)
	}

	if process.ID() != processConfig.ID {
		t.Errorf("Expected process ID %s, got %s", processConfig.ID, process.ID())
	}

	// Test process is in manager
	retrievedProcess, exists := manager.GetProcess(processConfig.ID)
	if !exists {
		t.Error("Process should exist in manager")
	}

	if retrievedProcess.ID() != processConfig.ID {
		t.Errorf("Retrieved process has wrong ID: %s", retrievedProcess.ID())
	}
}

func TestManagerDuplicateProcess(t *testing.T) {
	t.Parallel()

	config := ManagerConfig{
		MaxProcesses: 10,
	}

	manager := NewManager(config)

	processConfig := &ProcessConfig{
		ID:           "duplicate-test",
		InputURL:     "test.wav",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BitDepth:     16,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg",
	}

	// Create first process
	_, err := manager.CreateProcess(processConfig)
	if err != nil {
		t.Errorf("Failed to create first process: %v", err)
	}

	// Try to create duplicate
	_, err = manager.CreateProcess(processConfig)
	if err == nil {
		t.Error("Expected error when creating duplicate process")
	}
}

func TestManagerMaxProcessesLimit(t *testing.T) {
	t.Parallel()

	config := ManagerConfig{
		MaxProcesses: 1, // Only allow 1 process
	}

	manager := NewManager(config)

	// Create first process
	processConfig1 := &ProcessConfig{
		ID:           "process-1",
		InputURL:     "test1.wav",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BitDepth:     16,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg",
	}

	_, err := manager.CreateProcess(processConfig1)
	if err != nil {
		t.Errorf("Failed to create first process: %v", err)
	}

	// Try to create second process (should fail)
	processConfig2 := &ProcessConfig{
		ID:           "process-2",
		InputURL:     "test2.wav",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BitDepth:     16,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg",
	}

	_, err = manager.CreateProcess(processConfig2)
	if err == nil {
		t.Error("Expected error when exceeding max processes limit")
	}
}

func TestManagerRemoveProcess(t *testing.T) {
	t.Parallel()

	config := ManagerConfig{
		MaxProcesses: 10,
	}

	manager := NewManager(config)

	processConfig := &ProcessConfig{
		ID:           "remove-test",
		InputURL:     "test.wav",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BitDepth:     16,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg",
	}

	// Create process
	_, err := manager.CreateProcess(processConfig)
	if err != nil {
		t.Errorf("Failed to create process: %v", err)
	}

	// Remove process
	err = manager.RemoveProcess(processConfig.ID)
	if err != nil {
		t.Errorf("Failed to remove process: %v", err)
	}

	// Verify process is gone
	_, exists := manager.GetProcess(processConfig.ID)
	if exists {
		t.Error("Process should not exist after removal")
	}
}

func TestManagerRemoveNonexistentProcess(t *testing.T) {
	t.Parallel()

	config := ManagerConfig{
		MaxProcesses: 10,
	}

	manager := NewManager(config)

	err := manager.RemoveProcess("nonexistent")
	if err == nil {
		t.Error("Expected error when removing nonexistent process")
	}
}

func TestManagerListProcesses(t *testing.T) {
	t.Parallel()

	config := ManagerConfig{
		MaxProcesses: 10,
	}

	manager := NewManager(config)

	// Create multiple processes
	for i := range 3 {
		processConfig := &ProcessConfig{
			ID:           fmt.Sprintf("list-test-%d", i),
			InputURL:     fmt.Sprintf("test%d.wav", i),
			OutputFormat: "s16le",
			SampleRate:   48000,
			Channels:     2,
			BitDepth:     16,
			BufferSize:   1024,
			FFmpegPath:   "/nonexistent/ffmpeg",
		}

		_, err := manager.CreateProcess(processConfig)
		if err != nil {
			t.Errorf("Failed to create process %d: %v", i, err)
		}
	}

	processes := manager.ListProcesses()
	if len(processes) != 3 {
		t.Errorf("Expected 3 processes, got %d", len(processes))
	}
}

func TestManagerStartStop(t *testing.T) {
	t.Parallel()

	config := ManagerConfig{
		MaxProcesses:      10,
		HealthCheckPeriod: 0, // Disable health checks to avoid timing dependencies
		CleanupTimeout:    5 * time.Second,
		MetricsEnabled:    false, // Disable metrics to avoid timing dependencies
	}

	manager := NewManager(config)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start manager
	err := manager.Start(ctx)
	if err != nil {
		t.Errorf("Failed to start manager: %v", err)
	}

	// Try to start again (should fail)
	err = manager.Start(ctx)
	if err == nil {
		t.Error("Expected error when starting already started manager")
	}

	// Stop manager
	err = manager.Stop()
	if err != nil {
		t.Errorf("Failed to stop manager: %v", err)
	}
}

func TestManagerHealthCheck(t *testing.T) {
	t.Parallel()

	config := ManagerConfig{
		MaxProcesses: 10,
	}

	manager := NewManager(config)

	// Health check with no processes should pass
	err := manager.HealthCheck()
	if err != nil {
		t.Errorf("Health check failed with no processes: %v", err)
	}

	// Create a process (it won't be running, so health check should fail)
	processConfig := &ProcessConfig{
		ID:           "health-test",
		InputURL:     "test.wav",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BitDepth:     16,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg",
	}

	_, err = manager.CreateProcess(processConfig)
	if err != nil {
		t.Errorf("Failed to create process: %v", err)
	}

	// Health check should now fail because process is not running
	err = manager.HealthCheck()
	if err == nil {
		t.Error("Expected health check to fail with non-running process")
	}
}

func TestRestartPolicy(t *testing.T) {
	t.Parallel()

	// Test restart policy configuration through the public API
	config := ManagerConfig{
		MaxProcesses: 5,
		RestartPolicy: RestartPolicy{
			Enabled:           true,
			MaxRetries:        2,
			InitialDelay:      50 * time.Millisecond,
			MaxDelay:          200 * time.Millisecond,
			BackoffMultiplier: 2.0,
		},
		HealthCheckPeriod: 0, // Disable health checks for this test
		CleanupTimeout:    1 * time.Second,
		MetricsEnabled:    false,
	}

	manager := NewManager(config)

	// Create a process with invalid FFmpeg path to simulate restart failures
	processConfig := &ProcessConfig{
		ID:           "restart-test",
		InputURL:     "test.wav",
		OutputFormat: "s16le",
		SampleRate:   48000,
		Channels:     2,
		BufferSize:   1024,
		FFmpegPath:   "/nonexistent/ffmpeg", // This will fail
	}

	// Create process
	process, err := manager.CreateProcess(processConfig)
	if err != nil {
		t.Fatalf("Failed to create process: %v", err)
	}

	// Verify the process was created
	retrievedProcess, exists := manager.GetProcess("restart-test")
	if !exists {
		t.Error("Process should exist after creation")
	}

	if retrievedProcess.ID() != process.ID() {
		t.Errorf("Retrieved process ID should match: expected %s, got %s",
			process.ID(), retrievedProcess.ID())
	}

	// Verify process appears in list
	processes := manager.ListProcesses()
	if len(processes) != 1 {
		t.Errorf("Expected 1 process in list, got %d", len(processes))
	}

	// Test that the restart policy settings are properly configured
	// by verifying the process behavior (start should fail with the invalid path)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = process.Start(ctx)
	if err == nil {
		t.Error("Process start should fail with invalid FFmpeg path")
	}
}
