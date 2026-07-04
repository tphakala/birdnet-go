package elevation

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// stagedDBName is the fixed filename import-stage writes the staged database as.
// It must match staging.stagedDBName; kept as a local constant to avoid importing
// the staging package (which pulls in cgo) just for a string.
const stagedDBName = "birds.db"

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
	// Clear the password on every return path, regardless of which rung is
	// reached or whether elevation is attempted at all. A direct-read or
	// passwordless-sudo success returns before the password rung, so clearing
	// only inside that rung would leave a caller-supplied password in memory.
	defer req.Password.Clear()

	// Rung 1: direct read. If the service user can read the source, no copy needed.
	if l.Direct.CanRead(req.Src) {
		l.Log.Info("import: direct read available",
			slog.String("src", req.Src),
			slog.String("method", string(MethodDirect)),
		)
		return Outcome{Method: MethodDirect, StagedDB: req.Src, StagedAudio: req.Audio}, nil
	}

	// cmd is the import-stage invocation (binary + flags), shared by both sudo
	// rungs. Each rung prepends its own sudo flags and the "--" sentinel.
	cmd := buildStageCommand(l.SelfExe, req)

	// Rung 2: passwordless sudo probe, then the staged copy via `sudo -n`.
	if err := l.Runner.Run(ctx, nil, "sudo", "-n", "true"); err == nil {
		sudoArgs := append([]string{"-n", "--"}, cmd...)
		if runErr := l.Runner.Run(ctx, nil, "sudo", sudoArgs...); runErr == nil {
			l.Log.Info("import: staged via passwordless sudo",
				slog.String("src", req.Src),
				slog.String("dst", req.Dst),
				slog.String("method", string(MethodSudoNonInteractive)),
			)
			return stagedOutcome(MethodSudoNonInteractive, req), nil
		}
		// Probe succeeded but the staging command was refused (e.g. sudoers allows
		// `true` but not `birdnet-go import-stage`). Log before falling through,
		// mirroring the password-rung failure log.
		l.Log.Warn("import: passwordless sudo staging failed",
			slog.String("src", req.Src),
		)
	}

	// Rung 3: in-app sudo password.
	if req.AllowElevation && len(req.Password) > 0 {
		// Drop any cached sudo grant first so the password attempt starts fresh
		// and a stale timestamp cache cannot mask a wrong password.
		_ = l.Runner.Run(ctx, nil, "sudo", "-k")
		// Feed the password on stdin; -p "" suppresses the prompt. sudo -S reads
		// the password as a line, so it MUST be newline-terminated. Build a
		// throwaway newline-terminated copy and zero it right after use.
		pwLine := make([]byte, len(req.Password)+1)
		copy(pwLine, req.Password)
		pwLine[len(pwLine)-1] = '\n'
		sudoArgs := append([]string{"-S", "-p", "", "--"}, cmd...)
		runErr := l.Runner.Run(ctx, pwLine, "sudo", sudoArgs...)
		clear(pwLine)
		if runErr == nil {
			l.Log.Info("import: staged via sudo with password",
				slog.String("src", req.Src),
				slog.String("dst", req.Dst),
				slog.String("method", string(MethodSudoPassword)),
			)
			return stagedOutcome(MethodSudoPassword, req), nil
		}
		l.Log.Warn("import: sudo password elevation failed",
			slog.String("src", req.Src),
		)
	}

	// Rung 4: fallback. Provide copy-paste remediation hints, no error.
	cmds := fallbackCommands(req)
	l.Log.Info("import: elevation unavailable, returning fallback commands",
		slog.String("src", req.Src),
		slog.String("method", string(MethodFallback)),
	)
	return Outcome{Method: MethodFallback, FallbackCommands: cmds}, nil
}

