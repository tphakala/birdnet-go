// internal/api/v2/events.go
package api

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/logger/reader"
)

// detectionOperations lists the log operations consumed by the detection events endpoint.
var detectionOperations = []string{
	"create_pending_detection",
	"approve_detection",
	"discard_detection",
	"flush_detection",
	"dog_bark_filter",
	"privacy_filter",
	"audio_export_success",
}

// noiseOperations lists operations that should be excluded from system events.
var noiseOperations = []string{
	"create_pending_detection",
	"approve_detection",
	"discard_detection",
	"flush_detection",
	"dog_bark_filter",
	"privacy_filter",
	"audio_export_success",
	"process_detections_summary",
}

// noiseMessages contains message substrings that should be filtered from system events.
var noiseMessages = []string{
	"Disk usage below threshold",
}

// validLogLevels enumerates the accepted log level filter values.
var validLogLevels = map[string]bool{
	"DEBUG": true,
	"INFO":  true,
	"WARN":  true,
	"ERROR": true,
}

// --- Response types ---

// DetectionEventsResponse is the top-level response for GET /events/detections.
type DetectionEventsResponse struct {
	Buckets []DetectionBucket         `json:"buckets"`
	Metrics DetectionMetrics          `json:"metrics"`
	Species []DetectionSpeciesSummary `json:"species"`
}

// DetectionBucket represents one hour-wide time bucket of detection events.
type DetectionBucket struct {
	Key          string          `json:"key"`
	Label        string          `json:"label"`
	Timestamp    time.Time       `json:"timestamp"`
	Species      []SpeciesEntry  `json:"species"`
	SpeciesCount int             `json:"species_count"`
	Totals       BucketTotals    `json:"totals"`
	ApproveRatio float64         `json:"approve_ratio"`
	PreFilters   PreFilterCounts `json:"pre_filters"`
}

// SpeciesEntry holds per-species counts and metadata within a single bucket.
type SpeciesEntry struct {
	Name              string   `json:"name"`
	Approved          int      `json:"approved"`
	Discarded         int      `json:"discarded"`
	Flushed           int      `json:"flushed"`
	PeakConfidence    float64  `json:"peak_confidence"`
	MaxMatchCount     int      `json:"max_match_count"`
	DiscardReasons    []string `json:"discard_reasons"`
	DiscardTimestamps []string `json:"discard_timestamps"`
	ApproveTimestamps []string `json:"approve_timestamps"`
	ClipPaths         []string `json:"clip_paths"`
}

// BucketTotals aggregates event counts across all species in a bucket.
type BucketTotals struct {
	Pending   int `json:"pending"`
	Approved  int `json:"approved"`
	Discarded int `json:"discarded"`
	Flushed   int `json:"flushed"`
}

// PreFilterCounts tracks pre-analysis filter hits within a bucket.
type PreFilterCounts struct {
	DogBark int `json:"dog_bark"`
	Privacy int `json:"privacy"`
}

// DetectionMetrics provides day-level aggregate metrics for detection events.
type DetectionMetrics struct {
	PendingTotal    int            `json:"pending_total"`
	ApprovedTotal   int            `json:"approved_total"`
	DiscardedTotal  int            `json:"discarded_total"`
	FlushedTotal    int            `json:"flushed_total"`
	DogBarkTotal    int            `json:"dog_bark_total"`
	PrivacyTotal    int            `json:"privacy_total"`
	TopDiscarded    []SpeciesCount `json:"top_discarded"`
	HourlyPending   [24]int        `json:"hourly_pending"`
	ApprovedPerHour string         `json:"approved_per_hour"`
}

// SpeciesCount is a name+count pair used in top-N lists.
type SpeciesCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// DetectionSpeciesSummary provides day-level totals for a single species.
type DetectionSpeciesSummary struct {
	Name      string `json:"name"`
	Total     int    `json:"total"`
	Approved  int    `json:"approved"`
	Discarded int    `json:"discarded"`
}

