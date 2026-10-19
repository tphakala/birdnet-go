package telemetry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBuildTooOld(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		buildDate string
		now       time.Time
		want      bool
	}{
		{"empty build date fails open", "", now, false},
		{"unparseable build date fails open", "garbage", now, false},
		{"build date after now (clock behind) is not expired", now.Add(24 * time.Hour).Format(time.RFC3339), epoch, false},
		{"exactly at the limit is not expired", now.Add(-maxTelemetryBuildAge).Format(time.RFC3339), now, false},
		{"one second past the limit is expired", now.Add(-maxTelemetryBuildAge - time.Second).Format(time.RFC3339), now, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, BuildTooOld(tt.buildDate, tt.now))
		})
	}
}
