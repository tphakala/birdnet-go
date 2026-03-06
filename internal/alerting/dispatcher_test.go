package alerting

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
)

type mockNotifCreator struct {
	calls    []struct{ title, message string }
	keyCalls []struct {
		title, message, titleKey string
		titleParams              map[string]any
		messageKey               string
		messageParams            map[string]any
	}
}

func (m *mockNotifCreator) CreateAndBroadcast(title, message string) error {
	m.calls = append(m.calls, struct{ title, message string }{title, message})
	return nil
}

func (m *mockNotifCreator) CreateAndBroadcastWithKeys(
	title, message, titleKey string, titleParams map[string]any,
	messageKey string, messageParams map[string]any,
) error {
	m.keyCalls = append(m.keyCalls, struct {
		title, message, titleKey string
		titleParams              map[string]any
		messageKey               string
		messageParams            map[string]any
	}{title, message, titleKey, titleParams, messageKey, messageParams})
	return nil
}

func dispatchTestLogger() logger.Logger {
	return logger.NewSlogLogger(io.Discard, logger.LogLevelError, nil)
}

func TestDispatcher_BellAction(t *testing.T) {
	mock := &mockNotifCreator{}
	dispatcher := NewActionDispatcher(mock, dispatchTestLogger())

	rule := &entities.AlertRule{
		ID:   1,
		Name: "Stream Down",
		Actions: []entities.AlertAction{
			{Target: TargetBell, TemplateTitle: "Stream lost", TemplateMessage: "A stream disconnected"},
		},
	}
	event := &AlertEvent{
		ObjectType: ObjectTypeStream,
		EventName:  EventStreamDisconnected,
		Timestamp:  time.Now(),
	}

	dispatcher.Dispatch(rule, event)

	require.Len(t, mock.calls, 1)
	assert.Equal(t, "Stream lost", mock.calls[0].title)
	assert.Equal(t, "A stream disconnected", mock.calls[0].message)
	assert.Empty(t, mock.keyCalls, "custom template should use CreateAndBroadcast, not WithKeys")
}

func TestDispatcher_DefaultTemplate_UsesKeys(t *testing.T) {
	mock := &mockNotifCreator{}
	dispatcher := NewActionDispatcher(mock, dispatchTestLogger())

	rule := &entities.AlertRule{
		ID:      1,
		Name:    "CPU High",
		NameKey: RuleKeyHighCPUName,
		Actions: []entities.AlertAction{
			{Target: TargetBell}, // empty templates → defaults with keys
		},
	}
	event := &AlertEvent{
		ObjectType: ObjectTypeSystem,
		MetricName: MetricCPUUsage,
		Timestamp:  time.Now(),
	}

	dispatcher.Dispatch(rule, event)

	assert.Empty(t, mock.calls, "default template should use CreateAndBroadcastWithKeys")
	require.Len(t, mock.keyCalls, 1)
	assert.Equal(t, MsgAlertFiredTitle, mock.keyCalls[0].titleKey)
	assert.Equal(t, "CPU High", mock.keyCalls[0].titleParams["rule_name"])
	assert.Equal(t, RuleKeyHighCPUName, mock.keyCalls[0].titleParams["rule_name_key"])
	assert.Equal(t, "CPU High", mock.keyCalls[0].title)
	assert.Empty(t, mock.keyCalls[0].message, "default message should be empty, not duplicate the title")
}

