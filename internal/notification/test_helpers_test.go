// Package notification provides test helpers for notification package tests.
// This file consolidates common test utilities to reduce code duplication
// and ensure consistent use of testify assertions across all test files.
package notification

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Service Test Helpers
// =============================================================================

// newTestService creates a Service with default test configuration.
// The caller should defer service.Stop() to clean up.
func newTestService(t *testing.T) *Service {
	t.Helper()
	return newTestServiceWithConfig(t, nil)
}

// newTestServiceWithConfig creates a Service with custom configuration.
// If config is nil, default test configuration is used.
// The caller should defer service.Stop() to clean up.
func newTestServiceWithConfig(t *testing.T, config *ServiceConfig) *Service {
	t.Helper()
	if config == nil {
		config = &ServiceConfig{
			MaxNotifications:   100,
			CleanupInterval:    5 * time.Minute,
			RateLimitWindow:    1 * time.Minute,
			RateLimitMaxEvents: 100,
		}
	}
	service := NewService(config)
	require.NotNil(t, service, "NewService should return non-nil service")
	return service
}

// newTestServiceForRateLimiting creates a Service configured for rate limit testing.
func newTestServiceForRateLimiting(t *testing.T, maxEvents int) *Service {
	t.Helper()
	return newTestServiceWithConfig(t, &ServiceConfig{
		MaxNotifications:   100,
		CleanupInterval:    5 * time.Minute,
		RateLimitWindow:    time.Second,
		RateLimitMaxEvents: maxEvents,
	})
}

// =============================================================================
// Notification Test Helpers
// =============================================================================

// createTestNotification creates a basic notification for testing.
func createTestNotification(notifType Type, priority Priority) *Notification {
	return NewNotification(notifType, priority, "Test Title", "Test Message")
}

// createTestNotificationWithComponent creates a notification with a component for testing.
func createTestNotificationWithComponent(notifType Type, priority Priority, component string) *Notification {
	return NewNotification(notifType, priority, "Test Title", "Test Message").
		WithComponent(component)
}

// =============================================================================
// Assertion Helpers
// =============================================================================

// assertNotificationCount verifies the number of notifications of a given type.
func assertNotificationCount(t *testing.T, svc *Service, notifType Type, expected int) {
	t.Helper()
	notifications, err := svc.List(&FilterOptions{
		Types: []Type{notifType},
		Limit: 100,
	})
	require.NoError(t, err, "List should not return error")
	assert.Len(t, notifications, expected, "notification count mismatch for type %s", notifType)
}

// assertServiceNotificationExists verifies a notification exists in the service and returns it.
func assertServiceNotificationExists(t *testing.T, svc *Service, id string) *Notification {
	t.Helper()
	notif, err := svc.Get(id)
	require.NoError(t, err, "Get should not return error for existing notification")
	require.NotNil(t, notif, "notification should exist")
	return notif
}

// assertServiceNotificationNotExists verifies a notification does not exist in the service.
func assertServiceNotificationNotExists(t *testing.T, svc *Service, id string) {
	t.Helper()
	notif, err := svc.Get(id)
	require.Error(t, err, "Get should return error for non-existing notification")
	require.Nil(t, notif, "notification should not exist")
}

// assertNotificationMetadata verifies notification metadata contains expected values.
func assertNotificationMetadata(t *testing.T, notif *Notification, key string, expected any) {
	t.Helper()
	require.NotNil(t, notif.Metadata, "notification metadata should not be nil")
	actual, exists := notif.Metadata[key]
	require.True(t, exists, "metadata key %q should exist", key)
	assert.Equal(t, expected, actual, "metadata value mismatch for key %q", key)
}

// =============================================================================
// Circuit Breaker Test Helpers
// =============================================================================

// CircuitBreakerTestConfig provides common circuit breaker test configurations.
type CircuitBreakerTestConfig struct {
	MaxFailures         int
	Timeout             time.Duration
	HalfOpenMaxRequests int
}

// DefaultCircuitBreakerTestConfig returns a standard test configuration.
func DefaultCircuitBreakerTestConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		MaxFailures:         3,
		Timeout:             100 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
}

// newTestCircuitBreaker creates a circuit breaker with test configuration.
func newTestCircuitBreaker(t *testing.T, config CircuitBreakerConfig) *PushCircuitBreaker {
	t.Helper()
	cb := NewPushCircuitBreaker(config, nil, "test-provider")
	require.NotNil(t, cb, "NewPushCircuitBreaker should return non-nil")
	return cb
}

// assertCircuitState verifies the circuit breaker is in the expected state.
func assertCircuitState(t *testing.T, cb *PushCircuitBreaker, expected CircuitState) {
	t.Helper()
	assert.Equal(t, expected, cb.State(), "circuit breaker state mismatch")
}

// assertCircuitHealthy verifies the circuit breaker is healthy.
func assertCircuitHealthy(t *testing.T, cb *PushCircuitBreaker, expected bool) {
	t.Helper()
	assert.Equal(t, expected, cb.IsHealthy(), "circuit breaker health mismatch")
}

