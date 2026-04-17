package ffmpeg

import (
	"bytes"
	"context"
	"io"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
)

// ffmpegTimeoutFlag is the FFmpeg flag name for connection timeout used in tests.
const ffmpegTimeoutFlag = "-timeout"

// newTestConfig returns a minimal StreamConfig suitable for unit tests.
func newTestConfig() StreamConfig {
	return StreamConfig{
		URL:        "rtsp://test.example.com/stream",
		SourceID:   "test-source-1",
		SourceName: "Test Stream",
		Type:       "rtsp",
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		FFmpegPath: "/usr/bin/ffmpeg",
		Transport:  "tcp",
		LogLevel:   "error",
	}
}

// newTestStream creates a Stream for testing with no-op callbacks.
func newTestStream(t *testing.T) *Stream {
	t.Helper()
	cfg := newTestConfig()
	return NewStream(&cfg, nil, nil, nil, nil)
}

// newTestStreamWithConfig creates a Stream for testing with a custom config.
func newTestStreamWithConfig(t *testing.T, cfg *StreamConfig) *Stream {
	t.Helper()
	return NewStream(cfg, nil, nil, nil, nil)
}

// --- Test helpers for accessing internal state ---

func (s *Stream) getConsecutiveFailures() int {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()
	return s.consecutiveFailures
}

func (s *Stream) setConsecutiveFailures(count int) {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()
	s.consecutiveFailures = count
}

func (s *Stream) getLastDataTimeForTest() time.Time {
	s.lastDataMu.RLock()
	defer s.lastDataMu.RUnlock()
	return s.lastDataTime
}

func (s *Stream) setLastDataTimeForTest(t time.Time) {
	s.lastDataMu.Lock()
	defer s.lastDataMu.Unlock()
	s.lastDataTime = t
}

func (s *Stream) setCircuitOpenTimeForTest(t time.Time) {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()
	s.circuitOpenTime = t
}

func (s *Stream) resetCircuitStateForTest() {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()
	s.consecutiveFailures = 0
	s.circuitOpenTime = time.Time{}
}

func (s *Stream) setProcessStartTimeForTest(t time.Time) {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	s.processStartTime = t
}

func (s *Stream) getProcessStartTimeForTest() time.Time {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	return s.processStartTime
}

func (s *Stream) setTotalBytesReceivedForTest(numBytes int64) {
	s.bytesReceivedMu.Lock()
	defer s.bytesReceivedMu.Unlock()
	s.totalBytesReceived = numBytes
}

func (s *Stream) getTotalBytesReceivedForTest() int64 {
	s.bytesReceivedMu.RLock()
	defer s.bytesReceivedMu.RUnlock()
	return s.totalBytesReceived
}

func (s *Stream) getStreamCreatedAtForTest() time.Time {
	s.streamCreatedAtMu.RLock()
	defer s.streamCreatedAtMu.RUnlock()
	return s.streamCreatedAt
}

func (s *Stream) getCircuitOpenTimeForTest() time.Time {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()
	return s.circuitOpenTime
}

// --- Tests ---

func TestStream_NewStream(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	assert.NotNil(t, stream)
	assert.Equal(t, "rtsp://test.example.com/stream", stream.config.URL)
	assert.Equal(t, "tcp", stream.config.Transport)
	assert.NotNil(t, stream.restartChan)
	assert.NotNil(t, stream.stopChan)
	assert.Equal(t, 5*time.Second, stream.backoffDuration)
	assert.Equal(t, 2*time.Minute, stream.maxBackoff)
	assert.Equal(t, StateIdle, stream.GetProcessState())
}

func TestStream_Stop(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	stream.Stop()

	stream.stoppedMu.RLock()
	stopped := stream.stopped
	stream.stoppedMu.RUnlock()
	assert.True(t, stopped)

	select {
	case <-stream.stopChan:
		// Expected - channel should be closed.
	default:
		require.Fail(t, "Stop channel should be closed")
	}

	assert.Equal(t, StateStopped, stream.GetProcessState())
}

func TestStream_Restart(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	stream.Restart(true)

	select {
	case <-stream.restartChan:
		// Expected - restart signal received.
	case <-time.After(100 * time.Millisecond):
		require.Fail(t, "Restart signal not received")
	}

	stream.restartCountMu.Lock()
	count := stream.restartCount
	stream.restartCountMu.Unlock()
	assert.Equal(t, 0, count)
}

func TestStream_GetHealth(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	// Initial health - should not be healthy without data.
	health := stream.GetHealth()
	assert.False(t, health.IsHealthy, "New stream should not be healthy without data")
	assert.True(t, health.LastDataReceived.IsZero(), "Initial LastDataReceived should be zero time")
	assert.Equal(t, 0, health.RestartCount)

	// Set state to running and update data time to make stream healthy.
	stream.processStateMu.Lock()
	stream.processState = StateRunning
	stream.processStateMu.Unlock()
	stream.updateLastDataTime()
	health = stream.GetHealth()
	assert.True(t, health.IsHealthy, "Stream should be healthy after receiving data")
	assert.WithinDuration(t, time.Now(), health.LastDataReceived, time.Second)

	// Simulate old data time.
	stream.lastDataMu.Lock()
	stream.lastDataTime = time.Now().Add(-2 * time.Minute)
	stream.lastDataMu.Unlock()

	health = stream.GetHealth()
	assert.False(t, health.IsHealthy)

	// Update restart count.
	stream.restartCountMu.Lock()
	stream.restartCount = 5
	stream.restartCountMu.Unlock()

	health = stream.GetHealth()
	assert.Equal(t, 5, health.RestartCount)
}

func TestStream_UpdateLastDataTime(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	oldTime := time.Now().Add(-1 * time.Hour)
	stream.lastDataMu.Lock()
	stream.lastDataTime = oldTime
	stream.lastDataMu.Unlock()

	stream.updateLastDataTime()

	stream.lastDataMu.RLock()
	newTime := stream.lastDataTime
	stream.lastDataMu.RUnlock()

	assert.True(t, newTime.After(oldTime))
	assert.WithinDuration(t, time.Now(), newTime, time.Second)
}

