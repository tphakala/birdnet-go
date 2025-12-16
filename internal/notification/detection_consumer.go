package notification

import (
	"bytes"
	"fmt"
	"log/slog"
	"sync"
	"text/template"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/events"
)

type DetectionNotificationConsumer struct {
	service *Service
	logger  *slog.Logger
	// speciesCooldowns tracks the last notification time for each species
	speciesCooldowns map[string]time.Time
	cooldownMu       sync.RWMutex
}

func NewDetectionNotificationConsumer(service *Service) *DetectionNotificationConsumer {
	return &DetectionNotificationConsumer{
		service:          service,
		logger:           service.logger,
		speciesCooldowns: make(map[string]time.Time),
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

	// Get settings for filtering
	settings := conf.GetSettings()

	// Check confidence threshold (if configured)
	if settings != nil && settings.Notification.Push.MinConfidenceThreshold > 0 {
		if event.GetConfidence() < settings.Notification.Push.MinConfidenceThreshold {
			c.logger.Debug("detection below confidence threshold, skipping notification",
				"species", event.GetSpeciesName(),
				"confidence", event.GetConfidence(),
				"threshold", settings.Notification.Push.MinConfidenceThreshold,
			)
			return nil
		}
	}

	// Check species cooldown (if configured)
	if settings != nil && settings.Notification.Push.SpeciesCooldownMinutes > 0 {
		if c.isWithinCooldown(event.GetSpeciesName(), settings.Notification.Push.SpeciesCooldownMinutes) {
			c.logger.Debug("species within cooldown period, skipping notification",
				"species", event.GetSpeciesName(),
				"cooldownMinutes", settings.Notification.Push.SpeciesCooldownMinutes,
			)
			return nil
		}
	}

	templateData := c.createTemplateData(event)
	title, message := c.renderTitleAndMessage(event, templateData)
	notification := c.buildDetectionNotification(event, title, message, templateData)

	if err := c.service.store.Save(notification); err != nil {
		c.logger.Error("failed to save new species notification",
			"species", event.GetSpeciesName(),
			"error", err,
		)
		return fmt.Errorf("failed to save notification: %w", err)
	}

	c.service.broadcast(notification)

	// Record cooldown after successful notification
	if settings != nil && settings.Notification.Push.SpeciesCooldownMinutes > 0 {
		c.recordCooldown(event.GetSpeciesName())
	}

	c.logger.Info("created new species notification",
		"species", event.GetSpeciesName(),
		"confidence", event.GetConfidence(),
		"location", event.GetLocation(),
	)

	return nil
}

// isWithinCooldown checks if the species is still within the cooldown period.
// Also performs lazy cleanup of expired cooldowns.
func (c *DetectionNotificationConsumer) isWithinCooldown(species string, cooldownMinutes int) bool {
	cooldownDuration := time.Duration(cooldownMinutes) * time.Minute
	now := time.Now()

	c.cooldownMu.RLock()
	lastNotification, exists := c.speciesCooldowns[species]
	c.cooldownMu.RUnlock()

	if !exists {
		return false
	}

	// Check if still within cooldown
	if now.Sub(lastNotification) < cooldownDuration {
		return true
	}

	// Cooldown expired, clean up entry
	c.cooldownMu.Lock()
	delete(c.speciesCooldowns, species)
	c.cooldownMu.Unlock()

	return false
}

// recordCooldown records the current time as the last notification time for a species.
func (c *DetectionNotificationConsumer) recordCooldown(species string) {
	c.cooldownMu.Lock()
	c.speciesCooldowns[species] = time.Now()
	c.cooldownMu.Unlock()
}

// createTemplateData creates template data from event and settings.
func (c *DetectionNotificationConsumer) createTemplateData(event events.DetectionEvent) *TemplateData {
	settings := conf.GetSettings()
	if settings == nil {
		c.logger.Warn("Settings unavailable during detection notification, using localhost for URL fields",
			"species", event.GetSpeciesName(),
			"confidence", event.GetConfidence())
		return NewTemplateData(event, "http://localhost", true)
	}

	baseURL := BuildBaseURL(settings.Security.Host, settings.WebServer.Port, settings.Security.AutoTLS)
	return NewTemplateData(event, baseURL, settings.Main.TimeAs24h)
}

// renderTitleAndMessage renders title and message from templates, with fallbacks.
func (c *DetectionNotificationConsumer) renderTitleAndMessage(event events.DetectionEvent, templateData *TemplateData) (title, message string) {
	title = c.renderTemplateField("title", event, templateData)
	message = c.renderTemplateField("message", event, templateData)
	return title, message
}

// renderTemplateField renders a single template field (title or message) with fallback.
func (c *DetectionNotificationConsumer) renderTemplateField(field string, event events.DetectionEvent, templateData *TemplateData) string {
	settings := conf.GetSettings()
	if settings == nil {
		return c.getDefaultValue(field, event)
	}

	var templateStr string
	switch field {
	case "title":
		templateStr = settings.Notification.Templates.NewSpecies.Title
	case "message":
		templateStr = settings.Notification.Templates.NewSpecies.Message
	}

	// Empty template means user explicitly wants empty value
	if templateStr == "" {
		return ""
	}

	rendered, err := renderTemplate(field, templateStr, templateData)
	if err != nil {
		c.logger.Error("failed to render "+field+" template, using default",
			"error", err,
			"template", templateStr,
		)
		return c.getDefaultValue(field, event)
	}
	return rendered
}

// getDefaultValue returns the default title or message for a detection event.
func (c *DetectionNotificationConsumer) getDefaultValue(field string, event events.DetectionEvent) string {
	switch field {
	case "title":
		return fmt.Sprintf("New Species Detected: %s", event.GetSpeciesName())
	case "message":
		return fmt.Sprintf("First detection of %s (%s) at %s",
			event.GetSpeciesName(),
			event.GetScientificName(),
			event.GetLocation())
	default:
		return ""
	}
}

// buildDetectionNotification creates a notification with all metadata.
func (c *DetectionNotificationConsumer) buildDetectionNotification(event events.DetectionEvent, title, message string, templateData *TemplateData) *Notification {
	notification := NewNotification(TypeDetection, PriorityHigh, title, message).
		WithComponent("detection").
		WithMetadata("species", event.GetSpeciesName()).
		WithMetadata("scientific_name", event.GetScientificName()).
		WithMetadata("confidence", event.GetConfidence()).
		WithMetadata("location", event.GetLocation()).
		WithMetadata("is_new_species", true).
		WithMetadata("days_since_first_seen", event.GetDaysSinceFirstSeen()).
		WithExpiry(24 * time.Hour)

	// Expose all TemplateData fields with bg_ prefix for use in provider templates
	notification = EnrichWithTemplateData(notification, templateData)

	// Add note_id from event metadata if available
	if eventMetadata := event.GetMetadata(); eventMetadata != nil {
		if noteID, ok := eventMetadata["note_id"]; ok {
			notification = notification.WithMetadata("note_id", noteID)
		}
	}

	return notification
}

// RenderTemplate renders a Go template string with the provided data.
// This is exported for use by the API when testing notifications.
func RenderTemplate(name, tmplStr string, data any) (string, error) {
	return renderTemplate(name, tmplStr, data)
}

func renderTemplate(name, tmplStr string, data any) (string, error) {
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
