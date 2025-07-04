package telemetry

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

const (
	// maxBreadcrumbs defines the maximum number of breadcrumbs to keep in Sentry events
	maxBreadcrumbs = 10
)

// Package-level logger specific to telemetry service
var (
	serviceLogger   *slog.Logger
	serviceLevelVar = new(slog.LevelVar) // Dynamic level control
	closeLogger     func() error
)

func init() {
	var err error
	// Define log file path relative to working directory
	logFilePath := filepath.Join("logs", "telemetry.log")
	initialLevel := slog.LevelDebug // Set desired initial level
	serviceLevelVar.Set(initialLevel)

	// Initialize the service-specific file logger
	serviceLogger, closeLogger, err = logging.NewFileLogger(logFilePath, "telemetry", serviceLevelVar)
	if err != nil {
		// Fallback: Log error to standard log and potentially disable service logging
		log.Printf("FATAL: Failed to initialize telemetry file logger at %s: %v. Service logging disabled.", logFilePath, err)
		// Set logger to a disabled handler to prevent nil panics, but respects level var
		fbHandler := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: serviceLevelVar})
		serviceLogger = slog.New(fbHandler).With("service", "telemetry")
		closeLogger = func() error { return nil } // No-op closer
	}
}

// AttachmentUploader handles uploading support dumps as Sentry attachments
type AttachmentUploader struct {
	enabled bool
}

// NewAttachmentUploader creates a new attachment uploader
func NewAttachmentUploader(enabled bool) *AttachmentUploader {
	return &AttachmentUploader{
		enabled: enabled,
	}
}

// UploadSupportDump uploads a support dump to Sentry as an event with attachment
func (au *AttachmentUploader) UploadSupportDump(ctx context.Context, dumpData []byte, systemID, userMessage string) error {
	// Extract trace ID early for use in error messages
	traceID := extractTraceID(ctx)

	// Log with privacy-safe message
	scrubbedMessage := ""
	if userMessage != "" {
		scrubbedMessage = privacy.ScrubMessage(userMessage)
	}

	logTelemetryInfo(nil, "telemetry: starting support dump upload",
		"system_id", systemID,
		"dump_size", len(dumpData),
		"has_message", userMessage != "",
		"trace_id", traceID,
		"scrubbed_message", scrubbedMessage)

	if !au.enabled {
		logTelemetryWarn(nil, "telemetry: upload blocked - telemetry not enabled")
		err := errors.Newf("telemetry is not enabled - cannot upload support dump").
			Component("telemetry").
			Category(errors.CategoryConfiguration).
			Context("operation", "upload_support_dump")
		if traceID != "" {
			err = err.Context("trace_id", traceID)
		}
		return err.Build()
	}

	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		logTelemetryWarn(nil, "telemetry: upload cancelled - context already done", "error", ctx.Err())
		err := errors.New(ctx.Err()).
			Component("telemetry").
			Category(errors.CategoryNetwork).
			Context("operation", "upload_support_dump").
			Context("reason", "context_cancelled_before_upload")
		if traceID != "" {
			err = err.Context("trace_id", traceID)
		}
		return err.Build()
	default:
		// Continue with upload
	}

	// Create a new event specifically for support dumps
	now := time.Now()
	event := sentry.NewEvent()
	event.Level = sentry.LevelInfo
	event.Message = fmt.Sprintf("Support Dump - System: %s - %s", systemID, now.Format(time.RFC3339))
	event.Timestamp = now

	// Add custom context
	supportContext := map[string]interface{}{
		"system_id":    systemID,
		"user_message": scrubbedMessage,
		"dump_size":    len(dumpData),
		"upload_time":  now.Format(time.RFC3339),
	}
	if traceID != "" {
		supportContext["trace_id"] = traceID
	}
	event.Contexts["support"] = supportContext

	// Set user context with system ID
	event.User = sentry.User{
		ID: systemID,
	}

	// Add tags for filtering
	event.Tags = map[string]string{
		"type":      "support_dump",
		"system_id": systemID,
	}
	if traceID != "" {
		event.Tags["trace_id"] = traceID
	}

	// Capture the event with attachment using WithScope
	var eventID *sentry.EventID

	logTelemetryDebug(nil, "telemetry: capturing event with attachment")
	sentry.WithScope(func(scope *sentry.Scope) {
		// Add the support dump as an attachment
		filename := fmt.Sprintf("support_dump_%s_%d.zip", systemID, time.Now().Unix())
		scope.AddAttachment(&sentry.Attachment{
			Filename:    filename,
			ContentType: "application/zip",
			Payload:     dumpData,
		})
		logTelemetryDebug(nil, "telemetry: attachment added to scope",
			"filename", filename,
			"attachment_type", "support_dump",
			"content_type", "application/zip",
			"size_bytes", len(dumpData),
			"system_id", systemID)

		// Add user message as breadcrumb
		if userMessage != "" {
			scope.AddBreadcrumb(&sentry.Breadcrumb{
				Type:      "user",
				Category:  "support",
				Message:   scrubbedMessage,
				Level:     sentry.LevelInfo,
				Timestamp: time.Now(),
			}, maxBreadcrumbs)
			logTelemetryDebug(nil, "telemetry: user message added as breadcrumb")
		}

		// Capture the event within this scope
		eventID = sentry.CaptureEvent(event)
	})

	if eventID == nil {
		logTelemetryError(nil, "telemetry: failed to capture support dump event in Sentry")
		return errors.Newf("failed to capture support dump event in Sentry").
			Component("telemetry").
			Category(errors.CategoryNetwork).
			Context("operation", "capture_support_event").
			Context("system_id", systemID).
			Context("dump_size", len(dumpData)).
			Build()
	}

	logTelemetryDebug(nil, "telemetry: event captured", "event_id", eventID)

	// Flush to ensure the event is sent with context awareness
	logTelemetryDebug(nil, "telemetry: flushing event to Sentry")
	flushDone := make(chan struct{})
	go func() {
		sentry.Flush(5 * time.Second)
		close(flushDone)
	}()

	// Wait for flush or context cancellation
	select {
	case <-ctx.Done():
		logTelemetryWarn(nil, "telemetry: upload cancelled during flush", "error", ctx.Err())
		return errors.New(ctx.Err()).
			Component("telemetry").
			Category(errors.CategoryNetwork).
			Context("operation", "upload_support_dump").
			Context("reason", "context_cancelled_during_flush").
			Build()
	case <-flushDone:
		logTelemetryInfo(nil, "telemetry: support dump uploaded successfully",
			"system_id", systemID,
			"event_id", eventID,
			"dump_size", len(dumpData))
		return nil
	}
}

