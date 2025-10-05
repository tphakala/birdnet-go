package notification

import (
	"bytes"
	"fmt"
	"log/slog"
	"text/template"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/events"
)

type DetectionNotificationConsumer struct {
	service *Service
	logger  *slog.Logger
}

func NewDetectionNotificationConsumer(service *Service) *DetectionNotificationConsumer {
	return &DetectionNotificationConsumer{
		service: service,
		logger:  service.logger,
	}
}

func (c *DetectionNotificationConsumer) Name() string {
	return "detection-notification-consumer"
}

func (c *DetectionNotificationConsumer) ProcessEvent(event events.ErrorEvent) error {
	return nil
}

func (c *DetectionNotificationConsumer) ProcessBatch(errorEvents []events.ErrorEvent) error {
	return nil
}

func (c *DetectionNotificationConsumer) SupportsBatching() bool {
	return false
}

func (c *DetectionNotificationConsumer) ProcessDetectionEvent(event events.DetectionEvent) error {
	if !event.IsNewSpecies() {
		return nil
	}

	var title, message string

	settings := conf.GetSettings()
	if settings != nil {
		// Build base URL for links
		baseURL := BuildBaseURL(settings.Security.Host, settings.WebServer.Port, settings.Security.AutoTLS)

		// Create template data from event
		templateData := NewTemplateData(event, baseURL, settings.Main.TimeAs24h)

		// Render title template
		titleTemplate := settings.Notification.Templates.NewSpecies.Title
		if titleTemplate != "" {
			var err error
			title, err = renderTemplate("title", titleTemplate, templateData)
			if err != nil {
				c.logger.Error("failed to render title template, using default",
					"error", err,
					"template", titleTemplate,
				)
				title = ""
			}
		}

		// Render message template
		messageTemplate := settings.Notification.Templates.NewSpecies.Message
		if messageTemplate != "" {
			var err error
			message, err = renderTemplate("message", messageTemplate, templateData)
			if err != nil {
				c.logger.Error("failed to render message template, using default",
					"error", err,
					"template", messageTemplate,
				)
				message = ""
			}
		}
	}

	// Use defaults if settings not available or template rendering failed
	if title == "" {
		title = fmt.Sprintf("New Species Detected: %s", event.GetSpeciesName())
	}
	if message == "" {
		message = fmt.Sprintf(
			"First detection of %s (%s) at %s",
			event.GetSpeciesName(),
			event.GetScientificName(),
			event.GetLocation(),
		)
	}

	notification := NewNotification(TypeDetection, PriorityHigh, title, message).
		WithComponent("detection").
		WithMetadata("species", event.GetSpeciesName()).
		WithMetadata("scientific_name", event.GetScientificName()).
		WithMetadata("confidence", event.GetConfidence()).
		WithMetadata("location", event.GetLocation()).
		WithMetadata("is_new_species", true).
		WithMetadata("days_since_first_seen", event.GetDaysSinceFirstSeen()).
		WithExpiry(24 * time.Hour)

	if err := c.service.store.Save(notification); err != nil {
		c.logger.Error("failed to save new species notification",
			"species", event.GetSpeciesName(),
			"error", err,
		)
		return fmt.Errorf("failed to save notification: %w", err)
	}

	c.service.broadcast(notification)

	c.logger.Info("created new species notification",
		"species", event.GetSpeciesName(),
		"confidence", event.GetConfidence(),
		"location", event.GetLocation(),
	)

	return nil
}

// RenderTemplate renders a Go template string with the provided data.
// This is exported for use by the API when testing notifications.
func RenderTemplate(name, tmplStr string, data interface{}) (string, error) {
	return renderTemplate(name, tmplStr, data)
}

func renderTemplate(name, tmplStr string, data interface{}) (string, error) {
	tmpl, err := template.New(name).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}
