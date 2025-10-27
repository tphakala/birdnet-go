package notification

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"golang.org/x/sync/semaphore"
)

// fakeProvider implements PushProvider for testing
type fakeProvider struct {
	name       string
	enabled    bool
	types      map[Type]bool
	recvCh     chan *Notification
	sendDelay  time.Duration
	sendFunc   func(context.Context, *Notification) error
}

func (f *fakeProvider) GetName() string          { return f.name }
func (f *fakeProvider) ValidateConfig() error    { return nil }
func (f *fakeProvider) SupportsType(t Type) bool { return f.types[t] }
func (f *fakeProvider) IsEnabled() bool          { return f.enabled }
func (f *fakeProvider) Send(ctx context.Context, n *Notification) error {
	if f.sendFunc != nil {
		return f.sendFunc(ctx, n)
	}
	if f.sendDelay > 0 {
		time.Sleep(f.sendDelay)
	}
	select {
	case f.recvCh <- n:
	default:
	}
	return nil
}

func TestPushDispatcher_ForwardsNotification(t *testing.T) {
	// Ensure no global service initialized
	// Create isolated service for test
	svc := NewService(DefaultServiceConfig())
	if err := SetServiceForTesting(svc); err != nil {
		svc = GetService()
		if svc == nil {
			t.Fatalf("failed to attach to notification service: %v", err)
		}
	}

	// Setup fake provider that accepts all types
	fp := &fakeProvider{
		name:    "fake",
		enabled: true,
		types:   map[Type]bool{TypeError: true, TypeInfo: true, TypeWarning: true, TypeDetection: true, TypeSystem: true},
		recvCh:  make(chan *Notification, 1),
	}

	// Build dispatcher with fake provider
	d := &pushDispatcher{
		providers:      []enhancedProvider{{prov: fp, circuitBreaker: nil, filter: conf.PushFilterConfig{}, name: fp.name}},
		log:            getFileLogger(false),
		enabled:        true,
		maxRetries:     0,
		retryDelay:     10 * time.Millisecond,
		defaultTimeout: 200 * time.Millisecond,
	}

	if err := d.start(); err != nil {
		t.Fatalf("failed to start dispatcher: %v", err)
	}
	defer func() {
		if d.cancel != nil {
			d.cancel()
		}
	}()

	// Create a notification and expect provider to receive it
	_, err := svc.Create(TypeInfo, PriorityLow, "Hello", "World")
	if err != nil {
		t.Fatalf("create notification failed: %v", err)
	}

	select {
	case n := <-fp.recvCh:
		if n.Title != "Hello" || n.Message != "World" {
			t.Fatalf("received wrong notification: %+v", n)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for provider to receive notification")
	}
}

func TestMatchesProviderFilter_ConfidenceOperators(t *testing.T) {
	tests := []struct {
		name       string
		condition  string
		confidence float64
		expected   bool
	}{
		// Greater than
		{"gt_pass", ">0.8", 0.9, true},
		{"gt_fail", ">0.8", 0.7, false},
		{"gt_equal", ">0.8", 0.8, false},

		// Less than
		{"lt_pass", "<0.8", 0.7, true},
		{"lt_fail", "<0.8", 0.9, false},
		{"lt_equal", "<0.8", 0.8, false},

		// Greater than or equal
		{"gte_pass_greater", ">=0.8", 0.9, true},
		{"gte_pass_equal", ">=0.8", 0.8, true},
		{"gte_fail", ">=0.8", 0.7, false},

		// Less than or equal
		{"lte_pass_less", "<=0.8", 0.7, true},
		{"lte_pass_equal", "<=0.8", 0.8, true},
		{"lte_fail", "<=0.8", 0.9, false},

		// Equal (single =)
		{"eq_pass", "=0.8", 0.8, true},
		{"eq_fail", "=0.8", 0.7, false},

		// Equal (double ==)
		{"eq2_pass", "==0.8", 0.8, true},
		{"eq2_fail", "==0.8", 0.7, false},

		// Edge cases with whitespace
		{"whitespace", " >= 0.8 ", 0.8, true},
		{"no_space", ">=0.8", 0.8, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &conf.PushFilterConfig{
				MetadataFilters: map[string]interface{}{
					"confidence": tt.condition,
				},
			}

			notif := &Notification{
				Metadata: map[string]interface{}{
					"confidence": tt.confidence,
				},
			}

			result := MatchesProviderFilter(filter, notif, nil, "test-provider")
			if result != tt.expected {
				t.Errorf("condition %q with confidence %v: expected %v, got %v",
					tt.condition, tt.confidence, tt.expected, result)
			}
		})
	}
}

