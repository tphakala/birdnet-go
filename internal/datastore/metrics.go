// Package datastore provides type aliases and integration with the observability metrics package
package datastore

import (
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// Metrics is a type alias for the metrics.DatastoreMetrics
// This allows us to use the metrics throughout the datastore package
type Metrics = metrics.DatastoreMetrics

// getMetrics returns the current metrics instance, or nil if none is set.
// Safe for concurrent use.
func (ds *DataStore) getMetrics() *Metrics {
	ds.metricsMu.RLock()
	m := ds.metrics
	ds.metricsMu.RUnlock()
	return m
}
