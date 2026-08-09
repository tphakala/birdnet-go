package conf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCaptureWindowSettings builds a minimal Settings with only the fields
// DetectionCaptureWindow reads populated.
func newCaptureWindowSettings(length, preCapture int, ecEnabled bool, ecBuffer int) *Settings {
	s := &Settings{}
	s.Realtime.Audio.Export.Length = length
	s.Realtime.Audio.Export.PreCapture = preCapture
	s.Realtime.ExtendedCapture.Enabled = ecEnabled
	s.Realtime.ExtendedCapture.CaptureBufferSeconds = ecBuffer
	return s
}

func TestDetectionCaptureWindow(t *testing.T) {
	t.Parallel()

	begin := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		length      int
		preCapture  int
		ecEnabled   bool
		ecBuffer    int
		begin       time.Time
		end         time.Time
		wantOK      bool
		wantLength  int
		wantReqLen  int
		wantCap     int
		wantDerived bool
		wantCapped  bool
	}{
		{
			name: "configured length wins over short span", length: 20, preCapture: 3,
			ecEnabled: false, ecBuffer: 0,
			begin: begin, end: begin.Add(5 * time.Second),
			wantOK: true, wantLength: 20, wantReqLen: 20, wantCap: DefaultCaptureBufferSeconds,
			wantDerived: false, wantCapped: false,
		},
		{
			name: "derived length wins over configured", length: 20, preCapture: 3,
			ecEnabled: false, ecBuffer: 0,
			begin: begin, end: begin.Add(40 * time.Second),
			// 40s span + 3s precapture = 43 > 20, under the 120 default cap.
			wantOK: true, wantLength: 43, wantReqLen: 43, wantCap: DefaultCaptureBufferSeconds,
			wantDerived: true, wantCapped: false,
		},
		{
			name: "derived length capped at extended-capture buffer", length: 20, preCapture: 3,
			ecEnabled: true, ecBuffer: 185,
			begin: begin, end: begin.Add(1000 * time.Second),
			// 1000 + 3 = 1003 requested, capped at the configured 185s EC buffer.
			wantOK: true, wantLength: 185, wantReqLen: 1003, wantCap: 185,
			wantDerived: true, wantCapped: true,
		},
		{
			name: "cap falls back to default when EC disabled", length: 20, preCapture: 3,
			ecEnabled: false, ecBuffer: 999, // ignored because EC disabled
			begin: begin, end: begin.Add(1000 * time.Second),
			wantOK: true, wantLength: DefaultCaptureBufferSeconds, wantReqLen: 1003,
			wantCap: DefaultCaptureBufferSeconds, wantDerived: true, wantCapped: true,
		},
		{
			name: "cap falls back to default when EC buffer is zero", length: 20, preCapture: 3,
			ecEnabled: true, ecBuffer: 0, // enabled but unset -> default cap under approach (a)
			begin: begin, end: begin.Add(1000 * time.Second),
			wantOK: true, wantLength: DefaultCaptureBufferSeconds, wantReqLen: 1003,
			wantCap: DefaultCaptureBufferSeconds, wantDerived: true, wantCapped: true,
		},
		{
			name: "sub-second span truncates like the scheduler", length: 5, preCapture: 0,
			ecEnabled: false, ecBuffer: 0,
			begin: begin, end: begin.Add(9500 * time.Millisecond),
			// int(9.5) = 9 > 5 configured.
			wantOK: true, wantLength: 9, wantReqLen: 9, wantCap: DefaultCaptureBufferSeconds,
			wantDerived: true, wantCapped: false,
		},
		{
			name: "end before begin ignores negative derived length", length: 20, preCapture: 3,
			ecEnabled: false, ecBuffer: 0,
			begin: begin, end: begin.Add(-30 * time.Second),
			// derived = -30 + 3 = -27 < 20 configured -> configured used, not derived.
			wantOK: true, wantLength: 20, wantReqLen: 20, wantCap: DefaultCaptureBufferSeconds,
			wantDerived: false, wantCapped: false,
		},
		{
			name: "zero end uses configured length", length: 20, preCapture: 3,
			ecEnabled: false, ecBuffer: 0,
			begin: begin, end: time.Time{},
			wantOK: true, wantLength: 20, wantReqLen: 20, wantCap: DefaultCaptureBufferSeconds,
			wantDerived: false, wantCapped: false,
		},
		{
			name: "zero begin returns not-ok", length: 20, preCapture: 3,
			ecEnabled: false, ecBuffer: 0,
			begin: time.Time{}, end: begin,
			wantOK: false, wantLength: 20, wantReqLen: 20, wantCap: DefaultCaptureBufferSeconds,
			wantDerived: false, wantCapped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newCaptureWindowSettings(tt.length, tt.preCapture, tt.ecEnabled, tt.ecBuffer)

			win, ok := s.DetectionCaptureWindow(tt.begin, tt.end)

			assert.Equal(t, tt.wantOK, ok, "ok")
			assert.Equal(t, tt.wantLength, win.Length, "Length")
			assert.Equal(t, tt.wantReqLen, win.RequestedLength, "RequestedLength")
			assert.Equal(t, tt.wantCap, win.BufferCap, "BufferCap")
			assert.Equal(t, tt.wantDerived, win.Derived, "Derived")
			assert.Equal(t, tt.wantCapped, win.Capped, "Capped")

			if !tt.begin.IsZero() {
				assert.Equal(t, tt.begin.Add(time.Duration(tt.wantLength)*time.Second), win.ReadyAt, "ReadyAt")
			}
		})
	}
}

// TestDetectionCaptureWindow_ReadyAtArithmetic pins the exact readiness time so a future
// refactor of the length math cannot silently shift when clips are considered pending.
func TestDetectionCaptureWindow_ReadyAtArithmetic(t *testing.T) {
	t.Parallel()

	begin := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	s := newCaptureWindowSettings(20, 3, true, 185)

	win, ok := s.DetectionCaptureWindow(begin, begin.Add(90*time.Second))
	require.True(t, ok)
	// 90 + 3 = 93 > 20, under the 185 cap.
	assert.Equal(t, 93, win.Length)
	assert.Equal(t, begin.Add(93*time.Second), win.ReadyAt)
}