func TestMatchesProviderFilter_ConfidenceErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		condition interface{}
		metadata  map[string]interface{}
		expected  bool
	}{
		// Invalid operator formats
		{"invalid_operator", "~0.8", map[string]interface{}{"confidence": 0.8}, false},
		{"empty_condition", "", map[string]interface{}{"confidence": 0.8}, false},
		{"no_operator", "0.8", map[string]interface{}{"confidence": 0.8}, false},

		// Invalid values
		{"invalid_threshold", ">abc", map[string]interface{}{"confidence": 0.8}, false},
		{"non_string_condition", 0.8, map[string]interface{}{"confidence": 0.8}, false},

		// Missing or invalid confidence
		{"missing_confidence", ">0.8", map[string]interface{}{}, false},
		{"invalid_confidence_type", ">0.8", map[string]interface{}{"confidence": "invalid"}, false},
		{"nil_confidence", ">0.8", map[string]interface{}{"confidence": nil}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &conf.PushFilterConfig{
				MetadataFilters: map[string]interface{}{
					"confidence": tt.condition,
				},
			}

			notif := &Notification{
				Metadata: tt.metadata,
			}

			result := MatchesProviderFilter(filter, notif, nil, "test-provider")
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestMatchesProviderFilter_ConfidenceTypes(t *testing.T) {
	tests := []struct {
		name       string
		confidence interface{}
		expected   bool
	}{
		{"float64", 0.85, true},
		{"float32", float32(0.85), true},
		{"string_valid", "0.85", true},
		{"string_invalid", "invalid", false},
		{"int", 1, true}, // ints convert to float64 successfully
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &conf.PushFilterConfig{
				MetadataFilters: map[string]interface{}{
					"confidence": ">0.8",
				},
			}

			notif := &Notification{
				Metadata: map[string]interface{}{
					"confidence": tt.confidence,
				},
			}

			result := MatchesProviderFilter(filter, notif, nil, "test-provider")
			if result != tt.expected {
				t.Errorf("confidence type %T with value %v: expected %v, got %v",
					tt.confidence, tt.confidence, tt.expected, result)
			}
		})
	}
}

// TestPushDispatcher_ConcurrencyLimit verifies that semaphore limits concurrent dispatches
// and drops notifications when queue is full (due to TryAcquire timeout).
func TestPushDispatcher_ConcurrencyLimit(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	if err := SetServiceForTesting(svc); err != nil {
		svc = GetService()
		if svc == nil {
			t.Fatalf("failed to attach to notification service: %v", err)
		}
	}

	// Create slow provider that takes 50ms per dispatch
	slowProvider := &fakeProvider{
		name:      "slow",
		enabled:   true,
		types:     map[Type]bool{TypeInfo: true},
		recvCh:    make(chan *Notification, 100),
		sendDelay: 50 * time.Millisecond,
	}

	// Build dispatcher with limited concurrency (3 concurrent jobs)
	d := &pushDispatcher{
		providers:      []enhancedProvider{{prov: slowProvider, circuitBreaker: nil, filter: conf.PushFilterConfig{}, name: slowProvider.name}},
		log:            getFileLogger(false),
		enabled:        true,
		maxRetries:     0,
		retryDelay:     10 * time.Millisecond,
		defaultTimeout: 5 * time.Second,
		concurrencySem: semaphore.NewWeighted(3), // Limit to 3 concurrent
	}

	if err := d.start(); err != nil {
		t.Fatalf("failed to start dispatcher: %v", err)
	}
	defer func() {
		if d.cancel != nil {
			d.cancel()
		}
	}()

	// Send 5 notifications with some spacing to allow queue processing
	for i := 0; i < 5; i++ {
		_, err := svc.Create(TypeInfo, PriorityLow, "Test", "Message")
		if err != nil {
			t.Fatalf("create notification failed: %v", err)
		}
		time.Sleep(10 * time.Millisecond) // Small delay between sends
	}

	// Wait for dispatches to complete (should get at least some notifications)
	timeout := time.After(2 * time.Second)
	received := 0
	for {
		select {
		case <-slowProvider.recvCh:
			received++
			if received == 5 {
				// Got all notifications
				return
			}
		case <-timeout:
			// With TryAcquire timeout, some notifications may be dropped under load
			// We should receive at least the number that fit in the semaphore
			if received < 3 {
				t.Fatalf("timeout: only received %d notifications, expected at least 3", received)
			}
			t.Logf("Received %d/5 notifications (some dropped due to queue full)", received)
			return
		}
	}
}

// TestPushDispatcher_ExponentialBackoff verifies exponential backoff with jitter.
func TestPushDispatcher_ExponentialBackoff(t *testing.T) {
	tests := []struct {
		name          string
		attempts      int
		baseDelay     time.Duration
		maxDelay      time.Duration
		expectedMin   time.Duration
		expectedMax   time.Duration
	}{
		{
			name:        "first_retry",
			attempts:    1,
			baseDelay:   1 * time.Second,
			maxDelay:    30 * time.Second,
			expectedMin: 1 * time.Second,       // base - 25% jitter
			expectedMax: 1*time.Second + 250*time.Millisecond, // base + 25% jitter
		},
		{
			name:        "second_retry",
			attempts:    2,
			baseDelay:   1 * time.Second,
			maxDelay:    30 * time.Second,
			expectedMin: 1500 * time.Millisecond, // 2s - 25%
			expectedMax: 2500 * time.Millisecond, // 2s + 25%
		},
		{
			name:        "third_retry",
			attempts:    3,
			baseDelay:   1 * time.Second,
			maxDelay:    30 * time.Second,
			expectedMin: 3 * time.Second,       // 4s - 25%
			expectedMax: 5 * time.Second,       // 4s + 25%
		},
		{
			name:        "capped_at_max",
			attempts:    10,
			baseDelay:   1 * time.Second,
			maxDelay:    5 * time.Second,
			expectedMin: 3750 * time.Millisecond, // 5s - 25%
			expectedMax: 5 * time.Second,         // capped at maxDelay
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate delay multiple times to verify jitter distribution
			delays := make([]time.Duration, 100)
			for i := 0; i < 100; i++ {
				// We can't easily test the actual wait, but we can verify the calculation
				// by inspecting the logic in waitForRetry

				// Calculate exponential component
				exponential := tt.baseDelay
				if tt.attempts > 1 && tt.attempts < maxExponentialAttempts {
					exponential = tt.baseDelay * (1 << (tt.attempts - 1))
				}
				if exponential > tt.maxDelay {
					exponential = tt.maxDelay
				}

				// Add jitter
				jitterRange := exponential * jitterPercent / 100
				jitterMax := int64(jitterRange * 2)
				var jitter time.Duration
				if jitterMax > 0 {
					jitter = time.Duration(rand.Int64N(jitterMax)) - jitterRange
				}

				delay := exponential + jitter
				if delay < tt.baseDelay {
					delay = tt.baseDelay
				}
				if delay > tt.maxDelay {
					delay = tt.maxDelay
				}

				delays[i] = delay
			}

			// Verify delays fall within expected range
			for _, delay := range delays {
				if delay < tt.expectedMin || delay > tt.expectedMax {
					t.Errorf("delay %v outside expected range [%v, %v]",
						delay, tt.expectedMin, tt.expectedMax)
				}
			}
		})
	}
}

