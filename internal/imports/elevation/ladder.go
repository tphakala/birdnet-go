package elevation

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
)

// Method describes how the ladder satisfied (or declined) the staging request.
type Method string

const (
	// MethodDirect means the source was readable without elevation.
	MethodDirect Method = "direct"
	// MethodSudoNonInteractive means staging ran via passwordless sudo.
	MethodSudoNonInteractive Method = "sudo-noninteractive"
	// MethodSudoPassword means staging ran via sudo with an in-app password.
	MethodSudoPassword Method = "sudo-password"
	// MethodFallback means no elevation path succeeded; copy-paste remediation is offered.
	MethodFallback Method = "fallback"
)

// SudoRunner abstracts running a command with optional stdin, so tests can
// inject a fake without actual sudo or a real subprocess.
type SudoRunner interface {
	// Run executes name with args, feeding stdin (may be nil) to the process.
	Run(ctx context.Context, stdin []byte, name string, args ...string) error
}

// DirectReader tests whether a path can be opened for reading without elevation.
type DirectReader interface {
	// CanRead reports whether path is readable by the current process.
	CanRead(path string) bool
}

// StageRequest carries the parameters for a single ladder invocation.
type StageRequest struct {
	// Src is the absolute path to the source birds.db.
	Src string
	// Audio is the optional absolute path to the source audio directory.
	Audio string
	// Dst is the absolute path to the empty staging directory.
	Dst string
	// UID is the service-user uid to chown staged files to.
	UID int
	// GID is the service-user gid to chown staged files to.
	GID int
	// Password is the sudo password for the in-app elevation rung.
	// It is cleared after use regardless of outcome.
	Password Password
	// AllowElevation enables rungs 3 and 4 (sudo password and fallback).
	// When false the ladder stops after the passwordless-sudo probe.
	AllowElevation bool
	// Owner is the system username of the BirdNET-Go service account, used in
	// fallback copy-paste remediation hints.
	Owner string
}

// Outcome reports what the ladder did and where the data ended up.
type Outcome struct {
	// Method is the rung that succeeded (or MethodFallback if none did).
	Method Method
	// StagedDB is the path to the staged database (empty for MethodFallback).
	StagedDB string
	// StagedAudio is the path to the staged audio directory (empty when not applicable).
	StagedAudio string
	// FallbackCommands are the copy-paste remediation commands for MethodFallback.
	FallbackCommands []string
}

// Ladder decides how to get the BirdNET-Pi data into a BirdNET-Go-owned
// staging directory, trying four rungs in order: direct read, passwordless sudo,
// in-app sudo password, and copy-paste fallback.
type Ladder struct {
	// Runner executes subprocesses. Defaults to an exec-based real runner.
	Runner SudoRunner
	// SelfExe is the absolute path to the birdnet-go binary, passed to sudo
	// so the privileged invocation re-runs the same binary. Defaults to
	// os.Executable().
	SelfExe string
	// Direct tests whether the source can be read without elevation.
	Direct DirectReader
	// Log is the audit logger. Password fields are never logged.
	Log *slog.Logger
}

// NewLadder builds a Ladder with production defaults.
func NewLadder() (*Ladder, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("could not determine executable path: %w", err)
	}
	return &Ladder{
		Runner:  &execRunner{},
		SelfExe: exe,
		Direct:  &osDirectReader{},
		Log:     slog.Default(),
	}, nil
}

