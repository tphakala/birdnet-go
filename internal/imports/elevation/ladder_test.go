package elevation

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRunner struct {
	calls  [][]string // name + args per call
	stdins [][]byte
	failOn map[string]bool // key: first arg after the command name, e.g. "-n" to fail the probe
}

func (f *fakeRunner) Run(_ context.Context, stdin []byte, name string, args ...string) error {
	f.calls = append(f.calls, append([]string{name}, args...))
	f.stdins = append(f.stdins, bytes.Clone(stdin))
	// Fail if any arg is registered in failOn. Matching on any arg (not just
	// args[0]) lets a test target a specific invocation: "-n" matches the
	// passwordless probe, "import-stage" matches a staging command, etc.
	for _, a := range args {
		if f.failOn[a] {
			return errSudoFailed
		}
	}
	return nil
}

// errSudoFailed is the sentinel error returned by fakeRunner when failOn matches.
var errSudoFailed = sudoError("sudo failed")

// sudoError is a simple error type for fakeRunner failures.
type sudoError string

func (e sudoError) Error() string { return string(e) }

type fakeDirect struct{ readable bool }

func (d fakeDirect) CanRead(string) bool { return d.readable }

func findCall(calls [][]string, arg string) []string {
	for _, c := range calls {
		if slices.Contains(c, arg) {
			return c
		}
	}
	return nil
}

func countStdin(stdins [][]byte, needle []byte) int {
	n := 0
	for _, s := range stdins {
		if bytes.Equal(s, needle) {
			n++
		}
	}
	return n
}

func TestLadderDirectRead(t *testing.T) {
	l := &Ladder{
		Runner:  &fakeRunner{},
		Direct:  fakeDirect{readable: true},
		SelfExe: "/bin/birdnet",
		Log:     slog.Default(),
	}
	out, err := l.Stage(t.Context(), &StageRequest{Src: "/data/birds.db", Dst: "/stg"})
	require.NoError(t, err)
	assert.Equal(t, MethodDirect, out.Method)
	assert.Equal(t, "/data/birds.db", out.StagedDB)
}

func TestLadderSudoNonInteractive(t *testing.T) {
	r := &fakeRunner{}
	l := &Ladder{Runner: r, Direct: fakeDirect{}, SelfExe: "/bin/birdnet", Log: slog.Default()}
	out, err := l.Stage(t.Context(), &StageRequest{
		Src: "/data/birds.db", Dst: "/stg", UID: 1000, GID: 1000,
	})
	require.NoError(t, err)
	assert.Equal(t, MethodSudoNonInteractive, out.Method)
	// First call probes `sudo -n true`, second runs the staged copy.
	assert.Equal(t, []string{"sudo", "-n", "true"}, r.calls[0])
	assert.Contains(t, r.calls[1], "import-stage")
	assert.Contains(t, r.calls[1], "--src=/data/birds.db")
}

func TestLadderPasswordPathFeedsAndClears(t *testing.T) {
	r := &fakeRunner{failOn: map[string]bool{"-n": true}} // passwordless probe fails
	var logBuf bytes.Buffer
	l := &Ladder{
		Runner:  r,
		Direct:  fakeDirect{},
		SelfExe: "/bin/birdnet",
		Log:     slog.New(slog.NewTextHandler(&logBuf, nil)),
	}
	pw := Password("hunter2")
	out, err := l.Stage(t.Context(), &StageRequest{
		Src: "/data/birds.db", Dst: "/stg", UID: 1000, GID: 1000,
		Password: pw, AllowElevation: true,
	})
	require.NoError(t, err)
	assert.Equal(t, MethodSudoPassword, out.Method)
	// sudo -k must be invoked before the password attempt.
	assert.Equal(t, []string{"sudo", "-k"}, findCall(r.calls, "-k"))
	// The password is fed on stdin exactly once, newline-terminated for sudo -S.
	assert.Equal(t, 1, countStdin(r.stdins, []byte("hunter2\n")))
	// The password bytes are zeroed after use (Clear() zeroes the backing
	// array; the caller's pw slice header still points at that array).
	assert.Equal(t, make([]byte, len("hunter2")), []byte(pw))
	// No log line contains the cleartext password.
	assert.NotContains(t, logBuf.String(), "hunter2")
}

func TestLadderSudoNonInteractiveArgvWellFormed(t *testing.T) {
	r := &fakeRunner{}
	l := &Ladder{Runner: r, Direct: fakeDirect{}, SelfExe: "/bin/birdnet", Log: slog.Default()}
	_, err := l.Stage(t.Context(), &StageRequest{
		Src: "/data/birds.db", Dst: "/stg", UID: 1000, GID: 1000,
	})
	require.NoError(t, err)
	// The staged-copy call must be: sudo -n -- /bin/birdnet import-stage ...
	// with exactly one "--" sentinel and the binary immediately after it.
	staged := r.calls[1]
	require.Equal(t, []string{
		"sudo", "-n", "--",
		"/bin/birdnet", "import-stage",
		"--src=/data/birds.db", "--dst=/stg", "--uid=1000", "--gid=1000",
	}, staged)
}

