package alerting

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// notificationAdapter lazily resolves the notification service to implement
// NotificationCreator. This avoids hard initialization ordering between the
// alerting and notification subsystems.
type notificationAdapter struct{}

func (a *notificationAdapter) CreateAndBroadcast(target string, notifType notification.Type, title, message string, eventProps map[string]any) error {
	svc := notification.GetService()
	if svc == nil {
		return nil // notification service not yet initialized
	}
	notif := notification.NewNotification(notifType, notification.PriorityHigh, title, message).
		WithDeliveryTarget(target)
	notif = enrichFromEventProps(notif, notifType, eventProps)
	return svc.CreateWithMetadata(notif)
}

func (a *notificationAdapter) CreateAndBroadcastWithKeys(
	target string, notifType notification.Type, title, message, titleKey string, titleParams map[string]any,
	messageKey string, messageParams map[string]any, eventProps map[string]any,
) error {
	svc := notification.GetService()
	if svc == nil {
		return nil // notification service not yet initialized
	}
	title, message = applyDetectionTemplates(notifType, title, message, eventProps)
	notif := notification.NewNotification(notifType, notification.PriorityHigh, title, message).
		WithDeliveryTarget(target).
		WithTitleKey(titleKey, titleParams)
	if messageKey != "" {
		notif = notif.WithMessageKey(messageKey, messageParams)
	}
	notif = enrichFromEventProps(notif, notifType, eventProps)
	return svc.CreateWithMetadata(notif)
}

func (a *notificationAdapter) CreateAndBroadcastTest(target string, notifType notification.Type, title, message string, eventProps map[string]any) error {
	svc := notification.GetService()
	if svc == nil {
		return nil // notification service not yet initialized
	}
	notif := notification.NewNotification(notifType, notification.PriorityHigh, title, message).
		WithDeliveryTarget(target)
	notif = enrichFromEventProps(notif, notifType, eventProps)
	return svc.CreateWithMetadata(notif)
}

func (a *notificationAdapter) CreateAndBroadcastTestWithKeys(
	target string, notifType notification.Type, title, message, titleKey string, titleParams map[string]any,
	messageKey string, messageParams map[string]any, eventProps map[string]any,
) error {
	svc := notification.GetService()
	if svc == nil {
		return nil // notification service not yet initialized
	}
	notif := notification.NewNotification(notifType, notification.PriorityHigh, title, message).
		WithDeliveryTarget(target).
		WithTitleKey(titleKey, titleParams)
	if messageKey != "" {
		notif = notif.WithMessageKey(messageKey, messageParams)
	}
	notif = enrichFromEventProps(notif, notifType, eventProps)
	return svc.CreateWithMetadata(notif)
}

// enrichFromEventProps adds detection metadata to a notification when the event
// is a detection. This enables webhook templates to reference fields like
// bg_image_url, bg_confidence_percent, bg_detection_time, etc.
//
// For non-detection notifications, the notification is returned unchanged.
func enrichFromEventProps(notif *notification.Notification, notifType notification.Type, props map[string]any) *notification.Notification {
	if notifType != notification.TypeDetection || len(props) == 0 {
		return notif
	}

	// Add top-level metadata fields matching detection_consumer.go behavior
	if species, ok := props[PropertySpeciesName].(string); ok {
		notif = notif.WithMetadata("species", species)
	}
	if sciName, ok := props[PropertyScientificName].(string); ok {
		notif = notif.WithMetadata("scientific_name", sciName)
	}
	if confVal, ok := props[PropertyConfidence]; ok {
		notif = notif.WithMetadata("confidence", confVal)
	}
	if location, ok := props[PropertyLocation].(string); ok {
		notif = notif.WithMetadata("location", location)
	}
	if isNew, ok := props[PropertyIsNewSpecies].(bool); ok {
		notif = notif.WithMetadata("is_new_species", isNew)
	}
	if days, ok := props[PropertyDaysSinceFirstSeen].(int); ok {
		notif = notif.WithMetadata("days_since_first_seen", days)
	}

	// Build TemplateData for bg_* metadata fields
	templateData := buildTemplateDataFromProps(props)
	if templateData != nil {
		notif = notification.EnrichWithTemplateData(notif, templateData)
	}

	notif = notif.WithComponent("detection").
		WithExpiry(notification.DefaultDetectionExpiry)

	// Pass through note_id from raw event metadata
	if rawMeta, ok := props[PropertyEventMetadata].(map[string]any); ok {
		if noteID, ok := rawMeta["note_id"]; ok {
			notif = notif.WithMetadata("note_id", noteID)
		}
	}

	return notif
}

