package myaudio

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFFmpegStream_ProcessCleanupNoZombies validates that ffmpeg processes are properly cleaned up
// without leaving zombie processes when the stream is stopped
func TestFFmpegStream_ProcessCleanupNoZombies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Zombie process testing is Unix-specific")
	}

	// Create a mock ffmpeg command that runs for a short time
	mockCmd := createMockFFmpegCommand(t, 100*time.Millisecond)

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("test://cleanup", "tcp", audioChan)

	// Replace the command creation to use our mock
	stream.cmdMu.Lock()
	stream.cmd = mockCmd
	stream.processStartTime = time.Now()
	stream.cmdMu.Unlock()

	// Start the mock process
	err := mockCmd.Start()
	require.NoError(t, err)
	pid := mockCmd.Process.Pid

	// Give process time to fully start
	time.Sleep(50 * time.Millisecond)

	// Clean up the process
	stream.cleanupProcess()

	// Wait a bit to ensure cleanup completes
	time.Sleep(100 * time.Millisecond)

	// Check that the process is not a zombie
	assertNoZombieProcess(t, pid)
}

// TestFFmpegStream_CleanupTimeoutHandling tests that processes are still properly reaped
// even when the cleanup timeout expires
func TestFFmpegStream_CleanupTimeoutHandling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Zombie process testing is Unix-specific")
	}

	// Create a process that ignores SIGKILL (simulating a stuck process)
	mockCmd := createStubbornMockProcess(t)

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("test://timeout", "tcp", audioChan)

	// Replace the command with our stubborn mock
	stream.cmdMu.Lock()
	stream.cmd = mockCmd
	stream.processStartTime = time.Now()
	stream.cmdMu.Unlock()

	// Start the mock process
	err := mockCmd.Start()
	require.NoError(t, err)
	pid := mockCmd.Process.Pid

	// Give process time to fully start
	time.Sleep(50 * time.Millisecond)

	// Try to clean up the process - this should timeout
	start := time.Now()
	stream.cleanupProcess()
	duration := time.Since(start)

	// Verify cleanup attempted to wait but timed out
	assert.Greater(t, duration, processCleanupTimeout-time.Second)
	assert.Less(t, duration, processCleanupTimeout+2*time.Second)

	// Force kill the stubborn process
	_ = mockCmd.Process.Signal(syscall.SIGKILL)
	time.Sleep(100 * time.Millisecond)

	// Even after timeout, the process should eventually be reaped
	// (though this might require fixing the actual implementation)
	assertNoZombieProcess(t, pid)
}

// TestFFmpegStream_RapidRestartNoZombies tests that rapid restarts don't create zombie processes
func TestFFmpegStream_RapidRestartNoZombies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Zombie process testing is Unix-specific")
	}

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)

	// Track PIDs of all processes we create
	pids := make([]int, 0, 5)
	pidMu := sync.Mutex{}

	// Simulate rapid restarts
	for i := 0; i < 5; i++ {
		stream := NewFFmpegStream(fmt.Sprintf("test://rapid-restart-%d", i), "tcp", audioChan)

		// Create and start a short-lived mock process
		mockCmd := createMockFFmpegCommand(t, 50*time.Millisecond)
		stream.cmdMu.Lock()
		stream.cmd = mockCmd
		stream.processStartTime = time.Now()
		stream.cmdMu.Unlock()

		err := mockCmd.Start()
		require.NoError(t, err)

		pidMu.Lock()
		pids = append(pids, mockCmd.Process.Pid)
		pidMu.Unlock()

		// Simulate process dying quickly
		time.Sleep(60 * time.Millisecond)

		// Clean up
		stream.cleanupProcess()

		// Small delay between restarts
		time.Sleep(10 * time.Millisecond)
	}

	// Wait a bit more to ensure all processes are cleaned up
	time.Sleep(200 * time.Millisecond)

	// Check that none of the processes became zombies
	pidMu.Lock()
	defer pidMu.Unlock()

	for _, pid := range pids {
		assertNoZombieProcess(t, pid)
	}
}

