package processor

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// TestValidateAndLogFilterConfig_NoFixedHardwareClaims verifies the startup
// filter-config logs make no fixed claim about the hardware a filter level
// needs: the analysis cadence cap adapts to the hardware, and the plan log
// reports the actual cadence. Not parallel: it replaces the global logger.
func TestValidateAndLogFilterConfig_NoFixedHardwareClaims(t *testing.T) {
	var buf bytes.Buffer
	capture := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cl, err := logger.NewCentralLogger(
		&logger.LoggingConfig{
			Console:      &logger.ConsoleOutput{Enabled: false},
			FileOutput:   &logger.FileOutput{Enabled: false},
			DefaultLevel: "debug",
		},
		capture,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cl.Close() })
	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(prev) })

	cases := []struct {
		level   int
		overlap float64
	}{
		{4, 2.7},
		{4, 2.8},
		{5, 2.7},
		{5, 2.8},
	}
	for _, tc := range cases {
		buf.Reset()
		settings := &conf.Settings{}
		settings.Realtime.FalsePositiveFilter.Level = tc.level
		settings.BirdNET.Overlap = tc.overlap

		validateAndLogFilterConfig(settings)

		out := buf.String()
		require.NotEmpty(t, out, "level %d overlap %.1f should log its configuration", tc.level, tc.overlap)
		assert.NotContains(t, out, "requires fast hardware", "level %d overlap %.1f", tc.level, tc.overlap)
		assert.NotContains(t, out, "RPi", "level %d overlap %.1f", tc.level, tc.overlap)
		assert.NotContains(t, out, "hardware_req", "level %d overlap %.1f", tc.level, tc.overlap)
	}
}
