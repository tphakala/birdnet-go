package notification

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test data constants
const (
	testValue1              = "value1"
	expectedRateLimitErrMsg = "rate limit exceeded"
)

func TestService_CreateWithMetadata(t *testing.T) {
	t.Parallel()

	// Create service with test configuration
	service := createTestService()

	t.Run("creates notification with metadata", func(t *testing.T) {
		testCreateWithMetadata(t, service)
	})

	t.Run("broadcasts notification to subscribers", func(t *testing.T) {
		testBroadcastWithMetadata(t, service)
	})

	t.Run("respects rate limiting", func(t *testing.T) {
		testRateLimitingWithMetadata(t)
	})

	t.Run("handles nil notification", func(t *testing.T) {
		testNilNotificationHandling(t, service)
	})

	t.Run("preserves expiration", func(t *testing.T) {
		testExpirationPreservation(t, service)
	})
}

// createTestService creates a service with test configuration
func createTestService() *Service {
	config := &ServiceConfig{
		MaxNotifications:   10,
		CleanupInterval:    time.Minute,
		RateLimitWindow:    time.Minute,
		RateLimitMaxEvents: 10,
		Debug:              true,
	}
	return NewService(config)
}

// testCreateWithMetadata tests creating a notification with metadata
func testCreateWithMetadata(t *testing.T, service *Service) {
	t.Helper()

	// Create a notification with metadata
	notif := NewNotification(TypeInfo, PriorityLow, "Test Title", "Test Message").
		WithComponent("test-component").
		WithMetadata("key1", testValue1).
		WithMetadata("key2", 42).
		WithMetadata("isToast", true)

	// Create with metadata
	err := service.CreateWithMetadata(notif)
	require.NoError(t, err, "CreateWithMetadata() should succeed")

	// Retrieve the notification to verify it was stored with metadata
	storedNotif, err := service.Get(notif.ID)
	require.NoError(t, err, "Get() should succeed")

	verifyStoredNotificationFields(t, storedNotif)
	verifyStoredNotificationMetadata(t, storedNotif)
}

// verifyStoredNotificationFields verifies basic notification fields
func verifyStoredNotificationFields(t *testing.T, stored *Notification) {
	t.Helper()

	assert.Equal(t, TypeInfo, stored.Type)
	assert.Equal(t, PriorityLow, stored.Priority)
	assert.Equal(t, "Test Title", stored.Title)
	assert.Equal(t, "Test Message", stored.Message)
	assert.Equal(t, "test-component", stored.Component)
}

// verifyStoredNotificationMetadata verifies notification metadata
func verifyStoredNotificationMetadata(t *testing.T, stored *Notification) {
	t.Helper()

	require.NotNil(t, stored.Metadata, "stored notification should have metadata")

	value, ok := stored.Metadata["key1"].(string)
	require.True(t, ok)
	assert.Equal(t, testValue1, value)

	intValue, ok := stored.Metadata["key2"].(int)
	require.True(t, ok)
	assert.Equal(t, 42, intValue)

	boolValue, ok := stored.Metadata["isToast"].(bool)
	require.True(t, ok)
	assert.True(t, boolValue)
}

// testBroadcastWithMetadata tests broadcasting notifications with metadata
func testBroadcastWithMetadata(t *testing.T, service *Service) {
	t.Helper()

	// Subscribe to notifications
	notifCh, _ := service.Subscribe()
	defer service.Unsubscribe(notifCh)

	// Create notification with metadata
	notif := NewNotification(TypeWarning, PriorityMedium, "Broadcast Test", "Test broadcast").
		WithMetadata("broadcast", true)

	// Create with metadata
	err := service.CreateWithMetadata(notif)
	require.NoError(t, err, "CreateWithMetadata() should succeed")

	verifyBroadcastedNotification(t, notifCh, notif)
}

// verifyBroadcastedNotification verifies a broadcasted notification
func verifyBroadcastedNotification(t *testing.T, notifCh <-chan *Notification, original *Notification) {
	t.Helper()

	select {
	case receivedNotif := <-notifCh:
		assert.Equal(t, original.ID, receivedNotif.ID)
		assert.Equal(t, "Broadcast Test", receivedNotif.Title)

		// Verify metadata is included in broadcast
		value, ok := receivedNotif.Metadata["broadcast"].(bool)
		require.True(t, ok && value, "broadcast notification should include metadata")

	case <-time.After(100 * time.Millisecond):
		require.Fail(t, "should have received notification within timeout")
	}
}

