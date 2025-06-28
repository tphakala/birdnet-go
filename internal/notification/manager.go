package notification

import (
	"sync"
)

var (
	instance *Service
	once     sync.Once
	mu       sync.RWMutex
)

// Initialize sets up the global notification service instance
func Initialize(config *ServiceConfig) {
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

// SetService allows setting a custom service instance (mainly for testing)
func SetService(service *Service) {
	mu.Lock()
	defer mu.Unlock()
	instance = service
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