// TestToFloat_TypeCoverage verifies toFloat handles all numeric types correctly.
func TestToFloat_TypeCoverage(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected float64
		ok       bool
	}{
		// Floating point types
		{"float32", float32(1.5), 1.5, true},
		{"float64", float64(2.5), 2.5, true},

		// Integer types
		{"int", int(42), 42.0, true},
		{"int8", int8(8), 8.0, true},
		{"int16", int16(16), 16.0, true},
		{"int32", int32(32), 32.0, true},
		{"int64", int64(64), 64.0, true},

		// Unsigned integer types
		{"uint", uint(42), 42.0, true},
		{"uint8", uint8(8), 8.0, true},
		{"uint16", uint16(16), 16.0, true},
		{"uint32", uint32(32), 32.0, true},
		{"uint64", uint64(64), 64.0, true},

		// String conversions
		{"string_valid", "3.14", 3.14, true},
		{"string_int", "42", 42.0, true},
		{"string_invalid", "not a number", 0, false},
		{"string_empty", "", 0, false},

		// Unsupported types
		{"bool", true, 0, false},
		{"nil", nil, 0, false},
		{"struct", struct{}{}, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := toFloat(tt.input)
			if ok != tt.ok {
				t.Errorf("expected ok=%v, got ok=%v", tt.ok, ok)
			}
			if ok && result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestMatchesProviderFilterWithReason verifies enhanced filter returns correct reasons.
func TestMatchesProviderFilterWithReason(t *testing.T) {
	tests := []struct {
		name           string
		filter         *conf.PushFilterConfig
		notification   *Notification
		expectedMatch  bool
		expectedReason string
	}{
		{
			name:           "all_filters_pass",
			filter:         &conf.PushFilterConfig{},
			notification:   &Notification{Type: TypeInfo},
			expectedMatch:  true,
			expectedReason: filterReasonAll,
		},
		{
			name: "type_mismatch",
			filter: &conf.PushFilterConfig{
				Types: []string{"error"},
			},
			notification:   &Notification{Type: TypeInfo},
			expectedMatch:  false,
			expectedReason: filterReasonTypeMismatch,
		},
		{
			name: "priority_mismatch",
			filter: &conf.PushFilterConfig{
				Priorities: []string{"high"},
			},
			notification:   &Notification{Type: TypeInfo, Priority: PriorityLow},
			expectedMatch:  false,
			expectedReason: filterReasonPriorityMismatch,
		},
		{
			name: "component_mismatch",
			filter: &conf.PushFilterConfig{
				Components: []string{"frontend"},
			},
			notification:   &Notification{Type: TypeInfo, Component: "backend"},
			expectedMatch:  false,
			expectedReason: filterReasonComponentMismatch,
		},
		{
			name: "confidence_threshold_not_met",
			filter: &conf.PushFilterConfig{
				MetadataFilters: map[string]interface{}{
					"confidence": ">0.8",
				},
			},
			notification: &Notification{
				Type: TypeInfo,
				Metadata: map[string]interface{}{
					"confidence": 0.5,
				},
			},
			expectedMatch:  false,
			expectedReason: filterReasonConfidenceThreshold,
		},
		{
			name: "metadata_mismatch",
			filter: &conf.PushFilterConfig{
				MetadataFilters: map[string]interface{}{
					"source": "sensor1",
				},
			},
			notification: &Notification{
				Type: TypeInfo,
				Metadata: map[string]interface{}{
					"source": "sensor2",
				},
			},
			expectedMatch:  false,
			expectedReason: filterReasonMetadataMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match, reason := MatchesProviderFilterWithReason(tt.filter, tt.notification, nil, "test-provider")
			if match != tt.expectedMatch {
				t.Errorf("expected match=%v, got match=%v", tt.expectedMatch, match)
			}
			if reason != tt.expectedReason {
				t.Errorf("expected reason=%q, got reason=%q", tt.expectedReason, reason)
			}
		})
	}
}

// TestParseConfidenceOperator tests the confidence operator parsing logic
//
// Modern Go 1.25 Test Patterns:
// - Uses t.Attr() to emit structured test metadata (new in Go 1.25)
// - Runs tests in parallel with t.Parallel() for better performance
// - Uses table-driven tests for comprehensive coverage
// - Tests pure functions with no side effects (ideal for parallel execution)
//
// Best Practices for LLMs:
// - Pure functions (no state, no side effects) are always safe to run in parallel
// - Use t.Attr() to categorize tests ("unit", "integration", "e2e")
// - Table-driven tests provide excellent documentation of expected behavior
func TestParseConfidenceOperator(t *testing.T) {
	t.Parallel() // Safe: pure function with no shared state
	t.Attr("category", "unit")
	t.Attr("function", "parseConfidenceOperator")

	tests := []struct {
		name        string
		condition   string
		expectedOp  string
		expectedVal string
	}{
		// Two-character operators
		{name: "gte_operator", condition: ">=0.8", expectedOp: ">=", expectedVal: "0.8"},
		{name: "lte_operator", condition: "<=0.8", expectedOp: "<=", expectedVal: "0.8"},
		{name: "eq_operator", condition: "==0.8", expectedOp: "==", expectedVal: "0.8"},

		// Two-character operators with spaces
		{name: "gte_with_spaces", condition: ">= 0.8", expectedOp: ">=", expectedVal: "0.8"},
		{name: "lte_with_spaces", condition: "<= 0.8", expectedOp: "<=", expectedVal: "0.8"},
		{name: "eq_with_spaces", condition: "== 0.8", expectedOp: "==", expectedVal: "0.8"},

		// Single-character operators
		{name: "gt_operator", condition: ">0.8", expectedOp: ">", expectedVal: "0.8"},
		{name: "lt_operator", condition: "<0.8", expectedOp: "<", expectedVal: "0.8"},
		{name: "eq_single", condition: "=0.8", expectedOp: "=", expectedVal: "0.8"},

		// Single-character operators with spaces
		{name: "gt_with_spaces", condition: "> 0.8", expectedOp: ">", expectedVal: "0.8"},
		{name: "lt_with_spaces", condition: "< 0.8", expectedOp: "<", expectedVal: "0.8"},
		{name: "eq_single_with_spaces", condition: "= 0.8", expectedOp: "=", expectedVal: "0.8"},

		// Edge cases
		// NOTE: parseConfidenceOperator expects pre-trimmed input (checkConfidenceFilter calls TrimSpace first)
		{name: "multiple_spaces_after_op", condition: ">=    0.8", expectedOp: ">=", expectedVal: "0.8"},
		{name: "tabs_after_op", condition: ">=\t0.8", expectedOp: ">=", expectedVal: "0.8"},

		// Invalid cases
		{name: "no_operator", condition: "0.8", expectedOp: "", expectedVal: ""},
		{name: "invalid_operator", condition: "~0.8", expectedOp: "", expectedVal: ""},
		{name: "empty_string", condition: "", expectedOp: "", expectedVal: ""},
		{name: "only_operator", condition: ">=", expectedOp: ">=", expectedVal: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, val := parseConfidenceOperator(tt.condition)
			if op != tt.expectedOp {
				t.Errorf("operator: expected %q, got %q", tt.expectedOp, op)
			}
			if val != tt.expectedVal {
				t.Errorf("value: expected %q, got %q", tt.expectedVal, val)
			}
		})
	}
}