// buildStageCommand builds the import-stage invocation (binary path plus
// --flag=value arguments), without any sudo flags. Each sudo rung prepends its
// own flags and a "--" sentinel, so the flags here are never re-interpreted by
// sudo. Using --flag=value form means a value that looks like a flag (e.g.
// --src=-rf) is bound to its flag, not treated as a new option.
func buildStageCommand(selfExe string, req *StageRequest) []string {
	// selfExe + "import-stage" + 4 required flags + optional --audio = 7 max.
	const maxStageArgs = 7
	args := make([]string, 0, maxStageArgs)
	args = append(args,
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

// stagedOutcome builds the Outcome for a successful sudo staging copy. The staged
// paths mirror what import-stage writes under Dst: the database as stagedDBName
// and, when audio was requested, the audio tree under its base name.
func stagedOutcome(method Method, req *StageRequest) Outcome {
	out := Outcome{Method: method, StagedDB: filepath.Join(req.Dst, stagedDBName)}
	if req.Audio != "" {
		out.StagedAudio = filepath.Join(req.Dst, filepath.Base(req.Audio))
	}
	return out
}

// fallbackCommands returns copy-paste shell commands the user can run manually
// to grant the service account read access to the source data.
func fallbackCommands(req *StageRequest) []string {
	owner := req.Owner
	if owner == "" {
		owner = "birdnet-go"
	}
	// Shell-quote every interpolated value: these are copy-paste commands the user
	// runs in a shell, so a path or owner containing spaces or shell metacharacters
	// (e.g. $(...), ;, backtick) must not be interpretable. shellQuote single-quotes
	// the value so it is always a single literal argument.
	//
	// A recursive rX on the data directory is not enough on its own: if a parent
	// (e.g. /home/pi, mode 0700) lacks execute/search for the service user, it
	// cannot traverse down to the data at all. So also grant a traversal (x) ACL on
	// each ancestor directory of the source, up to but not including the root.
	var cmds []string
	cmds = append(cmds, parentTraversalACLs(owner, req.Src)...)
	cmds = append(cmds, fmt.Sprintf("sudo setfacl -R -m u:%s:rX %s", shellQuote(owner), shellQuote(req.Src)))
	if req.Audio != "" {
		cmds = append(cmds, parentTraversalACLs(owner, req.Audio)...)
		cmds = append(cmds, fmt.Sprintf("sudo setfacl -R -m u:%s:rX %s", shellQuote(owner), shellQuote(req.Audio)))
	}
	return cmds
}

// parentTraversalACLs returns setfacl commands granting the owner execute
// (search) access on each ancestor directory of path, from its parent up to but
// not including the filesystem root. Without traversal on every ancestor, a
// recursive rX on the leaf data directory still cannot be reached.
func parentTraversalACLs(owner, path string) []string {
	var cmds []string
	dir := filepath.Dir(filepath.Clean(path))
	for dir != "" && dir != string(filepath.Separator) && dir != "." {
		cmds = append(cmds, fmt.Sprintf("sudo setfacl -m u:%s:x %s", shellQuote(owner), shellQuote(dir)))
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// List outermost ancestor first so the user grants traversal top-down.
	slices.Reverse(cmds)
	return cmds
}

// shellQuote wraps s in single quotes for safe copy-paste into a POSIX shell,
// escaping any embedded single quote as '\”. The result is always exactly one
// shell word, so spaces and metacharacters in s cannot be interpreted.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// execRunner is the real SudoRunner that uses os/exec.
type execRunner struct{}

func (e *execRunner) Run(ctx context.Context, stdin []byte, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // name is always "sudo", args are controlled
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	// Capture stderr for diagnosability. sudo's prompt is suppressed via -p "",
	// and our own subcommand never writes the password to stderr, so this does
	// not leak the secret; it surfaces the actual sudo / import-stage failure.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
		}
		return err
	}
	return nil
}

// osDirectReader checks readability using os.Open.
type osDirectReader struct{}

func (r *osDirectReader) CanRead(path string) bool {
	// Stat first (does not block) so a named pipe or device source is rejected
	// without os.Open blocking on it forever. Only a regular file is a readable
	// import source.
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(path) //nolint:gosec // path is caller-controlled and validated upstream
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
