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
	calls []struct{ title, message string }
}

func (m *mockNotifCreator) CreateAndBroadcast(title, message string) error {
	m.calls = append(m.calls, struct{ title, message string }{title, message})
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
}

func TestDispatcher_DefaultTemplate(t *testing.T) {
	mock := &mockNotifCreator{}
	dispatcher := NewActionDispatcher(mock, dispatchTestLogger())

	rule := &entities.AlertRule{
		ID:   1,
		Name: "CPU High",
		Actions: []entities.AlertAction{
			{Target: TargetBell}, // empty templates → defaults
		},
	}
	event := &AlertEvent{
		ObjectType: ObjectTypeSystem,
		MetricName: MetricCPUUsage,
		Timestamp:  time.Now(),
	}

	dispatcher.Dispatch(rule, event)

	require.Len(t, mock.calls, 1)
	assert.Contains(t, mock.calls[0].title, "CPU High")
	assert.Contains(t, mock.calls[0].title, MetricCPUUsage)
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