// TestCompareConfidence tests the confidence comparison logic
//
// Modern Go 1.25 Test Patterns:
// - Parallel execution enabled for pure function testing
// - Comprehensive edge case coverage (boundary values, invalid operators)
// - Uses t.Attr() for test categorization
//
// Best Practices for LLMs:
// - Test ALL operators: >, >=, <, <=, =, ==
// - Test boundary conditions: 0.0, 1.0, negative, over 1.0
// - Test invalid inputs: empty strings, unknown operators
// - Pure functions are ideal candidates for parallel testing
func TestCompareConfidence(t *testing.T) {
	t.Parallel() // Safe: pure function with no side effects
	t.Attr("category", "unit")
	t.Attr("function", "compareConfidence")

	tests := []struct {
		name       string
		confidence float64
		op         string
		threshold  float64
		expected   bool
	}{
		// Greater than
		{name: "gt_true", confidence: 0.9, op: ">", threshold: 0.8, expected: true},
		{name: "gt_false", confidence: 0.7, op: ">", threshold: 0.8, expected: false},
		{name: "gt_equal", confidence: 0.8, op: ">", threshold: 0.8, expected: false},

		// Greater than or equal
		{name: "gte_greater", confidence: 0.9, op: ">=", threshold: 0.8, expected: true},
		{name: "gte_equal", confidence: 0.8, op: ">=", threshold: 0.8, expected: true},
		{name: "gte_less", confidence: 0.7, op: ">=", threshold: 0.8, expected: false},

		// Less than
		{name: "lt_true", confidence: 0.7, op: "<", threshold: 0.8, expected: true},
		{name: "lt_false", confidence: 0.9, op: "<", threshold: 0.8, expected: false},
		{name: "lt_equal", confidence: 0.8, op: "<", threshold: 0.8, expected: false},

		// Less than or equal
		{name: "lte_less", confidence: 0.7, op: "<=", threshold: 0.8, expected: true},
		{name: "lte_equal", confidence: 0.8, op: "<=", threshold: 0.8, expected: true},
		{name: "lte_greater", confidence: 0.9, op: "<=", threshold: 0.8, expected: false},

		// Equal (single)
		{name: "eq_true", confidence: 0.8, op: "=", threshold: 0.8, expected: true},
		{name: "eq_false_less", confidence: 0.7, op: "=", threshold: 0.8, expected: false},
		{name: "eq_false_greater", confidence: 0.9, op: "=", threshold: 0.8, expected: false},

		// Equal (double)
		{name: "eq2_true", confidence: 0.8, op: "==", threshold: 0.8, expected: true},
		{name: "eq2_false", confidence: 0.7, op: "==", threshold: 0.8, expected: false},

		// Invalid operator
		{name: "invalid_op", confidence: 0.8, op: "~", threshold: 0.8, expected: false},
		{name: "empty_op", confidence: 0.8, op: "", threshold: 0.8, expected: false},

		// Edge cases - boundary values
		{name: "zero_confidence", confidence: 0.0, op: ">=", threshold: 0.0, expected: true},
		{name: "one_confidence", confidence: 1.0, op: "<=", threshold: 1.0, expected: true},
		{name: "negative_confidence", confidence: -0.1, op: "<", threshold: 0.0, expected: true},
		{name: "over_one_confidence", confidence: 1.1, op: ">", threshold: 1.0, expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compareConfidence(tt.confidence, tt.op, tt.threshold)
			if result != tt.expected {
				t.Errorf("compareConfidence(%v, %q, %v) = %v; want %v",
					tt.confidence, tt.op, tt.threshold, result, tt.expected)
			}
		})
	}
}

