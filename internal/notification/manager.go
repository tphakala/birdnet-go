package notification

import (
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

// ResetForTest returns the global notification service to its uninitialized
// state (nil instance, fresh sync.Once). It exists only for tests: the singleton
// is guarded by a sync.Once that otherwise fires permanently for the rest of a
// package's test run, so a test that calls Initialize would leak the service into
// unrelated tests under shuffled ordering, and a test asserting the not-initialized
// path could never run after it. It must never be called from production code.
//
// The caller is responsible for stopping any running service (svc.Stop) before
// resetting, and for serializing use: tests calling this must not run in parallel
// with anything that reads or initializes the singleton.
func ResetForTest() {
	mu.Lock()
	defer mu.Unlock()
	instance = nil
	once = sync.Once{}
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
