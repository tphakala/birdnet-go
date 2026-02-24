//go:build !windows

// terminal_unix.go — Unix PTY implementation for the browser terminal.
package api

import (
	"context"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// unixPTY wraps a Unix PTY file descriptor to satisfy ptyHandle.
// Close is guarded by sync.Once for parity with windowsPTY —
// double-closing a Unix fd can close a recycled fd belonging to
// another goroutine.
type unixPTY struct {
	ptmx      *os.File
	closeOnce sync.Once
	closeErr  error
}

func (u *unixPTY) Read(p []byte) (int, error)  { return u.ptmx.Read(p) }
func (u *unixPTY) Write(p []byte) (int, error) { return u.ptmx.Write(p) }

func (u *unixPTY) Close() error {
	u.closeOnce.Do(func() {
		u.closeErr = u.ptmx.Close()
	})
	return u.closeErr
}

// Resize sets the terminal dimensions on the Unix PTY.
func (u *unixPTY) Resize(cols, rows uint16) error {
	return pty.Setsize(u.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

// startPTY starts a shell in a Unix PTY and returns a ptyHandle.
func startPTY(ctx context.Context) (ptyHandle, func(), error) {
	shell := findShell()
	cmd := exec.CommandContext(ctx, shell) //nolint:gosec // shell path from findShell, not user input
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}

	return &unixPTY{ptmx: ptmx}, cleanup, nil
}

// findShell returns the path to an available Unix shell.
// Falls back to /bin/sh so exec.Command receives an absolute path rather than
// searching PATH, which could be manipulated in a compromised environment.
func findShell() string {
	for _, shell := range []string{"/bin/bash", "/usr/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(shell); err == nil {
			return shell
		}
	}
	return "/bin/sh"
}
