package checks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/observability"
)

const (
	latencyCriticalRatio = 0.90
	latencyWarningRatio  = 0.50
)

// ModelLoadInfo describes a loaded model for health reporting.
type ModelLoadInfo struct {
	ID       string
	Name     string
	Loaded   bool
	Backend  string
	SpecInfo string // e.g. "48kHz, 3s clips"
}

// ModelsLoadedCheck verifies that all analysis models are loaded and ready.
// Implements MultiResultCheck to produce one result per model.
type ModelsLoadedCheck struct {
	getModels func() []ModelLoadInfo
}

// NewModelsLoadedCheck creates a ModelsLoadedCheck using the given model provider.
func NewModelsLoadedCheck(getModels func() []ModelLoadInfo) *ModelsLoadedCheck {
	return &ModelsLoadedCheck{getModels: getModels}
}

// Name returns the check identifier.
func (c *ModelsLoadedCheck) Name() string { return "models_loaded" }

// Category returns the analysis category.
func (c *ModelsLoadedCheck) Category() health.Category { return health.CategoryAnalysis }

// Run returns a single aggregate result (worst status across all models).
func (c *ModelsLoadedCheck) Run(ctx context.Context) health.Result {
	start := time.Now()
	r := worstResult(c.Name(), c.Category(), c.RunMulti(ctx))
	r.DurationMS = float64(time.Since(start).Microseconds()) / 1000
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now()
	}
	return r
}

// RunMulti returns one result per loaded model.
func (c *ModelsLoadedCheck) RunMulti(_ context.Context) []health.Result {
	start := time.Now()

	if c.getModels == nil {
		return []health.Result{skippedResult(c.Name(), c.Category(), start)}
	}

	models := c.getModels()
	if len(models) == 0 {
		return []health.Result{{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusCritical,
			Message:    "No analysis models loaded",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}}
	}

	results := make([]health.Result, 0, len(models))
	for _, m := range models {
		checkName := "model_loaded_" + sanitizeID(m.ID)
		status := health.StatusHealthy
		msg := fmt.Sprintf("%s loaded (%s)", m.Name, m.SpecInfo)

		if !m.Loaded {
			status = health.StatusCritical
			msg = fmt.Sprintf("%s not loaded", m.Name)
		}

		results = append(results, health.Result{
			Name:     checkName,
			Category: c.Category(),
			Status:   status,
			Message:  msg,
			Details: map[string]any{
				"model_id":   m.ID,
				"model_name": m.Name,
				"backend":    m.Backend,
				"spec":       m.SpecInfo,
			},
			Timestamp: time.Now(),
		})
	}
	return results
}

// ModelInferenceInfo describes per-model inference statistics for health reporting.
type ModelInferenceInfo struct {
	ModelID   string
	ModelName string
	AvgMS     float64
	P99MS     float64
	WindowMS  float64 // model-specific analysis window (BufferInterval in ms)
}

// PerModelInferenceLatencyCheck verifies that inference latency for each model
// is within acceptable bounds relative to that model's analysis window.
// Implements MultiResultCheck to produce one result per model.
type PerModelInferenceLatencyCheck struct {
	getStats func() []ModelInferenceInfo
}

// NewPerModelInferenceLatencyCheck creates a check that evaluates each model's
// inference latency against its own analysis window.
func NewPerModelInferenceLatencyCheck(getStats func() []ModelInferenceInfo) *PerModelInferenceLatencyCheck {
	return &PerModelInferenceLatencyCheck{getStats: getStats}
}

// Name returns the check identifier.
func (c *PerModelInferenceLatencyCheck) Name() string { return "inference_latency" }

// Category returns the analysis category.
func (c *PerModelInferenceLatencyCheck) Category() health.Category { return health.CategoryAnalysis }

// Run returns a single aggregate result (worst status across all models).
func (c *PerModelInferenceLatencyCheck) Run(ctx context.Context) health.Result {
	start := time.Now()
	r := worstResult(c.Name(), c.Category(), c.RunMulti(ctx))
	r.DurationMS = float64(time.Since(start).Microseconds()) / 1000
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now()
	}
	return r
}

