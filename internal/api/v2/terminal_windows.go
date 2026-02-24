//go:build windows

// terminal_windows.go — Windows ConPTY implementation for the browser terminal.
package api

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/UserExistsError/conpty"
)

// windowsPTY wraps a Windows ConPTY to satisfy ptyHandle.
// Close is guarded by sync.Once because ConPty.Close() calls
// windows.CloseHandle on raw handles — double-closing a Windows
// handle is undefined behavior (it can close a recycled handle
// belonging to another subsystem).
type windowsPTY struct {
	cpty      *conpty.ConPty
	closeOnce sync.Once
	closeErr  error
}

func (w *windowsPTY) Read(p []byte) (int, error)  { return w.cpty.Read(p) }
func (w *windowsPTY) Write(p []byte) (int, error) { return w.cpty.Write(p) }

func (w *windowsPTY) Close() error {
	w.closeOnce.Do(func() {
		w.closeErr = w.cpty.Close()
	})
	return w.closeErr
}

// Resize sets the terminal dimensions on the Windows ConPTY.
func (w *windowsPTY) Resize(cols, rows uint16) error {
	return w.cpty.Resize(int(cols), int(rows))
}

// startPTY starts a shell in a Windows ConPTY and returns a ptyHandle.
func startPTY(ctx context.Context) (ptyHandle, func(), error) {
	shell := findShell()

	cpty, err := conpty.Start(shell, conpty.ConPtyDimensions(80, 24))
	if err != nil {
		return nil, nil, err
	}

	ph := &windowsPTY{cpty: cpty}

	// Use a cancellable context for the Wait goroutine so it doesn't
	// block forever on context.Background() after the ConPTY is closed.
	waitCtx, waitCancel := context.WithCancel(context.Background())

	cleanup := func() {
		ph.Close()
		waitCancel() // unblock the Wait goroutine if still running
	}

	// Monitor context cancellation to close the ConPTY when the server shuts down.
	go func() {
		select {
		case <-ctx.Done():
			ph.Close()
		case <-waitDone(waitCtx, cpty):
			// Process exited naturally.
		}
	}()

	return ph, cleanup, nil
}

// waitDone returns a channel that closes when the ConPTY process exits
// or when the provided context is cancelled.
func waitDone(ctx context.Context, cpty *conpty.ConPty) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		_, _ = cpty.Wait(ctx)
		close(ch)
	}()
	return ch
}

// findShell returns the path to an available Windows shell.
// Prefers PowerShell 7+ (pwsh.exe), falls back to Windows PowerShell 5.1,
// then cmd.exe.
func findShell() string {
	// Try PowerShell 7+ (cross-platform PowerShell).
	if path, err := exec.LookPath("pwsh.exe"); err == nil {
		return path
	}
	// Try Windows PowerShell 5.1 (ships with Windows 10+).
	if path, err := exec.LookPath("powershell.exe"); err == nil {
		return path
	}
	// Fallback to cmd.exe.
	return findCmdExe()
}

// findCmdExe locates cmd.exe via COMSPEC or the system directory.
func findCmdExe() string {
	if comspec, ok := os.LookupEnv("COMSPEC"); ok {
		if trimmed := strings.TrimSpace(comspec); trimmed != "" {
			return trimmed
		}
	}
	return `C:\Windows\System32\cmd.exe`
}