func TestStream_BackoffCalculation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		restartCount int
		expectedWait time.Duration
	}{
		{"First restart", 1, 5 * time.Second},
		{"Second restart", 2, 10 * time.Second},
		{"Third restart", 3, 20 * time.Second},
		{"Fourth restart", 4, 40 * time.Second},
		{"Fifth restart", 5, 80 * time.Second},
		{"Sixth restart (capped)", 6, 2 * time.Minute},
		{"Tenth restart (capped)", 10, 2 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stream := newTestStream(t)

			stream.restartCountMu.Lock()
			stream.restartCount = tt.restartCount - 1
			stream.restartCountMu.Unlock()

			exponent := min(tt.restartCount-1, maxBackoffExponent)
			backoff := min(stream.backoffDuration*time.Duration(1<<uint(exponent)), stream.maxBackoff) //nolint:gosec // G115: exponent is capped by min()

			assert.Equal(t, tt.expectedWait, backoff)
		})
	}
}

func TestStream_ConcurrentHealthAccess(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	done := make(chan bool)

	// Reader goroutines.
	for range 5 {
		go func() {
			for range 100 {
				health := stream.GetHealth()
				_ = health.IsHealthy
			}
			done <- true
		}()
	}

	// Writer goroutines.
	for range 3 {
		go func() {
			for range 100 {
				stream.updateLastDataTime()
			}
			done <- true
		}()
	}

	// Restart count updater.
	go func() {
		for range 100 {
			stream.restartCountMu.Lock()
			stream.restartCount++
			stream.restartCountMu.Unlock()
		}
		done <- true
	}()

	for range 9 {
		<-done
	}

	health := stream.GetHealth()
	assert.NotNil(t, health)
}

func TestStream_CircuitBreakerBehavior(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	// Circuit breaker is initially closed.
	assert.False(t, stream.isCircuitOpen())

	// Simulate failures to trigger circuit breaker.
	for range 12 {
		stream.recordFailure(2 * time.Second)
	}

	// Circuit should now be open.
	assert.True(t, stream.isCircuitOpen())

	// Reset and verify closed.
	stream.resetCircuitStateForTest()
	assert.False(t, stream.isCircuitOpen())
}

func TestStream_CircuitBreakerGraduatedThresholds(t *testing.T) {
	t.Parallel()

	t.Run("immediate failures open after 3", func(t *testing.T) {
		t.Parallel()
		stream := newTestStream(t)
		for range 3 {
			stream.recordFailure(500 * time.Millisecond)
		}
		assert.True(t, stream.isCircuitOpen())
	})

	t.Run("rapid failures open after 5", func(t *testing.T) {
		t.Parallel()
		stream := newTestStream(t)
		for range 5 {
			stream.recordFailure(2 * time.Second)
		}
		assert.True(t, stream.isCircuitOpen())
	})

	t.Run("quick failures open after 8", func(t *testing.T) {
		t.Parallel()
		stream := newTestStream(t)
		for range 8 {
			stream.recordFailure(10 * time.Second)
		}
		assert.True(t, stream.isCircuitOpen())
	})

	t.Run("normal failures open after 10", func(t *testing.T) {
		t.Parallel()
		stream := newTestStream(t)
		for range 10 {
			stream.recordFailure(60 * time.Second)
		}
		assert.True(t, stream.isCircuitOpen())
	})

	t.Run("not enough rapid failures stays closed", func(t *testing.T) {
		t.Parallel()
		stream := newTestStream(t)
		for range 4 {
			stream.recordFailure(2 * time.Second)
		}
		assert.False(t, stream.isCircuitOpen())
	})
}

func TestStream_CircuitBreakerCooldown(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	// Open the circuit breaker.
	stream.setConsecutiveFailures(circuitBreakerThreshold)
	stream.setCircuitOpenTimeForTest(time.Now())

	// Should be open.
	assert.True(t, stream.isCircuitOpen())

	// Set open time to past the cooldown period.
	stream.setCircuitOpenTimeForTest(time.Now().Add(-circuitBreakerCooldown - time.Second))

	// Should be closed after cooldown.
	assert.False(t, stream.isCircuitOpen())

	// Failures should be reset.
	assert.Equal(t, 0, stream.getConsecutiveFailures())
}

func TestStream_DataRateCalculation(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)
	calc := stream.dataRateCalc

	calc.addSample(1024)
	calc.addSample(2048)
	calc.addSample(1536)

	rate := calc.getRate()
	assert.Greater(t, rate, 0.0)
}

func TestStream_DataRateCalculator_EmptyRate(t *testing.T) {
	t.Parallel()

	calc := newDataRateCalculator(dataRateWindowSize)
	assert.InDelta(t, 0.0, calc.getRate(), 0.001)
}

func TestStream_DataRateCalculator_SingleSampleRate(t *testing.T) {
	t.Parallel()

	calc := newDataRateCalculator(dataRateWindowSize)
	calc.addSample(1024)

	rate := calc.getRate()
	assert.InDelta(t, 1024.0, rate, 0.01, "Single recent sample should return instantaneous rate")
}

func TestStream_HealthTracking(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	health := stream.GetHealth()
	assert.False(t, health.IsHealthy)

	// Set state to running so health check considers timestamp.
	stream.processStateMu.Lock()
	stream.processState = StateRunning
	stream.processStateMu.Unlock()
	stream.updateLastDataTime()
	health = stream.GetHealth()
	assert.True(t, health.IsHealthy)
	assert.Equal(t, 0, health.RestartCount)
	assert.Equal(t, int64(0), health.TotalBytesReceived)

	stream.updateLastDataTime()
	stream.bytesReceivedMu.Lock()
	stream.totalBytesReceived = 1024
	stream.bytesReceivedMu.Unlock()

	stream.restartCountMu.Lock()
	stream.restartCount = 3
	stream.restartCountMu.Unlock()

	health = stream.GetHealth()
	assert.True(t, health.IsHealthy)
	assert.Equal(t, 3, health.RestartCount)
	assert.Equal(t, int64(1024), health.TotalBytesReceived)
}

func TestStream_ConcurrentHealthAndDataUpdates(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	const numGoroutines = 10
	const numOperations = 100
	done := make(chan bool, numGoroutines)

	for range numGoroutines / 2 {
		go func() {
			defer func() { done <- true }()
			for range numOperations {
				health := stream.GetHealth()
				assert.NotNil(t, health)
			}
		}()
	}

	for range numGoroutines / 2 {
		go func() {
			defer func() { done <- true }()
			for range numOperations {
				stream.updateLastDataTime()
				stream.bytesReceivedMu.Lock()
				stream.totalBytesReceived += 100
				stream.bytesReceivedMu.Unlock()
			}
		}()
	}

	for range numGoroutines {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			require.Fail(t, "Concurrent test timed out")
		}
	}

	health := stream.GetHealth()
	assert.NotNil(t, health)
	assert.Positive(t, health.TotalBytesReceived)
}

