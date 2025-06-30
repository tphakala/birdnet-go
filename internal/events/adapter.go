package events

// EventPublisherAdapter adapts the EventBus to the EventPublisher interface
// This allows the errors package to publish events without importing the events package
type EventPublisherAdapter struct {
	eventBus *EventBus
}

// NewEventPublisherAdapter creates a new adapter
func NewEventPublisherAdapter(eventBus *EventBus) *EventPublisherAdapter {
	return &EventPublisherAdapter{
		eventBus: eventBus,
	}
}

// TryPublish attempts to publish an event
// It accepts any and type asserts to ErrorEvent
func (a *EventPublisherAdapter) TryPublish(event any) bool {
	// Fast path: check if any consumers are active
	if !HasActiveConsumers() {
		return false
	}
	
	if a.eventBus == nil {
		return false
	}
	
	// Type assert to ErrorEvent
	errorEvent, ok := event.(ErrorEvent)
	if !ok {
		return false
	}
	
	return a.eventBus.TryPublish(errorEvent)
}

// InitializeErrorsIntegration sets up the integration with the errors package
// This should be called after the event bus is initialized
func InitializeErrorsIntegration(setPublisher func(any)) error {
	eb := GetEventBus()
	if eb == nil {
		return nil // Event bus not initialized, skip integration
	}
	
	// Create adapter
	adapter := NewEventPublisherAdapter(eb)
	
	// Set the publisher in the errors package
	setPublisher(adapter)
	
	return nil
}