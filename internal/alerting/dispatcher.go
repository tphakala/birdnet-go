package alerting

import (
	"fmt"
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
	for i := range rule.Actions {
		action := &rule.Actions[i]
		title := renderTitle(action.TemplateTitle, rule, event)
		message := renderMessage(action.TemplateMessage, rule, event)

		switch action.Target {
		case TargetBell:
			hasCustomTemplate := action.TemplateTitle != "" || action.TemplateMessage != ""
			d.dispatchBell(title, message, rule, hasCustomTemplate)
		default:
			d.log.Warn("unknown alert action target",
				logger.String("target", action.Target),
				logger.Uint64("rule_id", uint64(rule.ID)))
		}
	}
}

func (d *ActionDispatcher) dispatchBell(title, message string, rule *entities.AlertRule, hasCustomTemplate bool) {
	if d.notifCreator == nil {
		return
	}

	// When no custom template is set, use translation keys so the
	// frontend can render the notification in the user's locale.
	if !hasCustomTemplate {
		titleKey, titleParams := defaultTitleKey(rule)
		if err := d.notifCreator.CreateAndBroadcastWithKeys(title, message, titleKey, titleParams, "", nil); err != nil {
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
		return fmt.Sprintf("Alert: %s", rule.Name)
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