func TestStream_BackoffOverflowProtection(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	stream.restartCountMu.Lock()
	stream.restartCount = 100
	stream.restartCountMu.Unlock()

	exponent := min(100-1, maxBackoffExponent)
	expectedBackoff := min(stream.backoffDuration*time.Duration(1<<uint(exponent)), stream.maxBackoff) //nolint:gosec // G115: exponent is capped by min()

	assert.Equal(t, 2*time.Minute, expectedBackoff)

	assert.NotPanics(t, func() {
		testBackoff := stream.backoffDuration * time.Duration(1<<uint(exponent)) //nolint:gosec // G115: exponent is capped by min()
		_ = testBackoff
	})
}

func TestStream_ProcessStateTransitions(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	assert.Equal(t, StateIdle, stream.GetProcessState())

	stream.transitionState(StateStarting, "test: starting")
	assert.Equal(t, StateStarting, stream.GetProcessState())

	stream.transitionState(StateRunning, "test: running")
	assert.Equal(t, StateRunning, stream.GetProcessState())

	stream.transitionState(StateRestarting, "test: restarting")
	assert.Equal(t, StateRestarting, stream.GetProcessState())

	stream.transitionState(StateBackoff, "test: backoff")
	assert.Equal(t, StateBackoff, stream.GetProcessState())

	stream.transitionState(StateStarting, "test: starting again")
	assert.Equal(t, StateStarting, stream.GetProcessState())

	stream.transitionState(StateRunning, "test: running again")
	assert.Equal(t, StateRunning, stream.GetProcessState())

	stream.transitionState(StateStopped, "test: stopped")
	assert.Equal(t, StateStopped, stream.GetProcessState())

	// Verify terminal state: cannot leave StateStopped.
	stream.transitionState(StateStarting, "test: should be blocked")
	assert.Equal(t, StateStopped, stream.GetProcessState())
}

func TestStream_StateHistory(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	stream.transitionState(StateStarting, "test1")
	stream.transitionState(StateRunning, "test2")
	stream.transitionState(StateRestarting, "test3")

	history := stream.GetStateHistory()
	require.Len(t, history, 3)

	assert.Equal(t, StateIdle, history[0].From)
	assert.Equal(t, StateStarting, history[0].To)
	assert.Equal(t, "test1", history[0].Reason)

	assert.Equal(t, StateStarting, history[1].From)
	assert.Equal(t, StateRunning, history[1].To)

	assert.Equal(t, StateRunning, history[2].From)
	assert.Equal(t, StateRestarting, history[2].To)
}

func TestStream_IdempotentTransition(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	stream.transitionState(StateStarting, "initial")

	// Same state transition should be silently ignored.
	stream.transitionState(StateStarting, "duplicate")

	history := stream.GetStateHistory()
	assert.Len(t, history, 1, "Idempotent transitions should not produce history entries")
}

func TestStream_IsRestarting(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	assert.False(t, stream.IsRestarting())

	stream.transitionState(StateStarting, "test")
	assert.True(t, stream.IsRestarting())

	stream.transitionState(StateRunning, "test")
	assert.False(t, stream.IsRestarting())

	stream.transitionState(StateRestarting, "test")
	assert.True(t, stream.IsRestarting())

	stream.transitionState(StateBackoff, "test")
	assert.True(t, stream.IsRestarting())

	stream.transitionState(StateCircuitOpen, "test")
	assert.True(t, stream.IsRestarting())

	stream.transitionState(StateStopped, "test")
	assert.False(t, stream.IsRestarting())
}

func TestStream_ValidTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		from  ProcessState
		to    ProcessState
		valid bool
	}{
		{"idle to starting", StateIdle, StateStarting, true},
		{"idle to stopped", StateIdle, StateStopped, true},
		{"idle to running", StateIdle, StateRunning, false},
		{"starting to running", StateStarting, StateRunning, true},
		{"starting to backoff", StateStarting, StateBackoff, true},
		{"starting to circuit_open", StateStarting, StateCircuitOpen, true},
		{"starting to stopped", StateStarting, StateStopped, true},
		{"running to restarting", StateRunning, StateRestarting, true},
		{"running to backoff", StateRunning, StateBackoff, true},
		{"running to stopped", StateRunning, StateStopped, true},
		{"running to starting", StateRunning, StateStarting, false},
		{"backoff to starting", StateBackoff, StateStarting, true},
		{"backoff to circuit_open", StateBackoff, StateCircuitOpen, true},
		{"backoff to stopped", StateBackoff, StateStopped, true},
		{"circuit_open to starting", StateCircuitOpen, StateStarting, true},
		{"circuit_open to stopped", StateCircuitOpen, StateStopped, true},
		{"circuit_open to running", StateCircuitOpen, StateRunning, false},
		{"stopped to any", StateStopped, StateStarting, false},
		{"same state", StateRunning, StateRunning, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.valid, isValidTransition(tt.from, tt.to))
		})
	}
}

func TestStream_ConditionalFailureReset(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	// Set up: failures, process start time, and bytes.
	stream.setConsecutiveFailures(5)
	stream.setProcessStartTimeForTest(time.Now().Add(-circuitBreakerMinStabilityTime - time.Second))
	stream.setTotalBytesReceivedForTest(circuitBreakerMinStabilityBytes + 1)

	stream.conditionalFailureReset(stream.getTotalBytesReceivedForTest())

	assert.Equal(t, 0, stream.getConsecutiveFailures(), "Failures should be reset after stable operation")
}

func TestStream_ConditionalFailureReset_NotEnoughTime(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	stream.setConsecutiveFailures(5)
	stream.setProcessStartTimeForTest(time.Now().Add(-5 * time.Second))
	stream.setTotalBytesReceivedForTest(circuitBreakerMinStabilityBytes + 1)

	stream.conditionalFailureReset(stream.getTotalBytesReceivedForTest())

	assert.Equal(t, 5, stream.getConsecutiveFailures(), "Failures should NOT be reset without enough runtime")
}

func TestStream_ConditionalFailureReset_NotEnoughBytes(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	stream.setConsecutiveFailures(5)
	stream.setProcessStartTimeForTest(time.Now().Add(-circuitBreakerMinStabilityTime - time.Second))
	stream.setTotalBytesReceivedForTest(100)

	stream.conditionalFailureReset(100)

	assert.Equal(t, 5, stream.getConsecutiveFailures(), "Failures should NOT be reset without enough bytes")
}

