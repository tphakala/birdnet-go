package migration

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// capturingLogger returns a logger that writes to a buffer for assertion.
func capturingLogger() (logger.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return logger.NewSlogLogger(&buf, logger.LogLevelTrace, time.UTC), &buf
}

func TestAuxiliaryMigrationResult_LogErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		setup       func(r *AuxiliaryMigrationResult)
		wantPhrases []string // substrings expected in output
		wantAbsent  []string // substrings that must NOT appear
	}{
		{
			name:  "no errors produces no warnings",
			setup: func(_ *AuxiliaryMigrationResult) {},
			wantAbsent: []string{
				"migration had errors",
			},
		},
		{
			name: "single section error logs only that section",
			setup: func(r *AuxiliaryMigrationResult) {
				r.Thresholds.Total = 10
				r.Thresholds.Migrated = 7
				r.Thresholds.Error = errors.NewStd("threshold fetch failed")
			},
			wantPhrases: []string{
				"threshold migration had errors",
				"threshold fetch failed",
			},
			wantAbsent: []string{
				"image cache migration had errors",
				"weather migration had errors",
			},
		},
		{
			name: "all sections with errors logs every section",
			setup: func(r *AuxiliaryMigrationResult) {
				r.ImageCaches.Error = errors.NewStd("img err")
				r.Thresholds.Error = errors.NewStd("thresh err")
				r.ThresholdEvents.Error = errors.NewStd("events err")
				r.Notifications.Error = errors.NewStd("notif err")
				r.Weather.Error = errors.NewStd("weather err")
			},
			wantPhrases: []string{
				"image cache migration had errors",
				"threshold migration had errors",
				"threshold events migration had errors",
				"notifications migration had errors",
				"weather migration had errors",
			},
		},
		{
			name: "weather error includes daily and hourly counts",
			setup: func(r *AuxiliaryMigrationResult) {
				r.Weather.DailyEventsTotal = 30
				r.Weather.DailyEventsMigrated = 28
				r.Weather.HourlyWeatherTotal = 720
				r.Weather.HourlyWeatherMigrated = 700
				r.Weather.Error = errors.NewStd("partial weather failure")
			},
			wantPhrases: []string{
				"weather migration had errors",
				"partial weather failure",
			},
			wantAbsent: []string{
				"image cache migration had errors",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := &AuxiliaryMigrationResult{}
			tt.setup(result)

			log, buf := capturingLogger()
			result.LogErrors(log)

			output := buf.String()
			for _, phrase := range tt.wantPhrases {
				assert.Contains(t, output, phrase, "expected log output to contain %q", phrase)
			}
			for _, phrase := range tt.wantAbsent {
				assert.NotContains(t, output, phrase, "expected log output NOT to contain %q", phrase)
			}
		})
	}
}

func TestAuxiliaryMigrationResult_Sections(t *testing.T) {
	t.Parallel()

	r := &AuxiliaryMigrationResult{}
	r.ImageCaches.Total = 1
	r.Thresholds.Total = 2
	r.ThresholdEvents.Total = 3
	r.Notifications.Total = 4

	sections := r.sections()
	assert.Len(t, sections, 4, "expected 4 standard sections")

	expectedNames := []string{"image cache", "threshold", "threshold events", "notifications"}
	for i, s := range sections {
		assert.Equal(t, expectedNames[i], s.name)
		assert.Equal(t, i+1, s.total)
	}
}
