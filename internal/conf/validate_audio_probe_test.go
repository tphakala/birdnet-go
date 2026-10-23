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
	// lingeringChildSeconds is how long the child of the clean-exit wrapper keeps
	// the output pipe open. It must exceed ffmpegProbeWaitDelay (2 s).
	lingeringChildSeconds = 5
	// cleanExitWrapperScript prints a version, leaves a child holding the output
	// pipe and exits 0, like a wrapper that backgrounds a helper.
	cleanExitWrapperScript = "#!/bin/sh\necho 'ffmpeg version 7.1.2 Copyright (c) 2000-2025'\nsleep %d &\nexit 0\n"
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

// TestGetFfmpegVersionFromContext_AcceptsVersionWhenChildKeepsPipeOpen pins that
// a probe whose process exited successfully and printed a version still reports
// it when a child keeps the output pipe open until the wait delay expires.
func TestGetFfmpegVersionFromContext_AcceptsVersionWhenChildKeepsPipeOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as the fake ffmpeg")
	}
	require.Greater(t, time.Duration(lingeringChildSeconds)*time.Second, ffmpegProbeWaitDelay)
	script := filepath.Join(t.TempDir(), "ffmpeg")
	require.NoError(t, os.WriteFile(script, []byte(fmt.Sprintf(cleanExitWrapperScript, lingeringChildSeconds)), 0o755))

	version, major, minor := GetFfmpegVersionFromContext(t.Context(), script)

	assert.Equal(t, "7.1.2", version)
	assert.Equal(t, 7, major)
	assert.Equal(t, 1, minor)
}