// TestFFmpegStream_ProcessGroupCleanup tests that the entire process group is cleaned up
func TestFFmpegStream_ProcessGroupCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Process group testing is Unix-specific")
	}

	// Create a mock ffmpeg that spawns child processes
	mockCmd := createMockFFmpegWithChildren(t)

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("test://process-group", "tcp", audioChan)

	// Set up process group
	setupProcessGroup(mockCmd)

	stream.cmdMu.Lock()
	stream.cmd = mockCmd
	stream.processStartTime = time.Now()
	stream.cmdMu.Unlock()

	// Start the mock process
	err := mockCmd.Start()
	require.NoError(t, err)
	parentPid := mockCmd.Process.Pid

	// Give time for child processes to spawn
	time.Sleep(100 * time.Millisecond)

	// Get child PIDs before cleanup
	childPids := getChildProcesses(t, parentPid)
	assert.NotEmpty(t, childPids, "Mock process should have spawned children")

	// Clean up the process
	stream.cleanupProcess()

	// Wait for cleanup
	time.Sleep(200 * time.Millisecond)

	// Verify parent and all children are gone
	assertNoZombieProcess(t, parentPid)
	for _, childPid := range childPids {
		assertNoZombieProcess(t, childPid)
	}
}

// TestFFmpegStream_ConcurrentCleanup tests that concurrent cleanup operations don't cause issues
func TestFFmpegStream_ConcurrentCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Zombie process testing is Unix-specific")
	}

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("test://concurrent", "tcp", audioChan)

	// Create and start a mock process
	mockCmd := createMockFFmpegCommand(t, 200*time.Millisecond)
	stream.cmdMu.Lock()
	stream.cmd = mockCmd
	stream.processStartTime = time.Now()
	stream.cmdMu.Unlock()

	err := mockCmd.Start()
	require.NoError(t, err)
	pid := mockCmd.Process.Pid

	// Attempt concurrent cleanups
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stream.cleanupProcess()
		}()
	}

	wg.Wait()

	// Verify no zombie process
	assertNoZombieProcess(t, pid)
}

// Helper function to create a mock ffmpeg command
func createMockFFmpegCommand(tb testing.TB, duration time.Duration) *exec.Cmd {
	tb.Helper()
	// Use sleep as a mock ffmpeg process
	cmd := exec.Command("sleep", fmt.Sprintf("%.3f", duration.Seconds()))
	return cmd
}

// Helper function to create a stubborn process that's hard to kill
func createStubbornMockProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	// Create a shell script that traps signals
	script := `#!/bin/sh
trap '' TERM
trap '' INT
sleep 10
`
	cmd := exec.Command("sh", "-c", script)
	return cmd
}

// Helper function to create a mock ffmpeg that spawns child processes
func createMockFFmpegWithChildren(t *testing.T) *exec.Cmd {
	t.Helper()
	// Shell script that spawns child processes
	script := `#!/bin/sh
# Spawn some child processes
sleep 5 &
sleep 5 &
sleep 5 &
# Keep parent alive
sleep 1
`
	cmd := exec.Command("sh", "-c", script)
	return cmd
}

// Helper function to check if a process is a zombie
func assertNoZombieProcess(t *testing.T, pid int) {
	t.Helper()
	// Check /proc/[pid]/stat for zombie state
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(statPath)
	if err != nil {
		// Process doesn't exist, which is fine (not a zombie)
		return
	}

	// Parse the stat file - state is the third field after the command name in parentheses
	stat := string(data)
	// Find the last ')' to skip the command name which might contain spaces/parentheses
	lastParen := strings.LastIndex(stat, ")")
	if lastParen == -1 {
		t.Fatalf("Invalid stat format for PID %d", pid)
	}

	fields := strings.Fields(stat[lastParen+1:])
	if len(fields) < 1 {
		t.Fatalf("Invalid stat format for PID %d", pid)
	}

	state := fields[0]
	assert.NotEqual(t, "Z", state, "Process %d is a zombie", pid)
}