// SystemEventsResponse is the top-level response for GET /system/events/operational.
type SystemEventsResponse struct {
	Events  []SystemEvent `json:"events"`
	Metrics SystemMetrics `json:"metrics"`
}

// SystemEvent represents a single system log entry in the API response.
type SystemEvent struct {
	ID        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`
	Source    string         `json:"source"`
	Operation string         `json:"operation,omitempty"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// SystemMetrics provides aggregate counts for system events.
type SystemMetrics struct {
	Total   int            `json:"total"`
	ByLevel map[string]int `json:"by_level"`
}

// --- Route registration ---

// registerEventsRoutes registers the /events sub-routes on the given parent group.
// Called from initSystemRoutes so events are under /system/events/* with auth middleware.
func (c *Controller) registerEventsRoutes(parent *echo.Group) {
	eventsGroup := parent.Group("/events")
	eventsGroup.GET("/detections", c.GetDetectionEvents)
	eventsGroup.GET("/operational", c.GetOperationalEvents)
}

// --- Handlers ---

// GetDetectionEvents returns detection lifecycle events aggregated into hourly buckets
// for the requested date (defaults to today).
func (c *Controller) GetDetectionEvents(ctx echo.Context) error {
	date := ctx.QueryParam("date")
	if date == "" {
		date = time.Now().Format(time.DateOnly)
	}
	if err := c.validateDateFormatWithResponse(ctx, date, "date", "GetDetectionEvents"); err != nil {
		return err
	}

	targetDate, _ := time.ParseInLocation(time.DateOnly, date, time.Local)

	// Get actions.log path from logger config
	actionsLogPath := logger.Global().GetOutputPath("analysis.processor")
	if actionsLogPath == "" {
		// Return empty response if no log path configured
		return ctx.JSON(http.StatusOK, DetectionEventsResponse{
			Buckets: []DetectionBucket{},
			Metrics: DetectionMetrics{
				TopDiscarded:    []SpeciesCount{},
				ApprovedPerHour: "0.0",
			},
			Species: []DetectionSpeciesSummary{},
		})
	}

	// Find all log files that could contain this date
	logFiles, err := reader.FindLogFiles(actionsLogPath)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to read log files", http.StatusInternalServerError)
	}

	// Read and filter entries
	entries, err := reader.ReadFiles(logFiles, &reader.ReadOptions{
		Date:       targetDate,
		Location:   time.Local,
		Operations: detectionOperations,
	})
	if err != nil {
		return c.HandleError(ctx, err, "Failed to parse log entries", http.StatusInternalServerError)
	}

	// Aggregate into response
	result := c.aggregateDetectionEvents(entries, targetDate)
	return ctx.JSON(http.StatusOK, result)
}

// GetOperationalEvents returns operational log events for the requested date and minimum level.
// Defaults to today and INFO level.
func (c *Controller) GetOperationalEvents(ctx echo.Context) error {
	date := ctx.QueryParam("date")
	if date == "" {
		date = time.Now().Format(time.DateOnly)
	}
	if err := c.validateDateFormatWithResponse(ctx, date, "date", "GetOperationalEvents"); err != nil {
		return err
	}

	level := ctx.QueryParam("level")
	if level == "" {
		level = reader.LevelInfo
	}
	level = strings.ToUpper(level)

	if !validLogLevels[level] {
		return c.HandleError(ctx, nil, "Invalid level. Use DEBUG, INFO, WARN, or ERROR", http.StatusBadRequest)
	}

	targetDate, _ := time.ParseInLocation(time.DateOnly, date, time.Local)

	// Collect entries from both application.log and audio.log
	var allEntries []reader.LogEntry

	// Read application.log
	appLogPath := logger.Global().GetDefaultOutputPath()
	if appLogPath != "" {
		if entries, err := readLogSource(appLogPath, targetDate, level, time.Local); err != nil {
			c.logWarnIfEnabled("Failed to read application log",
				logger.Error(err),
				logger.String("path", appLogPath),
				logger.String("date", date),
			)
		} else {
			allEntries = append(allEntries, entries...)
		}
	}

	// Read audio.log
	audioLogPath := logger.Global().GetOutputPath("audio")
	if audioLogPath != "" {
		if entries, err := readLogSource(audioLogPath, targetDate, level, time.Local); err != nil {
			c.logWarnIfEnabled("Failed to read audio log",
				logger.Error(err),
				logger.String("path", audioLogPath),
				logger.String("date", date),
			)
		} else {
			allEntries = append(allEntries, entries...)
		}
	}

	// Sort all entries by timestamp (descending — newest first)
	slices.SortFunc(allEntries, func(a, b reader.LogEntry) int {
		return b.Time.Compare(a.Time) // reverse order
	})

	// Filter out noise and build response
	events, metrics := buildSystemEvents(allEntries)

	return ctx.JSON(http.StatusOK, SystemEventsResponse{
		Events:  events,
		Metrics: metrics,
	})
}

