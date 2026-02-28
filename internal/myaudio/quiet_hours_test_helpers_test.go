package myaudio

import "time"

// makeTime creates a time.Time on a fixed date for the given HH:MM string.
func makeTime(hhmm string) time.Time {
	ref := time.Date(2025, 6, 15, 0, 0, 0, 0, time.Local)
	return parseHHMM(hhmm, ref)
}
