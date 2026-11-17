package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlogLogger_TraceLevelName(t *testing.T) {
	t.Parallel()

	const oddOffsetSeconds = 5*3600 + 1800
	tests := []struct {
		name string
		tz   *time.Location
		// checkOffset is false for UTC, which is left unconverted (host-local time).
		checkOffset bool
	}{
		{"utc", time.UTC, false},
		{"fixed zone", time.FixedZone("plus0530", oddOffsetSeconds), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			buf := &bytes.Buffer{}
			l := NewSlogLogger(buf, LogLevelTrace, tt.tz)
			l.Trace("trace line")
			l.Debug("debug line")

			lines := splitLines(buf.String())
			require.Len(t, lines, 2)
			recs := make([]map[string]any, len(lines))
			for i, line := range lines {
				require.NoError(t, json.Unmarshal([]byte(line), &recs[i]))
			}
			assert.Equal(t, "TRACE", recs[0]["level"])
			assert.Equal(t, "DEBUG", recs[1]["level"])

			for _, rec := range recs {
				ts, ok := rec["time"].(string)
				require.True(t, ok)
				parsed, err := time.Parse(time.RFC3339Nano, ts)
				require.NoError(t, err)
				if tt.checkOffset {
					_, offset := parsed.Zone()
					assert.Equal(t, oddOffsetSeconds, offset)
				}
			}
		})
	}
}

func TestCentralLogger_TraceLevelNameInFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.log")
	ownPath := filepath.Join(dir, "own.log")
	modules := DisabledModuleOutputs()
	modules["ownfile"] = ModuleOutput{Enabled: true, FilePath: ownPath, Level: "trace"}

	cl, err := NewCentralLogger(&LoggingConfig{
		DefaultLevel:  "trace",
		Timezone:      "Asia/Kolkata",
		Console:       &ConsoleOutput{Enabled: false},
		FileOutput:    &FileOutput{Enabled: true, Path: mainPath, Level: "trace"},
		ModuleOutputs: modules,
	})
	require.NoError(t, err)

	cl.Module("ownfile").Trace("own trace")
	cl.Module("shared").Trace("shared trace")
	require.NoError(t, cl.Flush())
	require.NoError(t, cl.Close())

	// The main file converts timestamps to the configured zone (UTC+05:30).
	var mainRec map[string]any
	require.NoError(t, json.Unmarshal([]byte(readLogLines(t, mainPath)[0]), &mainRec))
	mainTimeStr, ok := mainRec["time"].(string)
	require.True(t, ok)
	mainTime, err := time.Parse(time.RFC3339Nano, mainTimeStr)
	require.NoError(t, err)
	_, mainOffset := mainTime.Zone()
	assert.Equal(t, 5*3600+1800, mainOffset)

	for _, path := range []string{ownPath, mainPath} {
		data := strings.Join(readLogLines(t, path), "\n")
		assert.Contains(t, data, `"level":"TRACE"`, path)
		assert.NotContains(t, data, "DEBUG-4", path)
	}
}

func TestTextHandler_TraceLevelName(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(newTextHandler(&buf, traceLevelValue, nil))
	log.Log(t.Context(), traceLevelValue, "m")
	log.Log(t.Context(), slog.LevelDebug, "d")

	lines := splitLines(buf.String())
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "TRACE")
	assert.NotContains(t, lines[0], "DEBUG-4")
	assert.Contains(t, lines[1], "DEBUG")
}

func TestReplaceAttrFunc_IgnoresGroupedAndNonLevelValues(t *testing.T) {
	t.Parallel()

	replace := replaceAttrFunc(nil)

	// A level attribute inside a group is user data, not the record level.
	grouped := slog.Any(slog.LevelKey, traceLevelValue)
	assert.Equal(t, grouped, replace([]string{"g"}, grouped))

	// A user field named "level" that is not a slog.Level is untouched.
	userField := slog.Int(slog.LevelKey, -8)
	assert.Equal(t, userField, replace(nil, userField))
}
