package conf

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// shortProbeTimeout is the context deadline of the hung-probe test.
	shortProbeTimeout = 200 * time.Millisecond
	// hungProbeGiveUp is how long the test waits before declaring the probe unbounded.
	// It only matters when the bound is missing, so it never delays a passing run.
	hungProbeGiveUp = 20 * time.Second
	// hungWrapperScript mimics an ffmpeg wrapper that starts the real binary as a
	// child and waits for it: killing the wrapper leaves the child holding the
	// output pipe, which is what keeps an unbounded probe waiting.
	hungWrapperScript = "#!/bin/sh\nsleep 60 &\nwait\n"
)

// TestGetFfmpegVersionFromContext_StopsAWrapperWithALingeringChild pins that the
// probe returns once its context ends even when a wrapper script's child still
// holds the output pipe, which the kill of the wrapper alone does not release.
func TestGetFfmpegVersionFromContext_StopsAWrapperWithALingeringChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as the fake ffmpeg")
	}
	script := filepath.Join(t.TempDir(), "ffmpeg")
	require.NoError(t, os.WriteFile(script, []byte(hungWrapperScript), 0o755))

	ctx, cancel := context.WithTimeout(t.Context(), shortProbeTimeout)
	defer cancel()

	done := make(chan string, 1)
	go func() {
		version, _, _ := GetFfmpegVersionFromContext(ctx, script)
		done <- version
	}()

	select {
	case version := <-done:
		assert.Empty(t, version)
	case <-time.After(hungProbeGiveUp):
		t.Fatal("GetFfmpegVersionFromContext did not return after its context ended; a lingering child holds the pipe")
	}
}
