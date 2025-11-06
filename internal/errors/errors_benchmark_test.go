package errors

import (
	"fmt"
	"testing"
)

// BenchmarkErrorCreationNoTelemetry tests error creation performance when telemetry is disabled
func BenchmarkErrorCreationNoTelemetry(b *testing.B) {
	// Ensure no telemetry or hooks are active
	SetTelemetryReporter(nil)
	ClearErrorHooks()

	b.ReportAllocs()

	for b.Loop() {
		err := fmt.Errorf("test error")
		_ = New(err).
			Component("test").
			Category(CategoryGeneric).
			Build()
	}
}

// BenchmarkErrorCreationNoTelemetryAutoDetect tests error creation with auto-detection when telemetry is disabled
func BenchmarkErrorCreationNoTelemetryAutoDetect(b *testing.B) {
	// Ensure no telemetry or hooks are active
	SetTelemetryReporter(nil)
	ClearErrorHooks()

	b.ReportAllocs()

	for b.Loop() {
		err := fmt.Errorf("test error")
		_ = New(err).Build() // Let it auto-detect component and category
	}
}

// BenchmarkErrorCreationWithContext tests error creation with context when telemetry is disabled
func BenchmarkErrorCreationWithContext(b *testing.B) {
	// Ensure no telemetry or hooks are active
	SetTelemetryReporter(nil)
	ClearErrorHooks()

	b.ReportAllocs()

	for b.Loop() {
		err := fmt.Errorf("test error")
		_ = New(err).
			Component("test").
			Category(CategoryGeneric).
			Context("operation", "test_op").
			Context("count", 42).
			Build()
	}
}

// mockReporter is a test telemetry reporter that does nothing
type mockReporter struct {
	enabled bool
}

func (m *mockReporter) IsEnabled() bool {
	return m.enabled
}

func (m *mockReporter) ReportError(err *EnhancedError) {
	// Mock implementation - just trigger privacy scrubbing
	_ = scrubMessageForPrivacy(err.Error())
}

// BenchmarkErrorCreationWithTelemetry tests error creation when telemetry is enabled
func BenchmarkErrorCreationWithTelemetry(b *testing.B) {
	// Set up a mock telemetry reporter
	reporter := &mockReporter{enabled: true}
	SetTelemetryReporter(reporter)

	b.ReportAllocs()

	for b.Loop() {
		err := fmt.Errorf("test error with URL https://example.com?api_key=secret123&token=abc")
		_ = New(err).
			Component("test").
			Category(CategoryNetwork).
			Context("url", "https://example.com?api_key=secret123").
			Build()
	}
}

// BenchmarkPrivacyScrubbing tests the performance of privacy scrubbing
func BenchmarkPrivacyScrubbing(b *testing.B) {
	testMessage := "Error connecting to https://api.example.com?api_key=1234567890abcdef&station_id=test123&token=secret"

	b.ReportAllocs()

	for b.Loop() {
		_ = basicURLScrub(testMessage)
	}
}
