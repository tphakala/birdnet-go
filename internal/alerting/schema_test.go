package alerting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSchema_AllObjectTypesPresent(t *testing.T) {
	schema := GetSchema()
	names := make([]string, len(schema.ObjectTypes))
	for i, ot := range schema.ObjectTypes {
		names[i] = ot.Name
	}
	assert.ElementsMatch(t, []string{
		ObjectTypeStream, ObjectTypeDetection, ObjectTypeApplication,
		ObjectTypeIntegration, ObjectTypeDevice, ObjectTypeSystem,
	}, names)
}

func TestGetSchema_AllEventsPresent(t *testing.T) {
	schema := GetSchema()
	var allEvents []string
	for _, ot := range schema.ObjectTypes {
		for _, ev := range ot.Events {
			allEvents = append(allEvents, ev.Name)
		}
	}
	expectedEvents := []string{
		EventStreamConnected, EventStreamDisconnected, EventStreamError,
		EventDetectionNewSpecies, EventDetectionOccurred,
		EventApplicationStarted, EventApplicationStopped,
		EventBirdWeatherFailed, EventMQTTConnected, EventMQTTDisconnected,
		EventDeviceStarted, EventDeviceStopped, EventDeviceError,
	}
	assert.ElementsMatch(t, expectedEvents, allEvents)
}

func TestGetSchema_AllMetricsPresent(t *testing.T) {
	schema := GetSchema()
	var allMetrics []string
	for _, ot := range schema.ObjectTypes {
		for _, m := range ot.Metrics {
			allMetrics = append(allMetrics, m.Name)
		}
	}
	assert.ElementsMatch(t, []string{MetricCPUUsage, MetricMemoryUsage, MetricDiskUsage}, allMetrics)
}

func TestGetSchema_AllOperatorsPresent(t *testing.T) {
	schema := GetSchema()
	names := make([]string, len(schema.Operators))
	for i, op := range schema.Operators {
		names[i] = op.Name
	}
	assert.ElementsMatch(t, []string{
		OperatorIs, OperatorIsNot, OperatorContains, OperatorNotContains,
		OperatorGreaterThan, OperatorLessThan, OperatorGreaterOrEqual, OperatorLessOrEqual,
	}, names)
}

func TestGetSchema_PropertiesHaveValidOperators(t *testing.T) {
	schema := GetSchema()
	validOps := map[string]bool{
		OperatorIs: true, OperatorIsNot: true, OperatorContains: true, OperatorNotContains: true,
		OperatorGreaterThan: true, OperatorLessThan: true, OperatorGreaterOrEqual: true, OperatorLessOrEqual: true,
	}
	for _, ot := range schema.ObjectTypes {
		for _, ev := range ot.Events {
			for _, prop := range ev.Properties {
				require.NotEmpty(t, prop.Operators, "property %s in event %s has no operators", prop.Name, ev.Name)
				for _, op := range prop.Operators {
					assert.True(t, validOps[op], "invalid operator %q for property %s", op, prop.Name)
				}
			}
		}
		for _, m := range ot.Metrics {
			for _, prop := range m.Properties {
				require.NotEmpty(t, prop.Operators, "property %s in metric %s has no operators", prop.Name, m.Name)
				for _, op := range prop.Operators {
					assert.True(t, validOps[op], "invalid operator %q for property %s", op, prop.Name)
				}
			}
		}
	}
}

func TestGetSchema_LabelsNotEmpty(t *testing.T) {
	schema := GetSchema()
	for _, ot := range schema.ObjectTypes {
		assert.NotEmpty(t, ot.Label, "object type %s has empty label", ot.Name)
		for _, ev := range ot.Events {
			assert.NotEmpty(t, ev.Label, "event %s has empty label", ev.Name)
		}
		for _, m := range ot.Metrics {
			assert.NotEmpty(t, m.Label, "metric %s has empty label", m.Name)
			assert.NotEmpty(t, m.Unit, "metric %s has empty unit", m.Name)
		}
	}
}
