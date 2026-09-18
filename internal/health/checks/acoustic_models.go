package checks

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/health"
)

// AcousticModelsInfo is the snapshot the acoustic_models check reports on. It is
// populated from the classifier so the health package does not depend on classifier
// internals (model de-privilege epic, Phase 4).
type AcousticModelsInfo struct {
	// State is the classifier's AcousticModelsState: "ok", "none_installed" or
	// "load_failed". Empty when no orchestrator is wired.
	State string
	// LoadedCount is the number of acoustic models currently loaded.
	LoadedCount int
	// EnabledCount is the number of models.enabled entries that resolve to a known model.
	EnabledCount int
	// LoadFailures is the number of enabled models with a load error still on record.
	LoadFailures int
}

// Acoustic-model state values. These mirror the classifier's AcousticModelsState
// contract (internal/classifier defines "ok"/"none_installed"/"load_failed"); the health
// package must not import the classifier, so the wire values are restated here. A drift
// between the two surfaces as the default "Unknown acoustic model state" verdict in Run.
const (
	acousticStateOK            = "ok"
	acousticStateNoneInstalled = "none_installed"
	acousticStateLoadFailed    = "load_failed"
)

// AcousticModelsCheck reports whether any acoustic model is loaded. N = 0 (no model
// installed) is a supported state and reports Warning, not Critical; an enabled model
// that failed to load is a fault and reports Critical. The per-model models_loaded check
// stands down to Skipped at N = 0, so this check owns the single no-model verdict.
type AcousticModelsCheck struct {
	getInfo func() AcousticModelsInfo
}

// NewAcousticModelsCheck creates an AcousticModelsCheck using the given info provider.
func NewAcousticModelsCheck(getInfo func() AcousticModelsInfo) *AcousticModelsCheck {
	return &AcousticModelsCheck{getInfo: getInfo}
}

// Name returns the check identifier.
func (c *AcousticModelsCheck) Name() string { return "acoustic_models" }

// Category returns the analysis category.
func (c *AcousticModelsCheck) Category() health.Category { return health.CategoryAnalysis }

// Run reports the aggregate acoustic-model state.
func (c *AcousticModelsCheck) Run(_ context.Context) health.Result {
	start := time.Now()

	if c.getInfo == nil {
		return skippedResult(c.Name(), c.Category(), start)
	}

	info := c.getInfo()

	status := health.StatusUnknown
	message := fmt.Sprintf("Unknown acoustic model state %q", info.State)
	switch info.State {
	case acousticStateOK:
		status = health.StatusHealthy
		message = fmt.Sprintf("%d acoustic model(s) loaded", info.LoadedCount)
	case acousticStateNoneInstalled:
		// Not a fault: the process is healthy, audio is captured, but nothing is
		// analyzed. This state also covers "installed but not enabled" (models.enabled
		// empty), so the message avoids implying nothing is installed on disk.
		status = health.StatusWarning
		message = "No acoustic model enabled or loaded"
	case acousticStateLoadFailed:
		status = health.StatusCritical
		message = fmt.Sprintf("No acoustic model loaded: %d enabled model(s) failed to load (see the AI Models page)", info.LoadFailures)
	case "":
		message = "Acoustic model state unavailable"
	}

	return health.Result{
		Name:       c.Name(),
		Category:   c.Category(),
		Status:     status,
		Message:    message,
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
		Details: map[string]any{
			"state":         info.State,
			"loaded":        info.LoadedCount,
			"enabled":       info.EnabledCount,
			"load_failures": info.LoadFailures,
		},
	}
}
