package alerting

import (
	"fmt"
	"math"
	"strings"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// NotificationCreator abstracts the notification service for testability.
type NotificationCreator interface {
	CreateAndBroadcast(title, message string) error
	CreateAndBroadcastWithKeys(title, message, titleKey string, titleParams map[string]any, messageKey string, messageParams map[string]any) error
}

// ActionDispatcher routes alert rule actions to the notification bell
// and/or external targets.
type ActionDispatcher struct {
	notifCreator NotificationCreator
	log          logger.Logger
}

// NewActionDispatcher creates a new ActionDispatcher.
func NewActionDispatcher(notifCreator NotificationCreator, log logger.Logger) *ActionDispatcher {
	return &ActionDispatcher{
		notifCreator: notifCreator,
		log:          log,
	}
}

// Dispatch implements ActionFunc — called by the engine when a rule fires.
func (d *ActionDispatcher) Dispatch(rule *entities.AlertRule, event *AlertEvent) {
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
			d.dispatchBell(title, message, rule, event, hasCustomTemplate)
		default:
			d.log.Warn("unknown alert action target",
				logger.String("target", action.Target),
				logger.Uint64("rule_id", uint64(rule.ID)))
		}
	}
}

func (d *ActionDispatcher) dispatchBell(title, message string, rule *entities.AlertRule, event *AlertEvent, hasCustomTemplate bool) {
	if d.notifCreator == nil {
		return
	}

	// When no custom template is set, use translation keys so the
	// frontend can render the notification in the user's locale.
	if !hasCustomTemplate {
		titleKey, titleParams := defaultTitleKey(rule)
		msgKey, msgParams, fallbackMsg := defaultMessageKeyAndParams(rule, event)
		if err := d.notifCreator.CreateAndBroadcastWithKeys(
			title, fallbackMsg, titleKey, titleParams, msgKey, msgParams,
		); err != nil {
			d.log.Error("failed to create bell notification",
				logger.Uint64("rule_id", uint64(rule.ID)),
				logger.Error(err))
		}
		return
	}

	if err := d.notifCreator.CreateAndBroadcast(title, message); err != nil {
		d.log.Error("failed to create bell notification",
			logger.Uint64("rule_id", uint64(rule.ID)),
			logger.Error(err))
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
	if event == nil {
		return "", nil, ""
	}
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

	// Get threshold from the metric value condition.
	threshold := ""
	for i := range rule.Conditions {
		if rule.Conditions[i].Property == PropertyValue {
			threshold = rule.Conditions[i].Value
			break
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
	if sourceName != "" && message != "" {
		return fmt.Sprintf("%s: %s", sourceName, message)
	}
	if sourceName != "" {
		return sourceName
	}
	return message
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
