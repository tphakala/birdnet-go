package v2only

import (
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// hoursPerDay is the number of hour-of-day buckets in a species distribution (0..23).
const hoursPerDay = 24

// buildSpeciesHourlyDistribution turns per-label hourly counts into per-species normalized
// hour-of-day distributions for the who-sings-when ridgeline.
//
// top is the top-N species by volume from GetTopSpecies, in descending-volume order; each row is
// one label ID. hourlyByLabel maps a label ID to its [24]int false-positive-excluded hourly counts.
// Label IDs that resolve to the same scientific name (one per model) are merged into a single ridge
// whose buckets are the summed counts, preserving the first-seen volume order. Each species' 24
// buckets are then normalized to sum to 1.0 so timing shape is comparable across species regardless
// of raw volume; Total carries the unnormalized count for the tooltip.
//
// A species whose merged FP-excluded total is zero is dropped: GetTopSpecies ranks by raw volume
// without excluding false positives, so an all-false-positive species can rank into the top-N yet
// contribute no real detections; rendering it as an empty "0 detections" ridge would be misleading.
// The result is always non-nil.
func buildSpeciesHourlyDistribution(top []repository.SpeciesCount, hourlyByLabel map[uint][24]int) []datastore.SpeciesHourlyDistribution {
	// Merge label rows that share a scientific name, preserving first-seen (descending-volume)
	// order. Each distinct species accumulates the hourly counts of all its label IDs.
	order := make([]string, 0, len(top))
	countsByName := make(map[string]*[hoursPerDay]int, len(top))
	for i := range top {
		name := top[i].ScientificName
		acc, ok := countsByName[name]
		if !ok {
			acc = &[hoursPerDay]int{}
			countsByName[name] = acc
			order = append(order, name)
		}
		hours := hourlyByLabel[top[i].LabelID] // zero [24]int when the label has no detections
		for h := range hoursPerDay {
			acc[h] += hours[h]
		}
	}

	result := make([]datastore.SpeciesHourlyDistribution, 0, len(order))
	for _, name := range order {
		acc := countsByName[name]
		total := 0
		for h := range hoursPerDay {
			total += acc[h]
		}
		if total == 0 {
			continue // ranked by raw volume but no FP-excluded detections; skip the empty ridge
		}
		dist := datastore.SpeciesHourlyDistribution{ScientificName: name, Total: total}
		for h := range hoursPerDay {
			dist.Buckets[h] = float64(acc[h]) / float64(total)
		}
		result = append(result, dist)
	}
	return result
}

// buildAcousticSuccession turns per-label hourly counts into per-species raw hour-of-day counts for
// the acoustic succession streamgraph.
//
// top is the top-N species by volume from GetTopSpecies, in descending-volume order; each row is one
// label ID. hourlyByLabel maps a label ID to its [24]int false-positive-excluded hourly counts.
// Label IDs that resolve to the same scientific name (one per model) are merged into a single series
// whose buckets are the summed counts, preserving the first-seen volume order. Unlike
// buildSpeciesHourlyDistribution the counts are NOT normalized: the streamgraph stacks raw volume,
// so band width is detection count; Total carries the per-species sum for the tooltip.
//
// A species whose merged FP-excluded total is zero is dropped: GetTopSpecies ranks by raw volume
// without excluding false positives, so an all-false-positive species can rank into the top-N yet
// contribute no real detections; stacking an empty band would add a flat, meaningless layer. The
// result is always non-nil.
func buildAcousticSuccession(top []repository.SpeciesCount, hourlyByLabel map[uint][24]int) []datastore.SpeciesHourlyCounts {
	// Merge label rows that share a scientific name, preserving first-seen (descending-volume)
	// order. Each distinct species accumulates the hourly counts of all its label IDs.
	order := make([]string, 0, len(top))
	countsByName := make(map[string]*[hoursPerDay]int, len(top))
	for i := range top {
		name := top[i].ScientificName
		acc, ok := countsByName[name]
		if !ok {
			acc = &[hoursPerDay]int{}
			countsByName[name] = acc
			order = append(order, name)
		}
		hours := hourlyByLabel[top[i].LabelID] // zero [24]int when the label has no detections
		for h := range hoursPerDay {
			acc[h] += hours[h]
		}
	}

	result := make([]datastore.SpeciesHourlyCounts, 0, len(order))
	for _, name := range order {
		acc := countsByName[name]
		total := 0
		for h := range hoursPerDay {
			total += acc[h]
		}
		if total == 0 {
			continue // ranked by raw volume but no FP-excluded detections; skip the empty band
		}
		result = append(result, datastore.SpeciesHourlyCounts{ScientificName: name, Counts: *acc, Total: total})
	}
	return result
}

// minConfidenceHistogramDetections is the per-species floor for the confidence distribution (design
// spec section 6.5). Below this, a ~20-bin histogram averages under one detection per bin and reads
// as noise rather than a distribution, so the species is dropped from the top-N set. A single
// explicitly selected species bypasses this (the caller passes a floor of 1) so a requested species
// is never silently empty.
const minConfidenceHistogramDetections = 20

// buildSpeciesConfidenceHistogram bins each species' detection confidences into `bins` equal-width
// bins over [0,1], then normalizes each species so its bins sum to ~1.0 (the distribution shape is
// comparable across species regardless of detection volume). Label rows sharing a scientific name
// (multi-model) merge into one species, preserving the input (descending-volume) order. Species with
// fewer than minCount detections are dropped as noisy. Total is the species' detection count (false
// positives already excluded upstream), surfaced in the tooltip. Returns a non-nil empty slice when
// no species qualifies, or when bins is non-positive.
func buildSpeciesConfidenceHistogram(species []repository.SpeciesCount, confByLabel map[uint][]float64, bins, minCount int) []datastore.SpeciesConfidenceHistogram {
	if bins <= 0 {
		return []datastore.SpeciesConfidenceHistogram{}
	}

	// Merge label rows sharing a scientific name, preserving first-seen (descending-volume) order.
	// Each distinct species accumulates the confidences of all its label IDs.
	order := make([]string, 0, len(species))
	confByName := make(map[string][]float64, len(species))
	for i := range species {
		name := species[i].ScientificName
		if _, ok := confByName[name]; !ok {
			order = append(order, name)
		}
		confByName[name] = append(confByName[name], confByLabel[species[i].LabelID]...)
	}

	result := make([]datastore.SpeciesConfidenceHistogram, 0, len(order))
	for _, name := range order {
		confs := confByName[name]
		total := len(confs)
		// Drop low-volume species (and guard the division below). minCount is always >= 1 from the
		// caller, so total == 0 is covered too.
		if total < minCount || total == 0 {
			continue
		}
		counts := make([]int, bins)
		for _, conf := range confs {
			counts[confidenceBinIndex(conf, bins)]++
		}
		dist := datastore.SpeciesConfidenceHistogram{
			ScientificName: name,
			Bins:           make([]float64, bins),
			Total:          total,
		}
		for b, count := range counts {
			dist.Bins[b] = float64(count) / float64(total)
		}
		result = append(result, dist)
	}
	return result
}

// confidenceBinIndex maps a confidence score to its bin index in [0, bins-1] for `bins` equal-width
// bins over [0,1]. Scores are assumed to be in [0,1]; values are clamped so a confidence of exactly
// 1.0 lands in the last bin and any out-of-range value is pinned to the nearest edge bin rather than
// indexing out of bounds. Callers guarantee bins > 0.
func confidenceBinIndex(conf float64, bins int) int {
	idx := int(conf * float64(bins))
	if idx < 0 {
		return 0
	}
	if idx >= bins {
		return bins - 1
	}
	return idx
}

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

// Dawn-chorus onset constants (design spec section 6.3).
//
// The onset for a day is the minute-of-day of the onsetDetectionRank-th earliest detection. An
// absolute rank is used deliberately instead of a daily percentile: a percentile of the day's total
// volume couples the morning onset to unrelated later-in-day activity (a busy afternoon pushes a low
// percentile index later and fakes a later dawn), and at realistic daily counts a low percentile
// collapses to the very first detection, giving no false-positive robustness. The Nth-earliest
// detection is immune to later-in-day volume (adding detections after it never moves it) and rejects
// up to (rank-1) lone pre-dawn false positives, which is the robustness the spec actually wanted.
const (
	onsetDetectionRank = 3 // onset = the 3rd earliest detection of the day (rejects up to 2 stray pre-dawn false positives)
	minOnsetDetections = 5 // days with fewer detections are too sparse to read a meaningful onset (must be >= onsetDetectionRank)
)

// civilDawnMinuteLookup returns civil dawn's station-local minute-of-day (0..1439) for the given
// calendar date and whether civil dawn is defined for it. ok is false when civil dawn cannot be
// determined (polar day / white nights / polar night, or no sun calculator configured). It is
// injected into buildDailyActivityOnset so the polar-null and min-count paths are unit-testable
// without a real SunCalc.
type civilDawnMinuteLookup func(date time.Time) (minuteOfDay int, ok bool)

// buildDailyActivityOnset buckets false-positive-excluded detection timestamps (Unix epoch seconds)
// by station-local calendar date and computes, for each date in the inclusive [startDate, endDate]
// range, the dawn-chorus onset relative to civil dawn.
//
// The onset for a day is the minute-of-day of the rank-th earliest detection (see the
// onsetDetectionRank rationale). OnsetRelMinutes is that onset minus civil dawn's minute-of-day
// (negative = before civil dawn). It is left nil when the day has fewer than minDetections
// detections (too sparse) or when dawn reports civil dawn undefined for the date (polar day /
// night). Every date in the range is emitted with its DetectionCount (0 on quiet days) so the
// client has a continuous date axis and its trend line breaks over gaps rather than interpolating
// across them. startDate/endDate are inclusive YYYY-MM-DD bounds interpreted in loc (nil -> UTC);
// civil dawn is resolved in the same loc frame as the bucketed detections so the subtraction is a
// true duration. The result is always non-nil.
func buildDailyActivityOnset(timestamps []int64, loc *time.Location, startDate, endDate string, rank, minDetections int, dawn civilDawnMinuteLookup) ([]datastore.DailyActivityOnset, error) {
	if loc == nil {
		loc = time.UTC
	}

	start, err := time.ParseInLocation(time.DateOnly, startDate, loc)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "build_daily_activity_onset").
			Context("start_date", startDate).
			Build()
	}
	end, err := time.ParseInLocation(time.DateOnly, endDate, loc)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "build_daily_activity_onset").
			Context("end_date", endDate).
			Build()
	}

	// Group each detection's station-local minute-of-day by its local calendar date. Detections
	// whose local date falls outside the enumerated range simply never get looked up.
	minutesByDate := make(map[dateKey][]int)
	for _, ts := range timestamps {
		lt := time.Unix(ts, 0).In(loc)
		y, m, day := lt.Date()
		k := dateKey{year: y, month: m, day: day}
		minutesByDate[k] = append(minutesByDate[k], lt.Hour()*60+lt.Minute())
	}

	result := make([]datastore.DailyActivityOnset, 0)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		y, m, day := d.Date()
		mins := minutesByDate[dateKey{year: y, month: m, day: day}]
		item := datastore.DailyActivityOnset{Date: d.Format(time.DateOnly), DetectionCount: len(mins)}

		if rank >= 1 && len(mins) >= minDetections && len(mins) >= rank {
			slices.Sort(mins)
			onsetMinute := mins[rank-1]
			if dawnMinute, ok := dawn(d); ok {
				rel := onsetMinute - dawnMinute
				item.OnsetRelMinutes = &rel
			}
		}
		result = append(result, item)
	}
	return result, nil
}

