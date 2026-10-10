package checks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/health"
)

// ModelInferenceFailureInfo is the per-model failure state the
// inference_failures check reports on. It is populated from the classifier's
// per-model inference health, so this package does not depend on the classifier.
type ModelInferenceFailureInfo struct {
	// ModelID is the registry ID.
	ModelID string
	// ModelName is the display name.
	ModelName string
	// ConsecutiveFailures is the model's current run of failed analysis windows.
	ConsecutiveFailures int64
	// Failing is the classifier's verdict: the run reached its failure threshold.
	Failing bool
	// ErrorClass is the class of the latest failure, "" when no failure run is in
	// progress (ConsecutiveFailures == 0).
	ErrorClass string
}

// InferenceFailuresCheck reports Critical while any loaded model fails every
// analysis window, Healthy while every loaded model is analyzing, and Skipped
// when no model information is available (no orchestrator, or no loaded
// model). It is streak based (the classifier's consecutive-failure count,
// cleared by the first success) rather than a windowed error rate, so it has no
// reset-on-read counters or window boundaries to flap on, and it clears as soon
// as the model recovers.
type InferenceFailuresCheck struct {
	getInfo func() []ModelInferenceFailureInfo
}

// NewInferenceFailuresCheck creates an InferenceFailuresCheck using the given
// provider. A nil provider makes the check report Skipped.
func NewInferenceFailuresCheck(getInfo func() []ModelInferenceFailureInfo) *InferenceFailuresCheck {
	return &InferenceFailuresCheck{getInfo: getInfo}
}

// Name returns the check identifier.
func (c *InferenceFailuresCheck) Name() string { return "inference_failures" }

// Category returns the analysis category.
func (c *InferenceFailuresCheck) Category() health.Category { return health.CategoryAnalysis }

// Run reports whether any loaded model is failing.
func (c *InferenceFailuresCheck) Run(_ context.Context) health.Result {
	start := time.Now()
	if c.getInfo == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}
	infos := c.getInfo()
	if len(infos) == 0 {
		r := skippedResult(c.Name(), c.Category(), start)
		r.Message = "No acoustic model loaded"
		return r
	}

	var failing []string
	models := make([]map[string]any, 0, len(infos))
	for _, info := range infos {
		if info.Failing {
			failing = append(failing, info.ModelName)
		}
		models = append(models, map[string]any{
			"model_id":             info.ModelID,
			"model_name":           info.ModelName,
			"consecutive_failures": info.ConsecutiveFailures,
			"failing":              info.Failing,
			"error_class":          info.ErrorClass,
		})
	}

	status := health.StatusHealthy
	message := fmt.Sprintf("%d loaded model(s) analyzing normally", len(infos))
	if len(failing) > 0 {
		status = health.StatusCritical
		message = fmt.Sprintf("Every analysis fails for: %s (see the AI Models page)", strings.Join(failing, ", "))
	}
	return health.Result{
		Name:       c.Name(),
		Category:   c.Category(),
		Status:     status,
		Message:    message,
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
		Details: map[string]any{
			"failing_count": len(failing),
			"models":        models,
		},
	}
}
