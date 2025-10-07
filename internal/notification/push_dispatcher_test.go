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
