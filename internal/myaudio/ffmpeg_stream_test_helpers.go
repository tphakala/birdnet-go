package myaudio

import "time"

// Test helper methods for FFmpegStream
// These methods are provided for testing purposes to access internal state safely
// All methods are unexported (lowercase) since they're only used within the myaudio package tests

// getConsecutiveFailures returns the current consecutive failure count (test helper)
func (s *FFmpegStream) getConsecutiveFailures() int {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()
	return s.consecutiveFailures
}

// setConsecutiveFailures sets the consecutive failure count (test helper)
func (s *FFmpegStream) setConsecutiveFailures(count int) {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()
	s.consecutiveFailures = count
}

// getLastDataTime returns the last data time in a thread-safe manner (test helper)
func (s *FFmpegStream) getLastDataTime() time.Time {
	s.lastDataMu.RLock()
	defer s.lastDataMu.RUnlock()
	return s.lastDataTime
}

// setLastDataTimeForTest sets the last data time for testing purposes (test helper)
func (s *FFmpegStream) setLastDataTimeForTest(t time.Time) {
	s.lastDataMu.Lock()
	defer s.lastDataMu.Unlock()
	s.lastDataTime = t
}

// setCircuitOpenTimeForTest sets the circuit open time for testing purposes (test helper)
func (s *FFmpegStream) setCircuitOpenTimeForTest(t time.Time) {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()
	s.circuitOpenTime = t
}

// resetCircuitStateForTest resets both failures and circuit open time (test helper)
func (s *FFmpegStream) resetCircuitStateForTest() {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()
	s.consecutiveFailures = 0
	s.circuitOpenTime = time.Time{}
}

// setProcessStartTimeForTest sets the process start time for testing purposes (test helper)
func (s *FFmpegStream) setProcessStartTimeForTest(t time.Time) {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	s.processStartTime = t
}

// getProcessStartTimeForTest gets the process start time for testing purposes (test helper)
func (s *FFmpegStream) getProcessStartTimeForTest() time.Time {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	return s.processStartTime
}

// setTotalBytesReceivedForTest sets the total bytes received for testing purposes (test helper)
func (s *FFmpegStream) setTotalBytesReceivedForTest(bytes int64) {
	s.bytesReceivedMu.Lock()
	defer s.bytesReceivedMu.Unlock()
	s.totalBytesReceived = bytes
}

// getTotalBytesReceivedForTest gets the total bytes received for testing purposes (test helper)
func (s *FFmpegStream) getTotalBytesReceivedForTest() int64 {
	s.bytesReceivedMu.Lock()
	defer s.bytesReceivedMu.Unlock()
	return s.totalBytesReceived
}