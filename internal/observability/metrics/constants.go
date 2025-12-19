// Package metrics provides constants used across metric definitions.
package metrics

import "time"

// Operation type constants used in switch statements across metrics.
// These constants define the categories of operations that can be recorded.
const (
	// OpPrediction represents BirdNET prediction operations.
	OpPrediction = "prediction"
	// OpModelLoad represents model loading operations.
	OpModelLoad = "model_load"
	// OpDetection represents bird detection operations.
	OpDetection = "detection"
	// OpChunkProcess represents audio chunk processing operations.
	OpChunkProcess = "chunk_process"
	// OpModelInvoke represents TensorFlow model invocation operations.
	OpModelInvoke = "model_invoke"
	// OpRangeFilter represents range filter operations.
	OpRangeFilter = "range_filter"
	// OpProcessTimeMs represents process time in milliseconds.
	OpProcessTimeMs = "process_time_ms"
	// OpNoteCreate represents note creation operations.
	OpNoteCreate = "note_create"
	// OpNoteUpdate represents note update operations.
	OpNoteUpdate = "note_update"
	// OpNoteDelete represents note deletion operations.
	OpNoteDelete = "note_delete"
	// OpNoteGet represents note retrieval operations.
	OpNoteGet = "note_get"
	// OpNoteLock represents note lock operations.
	OpNoteLock = "note_lock"
	// OpNoteOperation represents generic note operations.
	OpNoteOperation = "note_operation"
	// OpDbQuery represents database query operations.
	OpDbQuery = "db_query"
	// OpDbInsert represents database insert operations.
	OpDbInsert = "db_insert"
	// OpDbUpdate represents database update operations.
	OpDbUpdate = "db_update"
	// OpDbDelete represents database delete operations.
	OpDbDelete = "db_delete"
	// OpTransaction represents database transaction operations.
	OpTransaction = "transaction"
	// OpSearch represents search operations.
	OpSearch = "search"
	// OpAnalytics represents analytics query operations.
	OpAnalytics = "analytics"
	// OpWeatherData represents weather data operations.
	OpWeatherData = "weather_data"
	// OpImageCache represents image cache operations.
	OpImageCache = "image_cache"
	// OpBackup represents backup operations.
	OpBackup = "backup"
	// OpMaintenance represents maintenance operations.
	OpMaintenance = "maintenance"
	// OpCacheGet represents cache get operations.
	OpCacheGet = "cache_get"
	// OpCacheSet represents cache set operations.
	OpCacheSet = "cache_set"
	// OpCacheDelete represents cache delete operations.
	OpCacheDelete = "cache_delete"
	// OpLockWait represents lock wait operations.
	OpLockWait = "lock_wait"
)

// Label value constants used for metric labels.
const (
	// LabelBirdnet is the model label value for BirdNET.
	LabelBirdnet = "birdnet"
	// LabelQuery is the operation label for query operations.
	LabelQuery = "query"
	// LabelFetch is the operation label for fetch operations.
	LabelFetch = "fetch"
	// LabelCommit is the operation label for commit operations.
	LabelCommit = "commit"
	// LabelGet is the operation label for get operations.
	LabelGet = "get"
	// LabelCreate is the operation label for create operations.
	LabelCreate = "create"
	// LabelExclusive is the lock type label for exclusive locks.
	LabelExclusive = "exclusive"
	// LabelUpdate is the operation label for update operations.
	LabelUpdate = "update"
	// LabelVacuum is the operation label for vacuum operations.
	LabelVacuum = "vacuum"
	// LabelNote is the lock type label for note locks.
	LabelNote = "note"
	// LabelSuntimes is the cache type label for sun times cache.
	LabelSuntimes = "suntimes"
)

// Histogram bucket configuration constants.
// These define the base values and factors for exponential/linear bucket generation.
const (
	// BucketStart1ms is the starting bucket for 1ms histograms (1ms to ~1s range).
	BucketStart1ms = 0.001
	// BucketStart100us is the starting bucket for 0.1ms histograms (0.1ms to ~400ms range).
	BucketStart100us = 0.0001
	// BucketStart10ms is the starting bucket for 10ms histograms (10ms to ~40s range).
	BucketStart10ms = 0.01
	// BucketStart100ms is the starting bucket for 100ms histograms (100ms to ~100s range).
	BucketStart100ms = 0.1
	// BucketStart1s is the starting bucket for 1s histograms (1s to ~9 hours range).
	BucketStart1s = 1.0
	// BucketStart64B is the starting bucket for 64 byte histograms.
	BucketStart64B = 64.0
	// BucketStart100B is the starting bucket for 100 byte histograms (100B to ~100MB range).
	BucketStart100B = 100.0
	// BucketStart1KB is the starting bucket for 1KB histograms (1KB to ~1GB range).
	BucketStart1KB = 1024.0
	// BucketGainLinearStart is the starting bucket for linear gain level histograms.
	BucketGainLinearStart = 0.0
	// BucketGainLinearWidth is the width of each bucket for linear gain level histograms.
	BucketGainLinearWidth = 0.1
	// BucketGainLinearCount is the number of buckets for linear gain level histograms (0.0 to 2.0).
	BucketGainLinearCount = 21

	// BucketFactor2 is the common exponential growth factor of 2 for histogram buckets.
	BucketFactor2 = 2
	// BucketFactor10 is the exponential growth factor of 10 for larger ranges.
	BucketFactor10 = 10

	// BucketCount8 defines 8 exponential buckets.
	BucketCount8 = 8
	// BucketCount10 defines 10 exponential buckets.
	BucketCount10 = 10
	// BucketCount12 defines 12 exponential buckets.
	BucketCount12 = 12
	// BucketCount15 defines 15 exponential buckets.
	BucketCount15 = 15
	// BucketCount20 defines 20 exponential buckets.
	BucketCount20 = 20
	// BucketCount6 defines 6 exponential buckets.
	BucketCount6 = 6
)

// Time and conversion constants.
const (
	// ShutdownTimeout is the timeout for graceful shutdown operations.
	ShutdownTimeout = 5 * time.Second
	// MillisecondsPerSecond is the conversion factor from seconds to milliseconds.
	MillisecondsPerSecond = 1000.0
	// PercentageFactor is the multiplier to convert ratio to percentage.
	PercentageFactor = 100.0
)

// String parsing constants.
const (
	// SplitPartsCount is the expected number of parts when splitting operation strings.
	SplitPartsCount = 2
)
