package audiocore

// RouterMetrics tracks frame dispatch and drop counts for the audio frame router.
// Callers must check for nil before calling methods on this interface.
type RouterMetrics interface {
	// IncFramesDispatched increments the count of frames successfully dispatched for a source.
	IncFramesDispatched(sourceID string)

	// IncFramesDropped increments the count of frames dropped for a given source and consumer.
	IncFramesDropped(sourceID, consumerID string)

	// IncRouteErrors increments the count of routing errors between a source and consumer.
	IncRouteErrors(sourceID, consumerID string)
}

// StreamMetrics tracks FFmpeg stream health and performance metrics.
// Callers must check for nil before calling methods on this interface.
type StreamMetrics interface {
	// IncStreamErrors increments the error count for a stream.
	IncStreamErrors(sourceID string)

	// SetStreamHealth updates the health status of a stream.
	SetStreamHealth(sourceID string, healthy bool)

	// RecordDataRate records the current data rate (bytes per second) for a stream.
	RecordDataRate(sourceID string, bytesPerSec float64)
}

// BufferMetrics tracks buffer pool allocation, usage, and performance metrics.
// Callers must check for nil before calling methods on this interface.
type BufferMetrics interface {
	// RecordBufferOverrun records when a buffer cannot accommodate new frames.
	RecordBufferOverrun(sourceID string)

	// RecordBufferOverwrite records when a buffer overwrites unconsumed data.
	RecordBufferOverwrite(sourceID string)

	// RecordPoolStats records object pool statistics for a named pool.
	RecordPoolStats(poolName string, hits, misses, discarded uint64)
}

// DeviceMetrics tracks audio device capture statistics and health.
// Callers must check for nil before calling methods on this interface.
type DeviceMetrics interface {
	// IncDeviceErrors increments the error count for a device.
	IncDeviceErrors(deviceID string)

	// RecordDeviceCaptureRate records the current capture rate (bytes per second) for a device.
	RecordDeviceCaptureRate(deviceID string, bytesPerSec float64)
}
