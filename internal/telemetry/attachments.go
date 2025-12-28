package telemetry

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

const (
	// maxBreadcrumbs defines the maximum number of breadcrumbs to keep in Sentry events
	maxBreadcrumbs = 10
	// sentryFlushTimeout is the timeout for flushing events to Sentry
	sentryFlushTimeout = 5 * time.Second
)

// flushWithContext flushes Sentry events with context cancellation awareness.
// Returns nil on successful flush, or an error if context is cancelled during flush.
//
// Design notes:
//   - This function does not check shouldSkipTelemetry() because it is only called
//     after event capture, which has already performed that check. Rechecking here
//     would be redundant and could cause events to be captured but never flushed.
//   - If context is cancelled, the flush goroutine continues running until Sentry's
//     internal timeout (sentryFlushTimeout) completes. This is intentional to allow
//     in-flight events to be sent even when the caller has given up waiting.
func flushWithContext(ctx context.Context, operation string) error {
	log := GetLogger()
	log.Debug("flushing event to Sentry", logger.String("operation", operation))

	flushDone := make(chan struct{})
	go func() {
		sentry.Flush(sentryFlushTimeout)
		close(flushDone)
	}()

	select {
	case <-ctx.Done():
		log.Warn(operation+" cancelled during flush", logger.Error(ctx.Err()))
		return errors.New(ctx.Err()).
			Component("telemetry").
			Category(errors.CategoryNetwork).
			Context("operation", operation).
			Context("reason", "context_cancelled_during_flush").
			Build()
	case <-flushDone:
		log.Debug("flush completed successfully", logger.String("operation", operation))
		return nil
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
	log := GetLogger()

	// Extract trace ID early for use in error messages
	traceID := extractTraceID(ctx)

	// Log with privacy-safe message
	scrubbedMessage := ""
	if userMessage != "" {
		scrubbedMessage = privacy.ScrubMessage(userMessage)
	}

	log.Info("starting support dump upload",
		logger.String("system_id", systemID),
		logger.Int("dump_size", len(dumpData)),
		logger.Bool("has_message", userMessage != ""),
		logger.String("trace_id", traceID),
		logger.String("scrubbed_message", scrubbedMessage))

	if !au.enabled {
		log.Warn("upload blocked - uploader not enabled")
		err := errors.Newf("attachment uploader is not enabled").
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
		log.Warn("upload cancelled - context already done", logger.Error(ctx.Err()))
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
	supportContext := map[string]any{
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

	log.Debug("capturing event with attachment")
	sentry.WithScope(func(scope *sentry.Scope) {
		// Add the support dump as an attachment
		filename := fmt.Sprintf("support_dump_%s_%d.zip", systemID, time.Now().Unix())
		scope.AddAttachment(&sentry.Attachment{
			Filename:    filename,
			ContentType: "application/zip",
			Payload:     dumpData,
		})
		log.Debug("attachment added to scope",
			logger.String("filename", filename),
			logger.String("attachment_type", "support_dump"),
			logger.String("content_type", "application/zip"),
			logger.Int("size_bytes", len(dumpData)),
			logger.String("system_id", systemID))

		// Add user message as breadcrumb
		if userMessage != "" {
			scope.AddBreadcrumb(&sentry.Breadcrumb{
				Type:      "user",
				Category:  "support",
				Message:   scrubbedMessage,
				Level:     sentry.LevelInfo,
				Timestamp: time.Now(),
			}, maxBreadcrumbs)
			log.Debug("user message added as breadcrumb")
		}

		// Capture the event within this scope
		eventID = sentry.CaptureEvent(event)
	})

	if eventID == nil {
		log.Error("failed to capture support dump event in Sentry")
		return errors.Newf("failed to capture support dump event in Sentry").
			Component("telemetry").
			Category(errors.CategoryNetwork).
			Context("operation", "capture_support_event").
			Context("system_id", systemID).
			Context("dump_size", len(dumpData)).
			Build()
	}

	log.Debug("event captured", logger.Any("event_id", eventID))

	// Flush to ensure the event is sent with context awareness
	if err := flushWithContext(ctx, "upload_support_dump"); err != nil {
		return err
	}

	log.Info("support dump uploaded successfully",
		logger.String("system_id", systemID),
		logger.Any("event_id", eventID),
		logger.Int("dump_size", len(dumpData)))
	return nil
}

// CreateSupportEvent creates a support request event without an attachment
func (au *AttachmentUploader) CreateSupportEvent(ctx context.Context, systemID, message string, metadata map[string]any) error {
	log := GetLogger()
	log.Info("creating support event",
		logger.String("system_id", systemID),
		logger.Bool("has_metadata", len(metadata) > 0))

	// Input validation
	if systemID == "" {
		log.Error("validation failed - empty systemID")
		return errors.Newf("systemID cannot be empty").
			Component("telemetry").
			Category(errors.CategoryValidation).
			Context("operation", "create_support_event").
			Build()
	}

	if message == "" {
		log.Error("validation failed - empty message")
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
		log.Debug("trace ID found", logger.String("trace_id", traceID))
		sentry.ConfigureScope(func(scope *sentry.Scope) {
			scope.SetTag("trace_id", traceID)
		})
	}

	// Scrub message for privacy
	scrubbedMessage := privacy.ScrubMessage(message)

	if !au.enabled {
		log.Warn("support event blocked - telemetry not enabled")
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
	supportContext := make(map[string]any)
	maps.Copy(supportContext, metadata)
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
	log.Debug("capturing support event")
	eventID := sentry.CaptureEvent(event)
	if eventID == nil {
		log.Error("failed to capture support event in Sentry")
		return errors.Newf("failed to capture support event in Sentry").
			Component("telemetry").
			Category(errors.CategoryNetwork).
			Context("operation", "capture_support_event").
			Context("system_id", systemID).
			Build()
	}

	log.Debug("support event captured", logger.Any("event_id", eventID))

	// Flush with context awareness
	if err := flushWithContext(ctx, "create_support_event"); err != nil {
		return err
	}

	log.Info("support event created successfully",
		logger.String("system_id", systemID),
		logger.Any("event_id", eventID))
	return nil
}

// contextKey is a typed key for context values to avoid collisions with other packages
type contextKey string

// Unexported context keys for trace ID extraction
const (
	traceIDKey   contextKey = "trace-id"
	xTraceIDKey  contextKey = "x-trace-id"
	requestIDKey contextKey = "request-id"
)

// NewTraceIDContext returns a new context with the given trace ID.
func NewTraceIDContext(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// TraceIDFromContext extracts the trace ID from the context.
// Returns the trace ID and true if present, or empty string and false if not.
func TraceIDFromContext(ctx context.Context) (string, bool) {
	if v := ctx.Value(traceIDKey); v != nil {
		if id, ok := v.(string); ok {
			return id, true
		}
	}
	return "", false
}

// NewXTraceIDContext returns a new context with the given X-Trace-ID.
func NewXTraceIDContext(ctx context.Context, xTraceID string) context.Context {
	return context.WithValue(ctx, xTraceIDKey, xTraceID)
}

// XTraceIDFromContext extracts the X-Trace-ID from the context.
// Returns the X-Trace-ID and true if present, or empty string and false if not.
func XTraceIDFromContext(ctx context.Context) (string, bool) {
	if v := ctx.Value(xTraceIDKey); v != nil {
		if id, ok := v.(string); ok {
			return id, true
		}
	}
	return "", false
}

// NewRequestIDContext returns a new context with the given request ID.
func NewRequestIDContext(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

// RequestIDFromContext extracts the request ID from the context.
// Returns the request ID and true if present, or empty string and false if not.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	if v := ctx.Value(requestIDKey); v != nil {
		if id, ok := v.(string); ok {
			return id, true
		}
	}
	return "", false
}

// extractTraceID attempts to extract a trace ID from the context.
// It looks for common trace ID keys used by various tracing systems.
// Priority: trace-id > x-trace-id > request-id
func extractTraceID(ctx context.Context) string {
	// Check for OpenTelemetry trace ID (highest priority)
	if id, ok := TraceIDFromContext(ctx); ok {
		return id
	}

	// Check for X-Trace-ID (common HTTP header)
	if id, ok := XTraceIDFromContext(ctx); ok {
		return id
	}

	// Check for request ID
	if id, ok := RequestIDFromContext(ctx); ok {
		return id
	}

	return ""
}
