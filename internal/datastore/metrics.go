// Package datastore provides type aliases and integration with the observability metrics package
package datastore

import (
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// DatastoreMetrics is a type alias for the metrics.DatastoreMetrics
// This allows us to use the metrics throughout the datastore package
type DatastoreMetrics = metrics.DatastoreMetrics