func TestDispatcher_DefaultTemplate_EventKey(t *testing.T) {
	mock := &mockNotifCreator{}
	dispatcher := NewActionDispatcher(mock, dispatchTestLogger())

	rule := &entities.AlertRule{
		ID:      1,
		Name:    "Species Alert",
		NameKey: RuleKeyNewSpeciesName,
		Actions: []entities.AlertAction{
			{Target: TargetBell},
		},
	}
	event := &AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  EventDetectionNewSpecies,
		Timestamp:  time.Now(),
	}

	dispatcher.Dispatch(rule, event)

	require.Len(t, mock.keyCalls, 1)
	assert.Equal(t, MsgAlertFiredTitle, mock.keyCalls[0].titleKey)
	assert.Equal(t, "Species Alert", mock.keyCalls[0].titleParams["rule_name"])
	assert.Equal(t, RuleKeyNewSpeciesName, mock.keyCalls[0].titleParams["rule_name_key"])
	assert.Equal(t, "Species Alert", mock.keyCalls[0].title)
	assert.Empty(t, mock.keyCalls[0].message, "default message should be empty, not duplicate the title")
}

func TestDispatcher_DefaultTemplate_FallbackKey(t *testing.T) {
	mock := &mockNotifCreator{}
	dispatcher := NewActionDispatcher(mock, dispatchTestLogger())

	rule := &entities.AlertRule{
		ID:   1,
		Name: "Generic",
		Actions: []entities.AlertAction{
			{Target: TargetBell},
		},
	}
	event := &AlertEvent{
		ObjectType: ObjectTypeSystem,
		Timestamp:  time.Now(),
	}

	dispatcher.Dispatch(rule, event)

	require.Len(t, mock.keyCalls, 1)
	assert.Equal(t, MsgAlertFiredTitle, mock.keyCalls[0].titleKey)
	assert.Equal(t, "Generic", mock.keyCalls[0].titleParams["rule_name"])
	assert.NotContains(t, mock.keyCalls[0].titleParams, "rule_name_key", "custom rule without NameKey should not have rule_name_key")
	assert.Empty(t, mock.keyCalls[0].message, "default message should be empty, not duplicate the title")
}

func TestDispatcher_MultipleActions(t *testing.T) {
	mock := &mockNotifCreator{}
	dispatcher := NewActionDispatcher(mock, dispatchTestLogger())

	rule := &entities.AlertRule{
		ID:   1,
		Name: "Test",
		Actions: []entities.AlertAction{
			{Target: TargetBell, TemplateTitle: "Bell 1"},
			{Target: TargetBell, TemplateTitle: "Bell 2"},
		},
	}
	event := &AlertEvent{
		ObjectType: ObjectTypeStream,
		EventName:  EventStreamDisconnected,
		Timestamp:  time.Now(),
	}

	dispatcher.Dispatch(rule, event)

	assert.Len(t, mock.calls, 2)
}

func TestDispatcher_UnknownTargetSkipped(t *testing.T) {
	mock := &mockNotifCreator{}
	dispatcher := NewActionDispatcher(mock, dispatchTestLogger())

	rule := &entities.AlertRule{
		ID:   1,
		Name: "Test",
		Actions: []entities.AlertAction{
			{Target: "nonexistent"},
		},
	}
	event := &AlertEvent{
		ObjectType: ObjectTypeStream,
		EventName:  EventStreamDisconnected,
		Timestamp:  time.Now(),
	}

	dispatcher.Dispatch(rule, event)

	assert.Empty(t, mock.calls, "unknown target should not produce a notification")
	assert.Empty(t, mock.keyCalls, "unknown target should not produce a keyed notification")
}

func TestDispatcher_NilNotifCreator(t *testing.T) {
	dispatcher := NewActionDispatcher(nil, dispatchTestLogger())

	rule := &entities.AlertRule{
		ID:   1,
		Name: "Test",
		Actions: []entities.AlertAction{
			{Target: TargetBell},
		},
	}
	event := &AlertEvent{
		ObjectType: ObjectTypeStream,
		EventName:  EventStreamDisconnected,
		Timestamp:  time.Now(),
	}

	// Should not panic
	dispatcher.Dispatch(rule, event)
}

func TestRenderTemplate_WithVariables(t *testing.T) {
	rule := &entities.AlertRule{Name: "My Rule"}
	event := &AlertEvent{EventName: "stream.disconnected"}

	result := renderTemplate("Rule {{rule_name}} fired on {{event_name}}", rule, event)
	assert.Equal(t, "Rule My Rule fired on stream.disconnected", result)
}

