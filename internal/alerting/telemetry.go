package alerting

import (
	"fmt"
	"sync/atomic"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

const (
	// telemetryComponent is the Sentry component tag for all alerting telemetry.
	telemetryComponent = "alerting"

	// dropReportThreshold is the number of dropped events before reporting to Sentry.
	// This prevents flooding Sentry when the bus is persistently overloaded.
	dropReportThreshold int64 = 10
)

// AlertingTelemetry reports alerting engine operational health to Sentry.
// All methods are nil-safe -- if the receiver is nil, calls are no-ops.
// No alert content (rule names, species, event properties) is ever sent.
// Consent is handled by the telemetry package (checks settings.Sentry.Enabled).
type AlertingTelemetry struct {
	// droppedEvents counts events dropped due to full buffer since last report.
	droppedEvents atomic.Int64
}

// NewAlertingTelemetry creates a new alerting telemetry reporter.
func NewAlertingTelemetry() *AlertingTelemetry {
	return &AlertingTelemetry{}
}

// ReportInitialized reports that the alerting engine started successfully.
func (at *AlertingTelemetry) ReportInitialized(rulesLoaded int) {
	if at == nil {
		return
	}

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", telemetryComponent)
		scope.SetTag("outcome", "initialized")
		scope.SetFingerprint([]string{telemetryComponent, "initialized"})

		scope.SetContext(telemetryComponent, map[string]any{
			"rules_loaded": rulesLoaded,
		})

		telemetry.CaptureMessage(
			fmt.Sprintf("Alerting engine initialized (%d rules)", rulesLoaded),
			sentry.LevelInfo,
			telemetryComponent,
		)
	})
}

// ReportInitFailed reports that the alerting engine failed to initialize.
func (at *AlertingTelemetry) ReportInitFailed(errMsg string) {
	if at == nil {
		return
	}

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", telemetryComponent)
		scope.SetTag("outcome", "init_failed")
		scope.SetFingerprint([]string{telemetryComponent, "init-failed"})

		scope.SetContext(telemetryComponent, map[string]any{
			"error": privacy.ScrubMessage(errMsg),
		})

		telemetry.CaptureMessage(
			fmt.Sprintf("Alerting engine initialization failed: %s", privacy.ScrubMessage(errMsg)),
			sentry.LevelError,
			telemetryComponent,
		)
	})
}

// ReportPanic reports that a handler panicked inside the event bus.
// This is the most critical health signal -- it means there is a bug
// in the engine or dispatcher code.
//
// PRIVACY: Only the panic type (%T) is sent to Sentry, NOT the panic value.
// The value could contain alert content (species names, event properties)
// if the handler panics while processing an AlertEvent. We deliberately
// sacrifice debuggability for privacy -- the type alone (e.g., *runtime.Error,
// string) plus Sentry's automatic stack trace is sufficient for diagnosis.
func (at *AlertingTelemetry) ReportPanic(panicValue any) {
	if at == nil {
		return
	}

	panicType := fmt.Sprintf("%T", panicValue)

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", telemetryComponent)
		scope.SetTag("outcome", "panic")
		scope.SetFingerprint([]string{telemetryComponent, "panic", panicType})

		scope.SetContext(telemetryComponent, map[string]any{
			"panic_type": panicType,
		})

		telemetry.CaptureMessage(
			fmt.Sprintf("Alerting handler panic (type: %s)", panicType),
			sentry.LevelFatal,
			telemetryComponent,
		)
	})
}

// ReportEventDropped increments the dropped event counter and reports to
// Sentry when the threshold is reached. This is called on the hot path
// so it uses atomic operations and rate-limits Sentry calls.
func (at *AlertingTelemetry) ReportEventDropped() {
	if at == nil {
		return
	}

	count := at.droppedEvents.Add(1)
	if count%dropReportThreshold == 0 {
		telemetry.FastCaptureMessage(
			fmt.Sprintf("Alert event bus buffer full, %d events dropped", count),
			sentry.LevelWarning,
			telemetryComponent,
		)
	}
}

// ReportDBWriteFailed reports that a database operation in the engine failed.
// The operation parameter identifies what failed (e.g., "save_history", "cleanup").
func (at *AlertingTelemetry) ReportDBWriteFailed(operation, errMsg string) {
	if at == nil {
		return
	}

	telemetry.FastCaptureMessage(
		fmt.Sprintf("Alerting DB operation failed: %s: %s", operation, privacy.ScrubMessage(errMsg)),
		sentry.LevelError,
		telemetryComponent,
	)
}

// ReportDispatchFailed reports that a notification dispatch failed.
// The target parameter identifies the action target (e.g., "bell").
func (at *AlertingTelemetry) ReportDispatchFailed(target, errMsg string) {
	if at == nil {
		return
	}

	telemetry.FastCaptureMessage(
		fmt.Sprintf("Alert dispatch to %s failed: %s", target, privacy.ScrubMessage(errMsg)),
		sentry.LevelWarning,
		telemetryComponent,
	)
}

// ReportBridgeRegistrationFailed reports that an event bridge failed to register.
func (at *AlertingTelemetry) ReportBridgeRegistrationFailed(errMsg string) {
	if at == nil {
		return
	}

	telemetry.FastCaptureMessage(
		fmt.Sprintf("Alert bridge registration failed: %s", privacy.ScrubMessage(errMsg)),
		sentry.LevelWarning,
		telemetryComponent,
	)
}