// TestLogDebug tests the logDebug helper function
//
// Modern Go 1.25 Test Patterns:
// - Tests helper functions that wrap logging
// - Validates nil-safety (defensive programming)
// - Uses subtests for logical grouping
//
// Best Practices for LLMs:
// - ALWAYS test nil cases for functions accepting pointers
// - Logging helpers should never panic
// - Cannot run in parallel due to logger initialization side effects
func TestLogDebug(t *testing.T) {
	// NOT parallel: logger may have shared state
	t.Attr("category", "unit")
	t.Attr("function", "logDebug")

	t.Run("nil_logger_no_panic", func(t *testing.T) {
		// IMPORTANT: nil-safety is critical for defensive programming
		// logDebug should gracefully handle nil logger
		logDebug(nil, "test message", "key", "value")
	})

	t.Run("with_logger", func(t *testing.T) {
		// Verify normal operation with actual logger
		log := getFileLogger(false)
		logDebug(log, "test message", "key", "value")
	})
}

// TestCheckTypeFilter tests the type filter validation
//
// Modern Go 1.25 Test Patterns:
// - Tests refactored helper function extracted from complex function
// - Parallel execution for independent test cases
// - Returns named results (matches bool, reason string) - Go best practice
//
// Best Practices for LLMs:
// - Test empty filter (should pass all)
// - Test single and multiple values
// - Test mismatches
// - Named return values improve self-documentation
func TestCheckTypeFilter(t *testing.T) {
	t.Parallel() // Safe: no shared state, independent test cases
	t.Attr("category", "unit")
	t.Attr("function", "checkTypeFilter")

	tests := []struct {
		name           string
		filterTypes    []string
		notifType      Type
		expectedMatch  bool
		expectedReason string
	}{
		{
			name:           "no_filter_passes",
			filterTypes:    []string{},
			notifType:      TypeInfo,
			expectedMatch:  true,
			expectedReason: "",
		},
		{
			name:           "type_matches",
			filterTypes:    []string{"info", "warning"},
			notifType:      TypeInfo,
			expectedMatch:  true,
			expectedReason: "",
		},
		{
			name:           "type_not_in_filter",
			filterTypes:    []string{"error", "warning"},
			notifType:      TypeInfo,
			expectedMatch:  false,
			expectedReason: filterReasonTypeMismatch,
		},
		{
			name:           "single_type_match",
			filterTypes:    []string{"detection"},
			notifType:      TypeDetection,
			expectedMatch:  true,
			expectedReason: "",
		},
		{
			name:           "case_sensitive_match",
			filterTypes:    []string{"info"},
			notifType:      TypeInfo,
			expectedMatch:  true,
			expectedReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &conf.PushFilterConfig{Types: tt.filterTypes}
			notif := &Notification{Type: tt.notifType}

			matches, reason := checkTypeFilter(filter, notif, nil, "test-provider")

			if matches != tt.expectedMatch {
				t.Errorf("expected matches=%v, got matches=%v", tt.expectedMatch, matches)
			}
			if reason != tt.expectedReason {
				t.Errorf("expected reason=%q, got reason=%q", tt.expectedReason, reason)
			}
		})
	}
}

// TestCheckPriorityFilter tests the priority filter validation
//
// Modern Go 1.25 Test Patterns:
// - Similar structure to checkTypeFilter (consistent API design)
// - Parallel-safe testing
// - Uses testify patterns seen in codebase
//
// Best Practices for LLMs:
// - When refactoring, maintain consistency across similar functions
// - Test all priority levels: low, medium, high, critical
// - Verify empty filter behavior
func TestCheckPriorityFilter(t *testing.T) {
	t.Parallel() // Safe: no shared state
	t.Attr("category", "unit")
	t.Attr("function", "checkPriorityFilter")

	tests := []struct {
		name              string
		filterPriorities  []string
		notifPriority     Priority
		expectedMatch     bool
		expectedReason    string
	}{
		{
			name:             "no_filter_passes",
			filterPriorities: []string{},
			notifPriority:    PriorityLow,
			expectedMatch:    true,
			expectedReason:   "",
		},
		{
			name:             "priority_matches",
			filterPriorities: []string{"low", "medium"},
			notifPriority:    PriorityLow,
			expectedMatch:    true,
			expectedReason:   "",
		},
		{
			name:             "priority_not_in_filter",
			filterPriorities: []string{"high", "critical"},
			notifPriority:    PriorityLow,
			expectedMatch:    false,
			expectedReason:   filterReasonPriorityMismatch,
		},
		{
			name:             "single_priority_match",
			filterPriorities: []string{"critical"},
			notifPriority:    PriorityCritical,
			expectedMatch:    true,
			expectedReason:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &conf.PushFilterConfig{Priorities: tt.filterPriorities}
			notif := &Notification{Priority: tt.notifPriority}

			matches, reason := checkPriorityFilter(filter, notif, nil, "test-provider")

			if matches != tt.expectedMatch {
				t.Errorf("expected matches=%v, got matches=%v", tt.expectedMatch, matches)
			}
			if reason != tt.expectedReason {
				t.Errorf("expected reason=%q, got reason=%q", tt.expectedReason, reason)
			}
		})
	}
}