func TestStream_ValidateUserTimeout(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	tests := []struct {
		name          string
		timeoutStr    string
		expectError   bool
		errorContains string
	}{
		{"valid_1_second", "1000000", false, ""},
		{"valid_5_seconds", "5000000", false, ""},
		{"valid_30_seconds", "30000000", false, ""},
		{"valid_large_timeout", "120000000", false, ""},
		{"invalid_format_letters", "abc", true, "invalid timeout format"},
		{"invalid_format_mixed", "123abc", true, "invalid timeout format"},
		{"empty_string", "", true, "invalid timeout format"},
		{"too_short_zero", "0", true, "timeout too short"},
		{"too_short_negative", "-1000", true, "timeout too short"},
		{"too_short_half_second", "500000", true, "timeout too short"},
		{"boundary_minimum_minus_one", "999999", true, "timeout too short"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := stream.validateUserTimeout(tt.timeoutStr)

			if tt.expectError {
				require.Error(t, err, "Expected error for timeout: %s", tt.timeoutStr)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				assert.NoError(t, err, "Expected no error for valid timeout: %s", tt.timeoutStr)
			}
		})
	}
}

func TestDetectUserTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		params        []string
		expectedFound bool
		expectedValue string
	}{
		{"empty_params", []string{}, false, ""},
		{"no_timeout_param", []string{"-loglevel", "debug"}, false, ""},
		{"timeout_with_value", []string{"-timeout", "5000000"}, true, "5000000"},
		{"timeout_without_value", []string{"-timeout"}, false, ""},
		{"timeout_in_middle", []string{"-loglevel", "debug", "-timeout", "10000000", "-rtsp_flags", "prefer_tcp"}, true, "10000000"},
		{"first_timeout_wins", []string{"-timeout", "5000000", "-timeout", "10000000"}, true, "5000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			found, value := detectUserTimeout(tt.params)
			assert.Equal(t, tt.expectedFound, found)
			assert.Equal(t, tt.expectedValue, value)
		})
	}
}

func TestStream_BuildFFmpegInputArgs_RTSP(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig()
	cfg.Type = "rtsp"
	stream := newTestStreamWithConfig(t, &cfg)

	args := stream.buildFFmpegInputArgs(nil)

	assert.Contains(t, args, "-rtsp_transport")
	assert.Contains(t, args, "tcp")
	assert.Contains(t, args, ffmpegTimeoutFlag)
}

func TestStream_BuildFFmpegInputArgs_HTTP(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig()
	cfg.URL = "http://192.168.1.183/stream"
	cfg.Type = "http"
	stream := newTestStreamWithConfig(t, &cfg)

	args := stream.buildFFmpegInputArgs(nil)

	assert.NotContains(t, args, "-rtsp_transport", "HTTP stream should not have RTSP transport")
	assert.Contains(t, args, ffmpegTimeoutFlag)
}

func TestStream_BuildFFmpegInputArgs_InvalidTimeout(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig()
	stream := newTestStreamWithConfig(t, &cfg)

	params := []string{"-timeout", "abc", "-loglevel", "debug"}
	args := stream.buildFFmpegInputArgs(params)

	timeoutIdx := -1
	for i, a := range args {
		if a == ffmpegTimeoutFlag {
			timeoutIdx = i
			break
		}
	}
	require.NotEqual(t, -1, timeoutIdx, "-timeout flag must be present")
	require.Less(t, timeoutIdx+1, len(args), "-timeout must have a following value")
	assert.NotEqual(t, "abc", args[timeoutIdx+1], "invalid timeout value must not reach FFmpeg")

	count := 0
	for _, a := range args {
		if a == ffmpegTimeoutFlag {
			count++
		}
	}
	assert.Equal(t, 1, count, "-timeout must appear exactly once")

	assert.Contains(t, args, "-loglevel")
	assert.Contains(t, args, "debug")
}

func TestStream_BuildFFmpegInputArgs_ProtocolSpecific(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		url               string
		sourceType        string
		transport         string
		ffmpegParams      []string
		wantRTSPTransport bool
		wantTimeout       bool
	}{
		{
			name:              "rtsp_stream_includes_rtsp_transport",
			url:               "rtsp://192.168.1.100/stream",
			sourceType:        "rtsp",
			transport:         "tcp",
			ffmpegParams:      []string{},
			wantRTSPTransport: true,
			wantTimeout:       true,
		},
		{
			name:              "http_stream_excludes_rtsp_transport",
			url:               "http://192.168.1.183/stream",
			sourceType:        "http",
			transport:         "tcp",
			ffmpegParams:      []string{"-f", "s16le", "-ar", "48000", "-ac", "1"},
			wantRTSPTransport: false,
			wantTimeout:       true,
		},
		{
			name:              "hls_stream_excludes_rtsp_transport",
			url:               "http://example.com/stream.m3u8",
			sourceType:        "hls",
			transport:         "tcp",
			ffmpegParams:      []string{},
			wantRTSPTransport: false,
			wantTimeout:       true,
		},
		{
			name:              "rtsp_with_user_timeout",
			url:               "rtsp://192.168.1.100/stream",
			sourceType:        "rtsp",
			transport:         "udp",
			ffmpegParams:      []string{"-timeout", "5000000"},
			wantRTSPTransport: true,
			wantTimeout:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := newTestConfig()
			cfg.URL = tt.url
			cfg.Type = tt.sourceType
			cfg.Transport = tt.transport
			stream := newTestStreamWithConfig(t, &cfg)

			args := stream.buildFFmpegInputArgs(tt.ffmpegParams)

			hasRTSPTransport := slices.Contains(args, "-rtsp_transport")
			hasTimeout := slices.Contains(args, ffmpegTimeoutFlag)

			assert.Equal(t, tt.wantRTSPTransport, hasRTSPTransport, "rtsp_transport flag presence mismatch")
			assert.Equal(t, tt.wantTimeout, hasTimeout, "timeout flag presence mismatch")
		})
	}
}

func TestStream_ZeroTimeHandling(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	// New stream with zero lastDataTime should not be healthy.
	health := stream.GetHealth()
	assert.True(t, health.LastDataReceived.IsZero())
	assert.False(t, health.IsHealthy)
	assert.False(t, health.IsReceivingData)

	// After grace period: still not healthy.
	stream.streamCreatedAtMu.Lock()
	stream.streamCreatedAt = time.Now().Add(-2 * defaultGracePeriod)
	stream.streamCreatedAtMu.Unlock()
	health = stream.GetHealth()
	assert.True(t, health.LastDataReceived.IsZero())
	assert.False(t, health.IsHealthy)

	// After receiving data with state=running: healthy.
	stream.processStateMu.Lock()
	stream.processState = StateRunning
	stream.processStateMu.Unlock()
	stream.updateLastDataTime()
	health = stream.GetHealth()
	assert.False(t, health.LastDataReceived.IsZero())
	assert.True(t, health.IsHealthy)
	assert.True(t, health.IsReceivingData)
}

