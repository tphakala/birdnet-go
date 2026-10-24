package suncalc

import (
	"slices"
	"time"
)

// TimeWindow is a half-open [Start, End) span of Unix seconds.
type TimeWindow struct {
	Start int64
	End   int64
}

// TimeOfDayWindows returns the spans inside [from, to) whose instants
// ClassifyTimeOfDay labels as one of periods ("sunrise", "sunset", "day",
// "night"), merged where they touch.
//
// It exists so a store that keeps detection times as absolute instants can filter
// by real sun events in SQL and still agree with the per-row label: a detection at
// t is labelled by ClassifyTimeOfDay(t, events for t's station date), and this
// derives its windows from that same function rather than restating its rules.
// Within one station-local date the label can only change where the wall clock
// crosses a sunrise or sunset boundary, so the date is cut at those points and
// each piece is classified once.
//
// Dates are the station's own (the coordinate-derived timezone), matching what
// GetSunEventTimes does for a row's timestamp. A date with no sun events (polar
// day or night) contributes no windows, just as its rows get no label. A cut that
// lands inside a daylight-saving gap or overlap is placed by time.Date's usual
// resolution, which can move it by up to an hour on those two days a year.
func (sc *SunCalc) TimeOfDayWindows(periods []string, from, to time.Time) []TimeWindow {
	if len(periods) == 0 || !to.After(from) {
		return nil
	}
	wanted := make(map[string]struct{}, len(periods))
	for _, p := range periods {
		wanted[p] = struct{}{}
	}

	st := sc.current()
	loc := st.location
	start := from.In(loc)
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)

	var windows []TimeWindow
	for day.Before(to) {
		next := time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, loc)
		noon := time.Date(day.Year(), day.Month(), day.Day(), noonHour, 0, 0, 0, loc)
		if events, err := sc.sunEventTimes(noon, st); err == nil {
			windows = appendDayWindows(windows, wanted, day, next, &events)
		}
		day = next
	}
	return clipWindows(windows, from.Unix(), to.Unix())
}

// appendDayWindows cuts the station-local date [dayStart, dayEnd) at every point
// where ClassifyTimeOfDay can change its answer, classifies each piece and
// appends the wanted ones to windows, merging with the previous window when they
// touch.
func appendDayWindows(
	windows []TimeWindow, wanted map[string]struct{}, dayStart, dayEnd time.Time, events *SunEventTimes,
) []TimeWindow {
	loc := dayStart.Location()
	sunrise := clockOffset(events.Sunrise)
	sunset := clockOffset(events.Sunset)

	// The sunrise and sunset windows are closed at both ends at one-second
	// resolution, so they leave at centre + window + 1s. The daytime arc starts at
	// sunrise and ends at sunset.
	offsets := []time.Duration{
		sunrise - SunEventWindow, sunrise, sunrise + SunEventWindow + time.Second,
		sunset - SunEventWindow, sunset, sunset + SunEventWindow + time.Second,
	}
	cuts := make([]time.Time, 0, len(offsets)+2)
	cuts = append(cuts, dayStart, dayEnd)
	for _, off := range offsets {
		off = ((off % clockPeriod) + clockPeriod) % clockPeriod
		cut := time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), 0, 0, int(off/time.Second), 0, loc)
		if cut.After(dayStart) && cut.Before(dayEnd) {
			cuts = append(cuts, cut)
		}
	}
	slices.SortFunc(cuts, func(a, b time.Time) int { return a.Compare(b) })
	cuts = slices.CompactFunc(cuts, func(a, b time.Time) bool { return a.Equal(b) })

	for i := 0; i+1 < len(cuts); i++ {
		if _, ok := wanted[ClassifyTimeOfDay(cuts[i], events)]; !ok {
			continue
		}
		w := TimeWindow{Start: cuts[i].Unix(), End: cuts[i+1].Unix()}
		if n := len(windows); n > 0 && windows[n-1].End == w.Start {
			windows[n-1].End = w.End
			continue
		}
		windows = append(windows, w)
	}
	return windows
}

// clipWindows trims windows to [from, to) and drops those left empty. windows
// must already be in ascending order, which TimeOfDayWindows produces.
func clipWindows(windows []TimeWindow, from, to int64) []TimeWindow {
	clipped := windows[:0]
	for _, w := range windows {
		w.Start = max(w.Start, from)
		w.End = min(w.End, to)
		if w.Start < w.End {
			clipped = append(clipped, w)
		}
	}
	return slices.Clip(clipped)
}
