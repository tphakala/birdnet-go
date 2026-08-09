package birdweather

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/clipenc"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// captureUploadLogs redirects the global logger to a buffer at Info level, which
// is the level a default support dump actually contains. Anything the tests here
// require must therefore be emitted at Info or above; a field only present at
// Debug is not available to triage. It swaps process-wide state, so tests using
// it must not run with t.Parallel().
func captureUploadLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	capture := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	cl, err := logger.NewCentralLogger(
		&logger.LoggingConfig{
			Console:      &logger.ConsoleOutput{Enabled: false},
			FileOutput:   &logger.FileOutput{Enabled: false},
			DefaultLevel: "info",
		},
		capture,
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, cl.Close()) })

	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(prev) })

	return &buf
}

// TestEncodeWithNativeFLAC_LogsEncoderAttribution covers the gap that made
// "my BirdWeather uploads are too quiet" unanswerable from a default-level
// dump, even though the same question was answerable for saved clips: the
// upload path recorded neither the encoder that ran nor the gain it applied
// anywhere above Debug.
func TestEncodeWithNativeFLAC_LogsEncoderAttribution(t *testing.T) {
	logs := captureUploadLogs(t)

	client := &BwClient{Settings: &conf.Settings{}}
	res, err := client.encodeWithNativeFLAC(sinePCM(conf.SampleRate, -30), testTimestamp)
	require.NoError(t, err)
	require.NotNil(t, res)

	out := logs.String()
	assert.Contains(t, out, "encoder="+clipenc.NativeFLAC,
		"the upload log must name the encoder, using the same vocabulary as the clip export path")
	// Asserted by value, not just by key: a key-only check passes on a garbage
	// value, and the point of the field is that it reports the gain actually
	// applied. -30 dBFS input normalised toward -23 LUFS yields a positive gain.
	gainLine := logLineWith(t, out, "encoder=native-flac")
	assert.Regexp(t, `gain_db=[0-9]+\.[0-9]+`, gainLine,
		"gain must be visible at Info with a real value; at Debug it is absent from a default dump")
	// Exact match, not a prefix: birdweather_soundscape_encode is a strict
	// prefix of birdweather_soundscape_encode_failed, the failure tag emitted
	// from birdweather_client.go, so a Contains on the bare name cannot tell the
	// two apart.
	assert.Contains(t, out, "operation=birdweather_soundscape_encode\n",
		"the success line must carry its operation tag, so success and failure are "+
			"distinguishable without parsing the message")
	assert.NotContains(t, out, "birdweather_soundscape_encode_failed",
		"a successful encode must not emit the failure tag")
}

// logLineWith returns the single captured log line containing marker, failing
// if none or more than one matches. Assertions scoped to a line cannot be
// satisfied by a value that happens to appear on some other line.
func logLineWith(t *testing.T, logs, marker string) string {
	t.Helper()
	var found string
	for line := range strings.SplitSeq(logs, "\n") {
		if strings.Contains(line, marker) {
			require.Empty(t, found, "marker %q matched more than one line", marker)
			found = line
		}
	}
	require.NotEmpty(t, found, "no log line contains %q; captured:\n%s", marker, logs)
	return found
}
