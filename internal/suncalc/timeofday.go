package suncalc

import "time"

// SunEventWindow is the half-width of the sunrise/sunset transition window: a
// detection within this much time before or after sunrise (or sunset) is
// classified as "sunrise" (or "sunset") rather than day or night. It is exported
// as the single source of truth for the window width so the datastore's SQL
// time-of-day filter can derive its boundaries from the same value the per-row
// ClassifyTimeOfDay uses, keeping the two paths from drifting apart.
const SunEventWindow = 30 * time.Minute

// clockPeriod is the 24-hour clock-circle period used for wraparound-safe
// comparisons.
const clockPeriod = 24 * time.Hour

// Time-of-day categories returned by ClassifyTimeOfDay. The string values are
// kept in lockstep with datastore.TimeOfDay{Day,Night,Sunrise,Sunset}; suncalc
// is a leaf package and cannot import datastore (that would be an import cycle),
// so a drift test in the datastore package asserts the values stay equal.
const (
	timeOfDayDay     = "day"
	timeOfDayNight   = "night"
	timeOfDaySunrise = "sunrise"
	timeOfDaySunset  = "sunset"
)

// ClassifyTimeOfDay categorizes a detection timestamp relative to that day's sun
// events, returning one of "sunrise", "sunset", "day", or "night". A detection
// within SunEventWindow (30 minutes) of sunrise or sunset is classified as that
// transition; otherwise it is "day" between sunrise and sunset and "night"
// outside. The sunrise and sunset windows are checked first, so a timestamp that
// falls in both a transition window and the daytime span is reported as the
// transition.
//
// Comparison is done on the wall-clock time of day (hours:minutes:seconds) of
// each value in its own location, matching how sun events and detection times
// are recorded, and is wraparound-safe: a transition window that straddles
// midnight (for example a high-latitude summer sunset at 23:50, whose window
// runs to 00:20) is handled correctly, where a naive lexical time-string
// comparison would silently misclassify it.
//
// If sunEvents is nil the timestamp cannot be classified and the empty string is
// returned; callers that lack sun data should not call this.
func ClassifyTimeOfDay(detectionTime time.Time, sunEvents *SunEventTimes) string {
	if sunEvents == nil {
		return ""
	}

	det := clockOffset(detectionTime)
	sunrise := clockOffset(sunEvents.Sunrise)
	sunset := clockOffset(sunEvents.Sunset)

	switch {
	case withinWindow(det, sunrise, SunEventWindow):
		return timeOfDaySunrise
	case withinWindow(det, sunset, SunEventWindow):
		return timeOfDaySunset
	case inForwardArc(det, sunrise, sunset):
		return timeOfDayDay
	default:
		return timeOfDayNight
	}
}

// inForwardArc reports whether the time of day tod lies on the forward arc from
// start (inclusive) to end (exclusive) on the 24-hour clock. When start <= end
// this is the plain interval [start, end). When start > end the arc wraps past
// midnight, [start, 24h) plus [0, end): this happens at high latitudes in summer
// where the local sunset falls after midnight (the observer timezone's offset
// carries the evening sunset past 00:00), so sunset's wall-clock precedes
// sunrise's even though the day spans nearly 24 hours.
func inForwardArc(tod, start, end time.Duration) bool {
	if start <= end {
		return tod >= start && tod < end
	}
	return tod >= start || tod < end
}

// clockOffset returns the wall-clock time of day of t (in t's own location) as a
// duration since local midnight, at one-second resolution.
func clockOffset(t time.Time) time.Duration {
	h, m, s := t.Clock()
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second
}

// withinWindow reports whether the time-of-day tod is within radius of center on
// a 24-hour clock circle. Using the circular (shorter-arc) distance makes a
// window that crosses midnight behave correctly: with center 23:50 and radius
// 30m, a tod of 00:10 is 20 minutes away, not 23h40m.
func withinWindow(tod, center, radius time.Duration) bool {
	d := tod - center
	if d < 0 {
		d = -d
	}
	if d > clockPeriod-d {
		d = clockPeriod - d
	}
	return d <= radius
}