// buildTemplateDataFromProps constructs a notification.TemplateData from alert
// event properties. This mirrors the logic in detection_consumer.go's
// createTemplateData but works from the flat property map rather than a
// DetectionEvent interface.
func buildTemplateDataFromProps(props map[string]any) *notification.TemplateData {
	settings := conf.GetSettings()

	baseURL := "http://localhost"
	timeAs24h := true
	if settings != nil {
		baseURL = settings.Security.GetBaseURL(settings.WebServer.Port)
		if baseURL == "" {
			baseURL = "http://localhost"
		}
		timeAs24h = settings.Main.TimeAs24h
	}

	// Extract raw event metadata
	rawMeta, _ := props[PropertyEventMetadata].(map[string]any)

	// Determine timestamp
	var beginTime time.Time
	if rawMeta != nil {
		if bt, ok := rawMeta["begin_time"].(time.Time); ok {
			beginTime = bt
		}
	}
	if beginTime.IsZero() {
		if ts, ok := props[PropertyEventTimestamp].(time.Time); ok {
			beginTime = ts
		}
	}
	if beginTime.IsZero() {
		beginTime = time.Now()
	}

	detectionTime := beginTime.Format(time.TimeOnly)
	detectionDate := beginTime.Format(time.DateOnly)
	if !timeAs24h {
		detectionTime = beginTime.Format("3:04:05 PM")
	}

	// Confidence
	var confidence float64
	if c, ok := props[PropertyConfidence]; ok {
		if f, err := toFloat64(c); err == nil {
			confidence = f
		}
	}
	confidencePercent := fmt.Sprintf("%.0f", confidence*notification.PercentMultiplier)

	// Location data from raw metadata
	var latitude, longitude float64
	if rawMeta != nil {
		if lat, ok := rawMeta["latitude"].(float64); ok {
			latitude = lat
		}
		if lon, ok := rawMeta["longitude"].(float64); ok {
			longitude = lon
		}
	}

	location, _ := props[PropertyLocation].(string)

	// Note ID for detection URL
	var noteID string
	if rawMeta != nil {
		if id, ok := rawMeta["note_id"].(uint); ok {
			noteID = fmt.Sprintf("%d", id)
		}
	}

	detectionPath := "/ui/detections"
	if noteID != "" {
		detectionPath = fmt.Sprintf("/ui/detections/%s", noteID)
	}
	detectionURL := baseURL + detectionPath

	// Image URL
	scientificName, _ := props[PropertyScientificName].(string)
	var imageURL string
	if rawMeta != nil {
		if imgURL, ok := rawMeta["image_url"].(string); ok && imgURL != "" {
			imageURL = imgURL
		}
	}
	if imageURL == "" && scientificName != "" {
		encodedName := url.QueryEscape(scientificName)
		imageURL = fmt.Sprintf("%s/api/v2/media/species-image?scientific_name=%s", baseURL, encodedName)
	}

	commonName, _ := props[PropertySpeciesName].(string)
	daysSinceFirstSeen, _ := props[PropertyDaysSinceFirstSeen].(int)
	daysSinceLastSeen, _ := props[PropertyDaysSinceLastSeen].(int)

	return &notification.TemplateData{
		CommonName:         commonName,
		ScientificName:     scientificName,
		Confidence:         confidence,
		ConfidencePercent:  confidencePercent,
		DetectionTime:      detectionTime,
		DetectionDate:      detectionDate,
		Latitude:           latitude,
		Longitude:          longitude,
		Location:           location,
		DetectionID:        noteID,
		DetectionPath:      detectionPath,
		DetectionURL:       detectionURL,
		ImageURL:           imageURL,
		DaysSinceFirstSeen: daysSinceFirstSeen,
		DaysSinceLastSeen:  daysSinceLastSeen,
	}
}

