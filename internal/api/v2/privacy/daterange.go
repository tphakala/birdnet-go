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

	// minRangeYear and maxRangeYear bound the accepted dates so that the range
	// instants echoed in the response stay inside the years JSON can encode
	// (0 to 9999) in every time zone.
	minRangeYear = 1970
	maxRangeYear = 9998
)

var (
	// ErrRangeTooLong is returned by localDayRange when the inclusive day count
	// exceeds maxRangeDays.
	ErrRangeTooLong = errors.NewStd("privacy filter date range too long")
	// ErrInvalidDate is returned by localDayRange when a date does not parse or
	// the end lies before the start, or a date is outside the years minRangeYear
	// to maxRangeYear.
	ErrInvalidDate = errors.NewStd("privacy filter date invalid")
)

// localDayRange converts inclusive YYYY-MM-DD dates in loc to the half-open
// range [start, end) of local day starts. An empty endDate defaults to startDate
// plus defaultRangeDays. endDateOut is the inclusive end date after defaulting.
func localDayRange(startDate, endDate string, loc *time.Location) (start, end time.Time, endDateOut string, err error) {
	r, err := apicore.ResolveDayRange(startDate, endDate, defaultRangeDays, loc)
	if err != nil {
		return time.Time{}, time.Time{}, "", fmt.Errorf("%w: %w", ErrInvalidDate, err)
	}
	if r.StartDate.Year < minRangeYear || r.EndDate.Year > maxRangeYear {
		return time.Time{}, time.Time{}, "", fmt.Errorf("%w: year outside %d to %d", ErrInvalidDate, minRangeYear, maxRangeYear)
	}
	if r.Days > maxRangeDays {
		return time.Time{}, time.Time{}, "", fmt.Errorf("%w: %d days", ErrRangeTooLong, r.Days)
	}
	return r.Start, r.End, r.EndDate.String(), nil
}
