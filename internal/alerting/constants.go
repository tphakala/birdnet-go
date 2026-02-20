// Package alerting provides the notification alerting rules engine.
package alerting

// Object types define the categories of things that can be monitored.
const (
	ObjectTypeStream      = "stream"
	ObjectTypeDetection   = "detection"
	ObjectTypeApplication = "application"
	ObjectTypeIntegration = "integration"
	ObjectTypeDevice      = "device"
	ObjectTypeSystem      = "system"
)

// Trigger types define how a rule is activated.
const (
	TriggerTypeEvent  = "event"
	TriggerTypeMetric = "metric"
)

// Event names identify specific alertable events.
const (
	EventStreamConnected    = "stream.connected"
	EventStreamDisconnected = "stream.disconnected"
	EventStreamError        = "stream.error"

	EventDetectionNewSpecies = "detection.new_species"
	EventDetectionOccurred   = "detection.occurred"

	EventApplicationStarted = "application.started"
	EventApplicationStopped = "application.stopped"

	EventBirdWeatherFailed  = "integration.birdweather_failed"
	EventMQTTConnected      = "integration.mqtt_connected"
	EventMQTTDisconnected   = "integration.mqtt_disconnected"

	EventDeviceStarted = "device.started"
	EventDeviceStopped = "device.stopped"
	EventDeviceError   = "device.error"
)

// Metric names identify threshold-based metrics.
const (
	MetricCPUUsage    = "system.cpu_usage"
	MetricMemoryUsage = "system.memory_usage"
	MetricDiskUsage   = "system.disk_usage"
)

// Condition operators define how property values are compared.
const (
	OperatorIs             = "is"
	OperatorIsNot          = "is_not"
	OperatorContains       = "contains"
	OperatorNotContains    = "not_contains"
	OperatorGreaterThan    = "greater_than"
	OperatorLessThan       = "less_than"
	OperatorGreaterOrEqual = "greater_or_equal"
	OperatorLessOrEqual    = "less_or_equal"
)

// Condition properties identify event fields available for condition evaluation.
const (
	PropertyValue          = "value"
	PropertySpeciesName    = "species_name"
	PropertyScientificName = "scientific_name"
	PropertyConfidence     = "confidence"
	PropertyLocation       = "location"
	PropertyStreamName     = "stream_name"
	PropertyStreamURL      = "stream_url"
	PropertyDeviceName     = "device_name"
	PropertyError          = "error"
	PropertyPath           = "path"
	PropertyBroker         = "broker"
)

// Action targets identify where notifications are sent.
const (
	TargetBell = "bell"
)