// applyDetectionTemplates overrides title and message with user-configured
// notification templates for new-species detection events. Non-detection types
// and non-new-species detections pass through unchanged.
func applyDetectionTemplates(notifType notification.Type, title, message string, eventProps map[string]any) (renderedTitle, renderedMessage string) {
	if notifType != notification.TypeDetection {
		return title, message
	}
	isNew, _ := eventProps[PropertyIsNewSpecies].(bool)
	if !isNew {
		return title, message
	}
	rt, rm := renderDetectionTemplates(eventProps)
	if rt != "" {
		title = rt
	}
	if rm != "" {
		message = rm
	}
	return title, message
}

func renderDetectionTemplates(props map[string]any) (title, message string) {
	settings := conf.GetSettings()
	if settings == nil {
		return "", ""
	}

	titleTmpl := settings.Notification.Templates.NewSpecies.Title
	msgTmpl := settings.Notification.Templates.NewSpecies.Message
	if titleTmpl == "" && msgTmpl == "" {
		return "", ""
	}

	templateData := buildTemplateDataFromProps(props)

	log := notification.GetLogger()

	if titleTmpl != "" {
		rendered, err := notification.RenderTemplate("title", titleTmpl, templateData)
		if err != nil {
			log.Warn("failed to render detection title template",
				logger.String("template", titleTmpl), logger.Error(err))
		} else {
			title = rendered
		}
	}

	if msgTmpl != "" {
		rendered, err := notification.RenderTemplate("message", msgTmpl, templateData)
		if err != nil {
			log.Warn("failed to render detection message template",
				logger.String("template", msgTmpl), logger.Error(err))
		} else {
			message = rendered
		}
	}

	return title, message
}

// Initialize creates and starts the alerting engine.
// It seeds default rules if none exist, creates the engine with the
// action dispatcher, subscribes to the event bus, and loads rules.
func Initialize(
	repo repository.AlertRuleRepository,
	eventBus *AlertEventBus,
	log logger.Logger,
	at *AlertingTelemetry,
) (*Engine, error) {
	ctx := context.Background()

	// Seed default rules if the table is empty
	if err := seedDefaultRules(ctx, repo, log); err != nil {
		at.ReportInitFailed(err.Error())
		return nil, err
	}

	// Create dispatcher and engine (adapter lazily resolves notification service)
	dispatcher := NewActionDispatcher(&notificationAdapter{}, log, at)
	engine := NewEngine(repo, dispatcher.Dispatch, log, at)
	engine.SetTestActionFunc(dispatcher.DispatchTest)

	// Load rules from database
	if err := engine.RefreshRules(ctx); err != nil {
		at.ReportInitFailed(err.Error())
		return nil, err
	}

	// Subscribe engine to the event bus and set global singleton
	eventBus.Subscribe(engine.HandleEvent)
	SetGlobalBus(eventBus)

	// Register detection bridge with the global event bus so detection events
	// flow into the alerting engine.
	if eventBusInstance := events.GetEventBus(); eventBusInstance != nil {
		bridge := NewDetectionAlertBridge(log)
		if err := eventBusInstance.RegisterConsumer(bridge); err != nil {
			log.Warn("failed to register detection alert bridge", logger.Error(err))
			at.ReportBridgeRegistrationFailed(err.Error())
		}
	}

	// Signal to the notification subsystem that the alert engine now handles
	// detection notifications, bypassing the hardcoded consumer logic.
	notification.SetAlertEngineActive(true)

	// Start periodic history cleanup based on configured retention
	if settings := conf.GetSettings(); settings != nil {
		engine.StartHistoryCleanup(settings.Alerting.HistoryRetentionDays)
	}

	log.Info("alerting engine initialized",
		logger.Int("rules_loaded", len(engine.rules)))

	return engine, nil
}

