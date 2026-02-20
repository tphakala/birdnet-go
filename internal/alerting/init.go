package alerting

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// notificationAdapter lazily resolves the notification service to implement
// NotificationCreator. This avoids hard initialization ordering between the
// alerting and notification subsystems.
type notificationAdapter struct{}

func (a *notificationAdapter) CreateAndBroadcast(title, message string) error {
	svc := notification.GetService()
	if svc == nil {
		return nil // notification service not yet initialized
	}
	_, err := svc.Create(notification.TypeSystem, notification.PriorityHigh, title, message)
	return err
}

// Initialize creates and starts the alerting engine.
// It seeds default rules if none exist, creates the engine with the
// action dispatcher, subscribes to the event bus, and loads rules.
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

	// Create dispatcher and engine (adapter lazily resolves notification service)
	dispatcher := NewActionDispatcher(&notificationAdapter{}, log)
	engine := NewEngine(repo, dispatcher.Dispatch, log)

	// Load rules from database
	if err := engine.RefreshRules(ctx); err != nil {
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
// so partial seeds from previous runs self-heal on restart.
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
	return nil
}
