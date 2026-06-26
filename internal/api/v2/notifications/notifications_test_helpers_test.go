package notifications

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// testFailFastTimeout is a short timeout injected into deliberate-failure probe
// paths so the unreachable-host test does not wait the full production default
// for each scheme. It mirrors the package-api constant of the same name.
const testFailFastTimeout = 200 * time.Millisecond

// mockHandler creates a Handler with minimal setup for testing (debug settings,
// no injected services). It mirrors the old package-api mockController helper.
func mockHandler() *Handler {
	h := New(&apicore.Core{APILogger: nil}, nil, nil)
	h.Settings.Store(&conf.Settings{
		WebServer: conf.WebServerSettings{
			Debug: true,
		},
	})
	return h
}

// newNotificationTestHandler builds a Handler wired to an isolated, per-test
// notification service injected through New. The service is stopped via
// t.Cleanup so its cleanupLoop goroutine does not leak. Because no process-global
// state is touched, tests using this helper are safe to run with t.Parallel().
func newNotificationTestHandler(t *testing.T) (*Handler, *notification.Service) {
	t.Helper()

	config := &notification.ServiceConfig{
		Debug:              true,
		MaxNotifications:   100,
		CleanupInterval:    30 * time.Minute,
		RateLimitWindow:    1 * time.Minute,
		RateLimitMaxEvents: 10,
	}

	service := notification.NewService(config)
	t.Cleanup(service.Stop)

	h := New(&apicore.Core{}, service, nil)
	h.Settings.Store(&conf.Settings{})

	return h, service
}

// newToastTestHandler builds a Handler wired to an isolated, per-test
// notification service for the toast SSE-formatting tests. The service is
// stopped via tb.Cleanup so its cleanupLoop goroutine does not leak.
func newToastTestHandler(tb testing.TB) (*Handler, *notification.Service) {
	tb.Helper()
	service := notification.NewService(notification.DefaultServiceConfig())
	tb.Cleanup(service.Stop)
	h := mockHandler()
	h.notificationService = service
	return h, service
}

// assertEventDataNil checks that a field in eventData is nil.
func assertEventDataNil(t *testing.T, eventData map[string]any, field, context string) {
	t.Helper()
	assert.Nil(t, eventData[field], "Event data %s should be nil for %s", field, context)
}

// assertEventDataEmpty checks that a string field in eventData is empty.
func assertEventDataEmpty(t *testing.T, eventData map[string]any, field, context string) {
	t.Helper()
	assert.Empty(t, eventData[field], "Event data %s should be empty string for %s", field, context)
}

// assertEventDataMissing checks that a field is not present in eventData.
func assertEventDataMissing(t *testing.T, eventData map[string]any, field string) {
	t.Helper()
	_, exists := eventData[field]
	assert.False(t, exists, "Event data should not include %s when not set", field)
}

// assertEventDataValue checks a specific field value in eventData.
func assertEventDataValue(t *testing.T, eventData map[string]any, field string, expected any) {
	t.Helper()
	assert.Equal(t, expected, eventData[field], "Event data %s mismatch", field)
}
