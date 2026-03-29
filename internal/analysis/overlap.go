package analysis

import "time"

// effectiveOverlap scales user-configured overlap to a model's clip length.
// The overlap ratio relative to the base clip is preserved.
// Example: 2.0s overlap for 3s base -> for 5s model: (2.0 * 5) / 3 = 3.33s.
func effectiveOverlap(userOverlap, baseClipLength, modelClipLength time.Duration) time.Duration {
	if baseClipLength == 0 {
		return 0
	}
	// Work with raw int64 nanosecond counts to avoid time.Duration
	// multiplication lint issues and int64 overflow. Splitting into
	// quotient and remainder keeps every intermediate product small.
	u := int64(userOverlap)
	b := int64(baseClipLength)
	m := int64(modelClipLength)
	q := m / b
	r := m % b
	return time.Duration(u*q + u*r/b)
}

// overlapBytes converts an overlap duration to a byte count aligned to PCM
// sample boundaries. sampleRate is in Hz, bytesPerSample is typically 2 for
// 16-bit mono PCM.
func overlapBytes(overlap time.Duration, sampleRate, bytesPerSample int) int {
	if overlap <= 0 || sampleRate <= 0 || bytesPerSample <= 0 {
		return 0
	}
	samples := int((overlap * time.Duration(sampleRate)) / time.Second) //nolint:durationcheck // intentional: converts Hz rate to sample count via duration arithmetic
	return samples * bytesPerSample
}
