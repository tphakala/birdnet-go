package telemetry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// expiredBuildDate returns an RFC3339 build date a day past maxTelemetryBuildAge.
func expiredBuildDate() string {
	return time.Now().Add(-maxTelemetryBuildAge - 24*time.Hour).Format(time.RFC3339)
}

func TestBuildTooOld(t *testing.T) {
	t.Parallel()

	// wantMaxBuildAgeDays is the cutoff PRIVACY.md and the wiki promise; kept
	// separate from the production constant so a change to it fails here.
	const wantMaxBuildAgeDays = 183

	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	limit := now.AddDate(0, 0, -wantMaxBuildAgeDays)

	tests := []struct {
		name      string
		buildDate string
		want      bool
	}{
		{"empty build date fails open", "", false},
		{"unparseable build date fails open", "garbage", false},
		{"build date after now (clock behind) is not expired", now.Add(24 * time.Hour).Format(time.RFC3339), false},
		{"exactly at the limit is not expired", limit.Format(time.RFC3339), false},
		{"one second past the limit is expired", limit.Add(-time.Second).Format(time.RFC3339), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, buildTooOld(tt.buildDate, now))
		})
	}
}

// TestFrontendDSN verifies the frontend only gets a DSN when reporting is allowed.
// Not parallel: sets an environment variable.
func TestFrontendDSN(t *testing.T) {
	const dsn = "https://dummy@example.ingest.sentry.io/1"
	t.Setenv(sentryDSNEnvVar, dsn)

	fresh := time.Now().Add(-time.Hour).Format(time.RFC3339)
	tests := []struct {
		name     string
		settings *conf.Settings
		want     string
	}{
		{"nil settings", nil, ""},
		{"telemetry disabled", &conf.Settings{BuildDate: fresh}, ""},
		{"enabled and fresh build", &conf.Settings{Sentry: conf.SentrySettings{Enabled: true}, BuildDate: fresh}, dsn},
		{"enabled but expired build", &conf.Settings{Sentry: conf.SentrySettings{Enabled: true}, BuildDate: expiredBuildDate()}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FrontendDSN(tt.settings))
		})
	}
}