func TestSecondsSinceOrZero(t *testing.T) {
	t.Parallel()

	zeroTime := time.Time{}
	result := secondsSinceOrZero(zeroTime)
	assert.InDelta(t, 0.0, result, 0.001)

	recentTime := time.Now().Add(-5 * time.Second)
	result = secondsSinceOrZero(recentTime)
	assert.Greater(t, result, 4.0)
	assert.Less(t, result, 6.0)

	oldTime := time.Now().Add(-1 * time.Hour)
	result = secondsSinceOrZero(oldTime)
	assert.Greater(t, result, 3500.0)
	assert.Less(t, result, 3700.0)
}

func TestFormatLastDataDescription(t *testing.T) {
	t.Parallel()

	zeroTime := time.Time{}
	result := formatLastDataDescription(zeroTime)
	assert.Equal(t, "never received data", result)

	recentTime := time.Now().Add(-5 * time.Second)
	result = formatLastDataDescription(recentTime)
	assert.NotEqual(t, "never received data", result)
	assert.Contains(t, result, "ago")
	assert.Regexp(t, `^\d+\.\ds ago$`, result)
}

func TestStream_HealthWithZeroTime(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	stream.lastDataMu.Lock()
	stream.lastDataTime = time.Time{}
	stream.lastDataMu.Unlock()

	for i := range 5 {
		health := stream.GetHealth()
		assert.True(t, health.LastDataReceived.IsZero(), "LastDataReceived should be zero on iteration %d", i)
		assert.NotNil(t, health, "Health should not be nil on iteration %d", i)
	}
}

func TestStream_ConcurrentLastDataTimeAccess(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	const numGoroutines = 20
	const numOperations = 100
	done := make(chan bool, numGoroutines)

	for range numGoroutines / 2 {
		go func() {
			defer func() { done <- true }()
			for range numOperations {
				stream.lastDataMu.RLock()
				_ = stream.lastDataTime
				stream.lastDataMu.RUnlock()
			}
		}()
	}

	for range numGoroutines / 2 {
		go func() {
			defer func() { done <- true }()
			for range numOperations {
				stream.updateLastDataTime()
			}
		}()
	}

	for range numGoroutines {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			require.Fail(t, "Concurrent test timed out - possible deadlock")
		}
	}

	stream.lastDataMu.RLock()
	finalTime := stream.lastDataTime
	stream.lastDataMu.RUnlock()
	assert.False(t, finalTime.IsZero(), "After concurrent updates, lastDataTime should not be zero")
}

func TestStream_LastDataTimeReset(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	oldTime := time.Now().Add(-1 * time.Hour)
	stream.lastDataMu.Lock()
	stream.lastDataTime = oldTime
	stream.lastDataMu.Unlock()

	stream.lastDataMu.RLock()
	beforeReset := stream.lastDataTime
	stream.lastDataMu.RUnlock()
	assert.Equal(t, oldTime, beforeReset)

	// New stream should have zero lastDataTime.
	newStream := newTestStream(t)
	newStream.lastDataMu.RLock()
	initialTime := newStream.lastDataTime
	newStream.lastDataMu.RUnlock()
	assert.True(t, initialTime.IsZero(), "New stream should have zero lastDataTime")
}

func TestStream_ErrorContextTracking(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	assert.Nil(t, stream.getLastErrorContext())
	assert.Empty(t, stream.getErrorContexts())

	ctx1 := &ErrorContext{ErrorType: ErrTypeConnectionTimeout, PrimaryMessage: "test1"}
	ctx2 := &ErrorContext{ErrorType: ErrTypeConnectionRefused, PrimaryMessage: "test2"}

	stream.recordErrorContext(ctx1)
	assert.Equal(t, ctx1, stream.getLastErrorContext())

	stream.recordErrorContext(ctx2)
	assert.Equal(t, ctx2, stream.getLastErrorContext())

	contexts := stream.getErrorContexts()
	assert.Len(t, contexts, 2)
	assert.Equal(t, ctx1, contexts[0])
	assert.Equal(t, ctx2, contexts[1])
}

func TestStream_ErrorContextNilSafe(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)
	stream.recordErrorContext(nil)
	assert.Empty(t, stream.getErrorContexts())
}

func TestStream_OnFrameCallback(t *testing.T) {
	t.Parallel()

	var receivedFrame audiocore.AudioFrame

	cfg := newTestConfig()
	stream := NewStream(&cfg, func(frame audiocore.AudioFrame) {
		receivedFrame = frame
	}, nil, nil, nil)

	assert.NotNil(t, stream)
	assert.NotNil(t, stream.onFrame)

	// Simulate onFrame call with a fully populated frame.
	testFrame := audiocore.AudioFrame{
		SourceID:   "test-source-1",
		SourceName: "Test Source",
		Data:       []byte{1, 2, 3},
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
	}
	stream.onFrame(testFrame)
	assert.Equal(t, "test-source-1", receivedFrame.SourceID)
	assert.Equal(t, "Test Source", receivedFrame.SourceName)
	assert.Equal(t, []byte{1, 2, 3}, receivedFrame.Data)
	assert.Equal(t, 48000, receivedFrame.SampleRate)
}

func TestStream_OnResetCallback(t *testing.T) {
	t.Parallel()

	var resetCalled bool
	var resetSourceID string

	cfg := newTestConfig()
	stream := NewStream(&cfg, nil, func(sourceID string) {
		resetCalled = true
		resetSourceID = sourceID
	}, nil, nil)

	assert.NotNil(t, stream.onReset)

	// Simulate onReset call.
	stream.onReset("test-source-1")
	assert.True(t, resetCalled)
	assert.Equal(t, "test-source-1", resetSourceID)
}

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    time.Duration
		expected string
	}{
		{"zero", 0, "0ms"},
		{"milliseconds", 450 * time.Millisecond, "450ms"},
		{"one_second", time.Second, "1s"},
		{"seconds", 11*time.Second + 400*time.Millisecond, "11s"},
		{"one_minute", time.Minute, "1m 0s"},
		{"minutes_and_seconds", 2*time.Minute + 30*time.Second, "2m 30s"},
		{"one_hour", time.Hour, "1h 0m 0s"},
		{"hours_minutes_seconds", time.Hour + 23*time.Minute + 45*time.Second, "1h 23m 45s"},
		{"negative", -5 * time.Second, "-5s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, formatDuration(tt.input))
		})
	}
}