// seedDefaultRules ensures all built-in default rules exist. It checks by name
// so partial seeds from previous runs self-heal on restart. It also adds the
// default escalation steps to existing built-in rules that lack them,
// including legacy rows without a name key. The two checks differ on purpose:
// a rule counts as already seeded by name alone, but only counts as the shipped
// default for migration when its trigger and conditions match too (see
// defaultRuleFor).
func seedDefaultRules(ctx context.Context, repo repository.AlertRuleRepository, log logger.Logger) error {
	existing, err := repo.ListRules(ctx, repository.AlertRuleFilter{})
	if err != nil {
		return err
	}

	// Build set of existing rule names for fast lookup
	existingNames := make(map[string]struct{}, len(existing))
	for i := range existing {
		existingNames[existing[i].Name] = struct{}{}
	}

	defaults := DefaultRules()
	var created int
	for i := range defaults {
		if _, exists := existingNames[defaults[i].Name]; exists {
			continue
		}
		if err := repo.CreateRule(ctx, &defaults[i]); err != nil {
			return err
		}
		created++
	}
	if created > 0 {
		log.Info("seeded default alert rules", logger.Int("created", created))
	}

	migrateEscalationSteps(ctx, repo, existing, log)

	return nil
}

// defaultRuleFor returns the shipped default that a stored built-in rule was
// seeded from. Rules with a NameKey match the default with the same key. Rules
// seeded before name keys existed have an empty NameKey and match the default
// with the same Name. Either way the trigger definition and conditions must be
// equal to the default's too, so a rule whose threshold was changed is not
// mistaken for the default and given steps that start above its threshold.
func defaultRuleFor(rule *entities.AlertRule, defaults []entities.AlertRule) (*entities.AlertRule, bool) {
	if !rule.BuiltIn {
		return nil, false
	}
	for i := range defaults {
		def := &defaults[i]
		var identified bool
		if rule.NameKey != "" {
			identified = def.NameKey == rule.NameKey
		} else {
			identified = def.Name == rule.Name
		}
		if identified && sameTrigger(rule, def) {
			return def, true
		}
	}
	return nil, false
}

// sameTrigger reports whether two rules have the same trigger definition and
// conditions. Conditions are compared in slice order on Property, Operator,
// Value and DurationSec; IDs, sort order and timestamps are ignored. Stored
// conditions carry no guaranteed order, which does not matter because every
// default rule has at most one condition.
func sameTrigger(a, b *entities.AlertRule) bool {
	return a.ObjectType == b.ObjectType && a.TriggerType == b.TriggerType &&
		a.MetricName == b.MetricName && a.EventName == b.EventName &&
		slices.EqualFunc(a.Conditions, b.Conditions, func(x, y entities.AlertCondition) bool {
			return x.Property == y.Property && x.Operator == y.Operator &&
				x.Value == y.Value && x.DurationSec == y.DurationSec
		})
}

// migrateEscalationSteps fills EscalationSteps on stored built-in rules that
// were seeded before escalation support was added. Rules that already have
// steps are left alone, and so are rules whose trigger or conditions differ
// from the default (see defaultRuleFor). A failed update is logged and does
// not stop the remaining rules.
func migrateEscalationSteps(ctx context.Context, repo repository.AlertRuleRepository, existing []entities.AlertRule, log logger.Logger) {
	allDefaults := DefaultRules()
	for i := range existing {
		rule := &existing[i]
		def, ok := defaultRuleFor(rule, allDefaults)
		if !ok || len(def.EscalationSteps) == 0 || len(rule.EscalationSteps) > 0 {
			continue
		}
		rule.EscalationSteps = slices.Clone(def.EscalationSteps)
		if err := repo.UpdateRule(ctx, rule); err != nil {
			log.Warn("failed to migrate escalation steps for built-in rule",
				logger.String("name", rule.Name),
				logger.Error(err))
			continue
		}
		log.Info("migrated escalation steps for built-in rule",
			logger.String("name", rule.Name))
	}
}