// buildSpeciesAccumulation turns per-species in-period first-seen timestamps into the cumulative
// species accumulation curve (the biodiversity collector's curve).
//
// firstSeen carries each species' first detection (Unix epoch seconds) within the queried window,
// false positives already excluded upstream. Each timestamp is mapped to its station-local calendar
// date in loc; the count of species whose first-seen lands on a date is that day's NewSpecies, and
// CumulativeSpecies is the running total. One point is emitted for every calendar day in the inclusive
// [startDate, endDate] range so the client gets a continuous date axis whose flat tail reads as the
// curve's asymptote. First-seen dates outside the enumerated range are ignored (the SQL already bounds
// them, but a row skewed by loc onto an out-of-range day must never inflate the curve or index past
// the axis). startDate/endDate are inclusive YYYY-MM-DD bounds; the per-detection bucketing is done
// in loc (nil -> UTC), while the date axis is enumerated in UTC (see below). The result is always
// non-nil.
func buildSpeciesAccumulation(firstSeen []repository.SpeciesFirstSeen, loc *time.Location, startDate, endDate string) ([]datastore.SpeciesAccumulationPoint, error) {
	if loc == nil {
		loc = time.UTC
	}

	// Enumerate the date axis in UTC, not loc. We only need the sequence of calendar dates from
	// startDate to endDate, and UTC has no DST, so AddDate(0,0,1) steps exactly one calendar day every
	// iteration. Parsing the bounds in a loc whose DST transition skips midnight (clocks jump
	// 23:59:59 -> 01:00:00, e.g. America/Havana) would normalize the skipped 00:00 forward to 01:00,
	// drift the loop's wall-clock hour, and drop the final day at the !d.After(end) comparison. The
	// per-detection bucketing below stays in loc, which is the only place the station timezone matters.
	start, err := time.Parse(time.DateOnly, startDate)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "build_species_accumulation").
			Context("start_date", startDate).
			Build()
	}
	end, err := time.Parse(time.DateOnly, endDate)
	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "build_species_accumulation").
			Context("end_date", endDate).
			Build()
	}

	// Tally how many species are first seen on each station-local calendar date. Dates outside the
	// enumerated range are never looked up below, so they are ignored without an explicit filter. The
	// (year, month, day) key is timezone-frame-independent, so the loc-bucketed keys here line up with
	// the UTC-enumerated lookups below for the same calendar date.
	newByDate := make(map[dateKey]int, len(firstSeen))
	for i := range firstSeen {
		lt := time.Unix(firstSeen[i].FirstDetected, 0).In(loc)
		y, m, day := lt.Date()
		newByDate[dateKey{year: y, month: m, day: day}]++
	}

	result := make([]datastore.SpeciesAccumulationPoint, 0)
	cumulative := 0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		y, m, day := d.Date()
		n := newByDate[dateKey{year: y, month: m, day: day}]
		cumulative += n
		result = append(result, datastore.SpeciesAccumulationPoint{
			Date:              d.Format(time.DateOnly),
			CumulativeSpecies: cumulative,
			NewSpecies:        n,
		})
	}
	return result, nil
}

