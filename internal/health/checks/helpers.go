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

// windowedStatsConfig parameterises evalWindowedStats for counter-based checks.
type windowedStatsConfig struct {
	warnThreshold int64
	critThreshold int64
	metricPrefix  string
	window        time.Duration
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

	status := health.StatusHealthy
	switch {
	case windowTotal >= cfg.critThreshold:
		status = health.StatusCritical
	case windowTotal >= cfg.warnThreshold:
		status = health.StatusWarning
	}

	msg := formatWindowedMessage(name, windowTotal, lifetimeTotal, activeSources, lastEvent, cfg.window, now)

	metricType := extractMetricType(cfg.metricPrefix)

	sparkline := buildMergedSparkline(store, keys, sparklineBuckets, now)

	var recentEvents []observability.HealthEvent
	if getEvents != nil {
		recentEvents = getEvents(metricType, recentEventsLimit)
	}

	details := map[string]any{
		"window":         formatDuration(cfg.window),
		"window_total":   windowTotal,
		"lifetime_total": lifetimeTotal,
		"sources":        activeSources,
		"per_source":     perSource,
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

	// Collect in-window buckets in forward order, then examine the last two.
	var inWindow []observability.HourlyBucket
	for _, b := range buckets {
		if !b.Start.Add(time.Hour).Before(cutoff) {
			inWindow = append(inWindow, b)
		}
	}

	if len(inWindow) < 2 {
		return velocityStable
	}

	current := inWindow[len(inWindow)-1]
	previous := inWindow[len(inWindow)-2]

	switch {
	case current.Count > previous.Count:
		return velocityIncreasing
	case current.Count < previous.Count:
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
