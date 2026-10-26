package privacy

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// maxRangeDays caps the inclusive length of a request. Rollup and discard
	// rows are kept 30 days (privacyRollupRetention, privacyDiscardRetention in
	// internal/analysis/privacyrollup/writer.go), which spans 31 local dates, so
	// a longer range only costs a query on the single SQLite connection.
	maxRangeDays = 31

	// defaultRangeDays is how far end_date defaults past start_date (31 dates).
	defaultRangeDays = maxRangeDays - 1
)

var (
	// ErrRangeTooLong is returned by localDayRange when the inclusive day count
	// exceeds maxRangeDays.
	ErrRangeTooLong = errors.NewStd("privacy filter date range too long")
	// ErrInvalidDate is returned by localDayRange when a date does not parse or
	// the end lies before the start.
	ErrInvalidDate = errors.NewStd("privacy filter date invalid")
)

// localDayRange converts inclusive YYYY-MM-DD dates in loc to the half-open
// range [start, end) of local midnights. An empty endDate defaults to startDate
// plus defaultRangeDays. endDateOut is the inclusive end date after defaulting.
func localDayRange(startDate, endDate string, loc *time.Location) (start, end time.Time, endDateOut string, err error) {
	startDay, err := time.ParseInLocation(time.DateOnly, startDate, loc)
	if err != nil {
		return time.Time{}, time.Time{}, "", fmt.Errorf("%w: start_date: %w", ErrInvalidDate, err)
	}
	var endDay time.Time
	if endDate == "" {
		endDay = startDay.AddDate(0, 0, defaultRangeDays)
	} else {
		endDay, err = time.ParseInLocation(time.DateOnly, endDate, loc)
		if err != nil {
			return time.Time{}, time.Time{}, "", fmt.Errorf("%w: end_date: %w", ErrInvalidDate, err)
		}
	}

	// Count days on the UTC calendar so DST changes cannot skew the cap.
	startUTC := time.Date(startDay.Year(), startDay.Month(), startDay.Day(), 0, 0, 0, 0, time.UTC)
	endUTC := time.Date(endDay.Year(), endDay.Month(), endDay.Day(), 0, 0, 0, 0, time.UTC)
	days := int(endUTC.Sub(startUTC).Hours()/apicore.HoursPerDay) + 1
	if days < 1 {
		return time.Time{}, time.Time{}, "", fmt.Errorf("%w: end_date before start_date", ErrInvalidDate)
	}
	if days > maxRangeDays {
		return time.Time{}, time.Time{}, "", fmt.Errorf("%w: %d days", ErrRangeTooLong, days)
	}

	// AddDate, never +86400: a DST day is not 24 hours long.
	return startDay, endDay.AddDate(0, 0, 1), endDay.Format(time.DateOnly), nil
}