// buildSpeciesPhenology turns the per-species residency rows (Unix MIN/MAX detection timestamps plus
// the detection count) into the wire shape for the arrival/departure phenology chart: first and last
// detection formatted as station-local YYYY-MM-DD dates. Timestamps are projected into loc (nil ->
// UTC) with a single time.Unix(...).In(loc).Format call per row, so there is no date-range
// enumeration loop and therefore no DST midnight-skip pitfall.
//
// The input rows are top-N by volume (the query's ORDER BY count DESC); this re-sorts the returned
// rows by arrival (FirstSeen asc, then LastSeen asc, then ScientificName asc) so the Gantt reads
// top-to-bottom in arrival order, deterministically. The result is always non-nil.
func buildSpeciesPhenology(rows []repository.SpeciesPhenology, loc *time.Location) []datastore.SpeciesPhenologyPoint {
	if loc == nil {
		loc = time.UTC
	}

	result := make([]datastore.SpeciesPhenologyPoint, 0, len(rows))
	for i := range rows {
		first := time.Unix(rows[i].FirstDetected, 0).In(loc).Format(time.DateOnly)
		last := time.Unix(rows[i].LastDetected, 0).In(loc).Format(time.DateOnly)
		result = append(result, datastore.SpeciesPhenologyPoint{
			ScientificName: rows[i].ScientificName,
			FirstSeen:      first,
			LastSeen:       last,
			Count:          rows[i].Count,
		})
	}

	sort.SliceStable(result, func(a, b int) bool {
		if result[a].FirstSeen != result[b].FirstSeen {
			return result[a].FirstSeen < result[b].FirstSeen
		}
		if result[a].LastSeen != result[b].LastSeen {
			return result[a].LastSeen < result[b].LastSeen
		}
		return result[a].ScientificName < result[b].ScientificName
	})

	return result
}