func TestLadderSudoPasswordArgvWellFormed(t *testing.T) {
	r := &fakeRunner{failOn: map[string]bool{"-n": true}}
	l := &Ladder{Runner: r, Direct: fakeDirect{}, SelfExe: "/bin/birdnet", Log: slog.Default()}
	_, err := l.Stage(t.Context(), &StageRequest{
		Src: "/data/birds.db", Dst: "/stg", UID: 1000, GID: 1000,
		Password: Password("pw"), AllowElevation: true,
	})
	require.NoError(t, err)
	// The password staged-copy call must be:
	// sudo -S -p "" -- /bin/birdnet import-stage ...
	// The binary must sit immediately after the single "--" sentinel: a stray
	// "-n" or a doubled "--" would make sudo try to exec the wrong command.
	staged := findCall(r.calls, "import-stage")
	require.Equal(t, []string{
		"sudo", "-S", "-p", "", "--",
		"/bin/birdnet", "import-stage",
		"--src=/data/birds.db", "--dst=/stg", "--uid=1000", "--gid=1000",
	}, staged)
}

func TestLadderClearsPasswordOnDirectRead(t *testing.T) {
	// Even when the source is directly readable (password never used), a
	// caller-supplied password must be zeroed on return.
	pw := Password("hunter2")
	l := &Ladder{Runner: &fakeRunner{}, Direct: fakeDirect{readable: true}, SelfExe: "/bin/birdnet", Log: slog.Default()}
	_, err := l.Stage(t.Context(), &StageRequest{
		Src: "/data/birds.db", Dst: "/stg", Password: pw, AllowElevation: true,
	})
	require.NoError(t, err)
	assert.Equal(t, make([]byte, len("hunter2")), []byte(pw))
}

func TestLadderReportsStagedAudio(t *testing.T) {
	r := &fakeRunner{}
	l := &Ladder{Runner: r, Direct: fakeDirect{}, SelfExe: "/bin/birdnet", Log: slog.Default()}
	out, err := l.Stage(t.Context(), &StageRequest{
		Src: "/data/birds.db", Audio: "/data/BirdSongs", Dst: "/stg", UID: 1000, GID: 1000,
	})
	require.NoError(t, err)
	// stagedOutcome joins with filepath.Join, so the expected paths must use the
	// same OS separator (this test runs on the windows-amd64 CI runner too).
	assert.Equal(t, filepath.FromSlash("/stg/birds.db"), out.StagedDB)
	assert.Equal(t, filepath.FromSlash("/stg/BirdSongs"), out.StagedAudio)
}

func TestLadderFallbackWhenNoSudo(t *testing.T) {
	r := &fakeRunner{failOn: map[string]bool{"-n": true}}
	l := &Ladder{Runner: r, Direct: fakeDirect{}, SelfExe: "/bin/birdnet", Log: slog.Default()}
	out, err := l.Stage(t.Context(), &StageRequest{
		Src: "/data/birds.db", Dst: "/stg", Owner: "pi", AllowElevation: true, // no password
	})
	require.NoError(t, err)
	assert.Equal(t, MethodFallback, out.Method)
	require.NotEmpty(t, out.FallbackCommands)
	// Some remediation command must reference the (shell-quoted) source path so a
	// regression that drops argument interpolation is caught. The first commands
	// are parent-directory traversal grants, so check across all of them.
	assert.Contains(t, strings.Join(out.FallbackCommands, "\n"), "'/data/birds.db'")
}

func TestLadderSudoStagingFailsFallsThrough(t *testing.T) {
	// Passwordless probe succeeds (`sudo -n true`) but the staging command is
	// refused (`import-stage`). The ladder must log a warning and fall through to
	// the fallback rung (no password supplied).
	r := &fakeRunner{failOn: map[string]bool{"import-stage": true}}
	var logBuf bytes.Buffer
	l := &Ladder{
		Runner:  r,
		Direct:  fakeDirect{},
		SelfExe: "/bin/birdnet",
		Log:     slog.New(slog.NewTextHandler(&logBuf, nil)),
	}
	out, err := l.Stage(t.Context(), &StageRequest{
		Src: "/data/birds.db", Dst: "/stg", Owner: "pi", AllowElevation: true,
	})
	require.NoError(t, err)
	assert.Equal(t, MethodFallback, out.Method)
	// The probe passed but staging failed, so the rung-2 failure must be logged.
	assert.Contains(t, logBuf.String(), "passwordless sudo staging failed")
}
