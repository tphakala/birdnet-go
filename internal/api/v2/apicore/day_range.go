package apicore

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/wallclock"
)

const (
	// DayRangeStartField and DayRangeEndField name the query parameters in the
	// errors ResolveDayRange returns.
	DayRangeStartField = "start_date"
	DayRangeEndField   = "end_date"
)

// DayRangeError is the error ResolveDayRange returns for a date that does not
// parse as YYYY-MM-DD or an end date before the start date. Its text names the
// parameter and the reason, so callers wrap it under their own sentinel
// without the message changing. Unwrap returns the parse error, nil for an
// inverted range.
type DayRangeError struct {
	Field string // the offending parameter, DayRangeStartField or DayRangeEndField
	Err   error  // the underlying parse error, nil for an inverted range
	msg   string
}

// Error implements error.
func (e *DayRangeError) Error() string { return e.msg }

// Unwrap returns the underlying parse error.
func (e *DayRangeError) Unwrap() error { return e.Err }

// DayRange is an inclusive range of local calendar dates and the half-open
// range of instants they cover in one zone.
type DayRange struct {
	// StartDate and EndDate are the first and last date, both inclusive.
	StartDate, EndDate wallclock.Date
	// Start and End bound the instants of the range, [Start, End): the start of
	// StartDate and the start of the date after EndDate.
	Start, End time.Time
	// Days is the inclusive number of calendar dates, at least 1.
	Days int
}

// ResolveDayRange converts inclusive YYYY-MM-DD dates in loc to a DayRange. An
// empty endDate defaults to startDate plus defaultDays. A date whose midnight
// does not exist in loc begins at the transition instant, and the days of the
// range tile exactly. The error is a *DayRangeError. Callers apply their own limits to Days and the years.
func ResolveDayRange(startDate, endDate string, defaultDays int, loc *time.Location) (DayRange, error) {
	startDay, err := wallclock.ParseDate(startDate)
	if err != nil {
		return DayRange{}, &DayRangeError{Field: DayRangeStartField, Err: err, msg: DayRangeStartField + ": " + err.Error()}
	}
	endDay := startDay.AddDays(defaultDays)
	if endDate != "" {
		endDay, err = wallclock.ParseDate(endDate)
		if err != nil {
			return DayRange{}, &DayRangeError{Field: DayRangeEndField, Err: err, msg: DayRangeEndField + ": " + err.Error()}
		}
	}

	days := wallclock.DaysBetween(startDay, endDay) + 1
	if days < 1 {
		return DayRange{}, &DayRangeError{Field: DayRangeEndField, msg: DayRangeEndField + " before " + DayRangeStartField}
	}

	start, _ := wallclock.DayBounds(startDay, loc)
	_, end := wallclock.DayBounds(endDay, loc)
	return DayRange{StartDate: startDay, EndDate: endDay, Start: start, End: end, Days: days}, nil
}