func TestRenderTemplate_WithProperties(t *testing.T) {
	rule := &entities.AlertRule{Name: "Stream Alert"}
	event := &AlertEvent{
		EventName: "stream.disconnected",
		Properties: map[string]any{
			"stream_name": "backyard",
			"stream_url":  "rtsp://cam.local/feed",
		},
	}

	result := renderTemplate(
		"{{rule_name}}: {{stream_name}} ({{stream_url}}) - {{event_name}}",
		rule, event,
	)
	assert.Equal(t, "Stream Alert: backyard (rtsp://cam.local/feed) - stream.disconnected", result)
}

func TestDispatcher_DefaultTemplate_MetricMessage(t *testing.T) {
	mock := &mockNotifCreator{}
	dispatcher := NewActionDispatcher(mock, dispatchTestLogger())

	rule := &entities.AlertRule{
		ID:      1,
		Name:    "High CPU usage",
		NameKey: RuleKeyHighCPUName,
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "90", DurationSec: 300},
		},
		Actions: []entities.AlertAction{
			{Target: TargetBell},
		},
	}
	event := &AlertEvent{
		ObjectType: ObjectTypeSystem,
		MetricName: MetricCPUUsage,
		Properties: map[string]any{PropertyValue: 95.3},
		Timestamp:  time.Now(),
	}

	dispatcher.Dispatch(rule, event)

	require.Len(t, mock.keyCalls, 1)
	call := mock.keyCalls[0]
	// Title unchanged
	assert.Equal(t, MsgAlertFiredTitle, call.titleKey)
	// Message should now have key and params
	assert.Equal(t, MsgAlertMetricExceeded, call.messageKey)
	assert.Equal(t, "95.3", call.messageParams["value"])
	assert.Equal(t, "90", call.messageParams["threshold"])
	// English fallback message should be populated
	assert.Contains(t, call.message, "95.3")
	assert.Contains(t, call.message, "90")
}

func TestDispatcher_DefaultTemplate_DetectionMessage(t *testing.T) {
	mock := &mockNotifCreator{}
	dispatcher := NewActionDispatcher(mock, dispatchTestLogger())

	rule := &entities.AlertRule{
		ID:      1,
		Name:    "New species detected",
		NameKey: RuleKeyNewSpeciesName,
		Actions: []entities.AlertAction{
			{Target: TargetBell},
		},
	}
	event := &AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  EventDetectionNewSpecies,
		Properties: map[string]any{
			PropertySpeciesName:    "Eurasian Blue Tit",
			PropertyScientificName: "Cyanistes caeruleus",
			PropertyConfidence:     0.923,
		},
		Timestamp: time.Now(),
	}

	dispatcher.Dispatch(rule, event)

	require.Len(t, mock.keyCalls, 1)
	call := mock.keyCalls[0]
	assert.Equal(t, MsgAlertDetectionOccurred, call.messageKey)
	assert.Equal(t, "Eurasian Blue Tit", call.messageParams["species_name"])
	assert.Equal(t, "92", call.messageParams["confidence"])
	assert.Contains(t, call.message, "Eurasian Blue Tit")
	assert.Contains(t, call.message, "92")
}