func TestStream_ResetDataTracking(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	stream.updateLastDataTime()
	stream.bytesReceivedMu.Lock()
	stream.totalBytesReceived = 1000
	stream.bytesReceivedMu.Unlock()

	stream.resetDataTracking()

	stream.lastDataMu.RLock()
	lastData := stream.lastDataTime
	stream.lastDataMu.RUnlock()
	assert.True(t, lastData.IsZero(), "lastDataTime should be reset to zero")

	stream.bytesReceivedMu.RLock()
	totalBytes := stream.totalBytesReceived
	stream.bytesReceivedMu.RUnlock()
	assert.Equal(t, int64(0), totalBytes, "totalBytesReceived should be reset to 0")
}

func TestStream_SourceTypeDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configType string
		expected   string
	}{
		{"rtsp", "rtsp", "rtsp"},
		{"RTSP_uppercase", "RTSP", "rtsp"},
		{"http", "http", "http"},
		{"https", "https", "http"},
		{"hls", "hls", "hls"},
		{"rtmp", "rtmp", "rtmp"},
		{"udp", "udp", "udp"},
		{"unknown", "foobar", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := StreamConfig{Type: tt.configType}
			assert.Equal(t, tt.expected, cfg.sourceType().String())
		})
	}
}

func TestStream_HealthyThresholdConfig(t *testing.T) {
	t.Parallel()

	t.Run("default_when_zero", func(t *testing.T) {
		t.Parallel()
		cfg := StreamConfig{}
		assert.Equal(t, defaultHealthyDataThreshold, cfg.healthyThreshold())
	})

	t.Run("custom_threshold", func(t *testing.T) {
		t.Parallel()
		cfg := StreamConfig{HealthyDataThreshold: 30 * time.Second}
		assert.Equal(t, 30*time.Second, cfg.healthyThreshold())
	})
}

func TestStream_StopIdempotent(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	// Multiple calls to Stop should not panic.
	assert.NotPanics(t, func() {
		stream.Stop()
		stream.Stop()
		stream.Stop()
	})

	assert.Equal(t, StateStopped, stream.GetProcessState())
}

// --- Issue 1: Non-blocking reader goroutine ---

// blockingReader is a test io.ReadCloser that blocks until unblocked via a channel,
// used to verify that processAudio remains responsive to control signals even
// when stdout.Read is stuck.
type blockingReader struct {
	unblock chan struct{}
	data    []byte
	closed  bool
}

func (b *blockingReader) Read(p []byte) (int, error) {
	if b.closed {
		return 0, io.EOF
	}
	<-b.unblock
	if b.closed {
		return 0, io.EOF
	}
	n := copy(p, b.data)
	return n, nil
}

func (b *blockingReader) Close() error {
	b.closed = true
	// Unblock any pending Read by closing the channel (idempotent via select).
	select {
	case <-b.unblock:
	default:
		close(b.unblock)
	}
	return nil
}

// TestProcessAudio_ResponsiveToContextCancel verifies that processAudio returns
// promptly when the context is cancelled, even if the reader is blocked.
func TestProcessAudio_ResponsiveToContextCancel(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)
	ctx, cancel := context.WithCancel(t.Context())

	stream.cancelMu.Lock()
	stream.ctx = ctx
	stream.cancel = func(err error) { cancel() }
	stream.cancelMu.Unlock()

	reader := &blockingReader{unblock: make(chan struct{}), data: []byte{0x01}}

	stream.cmdMu.Lock()
	stream.stdout = reader
	stream.processStartTime = time.Now()
	stream.cmdMu.Unlock()

	stream.transitionState(StateStarting, "test setup")
	stream.transitionState(StateRunning, "test setup")

	done := make(chan error, 1)
	go func() {
		done <- stream.processAudio()
	}()

	// Cancel the context while the reader is blocked.
	cancel()

	select {
	case <-done:
		// processAudio returned promptly — this is the expected behavior.
	case <-time.After(2 * time.Second):
		require.Fail(t, "processAudio did not respond to context cancellation within 2s")
	}
}

// TestProcessAudio_ResponsiveToRestart verifies that processAudio returns
// promptly when a restart signal is sent, even if the reader is blocked.
func TestProcessAudio_ResponsiveToRestart(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	stream.cancelMu.Lock()
	stream.ctx = ctx
	stream.cancel = func(err error) { cancel() }
	stream.cancelMu.Unlock()

	reader := &blockingReader{unblock: make(chan struct{}), data: []byte{0x01}}

	stream.cmdMu.Lock()
	stream.stdout = reader
	stream.processStartTime = time.Now()
	stream.cmdMu.Unlock()

	stream.transitionState(StateStarting, "test setup")
	stream.transitionState(StateRunning, "test setup")

	done := make(chan error, 1)
	go func() {
		done <- stream.processAudio()
	}()

	// Send a restart signal while the reader is blocked.
	stream.restartChan <- struct{}{}

	select {
	case err := <-done:
		require.NoError(t, err, "restart path should return nil")
	case <-time.After(2 * time.Second):
		require.Fail(t, "processAudio did not respond to restart signal within 2s")
	}
}

// TestProcessAudio_DataFlowsThroughReaderGoroutine verifies that audio data
// from the reader goroutine is correctly dispatched via the onFrame callback.
// The pipe closes immediately after writing, which triggers the quick-exit
// path; this test focuses on data delivery, not error classification.
func TestProcessAudio_DataFlowsThroughReaderGoroutine(t *testing.T) {
	t.Parallel()

	testData := []byte("audio-payload-12345")
	var received []byte

	cfg := newTestConfig()
	stream := NewStream(&cfg, func(frame audiocore.AudioFrame) {
		received = frame.Data
	}, nil, nil, nil)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	stream.cancelMu.Lock()
	stream.ctx = ctx
	stream.cancel = func(err error) { cancel() }
	stream.cancelMu.Unlock()

	// Use a pipe so we can control exactly what the reader goroutine sees.
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pr.Close() })

	stream.cmdMu.Lock()
	stream.stdout = pr
	stream.processStartTime = time.Now()
	stream.cmdMu.Unlock()

	stream.transitionState(StateStarting, "test setup")
	stream.transitionState(StateRunning, "test setup")

	done := make(chan error, 1)
	go func() {
		done <- stream.processAudio()
	}()

	// Write data then close the writer to trigger EOF.
	_, err := pw.Write(testData)
	require.NoError(t, err)
	_ = pw.Close()

	select {
	case <-done:
		// processAudio exited (may return error due to quick-exit detection).
	case <-time.After(2 * time.Second):
		require.Fail(t, "processAudio did not complete within 2s")
	}

	// The key assertion: the data was dispatched via onFrame before the pipe closed.
	assert.Equal(t, testData, received, "onFrame should receive the exact data written to the pipe")
}