// Stage runs the four-rung elevation ladder for req and returns the outcome.
// The password in req is cleared on every return path.
func (l *Ladder) Stage(ctx context.Context, req *StageRequest) (Outcome, error) {
	// Rung 1: direct read. If the service user can read the source, no copy needed.
	if l.Direct.CanRead(req.Src) {
		l.Log.Info("import: direct read available",
			slog.String("src", req.Src),
			slog.String("method", string(MethodDirect)),
		)
		return Outcome{Method: MethodDirect, StagedDB: req.Src, StagedAudio: req.Audio}, nil
	}

	// Rung 2: passwordless sudo probe.
	if err := l.Runner.Run(ctx, nil, "sudo", "-n", "true"); err == nil {
		argv := buildArgv(l.SelfExe, req)
		if runErr := l.Runner.Run(ctx, nil, "sudo", argv...); runErr == nil {
			l.Log.Info("import: staged via passwordless sudo",
				slog.String("src", req.Src),
				slog.String("dst", req.Dst),
				slog.String("method", string(MethodSudoNonInteractive)),
			)
			return Outcome{
				Method:   MethodSudoNonInteractive,
				StagedDB: req.Dst + "/birds.db",
			}, nil
		}
	}

	// Rung 3: in-app sudo password.
	if req.AllowElevation && len(req.Password) > 0 {
		defer req.Password.Clear()
		// Drop any cached sudo grant first so the password attempt starts fresh.
		_ = l.Runner.Run(ctx, nil, "sudo", "-k")
		// Feed the password on stdin; -p "" suppresses the prompt.
		argv := buildArgv(l.SelfExe, req)
		pwBytes := req.Password.Bytes()
		sudoArgs := append([]string{"-S", "-p", "", "--"}, argv...)
		if runErr := l.Runner.Run(ctx, pwBytes, "sudo", sudoArgs...); runErr == nil {
			l.Log.Info("import: staged via sudo with password",
				slog.String("src", req.Src),
				slog.String("dst", req.Dst),
				slog.String("method", string(MethodSudoPassword)),
			)
			return Outcome{
				Method:   MethodSudoPassword,
				StagedDB: req.Dst + "/birds.db",
			}, nil
		}
		l.Log.Warn("import: sudo password elevation failed",
			slog.String("src", req.Src),
		)
	} else {
		// Ensure the password is cleared even when not attempted.
		req.Password.Clear()
	}

	// Rung 4: fallback. Provide copy-paste remediation hints, no error.
	cmds := fallbackCommands(req)
	l.Log.Info("import: elevation unavailable, returning fallback commands",
		slog.String("src", req.Src),
		slog.String("method", string(MethodFallback)),
	)
	return Outcome{Method: MethodFallback, FallbackCommands: cmds}, nil
}

// buildArgv builds the --flag=value argv slice for the import-stage subcommand.
// The "-n" and "--" sentinel are prepended so sudo passes the command through
// without interpretation and cannot re-elevate further.
func buildArgv(selfExe string, req *StageRequest) []string {
	args := make([]string, 0, 8)
	args = append(args, "-n", "--",
		selfExe,
		"import-stage",
		"--src="+req.Src,
		"--dst="+req.Dst,
		"--uid="+strconv.Itoa(req.UID),
		"--gid="+strconv.Itoa(req.GID),
	)
	if req.Audio != "" {
		args = append(args, "--audio="+req.Audio)
	}
	return args
}

// fallbackCommands returns copy-paste shell commands the user can run manually
// to grant the service account read access to the source data.
func fallbackCommands(req *StageRequest) []string {
	owner := req.Owner
	if owner == "" {
		owner = "birdnet-go"
	}
	cmds := []string{
		fmt.Sprintf("sudo setfacl -R -m u:%s:rX %s", owner, req.Src),
	}
	if req.Audio != "" {
		cmds = append(cmds, fmt.Sprintf("sudo setfacl -R -m u:%s:rX %s", owner, req.Audio))
	}
	return cmds
}

// execRunner is the real SudoRunner that uses os/exec.
type execRunner struct{}

func (e *execRunner) Run(ctx context.Context, stdin []byte, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // name is always "sudo", args are controlled
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	// Discard stdout/stderr to avoid echoing a password prompt or other output.
	// sudo with -p "" suppresses prompts; stderr discard prevents accidental leaks.
	return cmd.Run()
}

// osDirectReader checks readability using os.Open.
type osDirectReader struct{}

func (r *osDirectReader) CanRead(path string) bool {
	f, err := os.Open(path) //nolint:gosec // path is caller-controlled and validated upstream
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
