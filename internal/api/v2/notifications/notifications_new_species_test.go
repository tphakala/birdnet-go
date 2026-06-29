package notifications

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// parseJSONResponse unmarshals JSON response body into target struct
func parseJSONResponse(body []byte, target any) error {
	return json.Unmarshal(body, target)
}

func TestCreateTestNewSpeciesNotification_ServiceNotInitialized(t *testing.T) {
	t.Parallel()

	// This validates the requireNotificationService guard when no service is
	// available. The api/v2 test suite never initializes the process-global
	// singleton (every test injects an isolated instance), so a controller with
	// no injected service resolves to nil and the middleware must return 503.
	require.False(t, notification.IsInitialized(),
		"no api/v2 test may initialize the global notification singleton; this test relies on it staying unset")

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/notifications/test/new-species", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	controller := New(&apicore.Core{}, nil, nil)
	controller.Settings.Store(&conf.Settings{})

	// Call through middleware to test the guard
	handler := controller.requireNotificationService(controller.CreateTestNewSpeciesNotification)
	err := handler(c)
	require.NoError(t, err)

	// Verify we get the expected service unavailable error
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "Notification service not available")
}

func TestCreateTestNewSpeciesNotification_Success(t *testing.T) {
	// No t.Parallel(): this test publishes to the process-global settings
	// singleton via apitest.PublishTestSettings (CreateTestNewSpeciesNotification reads
	// the live snapshot through currentSettings(), which consults
	// conf.GetSettings() first). The notification service is fully isolated
	// per test via dependency injection.
	config := &notification.ServiceConfig{
		Debug:              true,
		MaxNotifications:   100,
		CleanupInterval:    30 * time.Minute,
		RateLimitWindow:    1 * time.Minute,
		RateLimitMaxEvents: 10,
	}

	service := notification.NewService(config)
	t.Cleanup(service.Stop)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/notifications/test/new-species", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	controller := New(&apicore.Core{}, service, nil)
	// Build a minimal settings snapshot with only the fields this test needs,
	// then publish it once (the empty base matches the original test).
	settings := &conf.Settings{}
	settings.Security.Host = "localhost"
	settings.WebServer.Port = "8080"
	settings.Main.TimeAs24h = true
	// Set default templates from config.yaml
	settings.Notification.Templates.NewSpecies.Title = "New Species: {{.CommonName}}"
	settings.Notification.Templates.NewSpecies.Message = "First detection of {{.CommonName}} ({{.ScientificName}}) with {{.ConfidencePercent}}% confidence at {{.DetectionTime}}. View: {{.DetectionURL}}"
	controller.Settings.Store(settings)
	// CreateTestNewSpeciesNotification reads the live snapshot via currentSettings();
	// publish the controller's settings so the read resolves to them.
	apitest.PublishTestSettings(t, settings)

	err := controller.CreateTestNewSpeciesNotification(c)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse response to verify notification structure
	var response notification.Notification
	err = parseJSONResponse(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify notification fields match detection_consumer.go patterns
	assert.Equal(t, notification.TypeDetection, response.Type)
	assert.Equal(t, notification.PriorityHigh, response.Priority)
	assert.Equal(t, "detection", response.Component)
	assert.Equal(t, notification.StatusUnread, response.Status)
	// Verify default title format matches config.yaml template
	assert.Equal(t, "New Species: Test Bird Species", response.Title)
	// Verify message matches default template: includes confidence and time, not location
	assert.Contains(t, response.Message, "First detection of Test Bird Species")
	assert.Contains(t, response.Message, "Testus birdicus")
	assert.Contains(t, response.Message, "99% confidence")
	assert.NotEmpty(t, response.ID)
	assert.False(t, response.Timestamp.IsZero())

	// Verify metadata fields match detection_consumer.go
	require.NotNil(t, response.Metadata)
	assert.Equal(t, "Test Bird Species", response.Metadata["species"])
	assert.Equal(t, "Testus birdicus", response.Metadata["scientific_name"])
	assert.InDelta(t, 0.99, response.Metadata["confidence"], 0.001)
	assert.Equal(t, "Test Location (Sample Data)", response.Metadata["location"])
	assert.Equal(t, true, response.Metadata["is_new_species"])
	assert.InDelta(t, 0, response.Metadata["days_since_first_seen"], 0.001)

	// Verify 24-hour expiry
	require.NotNil(t, response.ExpiresAt)
	expectedExpiry := response.Timestamp.Add(24 * time.Hour)
	assert.WithinDuration(t, expectedExpiry, *response.ExpiresAt, time.Second)
}
