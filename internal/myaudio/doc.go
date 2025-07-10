// Package myaudio implements audio processing with thread-safe metrics.
//
// All global metrics variables are protected by RWMutex and initialized
// using sync.Once to prevent race conditions during concurrent access.
//
// Thread-Safety Pattern:
//   - SetXXXMetrics functions use sync.Once to ensure one-time initialization
//   - getXXXMetrics functions use RLock for concurrent read access
//   - All metric access goes through getter functions
//
// This pattern ensures that metrics can only be set once per process lifetime,
// preventing race conditions during initialization while allowing efficient
// concurrent read access during normal operation.
package myaudio