package elevation

import (
	"bytes"
	"context"
	"log/slog"
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
	if len(args) > 0 && f.failOn[args[0]] {
		return errSudoFailed
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
		for _, a := range c {
			if a == arg {
				return c
			}
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
	// The password is fed on stdin exactly once.
	assert.Equal(t, 1, countStdin(r.stdins, []byte("hunter2")))
	// The password bytes are zeroed after use (Clear() zeroes the backing
	// array; the caller's pw slice header still points at that array).
	assert.Equal(t, make([]byte, len("hunter2")), []byte(pw))
	// No log line contains the cleartext password.
	assert.NotContains(t, logBuf.String(), "hunter2")
}

func TestLadderFallbackWhenNoSudo(t *testing.T) {
	r := &fakeRunner{failOn: map[string]bool{"-n": true}}
	l := &Ladder{Runner: r, Direct: fakeDirect{}, SelfExe: "/bin/birdnet", Log: slog.Default()}
	out, err := l.Stage(t.Context(), &StageRequest{
		Src: "/data/birds.db", Dst: "/stg", Owner: "pi", AllowElevation: true, // no password
	})
	require.NoError(t, err)
	assert.Equal(t, MethodFallback, out.Method)
	assert.NotEmpty(t, out.FallbackCommands)
}