// --- Issue 2: Manual restart resets backoff ---

// TestStream_ManualRestartResetsBackoff verifies that a manual restart
// resets both the restart count and the consecutive failures counter.
func TestStream_ManualRestartResetsBackoff(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	// Simulate accumulated failures and restart count.
	stream.restartCountMu.Lock()
	stream.restartCount = 10
	stream.restartCountMu.Unlock()

	stream.setConsecutiveFailures(7)
	stream.setCircuitOpenTimeForTest(time.Now())

	// Manual restart should clear everything.
	stream.Restart(true)

	stream.restartCountMu.Lock()
	restartCount := stream.restartCount
	stream.restartCountMu.Unlock()

	assert.Equal(t, 0, restartCount, "manual restart should reset restart count")
	assert.Equal(t, 0, stream.getConsecutiveFailures(), "manual restart should reset consecutive failures")
	assert.True(t, stream.getCircuitOpenTimeForTest().IsZero(), "manual restart should clear circuit open time")

	// Drain the restart channel.
	select {
	case <-stream.restartChan:
	default:
	}
}

// TestStream_AutomaticRestartPreservesBackoff verifies that an automatic
// restart does NOT reset the backoff counters.
func TestStream_AutomaticRestartPreservesBackoff(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	stream.restartCountMu.Lock()
	stream.restartCount = 10
	stream.restartCountMu.Unlock()

	stream.setConsecutiveFailures(7)

	// Automatic restart should preserve counters.
	stream.Restart(false)

	stream.restartCountMu.Lock()
	restartCount := stream.restartCount
	stream.restartCountMu.Unlock()

	assert.Equal(t, 10, restartCount, "automatic restart should preserve restart count")
	assert.Equal(t, 7, stream.getConsecutiveFailures(), "automatic restart should preserve consecutive failures")

	// Drain the restart channel.
	select {
	case <-stream.restartChan:
	default:
	}
}

// --- Issue 3: streamCreatedAt resets on data tracking reset ---

// TestStream_ResetDataTrackingRefreshesStreamCreatedAt verifies that
// resetDataTracking refreshes streamCreatedAt so that health calculations
// reference the current process lifetime.
func TestStream_ResetDataTrackingRefreshesStreamCreatedAt(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	// Backdate streamCreatedAt to simulate an old stream.
	oldCreatedAt := time.Now().Add(-1 * time.Hour)
	stream.streamCreatedAtMu.Lock()
	stream.streamCreatedAt = oldCreatedAt
	stream.streamCreatedAtMu.Unlock()

	stream.resetDataTracking()

	newCreatedAt := stream.getStreamCreatedAtForTest()
	assert.True(t, newCreatedAt.After(oldCreatedAt),
		"streamCreatedAt should be refreshed after resetDataTracking")
	assert.WithinDuration(t, time.Now(), newCreatedAt, time.Second,
		"streamCreatedAt should be approximately now")
}

// --- Issue 4: Process start time captured before processAudio ---

// TestStream_RuntimeCalculationWithCleanup verifies that runtime is
// calculated correctly even when cleanupProcess zeroes processStartTime.
// This is a structural test that the capture happens before processAudio.
func TestStream_RuntimeCalculationWithCleanup(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	// Set a known process start time.
	knownStart := time.Now().Add(-30 * time.Second)
	stream.setProcessStartTimeForTest(knownStart)

	// After cleanupProcess, processStartTime is zeroed.
	stream.cleanupProcess()

	// The zero value should be detectable.
	assert.True(t, stream.getProcessStartTimeForTest().IsZero(),
		"cleanupProcess should zero processStartTime")

	// This confirms why capturing processStartTime before processAudio matters:
	// if captured after cleanup, time.Since(time.Time{}) would be huge.
	zeroRuntime := time.Since(time.Time{})
	knownRuntime := time.Since(knownStart)
	assert.Greater(t, zeroRuntime, 24*time.Hour,
		"runtime from zero-time should be unreasonably large")
	assert.Less(t, knownRuntime, time.Minute,
		"runtime from known start should be reasonable")
}

// --- Issue 5: Circuit breaker cooldown resets failure counter ---

// TestStream_CircuitBreakerCooldownResetsFailures verifies that when
// the circuit breaker cooldown expires, the failure counter is reset.
func TestStream_CircuitBreakerCooldownResetsFailures(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	// Open the circuit breaker with accumulated failures.
	stream.setConsecutiveFailures(circuitBreakerThreshold)
	stream.setCircuitOpenTimeForTest(time.Now().Add(-circuitBreakerCooldown - time.Second))

	// The cooldown has expired; circuitCooldownRemaining should return false
	// AND reset the failure counter.
	remaining, open := stream.circuitCooldownRemaining()

	assert.False(t, open, "circuit should be closed after cooldown")
	assert.Equal(t, time.Duration(0), remaining, "no remaining cooldown expected")
	assert.Equal(t, 0, stream.getConsecutiveFailures(),
		"consecutive failures should be reset when cooldown expires")
	assert.True(t, stream.getCircuitOpenTimeForTest().IsZero(),
		"circuit open time should be cleared when cooldown expires")
}

// TestStream_CircuitBreakerCooldownStillOpen verifies that when
// the circuit breaker cooldown has NOT expired, the failure counter is preserved.
func TestStream_CircuitBreakerCooldownStillOpen(t *testing.T) {
	t.Parallel()

	stream := newTestStream(t)

	// Open the circuit breaker recently.
	stream.setConsecutiveFailures(circuitBreakerThreshold)
	stream.setCircuitOpenTimeForTest(time.Now())

	remaining, open := stream.circuitCooldownRemaining()

	assert.True(t, open, "circuit should still be open during cooldown")
	assert.Greater(t, remaining, time.Duration(0), "remaining cooldown should be positive")
	assert.Equal(t, circuitBreakerThreshold, stream.getConsecutiveFailures(),
		"consecutive failures should be preserved during cooldown")
}

