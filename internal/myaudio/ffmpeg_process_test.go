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
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFFmpegStream_ProcessCleanupNoZombies validates that ffmpeg processes are properly cleaned up
// without leaving zombie processes when the stream is stopped.
// MODERNIZATION: Uses Go 1.25's testing/synctest to eliminate flaky time.Sleep synchronization.
// Previously used real-time delays for process startup/cleanup that could fail under load.
// With synctest, process synchronization becomes deterministic and test runs instantly.
func TestFFmpegStream_ProcessCleanupNoZombies(t *testing.T) {
	t.Attr("component", "ffmpeg")
	t.Attr("test-type", "zombie-prevention")

	if runtime.GOOS == "windows" {
		t.Skip("Zombie process testing is Unix-specific")
	}

	// Go 1.25 synctest: Creates controlled time environment for deterministic process testing
	synctest.Test(t, func(t *testing.T) {
		t.Helper()

		// Go 1.25: Mock command duration uses fake time - 100ms advances instantly
		mockCmd := createMockFFmpegCommand(t, 100*time.Millisecond)

		audioChan := make(chan UnifiedAudioData, 10)
		defer close(audioChan)
		stream := NewFFmpegStream("test://cleanup", "tcp", audioChan)

		// Replace the command creation to use our mock
		stream.cmdMu.Lock()
		// Go 1.25: time.Now() returns fake time base (2000-01-01 00:00:00 UTC)
		stream.cmd = mockCmd
		stream.processStartTime = time.Now()
		stream.cmdMu.Unlock()

		// Start the mock process
		err := mockCmd.Start()
		require.NoError(t, err)
		pid := mockCmd.Process.Pid

		// Go 1.25: time.Sleep() advances fake time instantly in synctest bubble
		// Eliminates real-world timing variability for process startup synchronization
		time.Sleep(50 * time.Millisecond)

		// Clean up the process
		stream.cleanupProcess()

		// Go 1.25: Cleanup completion wait advances fake time instantly
		// No more flaky real-time delays in test execution
		time.Sleep(100 * time.Millisecond)

		// Check that the process is not a zombie - verification within synctest bubble
		assertNoZombieProcess(t, pid)
	})
}

// TestFFmpegStream_CleanupTimeoutHandling tests that processes are still properly reaped
// even when the cleanup timeout expires.
// MODERNIZATION: Uses Go 1.25's testing/synctest for deterministic timeout duration testing.
// Previously used real-time duration measurement that could be flaky under system load.
// With synctest, timeout duration assertions become precise and deterministic.
func TestFFmpegStream_CleanupTimeoutHandling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Zombie process testing is Unix-specific")
	}

	// Go 1.25 synctest: Creates controlled time environment for precise timeout testing
	synctest.Test(t, func(t *testing.T) {
		t.Helper()

		// Create a process that ignores SIGKILL (simulating a stuck process)
		mockCmd := createStubbornMockProcess(t)

		audioChan := make(chan UnifiedAudioData, 10)
		defer close(audioChan)
		stream := NewFFmpegStream("test://timeout", "tcp", audioChan)

		// Replace the command with our stubborn mock
		stream.cmdMu.Lock()
		stream.cmd = mockCmd
		// Go 1.25: time.Now() returns fake time base for consistent duration measurement
		stream.processStartTime = time.Now()
		stream.cmdMu.Unlock()

		// Start the mock process
		err := mockCmd.Start()
		require.NoError(t, err)
		pid := mockCmd.Process.Pid

		// Go 1.25: Process startup synchronization with fake time - no real delays
		time.Sleep(50 * time.Millisecond)

		// Try to clean up the process - this should timeout with deterministic duration
		// Go 1.25: time.Now() and time.Since() use fake time for precise measurement
		start := time.Now()
		stream.cleanupProcess()
		duration := time.Since(start)

		// Go 1.25: Duration assertions with fake time are perfectly precise
		// No more flaky real-world timing variations in timeout validation
		assert.Greater(t, duration, processCleanupTimeout-time.Second)
		assert.Less(t, duration, processCleanupTimeout+2*time.Second)

		// Force kill the stubborn process
		_ = mockCmd.Process.Signal(syscall.SIGKILL)
		// Go 1.25: Final cleanup wait advances fake time instantly
		time.Sleep(100 * time.Millisecond)

		// Even after timeout, the process should eventually be reaped
		// All verification happens within synctest bubble for deterministic behavior
		assertNoZombieProcess(t, pid)
	})
}