// RunMulti evaluates each model's inference latency independently.
func (c *PerModelInferenceLatencyCheck) RunMulti(_ context.Context) []health.Result {
	start := time.Now()

	if c.getStats == nil {
		return []health.Result{skippedResult(c.Name(), c.Category(), start)}
	}

	stats := c.getStats()
	if len(stats) == 0 {
		return []health.Result{{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusUnknown,
			Message:    "Inference stats not available",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}}
	}

	results := make([]health.Result, 0, len(stats))
	for _, s := range stats {
		checkName := "inference_latency_" + sanitizeID(s.ModelID)

		if s.WindowMS <= 0 {
			results = append(results, health.Result{
				Name:     checkName,
				Category: c.Category(),
				Status:   health.StatusUnknown,
				Message:  fmt.Sprintf("%s: inference stats not available", s.ModelName),
				Details: map[string]any{
					"model_id":   s.ModelID,
					"model_name": s.ModelName,
				},
				Timestamp: time.Now(),
			})
			continue
		}

		ratio := s.P99MS / s.WindowMS
		status := health.StatusHealthy
		msg := fmt.Sprintf("%s latency OK (p99=%.1fms, window=%.1fms)", s.ModelName, s.P99MS, s.WindowMS)

		switch {
		case ratio >= latencyCriticalRatio:
			status = health.StatusCritical
			msg = fmt.Sprintf("%s p99 (%.1fms) exceeds %.0f%% of analysis window (%.1fms)",
				s.ModelName, s.P99MS, latencyCriticalRatio*100, s.WindowMS)
		case ratio >= latencyWarningRatio:
			status = health.StatusWarning
			msg = fmt.Sprintf("%s p99 (%.1fms) exceeds %.0f%% of analysis window (%.1fms)",
				s.ModelName, s.P99MS, latencyWarningRatio*100, s.WindowMS)
		}

		results = append(results, health.Result{
			Name:     checkName,
			Category: c.Category(),
			Status:   status,
			Message:  msg,
			Details: map[string]any{
				"model_id":   s.ModelID,
				"model_name": s.ModelName,
				"avg_ms":     s.AvgMS,
				"p99_ms":     s.P99MS,
				"window_ms":  s.WindowMS,
			},
			Timestamp: time.Now(),
		})
	}
	return results
}

// DetectionRateCheck monitors whether detections are occurring at expected intervals.
type DetectionRateCheck struct {
	getRecentCount func(ctx context.Context, hours int) (int, error)
}

// NewDetectionRateCheck creates a DetectionRateCheck using the given count provider.
func NewDetectionRateCheck(getRecentCount func(ctx context.Context, hours int) (int, error)) *DetectionRateCheck {
	return &DetectionRateCheck{getRecentCount: getRecentCount}
}

// Name returns the check identifier.
func (c *DetectionRateCheck) Name() string { return "detection_rate" }

// Category returns the analysis category.
func (c *DetectionRateCheck) Category() health.Category { return health.CategoryAnalysis }

