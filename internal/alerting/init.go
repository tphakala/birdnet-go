package alerting

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// notificationAdapter wraps notification.Service to implement NotificationCreator.
type notificationAdapter struct {
	svc *notification.Service
}

func (a *notificationAdapter) CreateAndBroadcast(title, message string) error {
	_, err := a.svc.Create(notification.TypeSystem, notification.PriorityHigh, title, message)
	return err
}

// Initialize creates and starts the alerting engine.
// It seeds default rules if none exist, creates the engine with the
// action dispatcher, subscribes to the event bus, and loads rules.
// Returns nil engine if the notification service is not initialized.
func Initialize(
	repo repository.AlertRuleRepository,
	eventBus *AlertEventBus,
	log logger.Logger,
) (*Engine, error) {
	ctx := context.Background()

	// Seed default rules if the table is empty
	if err := seedDefaultRules(ctx, repo, log); err != nil {
		return nil, err
	}

	// Build the notification adapter (may be nil if notification service not ready)
	var notifCreator NotificationCreator
	if notification.IsInitialized() {
		notifCreator = &notificationAdapter{svc: notification.GetService()}
	}

	// Create dispatcher and engine
	dispatcher := NewActionDispatcher(notifCreator, log)
	engine := NewEngine(repo, dispatcher.Dispatch, log)

	// Load rules from database
	if err := engine.RefreshRules(ctx); err != nil {
		return nil, err
	}

	// Subscribe engine to the event bus
	eventBus.Subscribe(engine.HandleEvent)

	log.Info("alerting engine initialized",
		logger.Int("rules_loaded", len(engine.rules)))

	return engine, nil
}

// seedDefaultRules seeds the built-in default rules if no rules exist yet.
func seedDefaultRules(ctx context.Context, repo repository.AlertRuleRepository, log logger.Logger) error {
	existing, err := repo.ListRules(ctx, repository.AlertRuleFilter{})
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}

	defaults := DefaultRules()
	for i := range defaults {
		if err := repo.CreateRule(ctx, &defaults[i]); err != nil {
			return err
		}
	}
	log.Info("seeded default alert rules", logger.Int("count", len(defaults)))
	return nil
}
