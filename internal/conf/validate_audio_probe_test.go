package conf

import (
	"context"
	"fmt"
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
	// lingeringChildSeconds is how long the child of the wrapper keeps
	// the output pipe open. It must exceed ffmpegProbeWaitDelay (2 s).
	lingeringChildSeconds = 5
	// cleanExitWrapperScript prints a version, leaves a child holding the output
	// pipe and exits with the given status, like a wrapper that backgrounds a
	// helper. Its arguments are the child's lifetime in seconds, then the status.
	cleanExitWrapperScript = "#!/bin/sh\necho 'ffmpeg version 7.1.2 Copyright (c) 2000-2025'\nsleep %d &\nexit %d\n"
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

// TestGetFfmpegVersionFromContext_ChildKeepsPipeOpenAfterExit pins that a probe
// whose process printed a version and left a child holding the output pipe past
// the wait delay reports the version after a successful exit and none after a
// failed one.
func TestGetFfmpegVersionFromContext_ChildKeepsPipeOpenAfterExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as the fake ffmpeg")
	}
	require.Greater(t, time.Duration(lingeringChildSeconds)*time.Second, ffmpegProbeWaitDelay)

	tests := []struct {
		name        string
		exitCode    int
		wantVersion string
		wantMajor   int
		wantMinor   int
	}{
		{name: "successful exit reports the version", exitCode: 0, wantVersion: "7.1.2", wantMajor: 7, wantMinor: 1},
		{name: "failed exit reports none", exitCode: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "ffmpeg")
			body := fmt.Sprintf(cleanExitWrapperScript, lingeringChildSeconds, tt.exitCode)
			require.NoError(t, os.WriteFile(script, []byte(body), 0o755))

			version, major, minor := GetFfmpegVersionFromContext(t.Context(), script)

			assert.Equal(t, tt.wantVersion, version)
			assert.Equal(t, tt.wantMajor, major)
			assert.Equal(t, tt.wantMinor, minor)
		})
	}
}
