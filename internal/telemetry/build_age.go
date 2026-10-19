package telemetry

import "time"

// maxTelemetryBuildAgeDays is how many days after its build date a binary may
// still send telemetry. Reports from older builds describe bugs that are long
// fixed, so they are not sent. About six months.
const maxTelemetryBuildAgeDays = 183

// maxTelemetryBuildAge is maxTelemetryBuildAgeDays as a duration.
const maxTelemetryBuildAge = maxTelemetryBuildAgeDays * 24 * time.Hour

// BuildTooOld reports whether a build stamped with buildDate (RFC3339, set at
// link time) is older than maxTelemetryBuildAge at now. It fails open: an empty
// or unparseable buildDate, or one later than now (a clock that is behind, such
// as a board without an RTC before NTP sync), returns false.
func BuildTooOld(buildDate string, now time.Time) bool {
	if buildDate == "" {
		return false
	}
	built, err := time.Parse(time.RFC3339, buildDate)
	if err != nil {
		return false
	}
	return now.Sub(built) > maxTelemetryBuildAge
}
