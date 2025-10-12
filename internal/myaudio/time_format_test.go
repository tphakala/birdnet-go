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

func TestFormatDurationRounding(t *testing.T) {
	// Test that rounding works correctly for edge cases
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{
			name:     "11.4 seconds (rounds down)",
			duration: 11*time.Second + 400*time.Millisecond,
			want:     "11s",
		},
		{
			name:     "11.5 seconds (rounds up)",
			duration: 11*time.Second + 500*time.Millisecond,
			want:     "12s",
		},
		{
			name:     "11.6 seconds (rounds up)",
			duration: 11*time.Second + 600*time.Millisecond,
			want:     "12s",
		},
		{
			name:     "59.5 seconds (rounds to 1m 0s)",
			duration: 59*time.Second + 500*time.Millisecond,
			want:     "1m 0s",
		},
		{
			name:     "1m 29.4s (rounds to 1m 29s)",
			duration: time.Minute + 29*time.Second + 400*time.Millisecond,
			want:     "1m 29s",
		},
		{
			name:     "1m 29.5s (rounds to 1m 30s)",
			duration: time.Minute + 29*time.Second + 500*time.Millisecond,
			want:     "1m 30s",
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