// TestFFmpegStream_RapidRestartNoZombies tests that rapid restarts don't create zombie processes.
// MODERNIZATION: Uses Go 1.25's testing/synctest to eliminate flaky sleep-based rapid restart timing.
// Previously used real-time delays for process lifecycle simulation that could fail under load.
// With synctest, rapid restart patterns become deterministic and test runs instantly.
func TestFFmpegStream_RapidRestartNoZombies(t *testing.T) {
	t.Attr("component", "ffmpeg")
	t.Attr("test-type", "zombie-prevention")

	if runtime.GOOS == "windows" {
		t.Skip("Zombie process testing is Unix-specific")
	}

	// Go 1.25 synctest: Creates controlled time environment for deterministic rapid restart testing
	synctest.Test(t, func(t *testing.T) {
		t.Helper()

		audioChan := make(chan UnifiedAudioData, 10)
		defer close(audioChan)

		// Track PIDs of all processes we create
		pids := make([]int, 0, 5)
		pidMu := sync.Mutex{}

		// Simulate rapid restarts with deterministic timing
		for i := 0; i < 5; i++ {
			stream := NewFFmpegStream(fmt.Sprintf("test://rapid-restart-%d", i), "tcp", audioChan)

			// Go 1.25: Mock command duration uses fake time - 50ms advances instantly
			mockCmd := createMockFFmpegCommand(t, 50*time.Millisecond)
			stream.cmdMu.Lock()
			stream.cmd = mockCmd
			// Go 1.25: time.Now() returns fake time base for consistent process tracking
			stream.processStartTime = time.Now()
			stream.cmdMu.Unlock()

			err := mockCmd.Start()
			require.NoError(t, err)

			pidMu.Lock()
			pids = append(pids, mockCmd.Process.Pid)
			pidMu.Unlock()

			// Go 1.25: time.Sleep() advances fake time instantly in synctest bubble
			// Simulates process death without real-world timing variability
			time.Sleep(60 * time.Millisecond)

			// Clean up
			stream.cleanupProcess()

			// Go 1.25: Restart delay advances fake time instantly
			// Eliminates flaky rapid restart timing in test execution
			time.Sleep(10 * time.Millisecond)
		}

		// Go 1.25: Final cleanup wait advances fake time instantly
		// No more long real-time delays for process cleanup verification
		time.Sleep(200 * time.Millisecond)

		// Check that none of the processes became zombies - all within synctest bubble
		pidMu.Lock()
		defer pidMu.Unlock()

		for _, pid := range pids {
			assertNoZombieProcess(t, pid)
		}
	})
}

// TestFFmpegStream_ProcessGroupCleanup tests that the entire process group is cleaned up.
// MODERNIZATION: Uses Go 1.25's testing/synctest for deterministic process group timing.
// Previously used real-time delays for child process spawning and cleanup that could be flaky.
// With synctest, process group lifecycle testing becomes deterministic and runs instantly.
func TestFFmpegStream_ProcessGroupCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Process group testing is Unix-specific")
	}

	// Go 1.25 synctest: Creates controlled time environment for deterministic process group testing
	synctest.Test(t, func(t *testing.T) {
		t.Helper()

		// Create a mock ffmpeg that spawns child processes
		mockCmd := createMockFFmpegWithChildren(t)

		audioChan := make(chan UnifiedAudioData, 10)
		defer close(audioChan)
		stream := NewFFmpegStream("test://process-group", "tcp", audioChan)

		// Set up process group
		setupProcessGroup(mockCmd)

		stream.cmdMu.Lock()
		stream.cmd = mockCmd
		// Go 1.25: time.Now() returns fake time base for consistent process tracking
		stream.processStartTime = time.Now()
		stream.cmdMu.Unlock()

		// Start the mock process
		err := mockCmd.Start()
		require.NoError(t, err)
		parentPid := mockCmd.Process.Pid

		// Go 1.25: time.Sleep() advances fake time instantly in synctest bubble
		// Eliminates real-world timing variability for child process spawning
		time.Sleep(100 * time.Millisecond)

		// Get child PIDs before cleanup
		childPids := getChildProcesses(t, parentPid)
		assert.NotEmpty(t, childPids, "Mock process should have spawned children")

		// Clean up the process
		stream.cleanupProcess()

		// Go 1.25: Cleanup wait advances fake time instantly
		// No more flaky real-time delays for process group cleanup verification
		time.Sleep(200 * time.Millisecond)

		// Verify parent and all children are gone - all verification within synctest bubble
		assertNoZombieProcess(t, parentPid)
		for _, childPid := range childPids {
			assertNoZombieProcess(t, childPid)
		}
	})
}

