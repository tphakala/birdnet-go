package checks

import (
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// sanitizeID converts a model ID into a safe check-name suffix by
// lowercasing and replacing non-alphanumeric characters with underscores.
func sanitizeID(id string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r - 'A' + 'a'
		}
		return '_'
	}, id)
}

// worstResult returns the result with the most severe status, or a skipped
// result if the slice is empty. Used by MultiResultCheck.Run() implementations.
func worstResult(name string, cat health.Category, results []health.Result) health.Result {
	if len(results) == 0 {
		return skippedResult(name, cat, time.Now())
	}
	worst := results[0]
	for _, r := range results[1:] {
		if health.Severity(r.Status) > health.Severity(worst.Status) {
			worst = r
		}
	}
	worst.Name = name
	worst.Category = cat
	return worst
}

// skippedResult builds a StatusSkipped Result for checks without available data.
func skippedResult(name string, category health.Category, start time.Time) health.Result {
	return health.Result{
		Name:       name,
		Category:   category,
		Status:     health.StatusSkipped,
		Message:    "Metrics not yet available",
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// defaultSustainedHours is the number of active hours required before events
// are considered a sustained pattern rather than transient spikes.
const defaultSustainedHours = 3

// minWindowForRecurrence is the minimum evaluation window for recurrence and
// velocity analysis to be meaningful. Hourly bucket resolution is too coarse
// for shorter windows.
const minWindowForRecurrence = 3 * time.Hour

// Pattern classification labels for the details map.
const (
	patternNone      = "none"
	patternTransient = "transient"
	patternSustained = "sustained"
)

// windowedStatsConfig parameterises evalWindowedStats for counter-based checks.
type windowedStatsConfig struct {
	baseWarnThreshold int64
	baseCritThreshold int64
	metricPrefix      string
	window            time.Duration
	// sustainedHours is the minimum number of active hourly buckets for the
	// pattern to be classified as "sustained". Zero means use defaultSustainedHours.
	sustainedHours int
}

// sparklineBuckets is the number of hourly buckets included in the sparkline response.
const sparklineBuckets = 24

// recentEventsLimit is the number of recent events included in the response.
const recentEventsLimit = 20

// evalWindowedStats evaluates counter-based metrics using time-windowed data from
// the HealthMetricsStore. It sums events within the configured window, builds
// sparkline data, and formats messages with temporal context.
func evalWindowedStats(
	name string,
	category health.Category,
	store *observability.HealthMetricsStore,
	getEvents func(metric string, n int) []observability.HealthEvent,
	cfg *windowedStatsConfig,
	start time.Time,
) health.Result {
	if store == nil {
		return skippedResult(name, category, start)
	}

	now := time.Now()
	keys := store.KeysWithPrefix(cfg.metricPrefix)

	var windowTotal int64
	var lifetimeTotal int64
	var lastEvent time.Time
	perSource := make(map[string]int64, len(keys))
	activeSources := 0

	for _, key := range keys {
		sourceID := key[len(cfg.metricPrefix):]

		activeSources++
		wTotal := store.SumAt(key, cfg.window, now)
		perSource[sourceID] = wTotal
		windowTotal += wTotal
		lifetimeTotal += store.LifetimeTotal(key)

		if et := store.LastEventTime(key); et.After(lastEvent) {
			lastEvent = et
		}
	}

	if len(keys) == 0 {
		return skippedResult(name, category, start)
	}

	msg := formatWindowedMessage(name, windowTotal, lifetimeTotal, activeSources, lastEvent, cfg.window, now)

	metricType := extractMetricType(cfg.metricPrefix)

	sparkline := buildMergedSparkline(store, keys, sparklineBuckets, now)

	var recentEvents []observability.HealthEvent
	if getEvents != nil {
		recentEvents = getEvents(metricType, recentEventsLimit)
	}

	// Four-signal evaluation: scaled thresholds, peak spike, sustained pattern, velocity.
	sustainedHrs := cfg.sustainedHours
	if sustainedHrs <= 0 {
		sustainedHrs = defaultSustainedHours
	}

	windowHours := max(int64(cfg.window/time.Hour), 1)
	warnThreshold := cfg.baseWarnThreshold * windowHours
	critThreshold := cfg.baseCritThreshold * windowHours

	// For windows larger than the sparkline display (24h), build a wider
	// bucket set so signals cover the full evaluation window.
	signalBuckets := sparkline
	if windowBuckets := int(windowHours); windowBuckets > sparklineBuckets {
		signalBuckets = buildMergedSparkline(store, keys, windowBuckets, now)
	}

	recurrenceEnabled := cfg.window >= minWindowForRecurrence
	activeHours := countActiveHours(signalBuckets, cfg.window, now)
	maxHourly := maxBucketCount(signalBuckets, cfg.window, now)

	var velocity velocityTrend
	velocityDetail := "n/a"
	if recurrenceEnabled {
		velocity = detectVelocity(signalBuckets, cfg.window, now)
		velocityDetail = velocityString(velocity)
	}

	status, msg := applySeveritySignals(&severitySignals{
		name:              name,
		windowTotal:       windowTotal,
		warnThreshold:     warnThreshold,
		critThreshold:     critThreshold,
		baseWarnThreshold: cfg.baseWarnThreshold,
		baseCritThreshold: cfg.baseCritThreshold,
		maxHourly:         maxHourly,
		activeHours:       activeHours,
		sustainedHrs:      sustainedHrs,
		sources:           activeSources,
		lastEventSuffix:   formatLastEventSuffix(lastEvent, now),
		recurrenceEnabled: recurrenceEnabled,
		velocity:          velocity,
		window:            cfg.window,
	}, msg)

	// Determine pattern classification (aligned with the sustained-signal
	// conditions: recurrence must be enabled and volume must exceed the floor).
	sustainedVolumeFloor := warnThreshold / 2
	var pattern string
	switch {
	case windowTotal == 0:
		pattern = patternNone
	case recurrenceEnabled && activeHours >= sustainedHrs && windowTotal >= sustainedVolumeFloor:
		pattern = patternSustained
	default:
		pattern = patternTransient
	}

	details := map[string]any{
		"window":         formatDuration(cfg.window),
		"window_total":   windowTotal,
		"lifetime_total": lifetimeTotal,
		"sources":        activeSources,
		"per_source":     perSource,
		"active_hours":   activeHours,
		"velocity":       velocityDetail,
		"pattern":        pattern,
	}
	if !lastEvent.IsZero() {
		details["last_event"] = lastEvent.Format(time.RFC3339)
	}
	if len(sparkline) > 0 {
		details["sparkline"] = sparkline
	}
	if len(recentEvents) > 0 {
		details["recent_events"] = recentEvents
	}

	return health.Result{
		Name:       name,
		Category:   category,
		Status:     status,
		Message:    msg,
		Details:    details,
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// severitySignals holds the pre-computed inputs for the four-signal severity evaluation.
type severitySignals struct {
	name              string
	windowTotal       int64
	warnThreshold     int64
	critThreshold     int64
	baseWarnThreshold int64
	baseCritThreshold int64
	maxHourly         int64
	activeHours       int
	sustainedHrs      int
	sources           int
	lastEventSuffix   string
	recurrenceEnabled bool
	velocity          velocityTrend
	window            time.Duration
}

// applySeveritySignals runs the four-signal evaluation (peak spike, sustained
// recurrence, absolute volume, velocity) and returns the resulting status and
// message. The defaultMsg is returned unchanged when no signal fires.
func applySeveritySignals(s *severitySignals, defaultMsg string) (status health.Status, msg string) {
	if s.windowTotal == 0 {
		return health.StatusHealthy, defaultMsg
	}

	label := checkNameLabel(s.name)
	windowStr := formatDuration(s.window)
	ctx := fmt.Sprintf(" across %d source(s)%s", s.sources, s.lastEventSuffix)
	status = health.StatusHealthy
	msg = defaultMsg
	peakEscalated := false

	// Safety net: severe hourly spike overrides window-scaled thresholds.
	// Only enabled for windows >= 1h; sub-hour windows share a single bucket
	// with events outside the window, causing false positives.
	if s.window >= time.Hour {
		if s.maxHourly >= s.baseCritThreshold {
			return health.StatusCritical, fmt.Sprintf("%d %s in %s, peak hour: %d %s%s",
				s.windowTotal, label, windowStr, s.maxHourly, label, ctx)
		}

		if s.maxHourly >= s.baseWarnThreshold {
			peakEscalated = true
		}
	}

	// Volume floor prevents classifying negligible noise as sustained
	// (e.g. 1 drop/hr for 3 hrs = 3 total, below floor of 5 with baseWarn=10).
	sustainedVolumeFloor := s.warnThreshold / 2

	// Sustained recurrence pattern.
	if s.recurrenceEnabled && s.activeHours >= s.sustainedHrs && s.windowTotal >= sustainedVolumeFloor {
		if s.windowTotal >= s.critThreshold || s.velocity == velocityIncreasing {
			return health.StatusCritical, fmt.Sprintf("Sustained %s across %d hours, worsening%s", label, s.activeHours, ctx)
		}
		return health.StatusWarning, fmt.Sprintf("Sustained %s across %d hours%s", label, s.activeHours, ctx)
	}

	// Absolute threshold checks.
	if s.windowTotal >= s.critThreshold {
		return health.StatusCritical, fmt.Sprintf("%d %s in %s, concentrated in %d hour(s)%s",
			s.windowTotal, label, windowStr, max(s.activeHours, 1), ctx)
	}

	if s.windowTotal >= s.warnThreshold || peakEscalated {
		if s.recurrenceEnabled && s.velocity == velocityIncreasing {
			return health.StatusWarning, fmt.Sprintf("%d %s in %s, rate increasing%s", s.windowTotal, label, windowStr, ctx)
		}
		return health.StatusWarning, fmt.Sprintf("%d %s in %s%s", s.windowTotal, label, windowStr, ctx)
	}

	// Low volume within tolerance.
	if s.activeHours > 0 {
		msg = fmt.Sprintf("%d %s in %s, within tolerance%s", s.windowTotal, label, windowStr, ctx)
	}

	return status, msg
}

// formatWindowedMessage builds a human-readable message with temporal context.
func formatWindowedMessage(
	name string,
	windowTotal, lifetimeTotal int64,
	sources int,
	lastEvent time.Time,
	window time.Duration,
	now time.Time,
) string {
	windowStr := formatDuration(window)
	label := checkNameLabel(name)

	if lifetimeTotal == 0 {
		return fmt.Sprintf("No %s across %d source(s)", label, sources)
	}

	if windowTotal == 0 {
		timeAgo := formatTimeAgo(lastEvent, now)
		return fmt.Sprintf("No %s in last %s (%d lifetime, last: %s)", label, windowStr, lifetimeTotal, timeAgo)
	}

	timeAgo := formatTimeAgo(lastEvent, now)
	return fmt.Sprintf("%d %s in last %s across %d source(s) (last: %s)", windowTotal, label, windowStr, sources, timeAgo)
}

// checkNameLabel converts a check name to a human-readable label.
func checkNameLabel(name string) string {
	switch name {
	case "buffer_drops":
		return "drops"
	case "buffer_overrun":
		return "overruns"
	case "results_queue_drops":
		return "detections dropped"
	case "stream_error_rate":
		return "restarts"
	default:
		return "events"
	}
}

// formatDuration formats a duration as a compact string (e.g. "1h", "30m", "7d").
func formatDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		days := int(d / (24 * time.Hour))
		return fmt.Sprintf("%dd", days)
	case d >= time.Hour:
		hours := int(d / time.Hour)
		return fmt.Sprintf("%dh", hours)
	default:
		minutes := int(d / time.Minute)
		return fmt.Sprintf("%dm", minutes)
	}
}

// formatTimeAgo formats a time as a relative duration string.
func formatTimeAgo(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	}
}

// formatLastEventSuffix returns " (last: Xh ago)" or empty string when no event recorded.
func formatLastEventSuffix(lastEvent, now time.Time) string {
	if lastEvent.IsZero() {
		return ""
	}
	return fmt.Sprintf(" (last: %s)", formatTimeAgo(lastEvent, now))
}

// extractMetricType extracts the metric type from a prefix like "audio.drops." -> "drops".
func extractMetricType(prefix string) string {
	trimmed := strings.TrimSuffix(prefix, ".")
	if idx := strings.LastIndex(trimmed, "."); idx >= 0 {
		return trimmed[idx+1:]
	}
	return trimmed
}

// velocityTrend describes whether error activity is growing, shrinking, or flat
// over the most recent consecutive in-window sparkline buckets.
type velocityTrend int

const (
	// velocityStable means the two most recent in-window buckets are equal,
	// or fewer than two in-window buckets exist.
	velocityStable velocityTrend = iota
	// velocityIncreasing means the current bucket count exceeds the previous one.
	velocityIncreasing
	// velocityDecreasing means the current bucket count is below the previous one.
	velocityDecreasing
)

// countActiveHours returns the number of sparkline buckets within the given
// window that recorded at least one event. A bucket is considered in-window
// if its hour-long interval ends after cutoff (now - window).
func countActiveHours(buckets []observability.HourlyBucket, window time.Duration, now time.Time) int {
	cutoff := now.Add(-window)
	count := 0
	for _, b := range buckets {
		if !b.Start.Add(time.Hour).Before(cutoff) && b.Count > 0 {
			count++
		}
	}
	return count
}

// detectVelocity examines the two most recent consecutive in-window sparkline
// buckets and returns whether activity is increasing, decreasing, or stable.
// Zero-count buckets are NOT skipped so the comparison reflects the true
// trajectory (e.g. a drop to zero is decreasing, not stable).
// Returns velocityStable when fewer than two in-window buckets are found.
func detectVelocity(buckets []observability.HourlyBucket, window time.Duration, now time.Time) velocityTrend {
	cutoff := now.Add(-window)
	currentHourStart := now.Truncate(time.Hour)

	var prev, curr observability.HourlyBucket
	n := 0
	for _, b := range buckets {
		if !b.Start.Before(currentHourStart) {
			continue
		}
		if !b.Start.Add(time.Hour).Before(cutoff) {
			prev, curr = curr, b
			n++
		}
	}

	if n < 2 {
		return velocityStable
	}

	switch {
	case curr.Count > prev.Count:
		return velocityIncreasing
	case curr.Count < prev.Count:
		return velocityDecreasing
	default:
		return velocityStable
	}
}

// maxBucketCount returns the highest Count value across all in-window sparkline
// buckets. Used as a peak hourly rate safety net for severity decisions.
// Returns 0 when no in-window buckets are present.
func maxBucketCount(buckets []observability.HourlyBucket, window time.Duration, now time.Time) int64 {
	cutoff := now.Add(-window)
	var peak int64
	for _, b := range buckets {
		if !b.Start.Add(time.Hour).Before(cutoff) && b.Count > peak {
			peak = b.Count
		}
	}
	return peak
}

// velocityString returns a human-readable label for a velocityTrend value,
// suitable for inclusion in a health result details map.
func velocityString(v velocityTrend) string {
	switch v {
	case velocityIncreasing:
		return "increasing"
	case velocityDecreasing:
		return "decreasing"
	default:
		return "stable"
	}
}

// buildMergedSparkline merges multiple metric keys into a single sparkline
// of n hourly buckets, with zeros for hours without data.
func buildMergedSparkline(store *observability.HealthMetricsStore, keys []string, n int, now time.Time) []observability.HourlyBucket {
	startHour := now.Truncate(time.Hour).Add(-time.Duration(n-1) * time.Hour)

	merged := make([]observability.HourlyBucket, n)
	for i := range n {
		merged[i] = observability.HourlyBucket{
			Start: startHour.Add(time.Duration(i) * time.Hour),
		}
	}

	for _, key := range keys {
		buckets := store.Buckets(key, n)
		for _, b := range buckets {
			idx := int(b.Start.Sub(startHour) / time.Hour)
			if idx >= 0 && idx < n {
				merged[idx].Count += b.Count
			}
		}
	}

	return merged
}