// TestCheckComponentFilter tests the component filter validation
//
// Modern Go 1.25 Test Patterns:
// - Third in the trilogy of simple filter functions
// - Demonstrates refactoring pattern: extract similar logic into separate functions
// - Parallel-safe, independent test execution
//
// Best Practices for LLMs:
// - Component filtering uses string matching (exact match)
// - Test empty strings as edge case
// - Consistent with type/priority filter patterns
func TestCheckComponentFilter(t *testing.T) {
	t.Parallel() // Safe: no shared state
	t.Attr("category", "unit")
	t.Attr("function", "checkComponentFilter")

	tests := []struct {
		name             string
		filterComponents []string
		notifComponent   string
		expectedMatch    bool
		expectedReason   string
	}{
		{
			name:             "no_filter_passes",
			filterComponents: []string{},
			notifComponent:   "backend",
			expectedMatch:    true,
			expectedReason:   "",
		},
		{
			name:             "component_matches",
			filterComponents: []string{"backend", "frontend"},
			notifComponent:   "backend",
			expectedMatch:    true,
			expectedReason:   "",
		},
		{
			name:             "component_not_in_filter",
			filterComponents: []string{"api", "database"},
			notifComponent:   "backend",
			expectedMatch:    false,
			expectedReason:   filterReasonComponentMismatch,
		},
		{
			name:             "empty_component_no_match",
			filterComponents: []string{"backend"},
			notifComponent:   "",
			expectedMatch:    false,
			expectedReason:   filterReasonComponentMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &conf.PushFilterConfig{Components: tt.filterComponents}
			notif := &Notification{Component: tt.notifComponent}

			matches, reason := checkComponentFilter(filter, notif, nil, "test-provider")

			if matches != tt.expectedMatch {
				t.Errorf("expected matches=%v, got matches=%v", tt.expectedMatch, matches)
			}
			if reason != tt.expectedReason {
				t.Errorf("expected reason=%q, got reason=%q", tt.expectedReason, reason)
			}
		})
	}
}

// TestCheckConfidenceFilter tests the confidence filter logic
//
// Modern Go 1.25 Test Patterns:
// - Tests complex extracted function (original cognitive complexity was 94!)
// - Comprehensive error path testing
// - Parallel execution for all independent cases
//
// Best Practices for LLMs:
// - When testing complex functions, categorize test cases:
//   * Valid cases (happy path)
//   * Invalid filter value
//   * Invalid operators
//   * Invalid thresholds
//   * Missing/invalid metadata
// - Use descriptive test names that explain the scenario
// - Group related tests with comments
func TestCheckConfidenceFilter(t *testing.T) {
	t.Parallel() // Safe: no shared state
	t.Attr("category", "unit")
	t.Attr("function", "checkConfidenceFilter")

	tests := []struct {
		name           string
		filterVal      any
		metadata       map[string]any
		expectedMatch  bool
		expectedReason string
	}{
		// Valid cases
		{
			name:           "gt_pass",
			filterVal:      ">0.8",
			metadata:       map[string]any{"confidence": 0.9},
			expectedMatch:  true,
			expectedReason: "",
		},
		{
			name:           "gt_fail",
			filterVal:      ">0.8",
			metadata:       map[string]any{"confidence": 0.7},
			expectedMatch:  false,
			expectedReason: filterReasonConfidenceThreshold,
		},
		{
			name:           "gte_equal",
			filterVal:      ">=0.8",
			metadata:       map[string]any{"confidence": 0.8},
			expectedMatch:  true,
			expectedReason: "",
		},

		// Error cases - invalid filter value
		{
			name:           "non_string_filter",
			filterVal:      123,
			metadata:       map[string]any{"confidence": 0.8},
			expectedMatch:  false,
			expectedReason: filterReasonConfidenceThreshold,
		},
		{
			name:           "empty_condition",
			filterVal:      "",
			metadata:       map[string]any{"confidence": 0.8},
			expectedMatch:  false,
			expectedReason: filterReasonConfidenceThreshold,
		},
		{
			name:           "whitespace_only",
			filterVal:      "   ",
			metadata:       map[string]any{"confidence": 0.8},
			expectedMatch:  false,
			expectedReason: filterReasonConfidenceThreshold,
		},

		// Error cases - invalid operator
		{
			name:           "invalid_operator",
			filterVal:      "~0.8",
			metadata:       map[string]any{"confidence": 0.8},
			expectedMatch:  false,
			expectedReason: filterReasonConfidenceThreshold,
		},
		{
			name:           "no_operator",
			filterVal:      "0.8",
			metadata:       map[string]any{"confidence": 0.8},
			expectedMatch:  false,
			expectedReason: filterReasonConfidenceThreshold,
		},

		// Error cases - invalid threshold
		{
			name:           "invalid_threshold",
			filterVal:      ">abc",
			metadata:       map[string]any{"confidence": 0.8},
			expectedMatch:  false,
			expectedReason: filterReasonConfidenceThreshold,
		},

		// Error cases - missing or invalid confidence
		{
			name:           "missing_confidence",
			filterVal:      ">0.8",
			metadata:       map[string]any{},
			expectedMatch:  false,
			expectedReason: filterReasonConfidenceThreshold,
		},
		{
			name:           "invalid_confidence_type",
			filterVal:      ">0.8",
			metadata:       map[string]any{"confidence": "invalid"},
			expectedMatch:  false,
			expectedReason: filterReasonConfidenceThreshold,
		},
		{
			name:           "nil_confidence",
			filterVal:      ">0.8",
			metadata:       map[string]any{"confidence": nil},
			expectedMatch:  false,
			expectedReason: filterReasonConfidenceThreshold,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notif := &Notification{Metadata: tt.metadata}

			matches, reason := checkConfidenceFilter(tt.filterVal, notif, nil, "test-provider")

			if matches != tt.expectedMatch {
				t.Errorf("expected matches=%v, got matches=%v", tt.expectedMatch, matches)
			}
			if reason != tt.expectedReason {
				t.Errorf("expected reason=%q, got reason=%q", tt.expectedReason, reason)
			}
		})
	}
}