// CreateSupportEvent creates a support request event without an attachment
func (au *AttachmentUploader) CreateSupportEvent(ctx context.Context, systemID, message string, metadata map[string]interface{}) error {
	logTelemetryInfo(nil, "telemetry: creating support event",
		"system_id", systemID,
		"has_metadata", len(metadata) > 0)

	// Input validation
	if systemID == "" {
		logTelemetryError(nil, "telemetry: validation failed - empty systemID")
		return errors.Newf("systemID cannot be empty").
			Component("telemetry").
			Category(errors.CategoryValidation).
			Context("operation", "create_support_event").
			Build()
	}

	if message == "" {
		logTelemetryError(nil, "telemetry: validation failed - empty message")
		return errors.Newf("message cannot be empty").
			Component("telemetry").
			Category(errors.CategoryValidation).
			Context("operation", "create_support_event").
			Build()
	}

	// Extract trace ID from context if available
	traceID := extractTraceID(ctx)
	if traceID != "" {
		// Log trace ID for observability
		logTelemetryDebug(nil, "telemetry: trace ID found", "trace_id", traceID)
		sentry.ConfigureScope(func(scope *sentry.Scope) {
			scope.SetTag("trace_id", traceID)
		})
	}

	// Scrub message for privacy
	scrubbedMessage := privacy.ScrubMessage(message)

	if !au.enabled {
		logTelemetryWarn(nil, "telemetry: support event blocked - telemetry not enabled")
		return errors.Newf("telemetry is not enabled - cannot create support event").
			Component("telemetry").
			Category(errors.CategoryConfiguration).
			Context("operation", "create_support_event").
			Context("trace_id", traceID).
			Build()
	}

	// Create event
	event := sentry.NewEvent()
	event.Level = sentry.LevelInfo
	event.Message = "User Support Request"
	event.Timestamp = time.Now()

	// Create a copy of metadata to avoid modifying the input
	supportContext := make(map[string]interface{})
	for k, v := range metadata {
		supportContext[k] = v
	}
	supportContext["system_id"] = systemID
	supportContext["message"] = scrubbedMessage
	if traceID != "" {
		supportContext["trace_id"] = traceID
	}

	// Add contexts
	event.Contexts["support"] = supportContext

	// Set user
	event.User = sentry.User{
		ID: systemID,
	}

	// Add tags
	event.Tags = map[string]string{
		"type":      "support_request",
		"system_id": systemID,
	}

	// Capture event
	logTelemetryDebug(nil, "telemetry: capturing support event")
	eventID := sentry.CaptureEvent(event)
	if eventID == nil {
		logTelemetryError(nil, "telemetry: failed to capture support event in Sentry")
		return errors.Newf("failed to capture support event in Sentry").
			Component("telemetry").
			Category(errors.CategoryNetwork).
			Context("operation", "capture_support_event").
			Context("system_id", systemID).
			Build()
	}

	logTelemetryDebug(nil, "telemetry: support event captured", "event_id", eventID)

	// Flush with context awareness
	logTelemetryDebug(nil, "telemetry: flushing support event to Sentry")
	flushDone := make(chan struct{})
	go func() {
		sentry.Flush(5 * time.Second)
		close(flushDone)
	}()

	// Wait for flush or context cancellation
	select {
	case <-ctx.Done():
		logTelemetryWarn(nil, "telemetry: support event cancelled during flush", "error", ctx.Err())
		return errors.New(ctx.Err()).
			Component("telemetry").
			Category(errors.CategoryNetwork).
			Context("operation", "create_support_event").
			Context("reason", "context_cancelled_during_flush").
			Build()
	case <-flushDone:
		logTelemetryInfo(nil, "telemetry: support event created successfully",
			"system_id", systemID,
			"event_id", eventID)
		return nil
	}
}

// extractTraceID attempts to extract a trace ID from the context
// It looks for common trace ID keys used by various tracing systems
func extractTraceID(ctx context.Context) string {
	// Check for OpenTelemetry trace ID
	if traceID := ctx.Value("trace-id"); traceID != nil {
		if id, ok := traceID.(string); ok {
			return id
		}
	}

	// Check for X-Trace-ID (common HTTP header)
	if traceID := ctx.Value("x-trace-id"); traceID != nil {
		if id, ok := traceID.(string); ok {
			return id
		}
	}

	// Check for request ID
	if reqID := ctx.Value("request-id"); reqID != nil {
		if id, ok := reqID.(string); ok {
			return id
		}
	}

	return ""
}