func TestDispatcher_DefaultTemplate_ErrorMessage_Classified(t *testing.T) {
	mock := &mockNotifCreator{}
	dispatcher := NewActionDispatcher(mock, dispatchTestLogger())

	rule := &entities.AlertRule{
		ID:      1,
		Name:    "Audio stream error",
		NameKey: RuleKeyStreamErrorName,
		Actions: []entities.AlertAction{
			{Target: TargetBell},
		},
	}
	event := &AlertEvent{
		ObjectType: ObjectTypeStream,
		EventName:  EventStreamError,
		Properties: map[string]any{
			PropertyStreamName: "backyard-cam",
			PropertyError:      "connection timeout",
		},
		Timestamp: time.Now(),
	}

	dispatcher.Dispatch(rule, event)

	require.Len(t, mock.keyCalls, 1)
	call := mock.keyCalls[0]
	// "connection timeout" classifies as "timeout"
	assert.Equal(t, MsgAlertErrorPrefix+".timeout", call.messageKey)
	assert.Equal(t, "backyard-cam", call.messageParams["source_name"])
	assert.Equal(t, "connection timeout", call.messageParams["error"])
	// Fallback uses the friendly message, not the raw error
	assert.Contains(t, call.message, "backyard-cam")
	assert.NotContains(t, call.message, "connection timeout", "should use friendly message, not raw error")
}

func TestDispatcher_DefaultTemplate_ErrorMessage_Unclassified(t *testing.T) {
	mock := &mockNotifCreator{}
	dispatcher := NewActionDispatcher(mock, dispatchTestLogger())

	rule := &entities.AlertRule{
		ID:      1,
		Name:    "BirdWeather upload failed",
		NameKey: RuleKeyBirdWeatherName,
		Actions: []entities.AlertAction{
			{Target: TargetBell},
		},
	}
	event := &AlertEvent{
		ObjectType: ObjectTypeIntegration,
		EventName:  EventBirdWeatherFailed,
		Properties: map[string]any{
			PropertyError: "species not in taxonomy",
		},
		Timestamp: time.Now(),
	}

	dispatcher.Dispatch(rule, event)

	require.Len(t, mock.keyCalls, 1)
	call := mock.keyCalls[0]
	// Unrecognized error falls back to generic key with raw error
	assert.Equal(t, MsgAlertErrorOccurred, call.messageKey)
	assert.Equal(t, "species not in taxonomy", call.messageParams["error"])
	assert.Contains(t, call.message, "species not in taxonomy")
}

func TestDispatcher_DefaultTemplate_DisconnectMessage(t *testing.T) {
	mock := &mockNotifCreator{}
	dispatcher := NewActionDispatcher(mock, dispatchTestLogger())

	rule := &entities.AlertRule{
		ID:      1,
		Name:    "Audio stream disconnected",
		NameKey: RuleKeyStreamDiscName,
		Actions: []entities.AlertAction{
			{Target: TargetBell},
		},
	}
	event := &AlertEvent{
		ObjectType: ObjectTypeStream,
		EventName:  EventStreamDisconnected,
		Properties: map[string]any{
			PropertyStreamName: "front-yard",
		},
		Timestamp: time.Now(),
	}

	dispatcher.Dispatch(rule, event)

	require.Len(t, mock.keyCalls, 1)
	call := mock.keyCalls[0]
	assert.Equal(t, MsgAlertDisconnected, call.messageKey)
	assert.Equal(t, "front-yard", call.messageParams["source_name"])
	assert.Contains(t, call.message, "front-yard")
}

func TestDispatcher_DefaultTemplate_NoProperties_GracefulFallback(t *testing.T) {
	mock := &mockNotifCreator{}
	dispatcher := NewActionDispatcher(mock, dispatchTestLogger())

	rule := &entities.AlertRule{
		ID:   1,
		Name: "High CPU usage",
		Conditions: []entities.AlertCondition{
			{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "90"},
		},
		Actions: []entities.AlertAction{
			{Target: TargetBell},
		},
	}
	// Event with no properties (e.g., TestFireRule path)
	event := &AlertEvent{
		ObjectType: ObjectTypeSystem,
		MetricName: MetricCPUUsage,
		Properties: map[string]any{"test": true},
		Timestamp:  time.Now(),
	}

	dispatcher.Dispatch(rule, event)

	require.Len(t, mock.keyCalls, 1)
	call := mock.keyCalls[0]
	// Should still work, just without a message (no value property)
	assert.Empty(t, call.messageKey, "should not set message key when required properties are missing")
	assert.Empty(t, call.message)
}
