package myaudio

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFFmpegStream_ZombieCreationOnProcessExit specifically tests zombie process creation
// when ffmpeg exits unexpectedly during normal operation
func TestFFmpegStream_ZombieCreationOnProcessExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Zombie process testing is Unix-specific")
	}

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("test://zombie-exit", "tcp", audioChan)
	
	// Create a process that exits after a short time
	cmd := exec.Command("sh", "-c", "sleep 0.1 && exit 0")
	
	stream.cmdMu.Lock()
	stream.cmd = cmd
	stream.processStartTime = time.Now()
	stream.cmdMu.Unlock()
	
	// Start the process
	err := cmd.Start()
	require.NoError(t, err)
	pid := cmd.Process.Pid
	t.Logf("Started process PID: %d", pid)
	
	// Wait for process to exit naturally
	time.Sleep(200 * time.Millisecond)
	
	// Check if process became a zombie before cleanup
	if isProcessZombie(t, pid) {
		t.Logf("Process %d is already a zombie before cleanup", pid)
	}
	
	// Now cleanup
	stream.cleanupProcess()
	
	// Give time for cleanup to complete
	time.Sleep(100 * time.Millisecond)
	
	// Verify no zombie
	assertNoZombieProcess(t, pid)
}

// TestFFmpegStream_ZombiePreventionWithWaitTimeout tests that we don't create zombies
// even when the Wait() call times out in cleanupProcess
func TestFFmpegStream_ZombiePreventionWithWaitTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Zombie process testing is Unix-specific")
	}

	// Track all PIDs we create
	pids := make([]int, 0, 3)
	var pidMu sync.Mutex
	
	for i := 0; i < 3; i++ {
		audioChan := make(chan UnifiedAudioData, 10)
		stream := NewFFmpegStream(fmt.Sprintf("test://zombie-timeout-%d", i), "tcp", audioChan)
		
		// Create a process that's hard to kill
		cmd := exec.Command("sh", "-c", `trap '' TERM; sleep 10`)
		
		stream.cmdMu.Lock()
		stream.cmd = cmd
		stream.processStartTime = time.Now()
		stream.cmdMu.Unlock()
		
		err := cmd.Start()
		require.NoError(t, err)
		
		pidMu.Lock()
		pids = append(pids, cmd.Process.Pid)
		pidMu.Unlock()
		
		t.Logf("Started stubborn process PID: %d", cmd.Process.Pid)
		
		// Cleanup will timeout
		stream.cleanupProcess()
		
		// Force kill after cleanup attempt
		_ = cmd.Process.Kill()
		
		close(audioChan)
	}
	
	// Wait for all processes to be cleaned up
	time.Sleep(6 * time.Second) // Longer than cleanup timeout
	
	// Check for zombies
	pidMu.Lock()
	defer pidMu.Unlock()
	
	zombieCount := 0
	for _, pid := range pids {
		if isProcessZombie(t, pid) {
			t.Errorf("Process %d is still a zombie after cleanup timeout", pid)
			zombieCount++
		}
	}
	
	if zombieCount > 0 {
		t.Errorf("Found %d zombie processes out of %d total", zombieCount, len(pids))
	}
}

// TestFFmpegStream_ZombieAccumulationDuringRestarts tests zombie accumulation during repeated restarts
func TestFFmpegStream_ZombieAccumulationDuringRestarts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Zombie process testing is Unix-specific")
	}

	const numRestarts = 10
	pids := make([]int, 0, numRestarts)
	pidMu := sync.Mutex{}
	
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	
	// Simulate multiple restart cycles
	for i := 0; i < numRestarts; i++ {
		stream := NewFFmpegStream(fmt.Sprintf("test://accumulation-%d", i), "tcp", audioChan)
		
		// Create a process that exits quickly
		cmd := exec.Command("sh", "-c", "exit 1")
		
		stream.cmdMu.Lock()
		stream.cmd = cmd
		stream.processStartTime = time.Now()
		stream.cmdMu.Unlock()
		
		err := cmd.Start()
		require.NoError(t, err)
		
		pidMu.Lock()
		pids = append(pids, cmd.Process.Pid)
		pidMu.Unlock()
		
		// Don't wait for process to complete - simulate quick restarts
		// This mimics the scenario where ffmpeg crashes repeatedly
		
		// Minimal cleanup attempt
		go stream.cleanupProcess()
		
		// Small delay between restarts
		time.Sleep(50 * time.Millisecond)
	}
	
	// Wait for all cleanups to complete
	time.Sleep(2 * time.Second)
	
	// Count zombies
	pidMu.Lock()
	defer pidMu.Unlock()
	
	zombieCount := 0
	activePids := []int{}
	
	for _, pid := range pids {
		if isProcessZombie(t, pid) {
			zombieCount++
			activePids = append(activePids, pid)
			t.Logf("Process %d is a zombie", pid)
		}
	}
	
	if zombieCount > 0 {
		t.Errorf("Accumulated %d zombie processes out of %d restarts", zombieCount, numRestarts)
		t.Logf("Zombie PIDs: %v", activePids)
	}
}

