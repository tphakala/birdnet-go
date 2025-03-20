//go:build windows
// +build windows

package myaudio

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"syscall"
	"time"
)

// setupProcessGroup sets up a process group on Windows
func setupProcessGroup(cmd *exec.Cmd) {
	if cmd != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
		}
	}
}

// In ffmpeg_input_windows.go
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("invalid command or process is nil")
	}

	pid := cmd.Process.Pid
	if pid <= 0 {
		return errors.New("invalid process ID")
	}

	// Create context with timeout
	killCtx, killCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer killCancel() // Ensure this is called to prevent resource leaks

	// Create the kill command
	killCmd := exec.CommandContext(killCtx, "taskkill", "/F", "/T", "/PID", fmt.Sprint(pid))

	// Capture both stdout and stderr for better error reporting
	output, err := killCmd.CombinedOutput()
	if err != nil {
		log.Printf("⚠️ taskkill failed for PID %d: %v, output: %s", pid, err, string(output))
		return cmd.Process.Kill() // Fall back to direct kill
	}

	return nil
}
