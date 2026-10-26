package apicore

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

const (
	dayRangeStartField = "start_date"
	dayRangeEndField   = "end_date"
)

// errDayRangeInverted is returned when the end date lies before the start date.
var errDayRangeInverted = errors.NewStd(dayRangeEndField + " before " + dayRangeStartField)

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
// range tile exactly. The error text is "start_date: <parse error>",
// "end_date: <parse error>" or "end_date before start_date", so callers wrap it
// under their own sentinel with %w without the message changing. Callers apply
// their own limits to Days and the years.
func ResolveDayRange(startDate, endDate string, defaultDays int, loc *time.Location) (DayRange, error) {
	startDay, err := wallclock.ParseDate(startDate)
	if err != nil {
		return DayRange{}, fmt.Errorf("%s: %w", dayRangeStartField, err)
	}
	endDay := startDay.AddDays(defaultDays)
	if endDate != "" {
		endDay, err = wallclock.ParseDate(endDate)
		if err != nil {
			return DayRange{}, fmt.Errorf("%s: %w", dayRangeEndField, err)
		}
	}

	days := wallclock.DaysBetween(startDay, endDay) + 1
	if days < 1 {
		return DayRange{}, errDayRangeInverted
	}

	start, _ := wallclock.DayBounds(startDay, loc)
	_, end := wallclock.DayBounds(endDay, loc)
	return DayRange{StartDate: startDay, EndDate: endDay, Start: start, End: end, Days: days}, nil
}