// readLogSource finds and reads log entries from a base log path, filtering by date and level.
func readLogSource(basePath string, date time.Time, level string, loc *time.Location) ([]reader.LogEntry, error) {
	logFiles, err := reader.FindLogFiles(basePath)
	if err != nil {
		return nil, fmt.Errorf("finding log files for %s: %w", basePath, err)
	}

	entries, err := reader.ReadFiles(logFiles, &reader.ReadOptions{
		Date:     date,
		Location: loc,
		Level:    level,
	})
	if err != nil {
		return nil, fmt.Errorf("reading log files for %s: %w", basePath, err)
	}

	return entries, nil
}

// buildSystemEvents converts raw log entries into SystemEvent responses,
// filtering out noisy operational entries and computing level metrics.
func buildSystemEvents(entries []reader.LogEntry) ([]SystemEvent, SystemMetrics) {
	events := make([]SystemEvent, 0, len(entries))
	metrics := SystemMetrics{
		ByLevel: make(map[string]int),
	}

	for i := range entries {
		entry := &entries[i]

		if isNoiseEntry(entry) {
			continue
		}

		events = append(events, SystemEvent{
			ID:        generateEventID(entry),
			Timestamp: entry.Time,
			Level:     entry.Level,
			Source:    entry.Module,
			Operation: entry.Operation,
			Message:   entry.Msg,
			Fields:    entry.Fields,
		})

		metrics.Total++
		metrics.ByLevel[entry.Level]++
	}

	return events, metrics
}

// isNoiseEntry returns true if the log entry should be filtered from system events output.
func isNoiseEntry(entry *reader.LogEntry) bool {
	// Exclude detection lifecycle operations (served by /events/detections)
	if slices.Contains(noiseOperations, entry.Operation) {
		return true
	}

	// Exclude event bus performance / metrics noise
	if entry.Module == "events" {
		if strings.Contains(entry.Operation, "performance") || strings.Contains(entry.Operation, "metrics") {
			return true
		}
	}

	// Exclude noisy messages
	for _, pattern := range noiseMessages {
		if strings.Contains(entry.Msg, pattern) {
			return true
		}
	}

	return false
}

// generateEventID produces a stable, deterministic ID for a log entry
// based on its timestamp and message content. Uses FNV-1a for speed
// since this is not a security-sensitive hash.
func generateEventID(entry *reader.LogEntry) string {
	h := fnv.New64a()
	_ = binary.Write(h, binary.LittleEndian, entry.Time.UnixNano())
	h.Write([]byte(entry.Msg))
	h.Write([]byte(entry.Operation))
	if len(entry.Fields) > 0 {
		if fieldBytes, err := json.Marshal(entry.Fields); err == nil {
			h.Write(fieldBytes)
		}
	}
	return fmt.Sprintf("evt_%x", h.Sum64())
}
