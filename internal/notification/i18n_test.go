package notification

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotification_WithTitleKey(t *testing.T) {
	t.Parallel()

	notif := NewNotification(TypeInfo, PriorityLow, "Test Title", "Test Message")
	params := map[string]any{"species": "Robin"}

	result := notif.WithTitleKey("notifications.content.detection.title", params)

	assert.Same(t, notif, result, "should return same notification for chaining")
	assert.Equal(t, "notifications.content.detection.title", notif.TitleKey)
	require.NotNil(t, notif.TitleParams)
	assert.Equal(t, "Robin", notif.TitleParams["species"])
}

func TestNotification_WithMessageKey(t *testing.T) {
	t.Parallel()

	notif := NewNotification(TypeInfo, PriorityLow, "Test Title", "Test Message")
	params := map[string]any{"confidence": "95.5"}

	result := notif.WithMessageKey("notifications.content.detection.message", params)

	assert.Same(t, notif, result, "should return same notification for chaining")
	assert.Equal(t, "notifications.content.detection.message", notif.MessageKey)
	require.NotNil(t, notif.MessageParams)
	assert.Equal(t, "95.5", notif.MessageParams["confidence"])
}

func TestNotification_WithTitleKey_NilParams(t *testing.T) {
	t.Parallel()

	notif := NewNotification(TypeInfo, PriorityLow, "Title", "Message")
	notif.WithTitleKey("notifications.content.startup.title", nil)

	assert.Equal(t, "notifications.content.startup.title", notif.TitleKey)
	assert.Nil(t, notif.TitleParams)
}

func TestNotification_WithMessageKey_NilParams(t *testing.T) {
	t.Parallel()

	notif := NewNotification(TypeInfo, PriorityLow, "Title", "Message")
	notif.WithMessageKey("notifications.content.shutdown.message", nil)

	assert.Equal(t, "notifications.content.shutdown.message", notif.MessageKey)
	assert.Nil(t, notif.MessageParams)
}

func TestSanitizeParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		params   map[string]any
		wantNil  bool
		checkKey string
		wantType string // "string", "int", "float64", "bool", "coerced"
	}{
		{
			name:    "nil params returns nil",
			params:  nil,
			wantNil: true,
		},
		{
			name:     "string passes through",
			params:   map[string]any{"key": "value"},
			checkKey: "key",
			wantType: "string",
		},
		{
			name:     "int passes through",
			params:   map[string]any{"key": 42},
			checkKey: "key",
			wantType: "int",
		},
		{
			name:     "int64 passes through",
			params:   map[string]any{"key": int64(100)},
			checkKey: "key",
			wantType: "int64",
		},
		{
			name:     "float64 passes through",
			params:   map[string]any{"key": 3.14},
			checkKey: "key",
			wantType: "float64",
		},
		{
			name:     "bool passes through",
			params:   map[string]any{"key": true},
			checkKey: "key",
			wantType: "bool",
		},
		{
			name:     "struct is coerced to string",
			params:   map[string]any{"key": struct{ X int }{X: 1}},
			checkKey: "key",
			wantType: "coerced",
		},
		{
			name:     "time.Time is coerced to string",
			params:   map[string]any{"key": time.Now()},
			checkKey: "key",
			wantType: "coerced",
		},
		{
			name:     "slice is coerced to string",
			params:   map[string]any{"key": []string{"a", "b"}},
			checkKey: "key",
			wantType: "coerced",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := sanitizeParams(tt.params)

			if tt.wantNil {
				assert.Nil(t, result)
				return
			}

			require.NotNil(t, result)
			val, exists := result[tt.checkKey]
			require.True(t, exists, "key %q should exist", tt.checkKey)

			switch tt.wantType {
			case "string":
				assert.IsType(t, "", val)
			case "int":
				assert.IsType(t, 0, val)
			case "int64":
				assert.IsType(t, int64(0), val)
			case "float64":
				assert.IsType(t, float64(0), val)
			case "bool":
				assert.IsType(t, false, val)
			case "coerced":
				assert.IsType(t, "", val, "non-scalar should be coerced to string")
			}
		})
	}
}

func TestClone_DeepCopiesTranslationKeys(t *testing.T) {
	t.Parallel()

	original := NewNotification(TypeDetection, PriorityMedium, "Detected: Robin", "Confidence: 95.5%")
	original.WithTitleKey("notifications.content.detection.title", map[string]any{"species": "Robin"}).
		WithMessageKey("notifications.content.detection.message", map[string]any{"confidence": "95.5"})

	clone := original.Clone()

	// Verify keys are copied
	assert.Equal(t, original.TitleKey, clone.TitleKey)
	assert.Equal(t, original.MessageKey, clone.MessageKey)
	assert.Equal(t, original.TitleParams, clone.TitleParams)
	assert.Equal(t, original.MessageParams, clone.MessageParams)

	// Verify deep copy — modifying clone params should not affect original
	clone.TitleParams["species"] = "Modified"
	assert.Equal(t, "Robin", original.TitleParams["species"],
		"modifying clone's TitleParams should not affect original")

	clone.MessageParams["confidence"] = "99.9"
	assert.Equal(t, "95.5", original.MessageParams["confidence"],
		"modifying clone's MessageParams should not affect original")
}

func TestClone_NilTranslationParams(t *testing.T) {
	t.Parallel()

	original := NewNotification(TypeInfo, PriorityLow, "Title", "Message")
	original.WithTitleKey("test.key", nil)

	clone := original.Clone()

	assert.Equal(t, "test.key", clone.TitleKey)
	assert.Nil(t, clone.TitleParams)
	assert.Empty(t, clone.MessageKey)
	assert.Nil(t, clone.MessageParams)
}

func TestNotification_BuilderChaining(t *testing.T) {
	t.Parallel()

	notif := NewNotification(TypeSystem, PriorityMedium, "Migration Started", "Migration has started.").
		WithComponent("database").
		WithTitleKey(MsgMigrationStartedTitle, nil).
		WithMessageKey(MsgMigrationStartedMessage, nil)

	assert.Equal(t, "database", notif.Component)
	assert.Equal(t, MsgMigrationStartedTitle, notif.TitleKey)
	assert.Equal(t, MsgMigrationStartedMessage, notif.MessageKey)
}

func TestService_UpdateNotification(t *testing.T) {
	config := DefaultServiceConfig()
	service := NewService(config)
	defer service.Stop()

	// Create a notification first
	notif, err := service.Create(TypeInfo, PriorityLow, "Test", "Message")
	require.NoError(t, err)
	require.NotNil(t, notif)

	// Add translation keys after creation
	notif.WithTitleKey("test.title.key", map[string]any{"param": "value"})

	// Update should persist the changes
	err = service.UpdateNotification(notif)
	require.NoError(t, err)

	// Retrieve and verify
	retrieved, err := service.Get(notif.ID)
	require.NoError(t, err)
	assert.Equal(t, "test.title.key", retrieved.TitleKey)
	require.NotNil(t, retrieved.TitleParams)
	assert.Equal(t, "value", retrieved.TitleParams["param"])
}

func TestService_UpdateNotification_NilReturnsError(t *testing.T) {
	config := DefaultServiceConfig()
	service := NewService(config)
	defer service.Stop()

	err := service.UpdateNotification(nil)
	require.Error(t, err, "should return error for nil notification")
}
