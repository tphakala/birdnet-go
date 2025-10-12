package myaudio

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		// Milliseconds
		{
			name:     "zero duration",
			duration: 0,
			want:     "0ms",
		},
		{
			name:     "100 milliseconds",
			duration: 100 * time.Millisecond,
			want:     "100ms",
		},
		{
			name:     "450 milliseconds",
			duration: 450 * time.Millisecond,
			want:     "450ms",
		},
		{
			name:     "999 milliseconds",
			duration: 999 * time.Millisecond,
			want:     "999ms",
		},
		// Seconds
		{
			name:     "1 second",
			duration: time.Second,
			want:     "1s",
		},
		{
			name:     "11.46 seconds (the example from user)",
			duration: 11*time.Second + 460*time.Millisecond,
			want:     "11s",
		},
		{
			name:     "30 seconds",
			duration: 30 * time.Second,
			want:     "30s",
		},
		{
			name:     "59 seconds",
			duration: 59 * time.Second,
			want:     "59s",
		},
		// Minutes
		{
			name:     "1 minute",
			duration: time.Minute,
			want:     "1m 0s",
		},
		{
			name:     "1 minute 30 seconds",
			duration: time.Minute + 30*time.Second,
			want:     "1m 30s",
		},
		{
			name:     "2 minutes 45 seconds",
			duration: 2*time.Minute + 45*time.Second,
			want:     "2m 45s",
		},
		{
			name:     "59 minutes 59 seconds",
			duration: 59*time.Minute + 59*time.Second,
			want:     "59m 59s",
		},
		// Hours
		{
			name:     "1 hour",
			duration: time.Hour,
			want:     "1h 0m 0s",
		},
		{
			name:     "1 hour 23 minutes 45 seconds",
			duration: time.Hour + 23*time.Minute + 45*time.Second,
			want:     "1h 23m 45s",
		},
		{
			name:     "2 hours 30 minutes",
			duration: 2*time.Hour + 30*time.Minute,
			want:     "2h 30m 0s",
		},
		// Negative durations
		{
			name:     "negative 5 seconds",
			duration: -5 * time.Second,
			want:     "-5s",
		},
		{
			name:     "negative 1 minute 30 seconds",
			duration: -(time.Minute + 30*time.Second),
			want:     "-1m 30s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDuration(tt.duration)
			if got != tt.want {
				t.Errorf("FormatDuration(%v) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestFormatDurationCompact(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		// Milliseconds
		{
			name:     "zero duration",
			duration: 0,
			want:     "0ms",
		},
		{
			name:     "450 milliseconds",
			duration: 450 * time.Millisecond,
			want:     "450ms",
		},
		// Seconds
		{
			name:     "11.46 seconds",
			duration: 11*time.Second + 460*time.Millisecond,
			want:     "11s",
		},
		{
			name:     "30 seconds",
			duration: 30 * time.Second,
			want:     "30s",
		},
		// Minutes
		{
			name:     "1 minute",
			duration: time.Minute,
			want:     "1.0m",
		},
		{
			name:     "1 minute 30 seconds",
			duration: time.Minute + 30*time.Second,
			want:     "1.5m",
		},
		{
			name:     "2 minutes 45 seconds",
			duration: 2*time.Minute + 45*time.Second,
			want:     "2.8m",
		},
		// Hours
		{
			name:     "1 hour",
			duration: time.Hour,
			want:     "1.0h",
		},
		{
			name:     "1 hour 30 minutes",
			duration: time.Hour + 30*time.Minute,
			want:     "1.5h",
		},
		{
			name:     "2 hours 15 minutes",
			duration: 2*time.Hour + 15*time.Minute,
			want:     "2.2h",
		},
		// Negative durations
		{
			name:     "negative 5 seconds",
			duration: -5 * time.Second,
			want:     "-5s",
		},
		{
			name:     "negative 2.5 minutes",
			duration: -(2*time.Minute + 30*time.Second),
			want:     "-2.5m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDurationCompact(tt.duration)
			if got != tt.want {
				t.Errorf("FormatDurationCompact(%v) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestFormatDurationRoundTrip(t *testing.T) {
	// Test that formatting produces readable output for various durations
	durations := []time.Duration{
		100 * time.Millisecond,
		time.Second,
		30 * time.Second,
		time.Minute + 30*time.Second,
		time.Hour + 23*time.Minute + 45*time.Second,
		24 * time.Hour,
	}

	for _, d := range durations {
		formatted := FormatDuration(d)
		compact := FormatDurationCompact(d)

		// Just ensure no panics and output is non-empty
		if formatted == "" {
			t.Errorf("FormatDuration(%v) returned empty string", d)
		}
		if compact == "" {
			t.Errorf("FormatDurationCompact(%v) returned empty string", d)
		}

		t.Logf("Duration: %v -> Standard: %q, Compact: %q", d, formatted, compact)
	}
}