// --- Integration: Non-blocking reader ---

// TestProcessAudio_NonBlockingEventLoop verifies that the processAudio event
// loop correctly dispatches data received from the reader goroutine and exits
// when it encounters an EOF.
func TestProcessAudio_NonBlockingEventLoop(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig()
	var frameCount int
	stream := NewStream(&cfg, func(frame audiocore.AudioFrame) {
		frameCount++
	}, nil, nil, nil)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	stream.cancelMu.Lock()
	stream.ctx = ctx
	stream.cancel = func(err error) { cancel() }
	stream.cancelMu.Unlock()

	// Use a pipe to control data delivery. Write data, wait for it to be consumed,
	// then close the pipe after the quick-exit window so EOF is treated as normal.
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pr.Close() })

	stream.cmdMu.Lock()
	stream.stdout = pr
	stream.processStartTime = time.Now()
	stream.cmdMu.Unlock()

	stream.transitionState(StateStarting, "test setup")
	stream.transitionState(StateRunning, "test setup")

	done := make(chan error, 1)
	go func() {
		done <- stream.processAudio()
	}()

	// Write some audio data.
	data := bytes.Repeat([]byte{0xAA}, 128)
	_, err := pw.Write(data)
	require.NoError(t, err)

	// Wait past the quick-exit window before closing the pipe
	// so EOF is treated as a normal end, not a startup failure.
	time.Sleep(processQuickExitTime + 100*time.Millisecond)
	_ = pw.Close()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.Fail(t, "processAudio did not complete within 5s")
	}

	assert.Positive(t, frameCount, "at least one frame should have been dispatched")
}

// TestStream_DispatchAttachesFrameRef_WhenPooled verifies that a Stream
// constructed with a real buffer.Manager attaches a FrameRef to the
// dispatched AudioFrame. The ref here is synthesised directly by the test
// (dispatchAudioData does not allocate a pool slice; readStdout does) but
// it must survive round-tripping through the onFrame callback.
func TestStream_DispatchAttachesFrameRef_WhenPooled(t *testing.T) {
	t.Parallel()

	bufMgr := buffer.NewManager(audiocore.GetLogger())
	cfg := newTestConfig()

	var gotRef *audiocore.FrameRef
	stream := NewStream(&cfg, func(frame audiocore.AudioFrame) {
		gotRef = frame.Ref
	}, nil, nil, bufMgr)

	var released atomic.Int32
	ref := audiocore.NewFrameRef(func() { released.Add(1) })
	stream.dispatchAudioData([]byte{1, 2, 3}, ref)

	require.NotNil(t, gotRef, "pooled Stream must forward FrameRef to dispatched frames")
	assert.EqualValues(t, 1, released.Load(),
		"dispatchAudioData must release the producer's reference exactly once")
}

// TestStream_DispatchNilRef_WhenNoBufMgr verifies that when no buffer
// manager is wired the dispatch path forwards a nil Ref, preserving the
// legacy non-pooled contract.
func TestStream_DispatchNilRef_WhenNoBufMgr(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig()

	// Seed the captured ref with a non-nil sentinel; the callback must
	// overwrite it with nil when the producer has no buffer manager.
	gotRef := audiocore.NewFrameRef(func() {})
	stream := NewStream(&cfg, func(frame audiocore.AudioFrame) {
		gotRef = frame.Ref
	}, nil, nil, nil)

	stream.dispatchAudioData([]byte{1, 2, 3}, nil)

	assert.Nil(t, gotRef, "non-pooled Stream must dispatch with nil Ref")
}

// TestStream_DispatchEmptyData_ReleasesRef verifies that the empty-data
// early-return path in dispatchAudioData still releases the producer's
// reference exactly once, so pool slices are not leaked when a read
// returns zero useful bytes.
func TestStream_DispatchEmptyData_ReleasesRef(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig()
	stream := NewStream(&cfg, func(frame audiocore.AudioFrame) {
		t.Fatalf("onFrame must not be called for empty data")
	}, nil, nil, nil)

	var released atomic.Int32
	ref := audiocore.NewFrameRef(func() { released.Add(1) })
	stream.dispatchAudioData(nil, ref)

	assert.EqualValues(t, 1, released.Load(),
		"empty-data dispatch must release the producer's reference exactly once")
}

// TestStream_ReadStdout_AttachesRefWhenPooled exercises the real pool-borrow
// path inside readStdout: a Stream constructed with a buffer.Manager must
// attach a non-nil FrameRef to every readResult so pool.Put can return the
// slice when the last holder releases. This is the regression test that
// proves the bufMgr wiring in NewStream is load-bearing.
func TestStream_ReadStdout_AttachesRefWhenPooled(t *testing.T) {
	t.Parallel()

	bufMgr := buffer.NewManager(audiocore.GetLogger())
	cfg := newTestConfig()
	stream := NewStream(&cfg, nil, nil, nil, bufMgr)

	reader := io.NopCloser(bytes.NewReader([]byte{1, 2, 3}))
	readCh := make(chan readResult, 1)
	readerDone := make(chan struct{})
	t.Cleanup(func() { close(readerDone) })

	go stream.readStdout(reader, readCh, readerDone)

	select {
	case result := <-readCh:
		require.NoError(t, result.err)
		require.NotNil(t, result.ref, "pooled readStdout must attach a FrameRef to every readResult")
		assert.Equal(t, []byte{1, 2, 3}, result.data)
		result.ref.Release()
	case <-time.After(time.Second):
		t.Fatal("readStdout did not produce a readResult within 1s")
	}
}

// TestStream_ReadStdout_NilRefWhenNoBufMgr verifies that when no buffer
// manager is wired, readStdout dispatches readResult values with nil ref so
// the non-pooled legacy path remains intact.
func TestStream_ReadStdout_NilRefWhenNoBufMgr(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig()
	stream := NewStream(&cfg, nil, nil, nil, nil)

	reader := io.NopCloser(bytes.NewReader([]byte{1, 2, 3}))
	readCh := make(chan readResult, 1)
	readerDone := make(chan struct{})
	t.Cleanup(func() { close(readerDone) })

	go stream.readStdout(reader, readCh, readerDone)

	select {
	case result := <-readCh:
		require.NoError(t, result.err)
		assert.Nil(t, result.ref, "non-pooled readStdout must dispatch nil ref")
		assert.Equal(t, []byte{1, 2, 3}, result.data)
	case <-time.After(time.Second):
		t.Fatal("readStdout did not produce a readResult within 1s")
	}
}
