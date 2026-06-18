// Package observability provides metrics and monitoring capabilities for the BirdNET-Go application.
package observability

import "time"

// MetricPoint represents a single timestamped metric value.
type MetricPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// MetricsStore defines the interface for storing and retrieving time-series
// metric history. Implementations must be safe for concurrent use.
type MetricsStore interface {
	// RecordBatch stores a batch of metric values collected at the same tick.
	// All points share the same timestamp (time.Now() at call time).
	RecordBatch(points map[string]float64)

	// Get returns up to the last n points for the named metric in chronological order.
	// If n <= 0, all available points are returned.
	// Returns nil if the metric does not exist.
	Get(name string, n int) []MetricPoint

	// GetAll returns up to the last n points for every tracked metric.
	// If n <= 0, all available points are returned.
	// The returned map and slices are safe copies.
	GetAll(n int) map[string][]MetricPoint

	// GetLatest returns the most recent point for each tracked metric.
	// Returns an empty map if no metrics have been recorded.
	GetLatest() map[string]MetricPoint

	// Names returns the sorted list of tracked metric names.
	Names() []string

	// Subscribe returns a channel that receives the latest metric snapshot
	// after each RecordBatch call, plus a cancel function to unsubscribe.
	// The channel is buffered (cap 1); slow consumers may miss updates.
	// The cancel function removes the subscriber but does NOT close the channel
	// (avoids send-on-closed-channel panics). The channel is reclaimed by GC
	// once both sides drop references.
	Subscribe() (<-chan map[string]MetricPoint, func())

	// BroadcastTopologyChanged signals all topology subscribers that the
	// inference topology (loaded models or audio source attachment) changed.
	// The send is non-blocking: a subscriber whose buffer is already full keeps
	// its pending signal and does not block the broadcaster. It is safe to call
	// when there are no subscribers.
	BroadcastTopologyChanged()

	// SubscribeTopology returns a buffered (cap 1) signal channel that receives
	// a value on each BroadcastTopologyChanged call, plus a cancel function to
	// unsubscribe. The cancel function removes the subscriber but does NOT close
	// the channel (avoids send-on-closed-channel panics). The channel is
	// reclaimed by GC once both sides drop references. Consumers receive only a
	// coalesced signal (no payload); they react by re-fetching the snapshot.
	SubscribeTopology() (<-chan struct{}, func())
}
