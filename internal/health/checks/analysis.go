package checks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/health"
)

// ModelLoadedCheck verifies that the BirdNET analysis model is loaded and ready.
type ModelLoadedCheck struct {
	isLoaded  func() bool
	modelName func() string
}

// NewModelLoadedCheck creates a ModelLoadedCheck using the given predicate and name provider.
func NewModelLoadedCheck(isLoaded func() bool, modelName func() string) *ModelLoadedCheck {
	return &ModelLoadedCheck{isLoaded: isLoaded, modelName: modelName}
}

// Name returns the check identifier.
func (c *ModelLoadedCheck) Name() string { return "model_loaded" }

// Category returns the analysis category.
func (c *ModelLoadedCheck) Category() health.Category { return health.CategoryAnalysis }

// Run verifies that the analysis model is loaded.
func (c *ModelLoadedCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.isLoaded == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	name := ""
	if c.modelName != nil {
		name = c.modelName()
	}

	status := health.StatusHealthy
	msg := fmt.Sprintf("Model loaded: %s", name)
	if name == "" {
		msg = "Model loaded"
	}

	if !c.isLoaded() {
		status = health.StatusCritical
		msg = "Analysis model is not loaded"
		if name != "" {
			msg = fmt.Sprintf("Analysis model not loaded: %s", name)
		}
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"model_name": name,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}

// InferenceLatencyCheck verifies that inference latency is within acceptable bounds
// relative to the analysis window size.
type InferenceLatencyCheck struct {
	getStats func() (avgMS, p99MS, windowMS float64)
}

// NewInferenceLatencyCheck creates an InferenceLatencyCheck using the given stats provider.
func NewInferenceLatencyCheck(getStats func() (avgMS, p99MS, windowMS float64)) *InferenceLatencyCheck {
	return &InferenceLatencyCheck{getStats: getStats}
}

// Name returns the check identifier.
func (c *InferenceLatencyCheck) Name() string { return "inference_latency" }

// Category returns the analysis category.
func (c *InferenceLatencyCheck) Category() health.Category { return health.CategoryAnalysis }

// Run evaluates inference latency against the analysis window duration.
func (c *InferenceLatencyCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getStats == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	avgMS, p99MS, windowMS := c.getStats()

	if windowMS <= 0 {
		return health.Result{
			Name:       c.Name(),
			Category:   c.Category(),
			Status:     health.StatusUnknown,
			Message:    "Inference stats not available",
			DurationMS: float64(time.Since(start).Microseconds()) / 1000,
			Timestamp:  time.Now(),
		}
	}

	ratio := p99MS / windowMS
	status := health.StatusHealthy
	msg := fmt.Sprintf("Inference latency OK (p99=%.1fms, window=%.1fms)", p99MS, windowMS)

	switch {
	case ratio >= 0.90:
		status = health.StatusCritical
		msg = fmt.Sprintf("Inference p99 (%.1fms) exceeds 90%% of analysis window (%.1fms)", p99MS, windowMS)
	case ratio >= 0.50:
		status = health.StatusWarning
		msg = fmt.Sprintf("Inference p99 (%.1fms) exceeds 50%% of analysis window (%.1fms)", p99MS, windowMS)
	}

	return health.Result{
		Name:     c.Name(),
		Category: c.Category(),
		Status:   status,
		Message:  msg,
		Details: map[string]any{
			"avg_ms":    avgMS,
			"p99_ms":    p99MS,
			"window_ms": windowMS,
		},
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
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