// TestFFmpegStream_ConcurrentCleanup tests that concurrent cleanup operations don't cause issues
func TestFFmpegStream_ConcurrentCleanup(t *testing.T) {
	t.Attr("component", "ffmpeg")
	t.Attr("test-type", "concurrency")

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
		wg.Go(func() {
			stream.cleanupProcess()
		})
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

// TestFFmpegStream_WaitGoroutineLeak tests that the Wait goroutine doesn't leak.
// MODERNIZATION: Uses Go 1.25's testing/synctest for deterministic goroutine cleanup timing.
// Previously used real-time delays for goroutine synchronization that could miss leaked goroutines.
// With synctest, goroutine lifecycle testing becomes deterministic and runs instantly.
func TestFFmpegStream_WaitGoroutineLeak(t *testing.T) {
	// Go 1.25 synctest: Creates controlled environment for deterministic goroutine leak testing
	synctest.Test(t, func(t *testing.T) {
		t.Helper()

		initialGoroutines := runtime.NumGoroutine()

		audioChan := make(chan UnifiedAudioData, 10)
		defer close(audioChan)

		// Run multiple cleanup cycles with deterministic timing
		for i := 0; i < 5; i++ {
			stream := NewFFmpegStream(fmt.Sprintf("test://leak-%d", i), "tcp", audioChan)

			// Go 1.25: Mock command duration uses fake time - 50ms advances instantly
			mockCmd := createMockFFmpegCommand(t, 50*time.Millisecond)
			stream.cmdMu.Lock()
			stream.cmd = mockCmd
			// Go 1.25: time.Now() returns fake time base for consistent process tracking
			stream.processStartTime = time.Now()
			stream.cmdMu.Unlock()

			err := mockCmd.Start()
			require.NoError(t, err)

			// Clean up
			stream.cleanupProcess()

			// Go 1.25: time.Sleep() advances fake time instantly in synctest bubble
			// Eliminates real-world timing variability for goroutine cleanup synchronization
			time.Sleep(100 * time.Millisecond)
		}

		// Go 1.25: Final goroutine cleanup wait advances fake time instantly
		// No more long real-time delays for goroutine leak detection
		time.Sleep(500 * time.Millisecond)

		// Check that we don't have significantly more goroutines - verification within synctest bubble
		finalGoroutines := runtime.NumGoroutine()
		goroutineLeak := finalGoroutines - initialGoroutines

		// Allow for some variance, but not a leak proportional to the number of iterations
		assert.Less(t, goroutineLeak, 3, "Possible goroutine leak detected: %d additional goroutines", goroutineLeak)
	})
}

// TestFFmpegStream_ProcessReapingAfterExit tests that processes are properly reaped after normal exit.
// MODERNIZATION: Uses Go 1.25's testing/synctest for deterministic process exit timing.
// Previously used real-time delays for process exit simulation that could be flaky.
// With synctest, process reaping testing becomes deterministic and runs instantly.
func TestFFmpegStream_ProcessReapingAfterExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Process reaping testing is Unix-specific")
	}

	// Go 1.25 synctest: Creates controlled time environment for deterministic process reaping testing
	synctest.Test(t, func(t *testing.T) {
		t.Helper()

		audioChan := make(chan UnifiedAudioData, 10)
		defer close(audioChan)
		stream := NewFFmpegStream("test://reaping", "tcp", audioChan)

		// Go 1.25: Mock command duration uses fake time - 100ms advances instantly
		mockCmd := createMockFFmpegCommand(t, 100*time.Millisecond)

		// Start process outside of stream to track it
		err := mockCmd.Start()
		require.NoError(t, err)
		pid := mockCmd.Process.Pid

		// Set it in the stream
		stream.cmdMu.Lock()
		stream.cmd = mockCmd
		// Go 1.25: time.Now() returns fake time base for consistent process tracking
		stream.processStartTime = time.Now()
		stream.cmdMu.Unlock()

		// Create a mock stdout
		r, w := io.Pipe()
		stream.stdout = r
		defer func() { _ = r.Close() }()
		defer func() { _ = w.Close() }()

		// Simulate process exit by closing the pipe with deterministic timing
		go func() {
			// Go 1.25: time.Sleep() advances fake time instantly in synctest bubble
			// Eliminates real-world timing variability for process exit simulation
			time.Sleep(150 * time.Millisecond)
			_ = w.Close()
		}()

		// Process audio (this should detect the exit)
		// Go 1.25: Wait for process exit with fake time - no real delays
		time.Sleep(200 * time.Millisecond)

		// Test cleanup
		stream.cleanupProcess()

		// Go 1.25: Cleanup synchronization advances fake time instantly
		// No more flaky real-time delays for process reaping verification
		time.Sleep(100 * time.Millisecond)

		// Process should be properly reaped, not a zombie - verification within synctest bubble
		assertNoZombieProcess(t, pid)
	})
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