// openCircuitBreaker triggers failures to open the circuit breaker.
func openCircuitBreaker(t *testing.T, cb *PushCircuitBreaker, failures int) {
	t.Helper()
	ctx := t.Context()
	for range failures {
		_ = cb.Call(ctx, func(_ context.Context) error {
			return assert.AnError
		})
	}
}

// =============================================================================
// Mock Provider Helpers
// =============================================================================

// mockTestProvider implements Provider for testing.
type mockTestProvider struct {
	name           string
	enabled        bool
	types          map[Type]bool
	validateErr    error
	sendErr        error
	sendDelay      time.Duration
	sendCh         chan *Notification
	validateCalled int
	sendCalled     int
	mu             sync.Mutex
}

// newMockTestProvider creates a mock provider for testing.
func newMockTestProvider(name string, enabled bool, types ...Type) *mockTestProvider {
	typeMap := make(map[Type]bool)
	for _, t := range types {
		typeMap[t] = true
	}
	// If no types specified, enable all
	if len(types) == 0 {
		typeMap[TypeInfo] = true
		typeMap[TypeWarning] = true
		typeMap[TypeError] = true
		typeMap[TypeDetection] = true
		typeMap[TypeSystem] = true
	}
	return &mockTestProvider{
		name:    name,
		enabled: enabled,
		types:   typeMap,
		sendCh:  make(chan *Notification, 10),
	}
}

func (m *mockTestProvider) GetName() string {
	return m.name
}

func (m *mockTestProvider) ValidateConfig() error {
	m.mu.Lock()
	m.validateCalled++
	m.mu.Unlock()
	return m.validateErr
}

func (m *mockTestProvider) Send(ctx context.Context, n *Notification) error {
	m.mu.Lock()
	m.sendCalled++
	m.mu.Unlock()

	// Respect context cancellation during delays
	if m.sendDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.sendDelay):
		}
	}

	if m.sendErr != nil {
		return m.sendErr
	}

	select {
	case m.sendCh <- n:
	default:
	}
	return nil
}

func (m *mockTestProvider) SupportsType(t Type) bool {
	return m.types[t]
}

func (m *mockTestProvider) IsEnabled() bool {
	return m.enabled
}

func (m *mockTestProvider) getSendCalled() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sendCalled
}

func (m *mockTestProvider) getValidateCalled() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.validateCalled
}

// =============================================================================
// Health Check Test Helpers
// =============================================================================

// newTestHealthChecker creates a health checker with default test configuration.
func newTestHealthChecker(t *testing.T) *HealthChecker {
	t.Helper()
	config := DefaultHealthCheckConfig()
	hc := NewHealthChecker(config, nil, nil)
	require.NotNil(t, hc, "NewHealthChecker should return non-nil")
	return hc
}

// =============================================================================
// Concurrency Test Helpers
// =============================================================================

// runConcurrent runs a function concurrently n times and waits for completion.
func runConcurrent(t *testing.T, n int, fn func(id int)) {
	t.Helper()
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			fn(i)
		})
	}
	wg.Wait()
}

// waitForCondition waits for a condition to become true within a timeout.
// Uses testify's require.Eventually for consistent polling behavior.
func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool, msg string) {
	t.Helper()
	const pollInterval = 10 * time.Millisecond
	require.Eventually(t, condition, timeout, pollInterval, msg)
}

// =============================================================================
// Store Test Helpers (migrated from store_test.go)
// =============================================================================

// assertStoreUnreadCount checks the unread count in a store.
func assertStoreUnreadCount(t *testing.T, store *InMemoryStore, expected int, msg string) {
	t.Helper()
	count, err := store.GetUnreadCount()
	require.NoError(t, err, "GetUnreadCount should not return error")
	assert.Equal(t, expected, count, "%s: unread count mismatch", msg)
}

// mustStoreGet retrieves a notification from store, failing if not found.
func mustStoreGet(t *testing.T, store *InMemoryStore, id string) *Notification {
	t.Helper()
	notif, err := store.Get(id)
	require.NoError(t, err, "Get should not return error")
	require.NotNil(t, notif, "notification should exist")
	return notif
}

// mustStoreSave saves a notification to store, failing on error.
func mustStoreSave(t *testing.T, store *InMemoryStore, notif *Notification) {
	t.Helper()
	err := store.Save(notif)
	require.NoError(t, err, "Save should not return error")
}

// mustStoreUpdate updates a notification in store, failing on error.
func mustStoreUpdate(t *testing.T, store *InMemoryStore, notif *Notification) {
	t.Helper()
	err := store.Update(notif)
	require.NoError(t, err, "Update should not return error")
}

// mustStoreDelete deletes a notification from store, failing on error.
func mustStoreDelete(t *testing.T, store *InMemoryStore, id string) {
	t.Helper()
	err := store.Delete(id)
	require.NoError(t, err, "Delete should not return error")
}

// assertStoreNotificationExists checks if a notification exists in the store.
func assertStoreNotificationExists(t *testing.T, store *InMemoryStore, id string, shouldExist bool) {
	t.Helper()
	notif, err := store.Get(id)
	if shouldExist {
		require.NoError(t, err, "Get should not return error for existing notification")
		require.NotNil(t, notif, "notification should exist")
	} else {
		require.Error(t, err, "Get should return error for non-existing notification")
		require.Nil(t, notif, "notification should not exist")
	}
}