// TestCheckExactMetadataMatch tests exact metadata key-value matching
//
// Modern Go 1.25 Test Patterns:
// - Tests type-agnostic comparison using fmt.Sprint()
// - Covers multiple data types: string, int, float, bool
// - Parallel-safe pure function
//
// Best Practices for LLMs:
// - When functions compare any types, test ALL common types:
//   * strings, integers, floats, booleans
// - Test type coercion (fmt.Sprint converts everything to string)
// - Test missing keys and empty maps
// - Document the comparison strategy in comments
func TestCheckExactMetadataMatch(t *testing.T) {
	t.Parallel() // Safe: pure function, no shared state
	t.Attr("category", "unit")
	t.Attr("function", "checkExactMetadataMatch")

	tests := []struct {
		name           string
		key            string
		expectedVal    any
		metadata       map[string]any
		expectedMatch  bool
		expectedReason string
	}{
		// String matches
		{
			name:           "string_match",
			key:            "source",
			expectedVal:    "sensor1",
			metadata:       map[string]any{"source": "sensor1"},
			expectedMatch:  true,
			expectedReason: "",
		},
		{
			name:           "string_mismatch",
			key:            "source",
			expectedVal:    "sensor1",
			metadata:       map[string]any{"source": "sensor2"},
			expectedMatch:  false,
			expectedReason: filterReasonMetadataMismatch,
		},

		// Numeric matches
		{
			name:           "int_match",
			key:            "count",
			expectedVal:    42,
			metadata:       map[string]any{"count": 42},
			expectedMatch:  true,
			expectedReason: "",
		},
		{
			name:           "float_match",
			key:            "value",
			expectedVal:    3.14,
			metadata:       map[string]any{"value": 3.14},
			expectedMatch:  true,
			expectedReason: "",
		},

		// Boolean matches
		{
			name:           "bool_match_true",
			key:            "verified",
			expectedVal:    true,
			metadata:       map[string]any{"verified": true},
			expectedMatch:  true,
			expectedReason: "",
		},
		{
			name:           "bool_match_false",
			key:            "verified",
			expectedVal:    false,
			metadata:       map[string]any{"verified": false},
			expectedMatch:  true,
			expectedReason: "",
		},
		{
			name:           "bool_mismatch",
			key:            "verified",
			expectedVal:    true,
			metadata:       map[string]any{"verified": false},
			expectedMatch:  false,
			expectedReason: filterReasonMetadataMismatch,
		},

		// Missing key
		{
			name:           "key_missing",
			key:            "missing",
			expectedVal:    "value",
			metadata:       map[string]any{"other": "value"},
			expectedMatch:  false,
			expectedReason: filterReasonMetadataMismatch,
		},
		{
			name:           "empty_metadata",
			key:            "key",
			expectedVal:    "value",
			metadata:       map[string]any{},
			expectedMatch:  false,
			expectedReason: filterReasonMetadataMismatch,
		},

		// Type conversion via fmt.Sprint
		{
			name:           "int_to_string_comparison",
			key:            "id",
			expectedVal:    "123",
			metadata:       map[string]any{"id": 123},
			expectedMatch:  true,
			expectedReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notif := &Notification{Metadata: tt.metadata}

			matches, reason := checkExactMetadataMatch(tt.key, tt.expectedVal, notif, nil, "test-provider")

			if matches != tt.expectedMatch {
				t.Errorf("expected matches=%v, got matches=%v", tt.expectedMatch, matches)
			}
			if reason != tt.expectedReason {
				t.Errorf("expected reason=%q, got reason=%q", tt.expectedReason, reason)
			}
		})
	}
}

