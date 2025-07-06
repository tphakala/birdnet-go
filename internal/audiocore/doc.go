// Package audiocore provides the core audio processing framework for BirdNET-Go.
// It implements a modular architecture for audio capture, processing, and analysis
// with direct buffer integration between components.
//
// # Architecture Overview
//
// The audiocore package consists of several key components:
//
//   - Audio Sources: Capture audio from various inputs (soundcard, files, streams)
//   - Processing Pipeline: Manages data flow from sources to analyzers
//   - Analyzers: Perform audio analysis (e.g., bird detection)
//   - Buffer Management: Efficient memory management with buffer pooling
//   - Resource Tracking: Monitors resource usage and detects leaks
//
// # Concurrency and Thread Safety
//
// All public types and methods in audiocore are designed to be thread-safe unless
// explicitly documented otherwise. The following guarantees are provided:
//
// ## Thread-Safe Components
//
//   - AudioManager: All methods can be called concurrently from multiple goroutines
//   - AnalyzerManager: Thread-safe for registration, retrieval, and removal
//   - ProcessingPipeline: Safe for concurrent Start/Stop and metrics access
//   - SafeAnalyzerWrapper: Provides thread-safe wrapper around analyzers
//   - BufferPool: Concurrent Get/Put operations are safe
//   - ResourceTracker: Thread-safe resource tracking and leak detection
//   - CircularBuffer: All methods are protected by mutex
//   - ChunkBufferV2: Thread-safe accumulation and retrieval
//
// ## Concurrency Patterns
//
// The package uses several concurrency patterns:
//
//   - Worker Pools: SafeAnalyzerWrapper uses a configurable worker pool
//   - Channel-based Communication: Audio data flows through channels
//   - Mutex Protection: Shared state is protected with sync.RWMutex
//   - Atomic Operations: Counters and flags use atomic types
//
// ## Best Practices
//
// When using audiocore components:
//
//  1. Always close resources (sources, analyzers, pipelines) when done
//  2. Use context.Context for cancellation and timeouts
//  3. Monitor metrics for performance and health
//  4. Handle errors appropriately - all errors use enhanced error system
//
// # Buffer Lifecycle
//
// Buffers obtained from BufferPool follow this lifecycle:
//
//  1. Get: Obtain buffer from pool (or allocate if pool is empty)
//  2. Use: Fill buffer with audio data
//  3. Pass: Transfer ownership via AudioData.BufferHandle
//  4. Release: Consumer calls BufferHandle.Release() when done
//
// Example:
//
//	buffer := pool.Get(size)
//	defer buffer.Release() // Always release when done
//	
//	// Use buffer...
//	data := AudioData{
//	    Buffer: buffer.Data(),
//	    BufferHandle: buffer, // Transfer ownership
//	}
//	
//	// Consumer is now responsible for calling data.BufferHandle.Release()
//
// # Error Handling
//
// All errors in audiocore use the enhanced error system with proper
// component and category tagging. Always check errors and use the
// error context for debugging:
//
//	if err != nil {
//	    // Error will have component, category, and context
//	    logger.Error("operation failed", "error", err)
//	}
package audiocore