package notification

import (
	"fmt"
	"sync"
)

var (
	instance *Service
	once     sync.Once
	mu       sync.RWMutex
)

// Initialize sets up the global notification service instance
func Initialize(config *ServiceConfig) {
	mu.Lock()
	defer mu.Unlock()

	once.Do(func() {
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