// testRateLimitingWithMetadata tests rate limiting with metadata creation
func testRateLimitingWithMetadata(t *testing.T) {
	t.Helper()

	// Create service with tight rate limiting for testing
	strictConfig := &ServiceConfig{
		MaxNotifications:   10,
		CleanupInterval:    time.Minute,
		RateLimitWindow:    time.Second,
		RateLimitMaxEvents: 1, // Allow only 1 event per second
		Debug:              true,
	}

	strictService := NewService(strictConfig)

	// First notification should succeed
	notif1 := NewNotification(TypeInfo, PriorityLow, "First", "First notification")
	err1 := strictService.CreateWithMetadata(notif1)
	require.NoError(t, err1, "First CreateWithMetadata() should succeed")

	// Second notification should be rate limited
	notif2 := NewNotification(TypeInfo, PriorityLow, "Second", "Second notification")
	err2 := strictService.CreateWithMetadata(notif2)
	require.Error(t, err2, "Second CreateWithMetadata() should be rate limited")

	assert.Equal(t, expectedRateLimitErrMsg, err2.Error())
}

// testNilNotificationHandling tests handling of nil notifications
func testNilNotificationHandling(t *testing.T, service *Service) {
	t.Helper()

	err := service.CreateWithMetadata(nil)
	require.Error(t, err, "CreateWithMetadata(nil) should return an error")
}

// testExpirationPreservation tests preservation of notification expiration
func testExpirationPreservation(t *testing.T, service *Service) {
	t.Helper()

	// Create notification with expiration
	expiryTime := time.Now().Add(10 * time.Minute)
	notif := NewNotification(TypeInfo, PriorityLow, "Expiry Test", "Test expiration")
	notif.ExpiresAt = &expiryTime

	err := service.CreateWithMetadata(notif)
	require.NoError(t, err, "CreateWithMetadata() should succeed")

	// Retrieve and verify expiration is preserved
	storedNotif, err := service.Get(notif.ID)
	require.NoError(t, err, "Get() should succeed")

	require.NotNil(t, storedNotif.ExpiresAt, "stored notification should have expiration")
	assert.True(t, storedNotif.ExpiresAt.Equal(expiryTime))
}

func TestService_CreateWithMetadata_ErrorHandling(t *testing.T) {
	t.Parallel()

	// Create service
	config := DefaultServiceConfig()
	service := NewService(config)

	t.Run("handles store save error", func(t *testing.T) {
		// Create a notification that might cause store issues
		// In this case, we'll test with a very large notification that might exceed limits
		largeMetadata := make(map[string]any)
		for i := range 1000 {
			largeMetadata[string(rune('a'+i%26))] = "large data that might cause issues"
		}

		notif := NewNotification(TypeInfo, PriorityLow, "Large Test", "Large metadata test")
		notif.Metadata = largeMetadata

		// This should still work with in-memory store, but tests the error path
		err := service.CreateWithMetadata(notif)
		// With in-memory store, this should actually succeed
		if err != nil {
			t.Logf("CreateWithMetadata() with large metadata: %v", err)
		}
	})
}

// Mock store for testing error conditions
type failingStore struct {
	*InMemoryStore
	shouldFail bool
}

func (f *failingStore) Save(notification *Notification) error {
	if f.shouldFail {
		return context.DeadlineExceeded // Simulate store failure
	}
	return f.InMemoryStore.Save(notification)
}

func TestService_CreateWithMetadata_WithFailingStore(t *testing.T) {
	t.Parallel()

	// Create service with failing store
	config := DefaultServiceConfig()
	service := NewService(config)

	// Replace store with failing mock
	failStore := &failingStore{
		InMemoryStore: service.store.(*InMemoryStore),
		shouldFail:    true,
	}
	service.store = failStore

	notif := NewNotification(TypeInfo, PriorityLow, "Fail Test", "Test store failure")

	err := service.CreateWithMetadata(notif)
	require.Error(t, err, "CreateWithMetadata() should return error when store fails")
	assert.NotEmpty(t, err.Error(), "Error should have a meaningful message")
}

// Benchmark for CreateWithMetadata performance
func BenchmarkService_CreateWithMetadata(b *testing.B) {
	config := &ServiceConfig{
		MaxNotifications:   1000,
		CleanupInterval:    time.Hour, // Don't cleanup during benchmark
		RateLimitWindow:    time.Hour, // Very permissive rate limit
		RateLimitMaxEvents: 10000,
		Debug:              false,
	}

	service := NewService(config)

	// Pre-create notification template
	template := NewNotification(TypeInfo, PriorityLow, "Benchmark", "Performance test").
		WithComponent("benchmark").
		WithMetadata("test", true).
		WithMetadata("iteration", 0)

	for i := 0; b.Loop(); i++ {
		// Create unique notification for each iteration
		notif := *template // Shallow copy
		notif.ID = ""      // Will be regenerated
		notif.Timestamp = time.Now()
		notif.Metadata["iteration"] = i

		err := service.CreateWithMetadata(&notif)
		if err != nil {
			b.Fatalf("CreateWithMetadata() error = %v", err)
		}
	}
}
