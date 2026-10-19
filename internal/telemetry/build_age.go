package telemetry

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// maxTelemetryBuildAgeDays is how many days after its build date a binary may
// still send telemetry. Reports from older builds describe bugs that are long
// fixed, so they are not sent. About six months.
const maxTelemetryBuildAgeDays = 183

// maxTelemetryBuildAge is maxTelemetryBuildAgeDays as a duration.
const maxTelemetryBuildAge = maxTelemetryBuildAgeDays * 24 * time.Hour

// buildTooOld reports whether a build stamped with buildDate (RFC3339, set at
// link time) is older than maxTelemetryBuildAge at now. It fails open: an empty
// or unparseable buildDate, or one later than now (a clock that is behind, such
// as a board without an RTC before NTP sync), returns false.
func buildTooOld(buildDate string, now time.Time) bool {
	built, err := time.Parse(time.RFC3339, buildDate)
	return err == nil && now.Sub(built) > maxTelemetryBuildAge
}

// reportingAllowed reports whether settings permit sending telemetry at now:
// the user opted in and the build is not past maxTelemetryBuildAge.
func reportingAllowed(settings *conf.Settings, now time.Time) bool {
	return settings != nil && settings.Sentry.Enabled && !buildTooOld(settings.BuildDate, now)
}
