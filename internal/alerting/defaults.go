package alerting

import (
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// DefaultRules returns the built-in alert rules that ship with BirdNET-Go.
// These are seeded on first v2 activation and can be restored via reset-defaults.
func DefaultRules() []entities.AlertRule {
	return []entities.AlertRule{
		{
			Name:           "New species detected",
			Description:    "Notifies when a species is detected for the first time",
			NameKey:        RuleKeyNewSpeciesName,
			DescriptionKey: RuleKeyNewSpeciesDesc,
			Enabled:        true,
			BuiltIn:        true,
			ObjectType:     ObjectTypeDetection,
			TriggerType:    TriggerTypeEvent,
			EventName:      EventDetectionNewSpecies,
			CooldownSec:    60,
			Actions: []entities.AlertAction{
				{Target: TargetBell, SortOrder: 0},
			},
		},
		{
			Name:           "Audio stream disconnected",
			Description:    "Notifies when an RTSP or audio stream loses connection",
			NameKey:        RuleKeyStreamDiscName,
			DescriptionKey: RuleKeyStreamDiscDesc,
			Enabled:        true,
			BuiltIn:        true,
			ObjectType:     ObjectTypeStream,
			TriggerType:    TriggerTypeEvent,
			EventName:      EventStreamDisconnected,
			CooldownSec:    300,
			Actions: []entities.AlertAction{
				{Target: TargetBell, SortOrder: 0},
			},
		},
		{
			Name:           "Audio stream error",
			Description:    "Notifies when an audio stream encounters an error",
			NameKey:        RuleKeyStreamErrorName,
			DescriptionKey: RuleKeyStreamErrorDesc,
			Enabled:        true,
			BuiltIn:        true,
			ObjectType:     ObjectTypeStream,
			TriggerType:    TriggerTypeEvent,
			EventName:      EventStreamError,
			CooldownSec:    300,
			Actions: []entities.AlertAction{
				{Target: TargetBell, SortOrder: 0},
			},
		},
		{
			Name:           "Audio device error",
			Description:    "Notifies when a local audio capture device encounters an error",
			NameKey:        RuleKeyDeviceErrorName,
			DescriptionKey: RuleKeyDeviceErrorDesc,
			Enabled:        true,
			BuiltIn:        true,
			ObjectType:     ObjectTypeDevice,
			TriggerType:    TriggerTypeEvent,
			EventName:      EventDeviceError,
			CooldownSec:    300,
			Actions: []entities.AlertAction{
				{Target: TargetBell, SortOrder: 0},
			},
		},
		{
			Name:           "High CPU usage",
			Description:    "Notifies when CPU usage exceeds 90% for 5 minutes",
			NameKey:        RuleKeyHighCPUName,
			DescriptionKey: RuleKeyHighCPUDesc,
			Enabled:        true,
			BuiltIn:        true,
			ObjectType:     ObjectTypeSystem,
			TriggerType:    TriggerTypeMetric,
			MetricName:     MetricCPUUsage,
			CooldownSec:    900,
			Conditions: []entities.AlertCondition{
				{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "90", DurationSec: 300, SortOrder: 0},
			},
			Actions: []entities.AlertAction{
				{Target: TargetBell, SortOrder: 0},
			},
		},
		{
			Name:           "High memory usage",
			Description:    "Notifies when memory usage exceeds 90% for 5 minutes",
			NameKey:        RuleKeyHighMemoryName,
			DescriptionKey: RuleKeyHighMemoryDesc,
			Enabled:        true,
			BuiltIn:        true,
			ObjectType:     ObjectTypeSystem,
			TriggerType:    TriggerTypeMetric,
			MetricName:     MetricMemoryUsage,
			CooldownSec:    900,
			Conditions: []entities.AlertCondition{
				{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "90", DurationSec: 300, SortOrder: 0},
			},
			Actions: []entities.AlertAction{
				{Target: TargetBell, SortOrder: 0},
			},
		},
		{
			Name:            "Low disk space",
			Description:     "Notifies when disk usage exceeds 85% for 5 minutes",
			NameKey:         RuleKeyLowDiskName,
			DescriptionKey:  RuleKeyLowDiskDesc,
			Enabled:         true,
			BuiltIn:         true,
			ObjectType:      ObjectTypeSystem,
			TriggerType:     TriggerTypeMetric,
			MetricName:      MetricDiskUsage,
			CooldownSec:     1800,
			EscalationSteps: []float64{85, 90, 95, 99},
			Conditions: []entities.AlertCondition{
				{Property: PropertyValue, Operator: OperatorGreaterThan, Value: "85", DurationSec: 300, SortOrder: 0},
			},
			Actions: []entities.AlertAction{
				{Target: TargetBell, SortOrder: 0},
			},
		},
		{
			Name:           "MQTT publish failed",
			Description:    "Notifies when an MQTT message publish fails",
			NameKey:        RuleKeyMQTTPublishName,
			DescriptionKey: RuleKeyMQTTPublishDesc,
			Enabled:        true,
			BuiltIn:        true,
			ObjectType:     ObjectTypeIntegration,
			TriggerType:    TriggerTypeEvent,
			EventName:      EventMQTTPublishFailed,
			CooldownSec:    300,
			Actions: []entities.AlertAction{
				{Target: TargetBell, SortOrder: 0},
			},
		},
		{
			Name:           "MQTT disconnected",
			Description:    "Notifies when the MQTT broker connection is lost",
			NameKey:        RuleKeyMQTTDiscName,
			DescriptionKey: RuleKeyMQTTDiscDesc,
			Enabled:        true,
			BuiltIn:        true,
			ObjectType:     ObjectTypeIntegration,
			TriggerType:    TriggerTypeEvent,
			EventName:      EventMQTTDisconnected,
			CooldownSec:    600,
			Actions: []entities.AlertAction{
				{Target: TargetBell, SortOrder: 0},
			},
		},
		{
			Name:           "BirdWeather upload failed",
			Description:    "Notifies when a BirdWeather upload fails",
			NameKey:        RuleKeyBirdWeatherName,
			DescriptionKey: RuleKeyBirdWeatherDesc,
			Enabled:        true,
			BuiltIn:        true,
			ObjectType:     ObjectTypeIntegration,
			TriggerType:    TriggerTypeEvent,
			EventName:      EventBirdWeatherFailed,
			CooldownSec:    600,
			Actions: []entities.AlertAction{
				{Target: TargetBell, SortOrder: 0},
			},
		},
	}
}