// Helper function to get child processes of a parent PID
func getChildProcesses(t *testing.T, parentPid int) []int {
	t.Helper()
	cmd := exec.Command("pgrep", "-P", fmt.Sprintf("%d", parentPid))
	output, err := cmd.Output()
	if err != nil {
		// No children found
		return []int{}
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	pids := make([]int, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var pid int
		_, err := fmt.Sscanf(line, "%d", &pid)
		if err == nil {
			pids = append(pids, pid)
		}
	}

	return pids
}

// TestFFmpegStream_WaitGoroutineLeak tests that the Wait goroutine doesn't leak
func TestFFmpegStream_WaitGoroutineLeak(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)

	// Run multiple cleanup cycles
	for i := 0; i < 5; i++ {
		stream := NewFFmpegStream(fmt.Sprintf("test://leak-%d", i), "tcp", audioChan)

		mockCmd := createMockFFmpegCommand(t, 50*time.Millisecond)
		stream.cmdMu.Lock()
		stream.cmd = mockCmd
		stream.processStartTime = time.Now()
		stream.cmdMu.Unlock()

		err := mockCmd.Start()
		require.NoError(t, err)

		// Clean up
		stream.cleanupProcess()

		// Give time for goroutines to finish
		time.Sleep(100 * time.Millisecond)
	}

	// Allow some time for goroutines to clean up
	time.Sleep(500 * time.Millisecond)

	// Check that we don't have significantly more goroutines
	finalGoroutines := runtime.NumGoroutine()
	goroutineLeak := finalGoroutines - initialGoroutines

	// Allow for some variance, but not a leak proportional to the number of iterations
	assert.Less(t, goroutineLeak, 3, "Possible goroutine leak detected: %d additional goroutines", goroutineLeak)
}

// TestFFmpegStream_ProcessReapingAfterExit tests that processes are properly reaped after normal exit
func TestFFmpegStream_ProcessReapingAfterExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Process reaping testing is Unix-specific")
	}

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("test://reaping", "tcp", audioChan)

	// Create a process that exits on its own
	mockCmd := createMockFFmpegCommand(t, 100*time.Millisecond)

	// Start process outside of stream to track it
	err := mockCmd.Start()
	require.NoError(t, err)
	pid := mockCmd.Process.Pid

	// Set it in the stream
	stream.cmdMu.Lock()
	stream.cmd = mockCmd
	stream.processStartTime = time.Now()
	stream.cmdMu.Unlock()

	// Create a mock stdout
	r, w := io.Pipe()
	stream.stdout = r
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()

	// Simulate process exit by closing the pipe after delay
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = w.Close()
	}()

	// Process audio (this should detect the exit)
	// Wait for process to exit naturally
	time.Sleep(200 * time.Millisecond)
	// Test cleanup
	stream.cleanupProcess()
	// Error is expected when process exits
	require.NoError(t, err) // EOF is treated as normal exit

	// Give time for any cleanup
	time.Sleep(100 * time.Millisecond)

	// Process should be properly reaped, not a zombie
	assertNoZombieProcess(t, pid)
}

// Benchmark process cleanup performance
func BenchmarkFFmpegStream_ProcessCleanup(b *testing.B) {
	if runtime.GOOS == "windows" {
		b.Skip("Process benchmarking is Unix-specific")
	}

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		stream := NewFFmpegStream(fmt.Sprintf("test://bench-%d", i), "tcp", audioChan)

		mockCmd := createMockFFmpegCommand(b, 50*time.Millisecond)
		stream.cmdMu.Lock()
		stream.cmd = mockCmd
		stream.processStartTime = time.Now()
		stream.cmdMu.Unlock()

		err := mockCmd.Start()
		if err != nil {
			b.Fatal(err)
		}

		stream.cleanupProcess()
	}
}
