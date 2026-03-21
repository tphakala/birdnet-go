package alerting

import (
	"fmt"
	"math"
	"strings"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// NotificationCreator abstracts the notification service for testability.
// The notifType parameter allows the dispatcher to specify the correct
// notification type (e.g., TypeDetection for bird detections, TypeWarning
// for system alerts) so push providers can filter appropriately.
//
// The eventProps parameter carries event-specific properties (species name,
// confidence, raw event metadata, etc.) that the adapter uses to enrich
// notifications with template metadata (bg_image_url, bg_confidence_percent, etc.)
// for webhook templates.
type NotificationCreator interface {
	CreateAndBroadcast(notifType notification.Type, title, message string, eventProps map[string]any) error
	CreateAndBroadcastWithKeys(notifType notification.Type, title, message, titleKey string, titleParams map[string]any, messageKey string, messageParams map[string]any, eventProps map[string]any) error
	// CreateAndBroadcastTest creates a bell notification marked as a test so
	// that push providers (Telegram, Shoutrrr, etc.) skip it.
	CreateAndBroadcastTest(notifType notification.Type, title, message string, eventProps map[string]any) error
	// CreateAndBroadcastTestWithKeys is the i18n-aware variant of CreateAndBroadcastTest.
	CreateAndBroadcastTestWithKeys(notifType notification.Type, title, message, titleKey string, titleParams map[string]any, messageKey string, messageParams map[string]any, eventProps map[string]any) error
}

// ActionDispatcher routes alert rule actions to the notification bell
// and/or external targets.
type ActionDispatcher struct {
	notifCreator NotificationCreator
	log          logger.Logger
	telemetry    *AlertingTelemetry
}

// NewActionDispatcher creates a new ActionDispatcher.
func NewActionDispatcher(notifCreator NotificationCreator, log logger.Logger, at *AlertingTelemetry) *ActionDispatcher {
	return &ActionDispatcher{
		notifCreator: notifCreator,
		log:          log,
		telemetry:    at,
	}
}

// Dispatch implements ActionFunc — called by the engine when a rule fires.
func (d *ActionDispatcher) Dispatch(rule *entities.AlertRule, event *AlertEvent) {
	d.dispatchInternal(rule, event, false)
}

// DispatchTest is like Dispatch but marks the resulting bell notification as a
// test so that push providers (Telegram, Shoutrrr, etc.) will not forward it.
func (d *ActionDispatcher) DispatchTest(rule *entities.AlertRule, event *AlertEvent) {
	d.dispatchInternal(rule, event, true)
}

// dispatchInternal contains the shared dispatch logic. When isTest is true,
// the notification is tagged so push providers skip it.
func (d *ActionDispatcher) dispatchInternal(rule *entities.AlertRule, event *AlertEvent, isTest bool) {
	if event == nil {
		event = &AlertEvent{}
	}
	for i := range rule.Actions {
		action := &rule.Actions[i]
		title := renderTitle(action.TemplateTitle, rule, event)
		message := renderMessage(action.TemplateMessage, rule, event)

		switch action.Target {
		case TargetBell:
			hasCustomTemplate := action.TemplateTitle != "" || action.TemplateMessage != ""
			d.dispatchBell(title, message, rule, event, hasCustomTemplate, isTest)
		default:
			d.log.Warn("unknown alert action target",
				logger.String("target", action.Target),
				logger.Uint64("rule_id", uint64(rule.ID)))
		}
	}
}

func (d *ActionDispatcher) dispatchBell(title, message string, rule *entities.AlertRule, event *AlertEvent, hasCustomTemplate, isTest bool) {
	if d.notifCreator == nil {
		return
	}

	notifType := notificationTypeForEvent(event)

	// When no custom template is set, use translation keys so the
	// frontend can render the notification in the user's locale.
	if !hasCustomTemplate {
		titleKey, titleParams := defaultTitleKey(rule)
		msgKey, msgParams, fallbackMsg := defaultMessageKeyAndParams(rule, event)
		var err error
		if isTest {
			err = d.notifCreator.CreateAndBroadcastTestWithKeys(
				notifType, title, fallbackMsg, titleKey, titleParams, msgKey, msgParams, event.Properties,
			)
		} else {
			err = d.notifCreator.CreateAndBroadcastWithKeys(
				notifType, title, fallbackMsg, titleKey, titleParams, msgKey, msgParams, event.Properties,
			)
		}
		if err != nil {
			d.log.Error("failed to create bell notification",
				logger.Uint64("rule_id", uint64(rule.ID)),
				logger.Error(err))
			d.telemetry.ReportDispatchFailed(TargetBell, err.Error())
		}
		return
	}

	var err error
	if isTest {
		err = d.notifCreator.CreateAndBroadcastTest(notifType, title, message, event.Properties)
	} else {
		err = d.notifCreator.CreateAndBroadcast(notifType, title, message, event.Properties)
	}
	if err != nil {
		d.log.Error("failed to create bell notification",
			logger.Uint64("rule_id", uint64(rule.ID)),
			logger.Error(err))
		d.telemetry.ReportDispatchFailed(TargetBell, err.Error())
	}
}

// defaultTitleKey returns the i18n key and parameters for the default alert
// notification title.
func defaultTitleKey(rule *entities.AlertRule) (key string, params map[string]any) {
	params = map[string]any{
		"rule_name": rule.Name,
	}
	if rule.NameKey != "" {
		params["rule_name_key"] = rule.NameKey
	}
	return MsgAlertFiredTitle, params
}

// renderTitle substitutes template variables in the title string.
// Falls back to a default title if the template is empty.
func renderTitle(tmpl string, rule *entities.AlertRule, event *AlertEvent) string {
	if tmpl == "" {
		return rule.Name
	}
	return renderTemplate(tmpl, rule, event)
}

// renderMessage substitutes template variables in the message string.
// Returns an empty string if the template is empty (no default message).
func renderMessage(tmpl string, rule *entities.AlertRule, event *AlertEvent) string {
	if tmpl == "" {
		return ""
	}
	return renderTemplate(tmpl, rule, event)
}

// renderTemplate substitutes template variables in a string.
func renderTemplate(tmpl string, rule *entities.AlertRule, event *AlertEvent) string {
	pairs := make([]string, 0, 8+len(event.Properties)*2)
	pairs = append(pairs,
		"{{rule_name}}", rule.Name,
		"{{event_name}}", event.EventName,
		"{{metric_name}}", event.MetricName,
		"{{object_type}}", event.ObjectType,
	)
	for k, v := range event.Properties {
		pairs = append(pairs, fmt.Sprintf("{{%s}}", k), fmt.Sprintf("%v", v))
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

// defaultMessageKeyAndParams returns the i18n message key, params, and English
// fallback string for the default (no-custom-template) notification message.
// Returns empty key/message when required event properties are missing (e.g., test fires).
func defaultMessageKeyAndParams(rule *entities.AlertRule, event *AlertEvent) (key string, params map[string]any, fallback string) {
	switch {
	case event.MetricName != "":
		return metricMessage(rule, event)
	case isDetectionEvent(event.EventName):
		return detectionMessage(event)
	case isErrorEvent(event.EventName):
		return errorMessage(event)
	case isDisconnectEvent(event.EventName):
		return disconnectMessage(event)
	default:
		return "", nil, ""
	}
}

func metricMessage(rule *entities.AlertRule, event *AlertEvent) (key string, params map[string]any, fallback string) {
	val, ok := event.Properties[PropertyValue]
	if !ok {
		return "", nil, ""
	}
	floatVal, err := toFloat64(val)
	if err != nil {
		return "", nil, ""
	}
	formatted := formatMetricValue(floatVal)

	// Prefer the escalation step threshold if available; fall back to the
	// condition-level threshold for rules without escalation steps.
	threshold := ""
	if step, ok := event.Properties[PropertyThresholdStep]; ok {
		if stepFloat, err := toFloat64(step); err == nil {
			threshold = formatMetricValue(stepFloat)
		}
	}
	if threshold == "" {
		for i := range rule.Conditions {
			if rule.Conditions[i].Property == PropertyValue {
				threshold = rule.Conditions[i].Value
				break
			}
		}
	}
	if threshold == "" {
		return "", nil, ""
	}

	params = map[string]any{
		"value":     formatted,
		"threshold": threshold,
	}
	fallback = fmt.Sprintf("Current value: %s%% (threshold: %s%%)", formatted, threshold)
	return MsgAlertMetricExceeded, params, fallback
}

func detectionMessage(event *AlertEvent) (key string, params map[string]any, fallback string) {
	species, _ := event.Properties[PropertySpeciesName].(string)
	if species == "" {
		return "", nil, ""
	}
	confVal, ok := event.Properties[PropertyConfidence]
	if !ok {
		return "", nil, ""
	}
	confFloat, err := toFloat64(confVal)
	if err != nil {
		return "", nil, ""
	}
	confStr := fmt.Sprintf("%.0f", confFloat*100)

	params = map[string]any{
		"species_name": species,
		"confidence":   confStr,
	}
	fallback = fmt.Sprintf("%s detected with %s%% confidence", species, confStr)
	return MsgAlertDetectionOccurred, params, fallback
}

func errorMessage(event *AlertEvent) (key string, params map[string]any, fallback string) {
	sourceName := entityName(event)
	errMsg, _ := event.Properties[PropertyError].(string)
	if sourceName == "" && errMsg == "" {
		return "", nil, ""
	}

	params = map[string]any{
		"source_name": sourceName,
		"error":       errMsg,
	}

	// Try to classify the error for a user-friendly message.
	if classified := classifyError(errMsg); classified != nil {
		key = MsgAlertErrorPrefix + "." + classified.Key
		fallback = formatErrorFallback(sourceName, classified.Fallback)
		return key, params, fallback
	}

	// Unrecognized error: fall back to the generic key with raw error.
	fallback = formatErrorFallback(sourceName, errMsg)
	return MsgAlertErrorOccurred, params, fallback
}

// formatErrorFallback builds the English fallback string, prepending the
// source name when available.
func formatErrorFallback(sourceName, message string) string {
	if sourceName == "" {
		return message
	}
	if message == "" {
		return sourceName
	}
	return fmt.Sprintf("%s: %s", sourceName, message)
}

func disconnectMessage(event *AlertEvent) (key string, params map[string]any, fallback string) {
	sourceName := entityName(event)
	if sourceName == "" {
		return "", nil, ""
	}

	params = map[string]any{
		"source_name": sourceName,
	}
	fallback = fmt.Sprintf("%s disconnected", sourceName)
	return MsgAlertDisconnected, params, fallback
}

// entityName extracts the human-readable entity name from event properties
// based on the object type (stream_name, device_name, broker, etc.).
func entityName(event *AlertEvent) string {
	for _, prop := range []string{PropertyStreamName, PropertyDeviceName, PropertyBroker} {
		if name, ok := event.Properties[prop].(string); ok && name != "" {
			return name
		}
	}
	return ""
}

// formatMetricValue formats a float64 metric value for display.
// Whole numbers show no decimals (e.g., "90"), fractional values show one decimal (e.g., "90.2").
func formatMetricValue(v float64) string {
	if math.Trunc(v) == v {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

func isDetectionEvent(eventName string) bool {
	return eventName == EventDetectionNewSpecies || eventName == EventDetectionOccurred
}

func isErrorEvent(eventName string) bool {
	return eventName == EventStreamError || eventName == EventDeviceError || eventName == EventBirdWeatherFailed || eventName == EventMQTTPublishFailed
}

func isDisconnectEvent(eventName string) bool {
	return eventName == EventStreamDisconnected || eventName == EventMQTTDisconnected
}

// notificationTypeForEvent returns the correct notification type based on the
// alert event. Detection events use TypeDetection so push providers (Discord,
// Telegram, etc.) that filter on type will deliver them correctly. All other
// alert events use TypeWarning.
func notificationTypeForEvent(event *AlertEvent) notification.Type {
	if event != nil && isDetectionEvent(event.EventName) {
		return notification.TypeDetection
	}
	return notification.TypeWarning
}
