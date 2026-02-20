package alerting

// Schema describes the full catalog of alertable object types, events, and metrics.
type Schema struct {
	ObjectTypes []ObjectTypeSchema `json:"objectTypes"`
	Operators   []OperatorSchema   `json:"operators"`
}

// ObjectTypeSchema describes an object type and its available triggers.
type ObjectTypeSchema struct {
	Name    string         `json:"name"`
	Label   string         `json:"label"`
	Events  []EventSchema  `json:"events,omitempty"`
	Metrics []MetricSchema `json:"metrics,omitempty"`
}

// EventSchema describes an event trigger and its available properties.
type EventSchema struct {
	Name       string           `json:"name"`
	Label      string           `json:"label"`
	Properties []PropertySchema `json:"properties"`
}

// MetricSchema describes a metric trigger and its available properties.
type MetricSchema struct {
	Name       string           `json:"name"`
	Label      string           `json:"label"`
	Unit       string           `json:"unit"`
	Properties []PropertySchema `json:"properties"`
}

// PropertySchema describes a property available for condition building.
type PropertySchema struct {
	Name      string   `json:"name"`
	Label     string   `json:"label"`
	Type      string   `json:"type"` // "string" or "number"
	Operators []string `json:"operators"`
}

// OperatorSchema describes an operator for the UI.
type OperatorSchema struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Type  string `json:"type"` // "string", "number", or "all"
}

// stringOperators are operators valid for string properties.
var stringOperators = []string{OperatorIs, OperatorIsNot, OperatorContains, OperatorNotContains}

// numericOperators are operators valid for numeric properties.
var numericOperators = []string{OperatorGreaterThan, OperatorLessThan, OperatorGreaterOrEqual, OperatorLessOrEqual}

// GetSchema returns the full alerting schema for the UI.
func GetSchema() Schema {
	return Schema{
		ObjectTypes: []ObjectTypeSchema{
			{
				Name:  ObjectTypeStream,
				Label: "Audio Stream",
				Events: []EventSchema{
					{Name: EventStreamConnected, Label: "Stream Connected", Properties: streamProperties()},
					{Name: EventStreamDisconnected, Label: "Stream Disconnected", Properties: streamProperties()},
					{Name: EventStreamError, Label: "Stream Error", Properties: streamErrorProperties()},
				},
			},
			{
				Name:  ObjectTypeDetection,
				Label: "Detection",
				Events: []EventSchema{
					{Name: EventDetectionNewSpecies, Label: "New Species Detected", Properties: detectionProperties()},
					{Name: EventDetectionOccurred, Label: "Detection Occurred", Properties: detectionProperties()},
				},
			},
			{
				Name:  ObjectTypeApplication,
				Label: "Application",
				Events: []EventSchema{
					{Name: EventApplicationStarted, Label: "Application Started", Properties: nil},
					{Name: EventApplicationStopped, Label: "Application Stopped", Properties: nil},
				},
			},
			{
				Name:  ObjectTypeIntegration,
				Label: "Integration",
				Events: []EventSchema{
					{Name: EventBirdWeatherFailed, Label: "BirdWeather Upload Failed", Properties: errorProperties()},
					{Name: EventMQTTConnected, Label: "MQTT Connected", Properties: mqttProperties()},
					{Name: EventMQTTDisconnected, Label: "MQTT Disconnected", Properties: mqttProperties()},
				},
			},
			{
				Name:  ObjectTypeDevice,
				Label: "Device",
				Events: []EventSchema{
					{Name: EventDeviceStarted, Label: "Device Started", Properties: deviceProperties()},
					{Name: EventDeviceStopped, Label: "Device Stopped", Properties: deviceProperties()},
					{Name: EventDeviceError, Label: "Device Error", Properties: deviceErrorProperties()},
				},
			},
			{
				Name:  ObjectTypeSystem,
				Label: "System",
				Metrics: []MetricSchema{
					{Name: MetricCPUUsage, Label: "CPU Usage", Unit: "%", Properties: numericValueProperties()},
					{Name: MetricMemoryUsage, Label: "Memory Usage", Unit: "%", Properties: numericValueProperties()},
					{Name: MetricDiskUsage, Label: "Disk Usage", Unit: "%", Properties: numericValueProperties()},
				},
			},
		},
		Operators: []OperatorSchema{
			{Name: OperatorIs, Label: "is", Type: "string"},
			{Name: OperatorIsNot, Label: "is not", Type: "string"},
			{Name: OperatorContains, Label: "contains", Type: "string"},
			{Name: OperatorNotContains, Label: "does not contain", Type: "string"},
			{Name: OperatorGreaterThan, Label: "greater than", Type: "number"},
			{Name: OperatorLessThan, Label: "less than", Type: "number"},
			{Name: OperatorGreaterOrEqual, Label: "greater or equal", Type: "number"},
			{Name: OperatorLessOrEqual, Label: "less or equal", Type: "number"},
		},
	}
}

func streamProperties() []PropertySchema {
	return []PropertySchema{
		{Name: PropertyStreamName, Label: "Stream Name", Type: "string", Operators: stringOperators},
		{Name: PropertyStreamURL, Label: "Stream URL", Type: "string", Operators: stringOperators},
	}
}

func streamErrorProperties() []PropertySchema {
	return append(streamProperties(),
		PropertySchema{Name: PropertyError, Label: "Error Message", Type: "string", Operators: stringOperators},
	)
}

func detectionProperties() []PropertySchema {
	return []PropertySchema{
		{Name: PropertySpeciesName, Label: "Species Name", Type: "string", Operators: stringOperators},
		{Name: PropertyScientificName, Label: "Scientific Name", Type: "string", Operators: stringOperators},
		{Name: PropertyConfidence, Label: "Confidence", Type: "number", Operators: numericOperators},
		{Name: PropertyLocation, Label: "Location", Type: "string", Operators: stringOperators},
	}
}

func errorProperties() []PropertySchema {
	return []PropertySchema{
		{Name: PropertyError, Label: "Error Message", Type: "string", Operators: stringOperators},
	}
}

func mqttProperties() []PropertySchema {
	return []PropertySchema{
		{Name: PropertyBroker, Label: "Broker", Type: "string", Operators: stringOperators},
	}
}

func deviceProperties() []PropertySchema {
	return []PropertySchema{
		{Name: PropertyDeviceName, Label: "Device Name", Type: "string", Operators: stringOperators},
	}
}

func deviceErrorProperties() []PropertySchema {
	return append(deviceProperties(),
		PropertySchema{Name: PropertyError, Label: "Error Message", Type: "string", Operators: stringOperators},
	)
}

func numericValueProperties() []PropertySchema {
	return []PropertySchema{
		{Name: PropertyValue, Label: "Value", Type: "number", Operators: numericOperators},
	}
}
