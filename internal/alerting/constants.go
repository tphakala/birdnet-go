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

	EventBirdWeatherFailed = "integration.birdweather_failed"
	EventMQTTConnected     = "integration.mqtt_connected"
	EventMQTTDisconnected  = "integration.mqtt_disconnected"
	EventMQTTPublishFailed = "integration.mqtt_publish_failed"

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
	PropertyThresholdStep  = "threshold_step"

	// Properties for detection event metadata passthrough.
	// These are not used for condition evaluation but carry data
	// needed by the notification adapter for webhook template enrichment.
	PropertyEventMetadata      = "event_metadata"
	PropertyEventTimestamp     = "event_timestamp"
	PropertyDaysSinceFirstSeen = "days_since_first_seen"
	PropertyIsNewSpecies       = "is_new_species"
)

// Action targets identify where notifications are sent.
const (
	TargetBell = "bell"
)

// Built-in rule i18n key constants.
// These correspond to entries in the frontend i18n files under
// "settings.alerts.builtInRules.*".
const (
	RuleKeyNewSpeciesName  = "settings.alerts.builtInRules.newSpecies.name"
	RuleKeyNewSpeciesDesc  = "settings.alerts.builtInRules.newSpecies.description"
	RuleKeyStreamDiscName  = "settings.alerts.builtInRules.streamDisconnected.name"
	RuleKeyStreamDiscDesc  = "settings.alerts.builtInRules.streamDisconnected.description"
	RuleKeyStreamErrorName = "settings.alerts.builtInRules.streamError.name"
	RuleKeyStreamErrorDesc = "settings.alerts.builtInRules.streamError.description"
	RuleKeyDeviceErrorName = "settings.alerts.builtInRules.deviceError.name"
	RuleKeyDeviceErrorDesc = "settings.alerts.builtInRules.deviceError.description"
	RuleKeyHighCPUName     = "settings.alerts.builtInRules.highCpu.name"
	RuleKeyHighCPUDesc     = "settings.alerts.builtInRules.highCpu.description"
	RuleKeyHighMemoryName  = "settings.alerts.builtInRules.highMemory.name"
	RuleKeyHighMemoryDesc  = "settings.alerts.builtInRules.highMemory.description"
	RuleKeyLowDiskName     = "settings.alerts.builtInRules.lowDisk.name"
	RuleKeyLowDiskDesc     = "settings.alerts.builtInRules.lowDisk.description"
	RuleKeyMQTTDiscName    = "settings.alerts.builtInRules.mqttDisconnected.name"
	RuleKeyMQTTDiscDesc    = "settings.alerts.builtInRules.mqttDisconnected.description"
	RuleKeyMQTTPublishName = "settings.alerts.builtInRules.mqttPublishFailed.name"
	RuleKeyMQTTPublishDesc = "settings.alerts.builtInRules.mqttPublishFailed.description"
	RuleKeyBirdWeatherName = "settings.alerts.builtInRules.birdWeatherFailed.name"
	RuleKeyBirdWeatherDesc = "settings.alerts.builtInRules.birdWeatherFailed.description"
)

// Alert notification i18n key constants.
// These correspond to entries in the frontend i18n files under
// "notifications.content.alert.*".
const (
	MsgAlertFiredTitle = "notifications.content.alert.firedTitle"

	// Default message keys per alert category (used when no custom template is set).
	MsgAlertMetricExceeded    = "notifications.content.alert.metricExceeded"
	MsgAlertDetectionOccurred = "notifications.content.alert.detectionOccurred"
	MsgAlertErrorOccurred     = "notifications.content.alert.errorOccurred"
	MsgAlertDisconnected      = "notifications.content.alert.disconnected"

	// MsgAlertErrorPrefix is the i18n key prefix for classified error messages.
	// Full key is MsgAlertErrorPrefix + "." + ErrorClass.Key.
	MsgAlertErrorPrefix = "notifications.content.alert.error"
)
