package notification

import (
	"fmt"
	"sync"
	"sync/atomic"
)

var (
	instance *Service
	once     sync.Once
	mu       sync.RWMutex

	// alertEngineActive indicates whether the alerting rules engine is running.
	// When true, the detection notification consumer skips its hardcoded logic
	// since the alert engine handles detection notifications via rules.
	alertEngineActive atomic.Bool
)

// Initialize sets up the global notification service instance
func Initialize(config *ServiceConfig) {
	once.Do(func() {
		mu.Lock()
		defer mu.Unlock()
		instance = NewService(config)
	})
}

// GetService returns the global notification service instance
func GetService() *Service {
	mu.RLock()
	defer mu.RUnlock()
	return instance
}

// SetServiceForTesting allows setting a custom service instance for testing only
// It returns an error if the service is already initialized in production
func SetServiceForTesting(service *Service) error {
	mu.Lock()
	defer mu.Unlock()

	if instance != nil {
		return fmt.Errorf("notification service already initialized")
	}

	instance = service
	return nil
}

// MustGetService returns the service instance or panics if not initialized
func MustGetService() *Service {
	service := GetService()
	if service == nil {
		panic("notification service not initialized")
	}
	return service
}

// IsInitialized checks if the notification service has been initialized
func IsInitialized() bool {
	mu.RLock()
	defer mu.RUnlock()
	return instance != nil
}

// SetAlertEngineActive marks the alert engine as active. Called by the alerting
// package during initialization to signal that the rules engine handles
// detection notifications, bypassing the hardcoded consumer logic.
func SetAlertEngineActive(active bool) {
	alertEngineActive.Store(active)
}

// IsAlertEngineActive returns whether the alerting rules engine is running.
func IsAlertEngineActive() bool {
	return alertEngineActive.Load()
}
