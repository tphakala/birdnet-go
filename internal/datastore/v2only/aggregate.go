package v2only

import (
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Slot resolution constants for the seasonal density heatmap. The intra-day slot width is
// downsampled as the requested range widens so the payload (and the rendered grid) stays
// bounded: a year at 15-minute resolution would be 365*96 cells, most of them noise.
const (
	heatmapMinutesPerDay = 24 * 60 // minutes in a day

	heatmapSlotFine   = 15 // <= heatmapMediumDays
	heatmapSlotMedium = 30 // (heatmapMediumDays, heatmapCoarseDays]
	heatmapSlotCoarse = 60 // > heatmapCoarseDays

	heatmapMediumDays = 90  // ~3 months: switch from 15- to 30-minute slots beyond this
	heatmapCoarseDays = 180 // ~6 months: switch from 30- to 60-minute slots beyond this
)

// heatmapSlotResolution returns the intra-day slot width in minutes for a range spanning
// numDays calendar days. Wider ranges use coarser slots to bound payload and render cost.
func heatmapSlotResolution(numDays int) int {
	switch {
	case numDays > heatmapCoarseDays:
		return heatmapSlotCoarse
	case numDays > heatmapMediumDays:
		return heatmapSlotMedium
	default:
		return heatmapSlotFine
	}
}

// cellKey identifies a heatmap cell by (date index, intra-day slot).
type cellKey struct {
	dateIndex int
	slot      int
}

// dateKey identifies a calendar day. Keying the date index by this struct (rather than a
// formatted string) lets the per-timestamp lookup avoid a string allocation on every row.
type dateKey struct {
	year  int
	month time.Month
	day   int
}

// buildActivityHeatmap buckets raw detection timestamps (Unix epoch seconds) into a columnar,
// sparse (date, slot) grid for the seasonal density heatmap.
//
// startDate/endDate are inclusive YYYY-MM-DD bounds interpreted in loc; the returned Dates
// slice lists every calendar date in [startDate, endDate]. Each timestamp is placed by its
// wall-clock date and minute-of-day in loc, so bucketing follows the station timezone and is
// correct across DST transitions (unlike a single-offset SQL expression). The slot width is
// downsampled for wide ranges (see heatmapSlotResolution). Timestamps whose local date falls
// outside the range are ignored. Only non-zero cells are emitted, ordered by (dateIndex, slot).
func buildActivityHeatmap(timestamps []int64, loc *time.Location, startDate, endDate string) (datastore.ActivityHeatmapData, error) {
	if loc == nil {
		loc = time.UTC
	}

	start, err := time.ParseInLocation(time.DateOnly, startDate, loc)
	if err != nil {
		return datastore.ActivityHeatmapData{}, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "build_activity_heatmap").
			Context("start_date", startDate).
			Build()
	}
	end, err := time.ParseInLocation(time.DateOnly, endDate, loc)
	if err != nil {
		return datastore.ActivityHeatmapData{}, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "build_activity_heatmap").
			Context("end_date", endDate).
			Build()
	}

	// Enumerate every calendar date in the inclusive range and index it for O(1) lookup. The
	// index is keyed by a (year, month, day) struct so the per-timestamp lookup below allocates
	// nothing; the human-readable date string is formatted just once per day here.
	dates := make([]string, 0)
	dateIndex := make(map[dateKey]int)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		y, m, day := d.Date()
		dateIndex[dateKey{year: y, month: m, day: day}] = len(dates)
		dates = append(dates, d.Format(time.DateOnly))
	}

	resolution := heatmapSlotResolution(len(dates))
	slotsPerDay := heatmapMinutesPerDay / resolution
	lastSlot := slotsPerDay - 1

	// Accumulate counts per (dateIndex, slot); cells are sparse so a map avoids allocating
	// the full dense grid for ranges where most cells are empty.
	counts := make(map[cellKey]int)
	for _, ts := range timestamps {
		lt := time.Unix(ts, 0).In(loc)
		y, m, day := lt.Date()
		idx, ok := dateIndex[dateKey{year: y, month: m, day: day}]
		if !ok {
			continue // detection falls outside the requested range in loc
		}
		// min guards against any boundary rounding pushing the slot past the last one.
		slot := min((lt.Hour()*60+lt.Minute())/resolution, lastSlot)
		counts[cellKey{dateIndex: idx, slot: slot}]++
	}

	// Emit cells ordered by (dateIndex, slot) for a deterministic, render-friendly payload.
	keys := slices.Collect(maps.Keys(counts))
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dateIndex != keys[j].dateIndex {
			return keys[i].dateIndex < keys[j].dateIndex
		}
		return keys[i].slot < keys[j].slot
	})

	result := datastore.ActivityHeatmapData{
		Dates:                 dates,
		SlotResolutionMinutes: resolution,
		CellDateIndex:         make([]int, 0, len(keys)),
		CellSlot:              make([]int, 0, len(keys)),
		CellCount:             make([]int, 0, len(keys)),
	}
	for _, k := range keys {
		result.CellDateIndex = append(result.CellDateIndex, k.dateIndex)
		result.CellSlot = append(result.CellSlot, k.slot)
		result.CellCount = append(result.CellCount, counts[k])
	}
	return result, nil
}
