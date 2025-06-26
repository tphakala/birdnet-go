package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// maxBreadcrumbs defines the maximum number of breadcrumbs to keep in Sentry events
	maxBreadcrumbs = 10
)

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
	if !au.enabled {
		return errors.Newf("telemetry is not enabled - cannot upload support dump").
			Component("telemetry").
			Category(errors.CategoryConfiguration).
			Context("operation", "upload_support_dump").
			Build()
	}

	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		return errors.New(ctx.Err()).
			Component("telemetry").
			Category(errors.CategoryNetwork).
			Context("operation", "upload_support_dump").
			Context("reason", "context_cancelled_before_upload").
			Build()
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
	event.Contexts["support"] = map[string]interface{}{
		"system_id":    systemID,
		"user_message": userMessage,
		"dump_size":    len(dumpData),
		"upload_time":  now.Format(time.RFC3339),
	}

	// Set user context with system ID
	event.User = sentry.User{
		ID: systemID,
	}

	// Add tags for filtering
	event.Tags = map[string]string{
		"type":      "support_dump",
		"system_id": systemID,
	}

	// Capture the event with attachment using WithScope
	var eventID *sentry.EventID

	sentry.WithScope(func(scope *sentry.Scope) {
		// Add the support dump as an attachment
		scope.AddAttachment(&sentry.Attachment{
			Filename:    fmt.Sprintf("support_dump_%s_%d.zip", systemID, time.Now().Unix()),
			ContentType: "application/zip",
			Payload:     dumpData,
		})

		// Add user message as breadcrumb
		if userMessage != "" {
			scope.AddBreadcrumb(&sentry.Breadcrumb{
				Type:      "user",
				Category:  "support",
				Message:   userMessage,
				Level:     sentry.LevelInfo,
				Timestamp: time.Now(),
			}, maxBreadcrumbs)
		}

		// Capture the event within this scope
		eventID = sentry.CaptureEvent(event)
	})

	if eventID == nil {
		return errors.Newf("failed to capture support dump event in Sentry").
			Component("telemetry").
			Category(errors.CategoryNetwork).
			Context("operation", "capture_support_event").
			Context("system_id", systemID).
			Context("dump_size", len(dumpData)).
			Build()
	}

	// Flush to ensure the event is sent with context awareness
	flushDone := make(chan struct{})
	go func() {
		Flush(5 * time.Second)
		close(flushDone)
	}()

	// Wait for flush or context cancellation
	select {
	case <-ctx.Done():
		return errors.New(ctx.Err()).
			Component("telemetry").
			Category(errors.CategoryNetwork).
			Context("operation", "upload_support_dump").
			Context("reason", "context_cancelled_during_flush").
			Build()
	case <-flushDone:
		return nil
	}
}

// CreateSupportEvent creates a support request event without an attachment
func (au *AttachmentUploader) CreateSupportEvent(ctx context.Context, systemID, message string, metadata map[string]interface{}) error {
	// Input validation
	if systemID == "" {
		return errors.Newf("systemID cannot be empty").
			Component("telemetry").
			Category(errors.CategoryValidation).
			Context("operation", "create_support_event").
			Build()
	}
	
	if message == "" {
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
		sentry.ConfigureScope(func(scope *sentry.Scope) {
			scope.SetTag("trace_id", traceID)
		})
	}

	if !au.enabled {
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
	supportContext["message"] = message
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
	eventID := sentry.CaptureEvent(event)
	if eventID == nil {
		return errors.Newf("failed to capture support event in Sentry").
			Component("telemetry").
			Category(errors.CategoryNetwork).
			Context("operation", "capture_support_event").
			Context("system_id", systemID).
			Build()
	}

	// Flush with context awareness
	flushDone := make(chan struct{})
	go func() {
		Flush(5 * time.Second)
		close(flushDone)
	}()

	// Wait for flush or context cancellation
	select {
	case <-ctx.Done():
		return errors.New(ctx.Err()).
			Component("telemetry").
			Category(errors.CategoryNetwork).
			Context("operation", "create_support_event").
			Context("reason", "context_cancelled_during_flush").
			Build()
	case <-flushDone:
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
