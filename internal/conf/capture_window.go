package conf

import "time"

// CaptureWindow describes the audio-clip capture length and readiness time derived for
// a detection. It is the single source of truth shared by the audio-export scheduler
// (which decides when to defer the clip write until the capture tail is recorded) and
// the media API (which decides whether a not-yet-written clip is still legitimately
// pending, and how long a client should wait before retrying).
type CaptureWindow struct {
	// Length is the capture length in seconds after applying both the derived-duration
	// and the buffer-cap rules. This is the length actually captured to disk.
	Length int
	// RequestedLength is the length after the derived-duration rule but before the
	// buffer cap. It equals Length when the clip is not capped, and is retained so the
	// scheduler can log how long a clip was requested before capping (diagnostically
	// useful for continuous vocalizers that request far more than the buffer holds).
	RequestedLength int
	// BufferCap is the ring-buffer size the length was capped at.
	BufferCap int
	// ReadyAt is the wall-clock time at which the capture tail has been recorded,
	// equal to beginTime advanced by Length seconds. Before this time the clip may not
	// yet exist on disk.
	ReadyAt time.Time
	// Derived is true when the detection time span produced a capture length longer
	// than the configured Export.Length.
	Derived bool
	// Capped is true when the capture length was reduced to the ring-buffer size.
	Capped bool
}

// DetectionCaptureWindow computes the capture length and readiness time for a detection
// spanning beginTime..endTime, using the same rules as the audio-export scheduler (see
// analysis/processor.buildSaveAudioAction, which is refactored onto this method so the
// two never diverge). The boolean return is false when beginTime is zero: no meaningful
// readiness time can be derived from an unknown start, so the media API must not treat
// such a clip as pending. The CaptureWindow is still fully populated in that case so the
// scheduler can consume it unconditionally.
//
// The rules, in order:
//   - start from the configured export length (Realtime.Audio.Export.Length);
//   - when both timestamps are set, derive a length from the detection span plus the
//     pre-capture padding and use it when it exceeds the configured length;
//   - cap the length at the ring-buffer size (ExtendedCapture.CaptureBufferSeconds when
//     extended capture is enabled and that value is positive, otherwise
//     DefaultCaptureBufferSeconds).
//
// ReadyAt is beginTime advanced by the resulting Length.
func (s *Settings) DetectionCaptureWindow(beginTime, endTime time.Time) (CaptureWindow, bool) {
	length := s.Realtime.Audio.Export.Length
	derived := false
	// The span-derived length is only meaningful when both endpoints are known. This
	// matches buildSaveAudioAction's guard exactly, including its integer truncation of
	// sub-second spans and its treatment of a negative span (endTime before beginTime),
	// where derivedLength stays below the configured length and is therefore ignored.
	if !beginTime.IsZero() && !endTime.IsZero() {
		derivedLength := int(endTime.Sub(beginTime).Seconds()) + s.Realtime.Audio.Export.PreCapture
		if derivedLength > length {
			length = derivedLength
			derived = true
		}
	}
	requested := length

	bufferCap := DefaultCaptureBufferSeconds
	if s.Realtime.ExtendedCapture.Enabled && s.Realtime.ExtendedCapture.CaptureBufferSeconds > 0 {
		bufferCap = s.Realtime.ExtendedCapture.CaptureBufferSeconds
	}
	capped := false
	if length > bufferCap {
		length = bufferCap
		capped = true
	}

	return CaptureWindow{
		Length:          length,
		RequestedLength: requested,
		BufferCap:       bufferCap,
		ReadyAt:         beginTime.Add(time.Duration(length) * time.Second),
		Derived:         derived,
		Capped:          capped,
	}, !beginTime.IsZero()
}
