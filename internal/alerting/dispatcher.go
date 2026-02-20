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
		title := renderTemplate(action.TemplateTitle, rule, event)
		message := renderTemplate(action.TemplateMessage, rule, event)

		switch action.Target {
		case TargetBell:
			d.dispatchBell(title, message, rule)
		default:
			d.log.Warn("unknown alert action target",
				logger.String("target", action.Target),
				logger.Uint64("rule_id", uint64(rule.ID)))
		}
	}
}

func (d *ActionDispatcher) dispatchBell(title, message string, rule *entities.AlertRule) {
	if d.notifCreator == nil {
		return
	}
	if err := d.notifCreator.CreateAndBroadcast(title, message); err != nil {
		d.log.Error("failed to create bell notification",
			logger.Uint64("rule_id", uint64(rule.ID)),
			logger.Error(err))
	}
}

// renderTemplate substitutes template variables in the title/message strings.
// Falls back to defaults if the template is empty.
func renderTemplate(tmpl string, rule *entities.AlertRule, event *AlertEvent) string {
	if tmpl == "" {
		return defaultTemplate(rule, event)
	}
	pairs := []string{
		"{{rule_name}}", rule.Name,
		"{{event_name}}", event.EventName,
		"{{metric_name}}", event.MetricName,
		"{{object_type}}", event.ObjectType,
	}
	for k, v := range event.Properties {
		pairs = append(pairs, fmt.Sprintf("{{%s}}", k), fmt.Sprintf("%v", v))
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

func defaultTemplate(rule *entities.AlertRule, event *AlertEvent) string {
	if event.EventName != "" {
		return fmt.Sprintf("Alert: %s (%s)", rule.Name, event.EventName)
	}
	if event.MetricName != "" {
		return fmt.Sprintf("Alert: %s (%s)", rule.Name, event.MetricName)
	}
	return fmt.Sprintf("Alert: %s", rule.Name)
}