// TestFFmpegStream_CleanupGoroutineLeak tests for goroutine leaks during cleanup
func TestFFmpegStream_CleanupGoroutineLeak(t *testing.T) {
	initialGoroutines := runtime.NumGoroutine()
	t.Logf("Initial goroutine count: %d", initialGoroutines)
	
	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	
	// Create multiple streams with timeout scenarios
	for i := 0; i < 5; i++ {
		stream := NewFFmpegStream(fmt.Sprintf("test://goroutine-leak-%d", i), "tcp", audioChan)
		
		// Create a process
		cmd := exec.Command("sleep", "0.1")
		
		stream.cmdMu.Lock()
		stream.cmd = cmd
		stream.processStartTime = time.Now()
		stream.cmdMu.Unlock()
		
		err := cmd.Start()
		require.NoError(t, err)
		
		// Wait for process to exit
		time.Sleep(150 * time.Millisecond)
		
		// Cleanup
		stream.cleanupProcess()
	}
	
	// Wait for goroutines to finish
	time.Sleep(1 * time.Second)
	
	currentGoroutines := runtime.NumGoroutine()
	goroutineDiff := currentGoroutines - initialGoroutines
	
	t.Logf("Final goroutine count: %d (diff: %d)", currentGoroutines, goroutineDiff)
	
	// Allow for some variance but detect leaks
	assert.LessOrEqual(t, goroutineDiff, 2, "Potential goroutine leak: %d additional goroutines", goroutineDiff)
}

// Helper function to check if a process is a zombie (returns bool instead of asserting)
func isProcessZombie(t *testing.T, pid int) bool {
	t.Helper()
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(statPath)
	if err != nil {
		// Process doesn't exist
		return false
	}
	
	stat := string(data)
	lastParen := strings.LastIndex(stat, ")")
	if lastParen == -1 {
		t.Logf("Invalid stat format for PID %d", pid)
		return false
	}
	
	fields := strings.Fields(stat[lastParen+1:])
	if len(fields) < 1 {
		t.Logf("Invalid stat format for PID %d", pid)
		return false
	}
	
	state := fields[0]
	return state == "Z"
}

// TestFFmpegStream_ProcessStateTransitions tracks process states during lifecycle
func TestFFmpegStream_ProcessStateTransitions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Process state testing is Unix-specific")
	}

	audioChan := make(chan UnifiedAudioData, 10)
	defer close(audioChan)
	stream := NewFFmpegStream("test://state-transitions", "tcp", audioChan)
	
	// Create a process that we can monitor
	cmd := exec.Command("sh", "-c", "sleep 0.2; exit 0")
	
	stream.cmdMu.Lock()
	stream.cmd = cmd
	stream.processStartTime = time.Now()
	stream.cmdMu.Unlock()
	
	err := cmd.Start()
	require.NoError(t, err)
	pid := cmd.Process.Pid
	
	// Track process states
	states := []string{}
	
	// Monitor process state changes
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10; i++ {
			state := getProcessState(t, pid)
			if state != "" {
				states = append(states, state)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	
	// Wait for monitoring to complete
	<-done
	
	// Cleanup
	stream.cleanupProcess()
	
	// Log state transitions
	t.Logf("Process state transitions: %v", states)
	
	// Check final state
	finalState := getProcessState(t, pid)
	if finalState == "Z" {
		t.Error("Process ended up as zombie")
	}
}

// Helper to get process state
func getProcessState(t *testing.T, pid int) string {
	t.Helper()
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(statPath)
	if err != nil {
		return ""
	}
	
	stat := string(data)
	lastParen := strings.LastIndex(stat, ")")
	if lastParen == -1 {
		return ""
	}
	
	fields := strings.Fields(stat[lastParen+1:])
	if len(fields) < 1 {
		return ""
	}
	
	return fields[0]
}