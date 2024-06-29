// Package metrics provides custom Prometheus metrics for various components of the BirdNET-Go application.
package metrics

import (
	"fmt"
	"log"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// MQTTMetrics contains all Prometheus metrics related to MQTT operations.
type MQTTMetrics struct {
	ConnectionStatus  prometheus.Gauge
	MessagesDelivered prometheus.Counter
	Errors            prometheus.Counter
	ReconnectAttempts prometheus.Counter
	LastConnectTime   prometheus.Gauge
	MessageSize       prometheus.Histogram
	PublishLatency    prometheus.Histogram
	registry          *prometheus.Registry
}

// NewMQTTMetrics creates a new instance of MQTTMetrics.
// It requires a Prometheus registry to register the metrics.
// It returns an error if metric registration fails.
func NewMQTTMetrics(registry *prometheus.Registry) (*MQTTMetrics, error) {
	m := &MQTTMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, fmt.Errorf("failed to initialize MQTT metrics: %w", err)
	}
	if err := registry.Register(m); err != nil {
		return nil, fmt.Errorf("failed to register MQTT metrics: %w", err)
	}
	return m, nil
}

// initMetrics initializes all metrics for MQTTMetrics.
func (m *MQTTMetrics) initMetrics() error {
	m.ConnectionStatus = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqtt_connection_status",
		Help: "Current MQTT connection status (1 for connected, 0 for disconnected)",
	})

	m.MessagesDelivered = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqtt_messages_delivered_total",
		Help: "Total number of MQTT messages successfully delivered",
	})

	m.Errors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqtt_errors_total",
		Help: "Total number of MQTT errors encountered",
	})

	m.ReconnectAttempts = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "mqtt_reconnect_attempts_total",
		Help: "Total number of MQTT reconnection attempts",
	})

	m.LastConnectTime = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mqtt_last_connect_time_seconds",
		Help: "Timestamp of the last successful MQTT connection",
	})

	m.MessageSize = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "mqtt_message_size_bytes",
		Help:    "Size of MQTT messages in bytes",
		Buckets: prometheus.ExponentialBuckets(64, 2, 10),
	})

	m.PublishLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "mqtt_publish_latency_seconds",
		Help:    "Latency of MQTT publish operations in seconds",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 10),
	})

	return nil
}

// UpdateConnectionStatus updates the MQTT connection status and last connect time.
// It should be called when the connection status changes.
func (m *MQTTMetrics) UpdateConnectionStatus(connected bool) {
	if connected {
		m.ConnectionStatus.Set(1)
		m.LastConnectTime.SetToCurrentTime()
	} else {
		m.ConnectionStatus.Set(0)
	}
}

// IncrementMessagesDelivered increments the count of successfully delivered MQTT messages.
func (m *MQTTMetrics) IncrementMessagesDelivered() {
	m.MessagesDelivered.Inc()
}

// IncrementErrors increments the count of MQTT errors.
func (m *MQTTMetrics) IncrementErrors() {
	m.Errors.Inc()
}

// IncrementReconnectAttempts increments the count of MQTT reconnection attempts.
func (m *MQTTMetrics) IncrementReconnectAttempts() {
	m.ReconnectAttempts.Inc()
}

// ObserveMessageSize records the size of an MQTT message.
func (m *MQTTMetrics) ObserveMessageSize(sizeBytes float64) {
	m.MessageSize.Observe(sizeBytes)
}

// ObservePublishLatency records the latency of an MQTT publish operation.
func (m *MQTTMetrics) ObservePublishLatency(latencySeconds float64) {
	m.PublishLatency.Observe(latencySeconds)
}

// StartPublishTimer starts a timer for measuring publish latency.
// It returns a PublishTimer that should be used to record the duration.
func (m *MQTTMetrics) StartPublishTimer() *PublishTimer {
	return &PublishTimer{
		startTime: time.Now(),
		metrics:   m,
	}
}

// PublishTimer is a helper struct for measuring publish latency.
type PublishTimer struct {
	startTime time.Time
	metrics   *MQTTMetrics
}

// ObserveDuration stops the timer and records the duration.
func (pt *PublishTimer) ObserveDuration() {
	duration := time.Since(pt.startTime).Seconds()
	pt.metrics.ObservePublishLatency(duration)
}

// Collect implements the prometheus.Collector interface.
func (m *MQTTMetrics) Collect(ch chan<- prometheus.Metric) {
	log.Println("MQTTMetrics Collect method called")
	ch <- m.ConnectionStatus
	ch <- m.MessagesDelivered
	ch <- m.Errors
	ch <- m.ReconnectAttempts
	ch <- m.LastConnectTime
	ch <- m.MessageSize
	ch <- m.PublishLatency
}

// Describe implements the prometheus.Collector interface.
func (m *MQTTMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.ConnectionStatus.Desc()
	ch <- m.MessagesDelivered.Desc()
	ch <- m.Errors.Desc()
	ch <- m.ReconnectAttempts.Desc()
	ch <- m.LastConnectTime.Desc()
	ch <- m.MessageSize.Desc()
	ch <- m.PublishLatency.Desc()
}
