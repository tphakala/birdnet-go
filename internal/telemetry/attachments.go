package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/errors"
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

	// Create a new event specifically for support dumps
	event := sentry.NewEvent()
	event.Level = sentry.LevelInfo
	event.Message = "User Support Dump"
	event.Timestamp = time.Now()

	// Add custom context
	event.Contexts["support"] = map[string]interface{}{
		"system_id":    systemID,
		"user_message": userMessage,
		"dump_size":    len(dumpData),
		"upload_time":  time.Now().Format(time.RFC3339),
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
			}, 10)
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

	// Flush to ensure the event is sent
	sentry.Flush(5 * time.Second)

	return nil
}

// CreateSupportEvent creates a support request event without an attachment
func (au *AttachmentUploader) CreateSupportEvent(ctx context.Context, systemID, message string, metadata map[string]interface{}) error {
	if !au.enabled {
		return errors.Newf("telemetry is not enabled - cannot create support event").
			Component("telemetry").
			Category(errors.CategoryConfiguration).
			Context("operation", "create_support_event").
			Build()
	}

	// Create event
	event := sentry.NewEvent()
	event.Level = sentry.LevelInfo
	event.Message = "User Support Request"
	event.Timestamp = time.Now()

	// Add contexts
	event.Contexts["support"] = metadata
	event.Contexts["support"]["system_id"] = systemID
	event.Contexts["support"]["message"] = message

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

	sentry.Flush(5 * time.Second)
	return nil
}