// Run checks recent detection counts for signs of stalled analysis.
func (c *DetectionRateCheck) Run(ctx context.Context) health.Result {
	start := time.Now()

	if c.getRecentCount == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	count6h, err6h := c.getRecentCount(ctx, 6)
	count24h, err24h := c.getRecentCount(ctx, 24)

	if err6h != nil || err24h != nil {
		var errParts []string
		if err6h != nil {
			errParts = append(errParts, fmt.Sprintf("6h: %v", err6h))
		}
		if err24h != nil {
			errParts = append(errParts, fmt.Sprintf("24h: %v", err24h))
		}
		msg := fmt.Sprintf("Detection count query failed: %s", strings.Join(errParts, "; "))
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusUnknown,
			Message:    msg,
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	status := health.StatusHealthy
	msg := fmt.Sprintf("Detection rate OK (%d in 6h, %d in 24h)", count6h, count24h)

	hour := time.Now().Hour()
	isDaytime := hour >= 6 && hour < 22

	switch {
	case count24h == 0:
		status = health.StatusCritical
		msg = "No detections recorded in the past 24 hours"
	case count6h == 0 && isDaytime:
		status = health.StatusWarning
		msg = "No detections recorded in the past 6 hours during daytime"
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"count_6h":  count6h,
			"count_24h": count24h,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// QueueDepthCheck monitors the analysis queue to detect backpressure.
type QueueDepthCheck struct {
	getQueueStats func() (int, int)
}

// NewQueueDepthCheck creates a QueueDepthCheck using the given queue stats provider.
// The returned function must return (currentDepth, maxDepth).
func NewQueueDepthCheck(getQueueStats func() (int, int)) *QueueDepthCheck {
	return &QueueDepthCheck{getQueueStats: getQueueStats}
}

// Name returns the check identifier.
func (c *QueueDepthCheck) Name() string { return "queue_depth" }

// Category returns the analysis category.
func (c *QueueDepthCheck) Category() health.Category { return health.CategoryAnalysis }

// Run evaluates queue utilization.
func (c *QueueDepthCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getQueueStats == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	depth, capacity := c.getQueueStats()

	if capacity <= 0 {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusUnknown,
			Message:    "Queue stats not available",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	percent := float64(depth) / float64(capacity) * 100

	status := health.StatusHealthy
	msg := fmt.Sprintf("Queue OK (%d/%d, %.0f%%)", depth, capacity, percent)

	switch {
	case percent >= 80:
		status = health.StatusCritical
		msg = fmt.Sprintf("Analysis queue nearly full (%d/%d, %.0f%%)", depth, capacity, percent)
	case percent >= 50:
		status = health.StatusWarning
		msg = fmt.Sprintf("Analysis queue filling up (%d/%d, %.0f%%)", depth, capacity, percent)
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"current": depth,
			"max":     capacity,
			"percent": percent,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// ORTAvailabilityCheck verifies that the ONNX Runtime library is available
// and version-compatible for models that require it (Perch, geomodel, bat).
type ORTAvailabilityCheck struct {
	getStatus func() (available, initialized bool, version, libraryPath, errMsg string)
}

// NewORTAvailabilityCheck creates an ORTAvailabilityCheck using the given status provider.
func NewORTAvailabilityCheck(getStatus func() (available, initialized bool, version, libraryPath, errMsg string)) *ORTAvailabilityCheck {
	return &ORTAvailabilityCheck{getStatus: getStatus}
}

// Name returns the check identifier.
func (c *ORTAvailabilityCheck) Name() string { return "ort_availability" }

// Category returns the analysis category.
func (c *ORTAvailabilityCheck) Category() health.Category { return health.CategoryAnalysis }

// Run checks ONNX Runtime availability and version compatibility.
func (c *ORTAvailabilityCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getStatus == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	available, initialized, version, libPath, errMsg := c.getStatus()

	details := map[string]any{
		"initialized":  initialized,
		"version":      version,
		"library_path": libPath,
	}

	if available {
		msg := fmt.Sprintf("ONNX Runtime %s available", version)
		if !initialized {
			msg = "ONNX Runtime library found (not yet initialized)"
		}
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusHealthy,
			Message:    msg,
			Details:    details,
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	return health.Result{
		Name:       c.Name(),
		Category:   c.Category(),
		Status:     health.StatusWarning,
		Message:    errMsg,
		Details:    details,
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// OpenVINOAvailabilityCheck reports whether the optional OpenVINO acceleration
// backend is compiled in and active. Unlike ORT (required for several models),
// OpenVINO is optional, so absence is never an error: the check is skipped on
// builds without the backend, and otherwise reports active vs. fell-back-to-ORT.
type OpenVINOAvailabilityCheck struct {
	getStatus func() (supported, active bool)
}

// NewOpenVINOAvailabilityCheck creates an OpenVINOAvailabilityCheck using the
// given status provider. active reports whether a classifier is actually running
// on OpenVINO (not merely that the core was loaded for device probing).
func NewOpenVINOAvailabilityCheck(getStatus func() (supported, active bool)) *OpenVINOAvailabilityCheck {
	return &OpenVINOAvailabilityCheck{getStatus: getStatus}
}

// Name returns the check identifier.
func (c *OpenVINOAvailabilityCheck) Name() string { return "openvino_availability" }

// Category returns the analysis category.
func (c *OpenVINOAvailabilityCheck) Category() health.Category { return health.CategoryAnalysis }

// Run reports OpenVINO backend state. It is skipped on builds without the
// backend (the common case), and healthy otherwise: OpenVINO is an optional
// accelerator, so falling back to ORT is normal, not a fault.
func (c *OpenVINOAvailabilityCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getStatus == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	supported, active := c.getStatus()
	if !supported {
		// Not an OpenVINO build: report Skipped rather than a misleading
		// always-"inactive" line. Skipped checks remain in the diagnostics payload
		// (like every other optional check) but do not count toward the aggregate
		// health status, so default builds are not penalized for lacking OpenVINO.
		return skippedResult(c.Name(), c.Category(), start)
	}

	msg := "OpenVINO compiled in, not in use (ONNX Runtime active)"
	if active {
		msg = "OpenVINO backend active"
	}
	return health.Result{
		Name:       c.Name(),
		Category:   c.Category(),
		Status:     health.StatusHealthy,
		Message:    msg,
		Details:    map[string]any{"supported": supported, "active": active},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// Threshold constants for the results-queue detection-drop check. Each dropped
// detection is a species that was heard but never recorded, so even a single
// drop within the window is worth a warning; a high or sustained rate (the
// detection consumer falling persistently behind) is critical.
const (
	resultsQueueDropBaseWarnThreshold = 1
	resultsQueueDropBaseCritThreshold = 50
)

// ResultsQueueDropCheck monitors detections dropped because the classifier
// results queue was full, using time-windowed evaluation over the health
// metrics store. The analysis pipeline records each drop into the same store
// this check reads (analysis -> observability store -> health check), so no
// import cycle is introduced and windowed counts, sparkline, and recent events
// come for free, exactly like the audio buffer-drops path.
type ResultsQueueDropCheck struct {
	store     *observability.HealthMetricsStore
	getEvents func(metric string, n int) []observability.HealthEvent
	window    time.Duration
}

// NewResultsQueueDropCheck creates a ResultsQueueDropCheck using the health
// metrics store and event getter.
func NewResultsQueueDropCheck(store *observability.HealthMetricsStore, getEvents func(metric string, n int) []observability.HealthEvent) *ResultsQueueDropCheck {
	return &ResultsQueueDropCheck{
		store:     store,
		getEvents: getEvents,
		window:    DefaultWindow,
	}
}

// Name returns the check identifier.
func (c *ResultsQueueDropCheck) Name() string { return "results_queue_drops" }

// Category returns the analysis category.
func (c *ResultsQueueDropCheck) Category() health.Category { return health.CategoryAnalysis }

// WithWindow returns a copy of this check configured with the given evaluation
// window. Returns the receiver unchanged when d equals the current window to
// avoid an allocation.
func (c *ResultsQueueDropCheck) WithWindow(d time.Duration) health.Check {
	if d == c.window {
		return c
	}
	cp := *c
	cp.window = d
	return &cp
}

// Run evaluates results-queue detection-drop statistics within the configured
// time window.
func (c *ResultsQueueDropCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	return evalWindowedStats(c.Name(), c.Category(), c.store, c.getEvents, &windowedStatsConfig{
		baseWarnThreshold: resultsQueueDropBaseWarnThreshold,
		baseCritThreshold: resultsQueueDropBaseCritThreshold,
		sustainedHours:    defaultSustainedHours,
		metricPrefix:      observability.MetricPrefixResultsQueueDrops,
		window:            c.window,
	}, start)
}