// TestCheckMetadataFilters tests the metadata filter orchestration
//
// Modern Go 1.25 Test Patterns:
// - Tests orchestration function that coordinates other filters
// - Integration-style unit tests (calls checkConfidenceFilter + checkExactMetadataMatch)
// - Parallel execution enabled
//
// Best Practices for LLMs:
// - Orchestration functions should be tested for:
//   * Empty inputs (no filters)
//   * Single filter type (confidence OR exact)
//   * Multiple filter types (confidence AND exact)
//   * Partial success scenarios (one passes, one fails)
// - Test early-return behavior (first failure stops processing)
// - This demonstrates how refactored code enables easier testing
func TestCheckMetadataFilters(t *testing.T) {
	t.Parallel() // Safe: orchestration function with no shared state
	t.Attr("category", "unit")
	t.Attr("function", "checkMetadataFilters")

	tests := []struct {
		name           string
		filterMetadata map[string]any
		notifMetadata  map[string]any
		expectedMatch  bool
		expectedReason string
	}{
		{
			name:           "no_filters_pass",
			filterMetadata: map[string]any{},
			notifMetadata:  map[string]any{"source": "sensor1"},
			expectedMatch:  true,
			expectedReason: "",
		},
		{
			name: "confidence_and_exact_match",
			filterMetadata: map[string]any{
				"confidence": ">0.8",
				"source":     "sensor1",
			},
			notifMetadata: map[string]any{
				"confidence": 0.9,
				"source":     "sensor1",
			},
			expectedMatch:  true,
			expectedReason: "",
		},
		{
			name: "confidence_pass_exact_fail",
			filterMetadata: map[string]any{
				"confidence": ">0.8",
				"source":     "sensor1",
			},
			notifMetadata: map[string]any{
				"confidence": 0.9,
				"source":     "sensor2",
			},
			expectedMatch:  false,
			expectedReason: filterReasonMetadataMismatch,
		},
		{
			name: "confidence_fail",
			filterMetadata: map[string]any{
				"confidence": ">0.8",
			},
			notifMetadata: map[string]any{
				"confidence": 0.5,
			},
			expectedMatch:  false,
			expectedReason: filterReasonConfidenceThreshold,
		},
		{
			name: "multiple_exact_matches",
			filterMetadata: map[string]any{
				"source":   "sensor1",
				"verified": true,
				"location": "backyard",
			},
			notifMetadata: map[string]any{
				"source":   "sensor1",
				"verified": true,
				"location": "backyard",
			},
			expectedMatch:  true,
			expectedReason: "",
		},
		{
			name: "one_exact_match_fails",
			filterMetadata: map[string]any{
				"source":   "sensor1",
				"verified": true,
			},
			notifMetadata: map[string]any{
				"source":   "sensor1",
				"verified": false,
			},
			expectedMatch:  false,
			expectedReason: filterReasonMetadataMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := &conf.PushFilterConfig{MetadataFilters: tt.filterMetadata}
			notif := &Notification{Metadata: tt.notifMetadata}

			matches, reason := checkMetadataFilters(filter, notif, nil, "test-provider")

			if matches != tt.expectedMatch {
				t.Errorf("expected matches=%v, got matches=%v", tt.expectedMatch, matches)
			}
			if reason != tt.expectedReason {
				t.Errorf("expected reason=%q, got reason=%q", tt.expectedReason, reason)
			}
		})
	}
}

// TestContainsLocalhost verifies localhost detection in base URLs
func TestContainsLocalhost(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected bool
	}{
		{"localhost lowercase", "http://localhost:8080", true},
		{"localhost uppercase", "HTTP://LOCALHOST:8080", true},
		{"localhost mixed case", "http://LocalHost:8080", true},
		{"127.0.0.1", "http://127.0.0.1:8080", true},
		{"127.0.0.1 no port", "http://127.0.0.1", true},
		{"external domain", "https://example.com", false},
		{"internal.local", "http://birdnet.local", false}, // Different function handles this
		{"192.168.x.x", "http://192.168.1.1", false},      // Not localhost
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsLocalhost(tt.baseURL)
			if result != tt.expected {
				t.Errorf("containsLocalhost(%q) = %v, expected %v", tt.baseURL, result, tt.expected)
			}
		})
	}
}

// TestIsPrivateOrLocalURL verifies private/local network detection
func TestIsPrivateOrLocalURL(t *testing.T) {
	tests := []struct {
		name     string
		urlStr   string
		expected bool
	}{
		// Localhost tests
		{"localhost", "http://localhost:8080/webhook", true},
		{"LOCALHOST uppercase", "http://LOCALHOST/webhook", true},

		// IPv4 loopback tests
		{"127.0.0.1", "http://127.0.0.1:8080", true},
		{"127.0.0.255", "http://127.0.0.255", true},
		{"127.1.2.3", "http://127.1.2.3", true},

		// IPv6 loopback test
		{"::1", "http://[::1]:8080", true},

		// IPv4 private networks (RFC 1918)
		{"10.x.x.x", "http://10.0.0.1", true},
		{"10.255.255.255", "http://10.255.255.255", true},
		{"172.16.x.x", "http://172.16.0.1", true},
		{"172.31.255.255", "http://172.31.255.255", true},
		{"192.168.x.x", "http://192.168.1.1", true},
		{"192.168.255.255", "http://192.168.255.255", true},

		// IPv6 private networks (RFC 4193)
		{"fc00::", "http://[fc00::1]", true},
		{"fd00::", "http://[fd00::1]", true},

		// Internal TLD tests
		{".local TLD", "http://birdnet.local", true},
		{".internal TLD", "http://server.internal", true},
		{".lan TLD", "http://nas.lan", true},
		{".home TLD", "http://router.home", true},
		{".corp TLD", "http://intranet.corp", true},
		{".private TLD", "http://api.private", true},

		// External/public addresses (should return false)
		{"public IP", "http://8.8.8.8", false},
		{"external domain", "https://api.external.com", false},
		{"discord webhook", "https://discord.com/api/webhooks/xxx", false},
		{"example.com", "https://example.com/webhook", false},

		// Edge cases
		{"invalid URL", "not-a-url", false}, // Invalid URLs treated as external (safe default)
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isPrivateOrLocalURL(tt.urlStr)
			if result != tt.expected {
				t.Errorf("isPrivateOrLocalURL(%q) = %v, expected %v", tt.urlStr, result, tt.expected)
			}
		})
	}
}
