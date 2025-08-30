package processor

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/mqtt"
)

// MockMqttClient is a mock implementation of the mqtt.Client interface for testing
type MockMqttClient struct {
	mock.Mock
}

func (m *MockMqttClient) Connect(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockMqttClient) Publish(ctx context.Context, topic, payload string) error {
	args := m.Called(ctx, topic, payload)
	return args.Error(0)
}

func (m *MockMqttClient) IsConnected() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockMqttClient) Disconnect() {
	m.Called()
}

func (m *MockMqttClient) TestConnection(ctx context.Context, resultChan chan<- mqtt.TestResult) {
	m.Called(ctx, resultChan)
}

func (m *MockMqttClient) SetControlChannel(ch chan string) {
	m.Called(ch)
}

// Add test functions below...

func TestMqttAction_Execute_NotConnected(t *testing.T) {
	// Arrange
	mockClient := new(MockMqttClient)
	// Provide a duration for NewEventTracker
	mockEventTracker := NewEventTracker(1 * time.Minute)
	// Set BirdImageCache to nil as its instantiation is unclear/unneeded for this test
	var mockImageCache *imageprovider.BirdImageCache // = nil

	mockClient.On("IsConnected").Return(false).Once()

	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			MQTT: conf.MQTTSettings{
				Topic: "test/topic",
			},
		},
	}
	note := datastore.Note{
		CommonName:     "Robin",
		ScientificName: "Turdus migratorius",
	}

	// Create RetryConfig manually
	retryConfig := jobqueue.RetryConfig{
		Enabled:      false, // Not relevant for this test path, but set default
		MaxRetries:   0,
		InitialDelay: 0,
		MaxDelay:     0,
		Multiplier:   0,
	}

	action := &MqttAction{
		Settings:       settings,
		Note:           note,
		BirdImageCache: mockImageCache, // Pass nil cache
		MqttClient:     mockClient,
		EventTracker:   mockEventTracker,
		RetryConfig:    retryConfig, // Use the manually created config
	}

	// Act
	err := action.Execute(nil)

	// Assert
	require.Error(t, err)
	require.ErrorContains(t, err, "MQTT client not connected")
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "Connect", mock.Anything)
	mockClient.AssertNotCalled(t, "Publish", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"))
}

func TestMqttAction_Execute_Connected(t *testing.T) {
	// Arrange
	mockClient := new(MockMqttClient)
	// Provide a duration for NewEventTracker
	mockEventTracker := NewEventTracker(1 * time.Minute)
	// Set BirdImageCache to nil as its instantiation is unclear/unneeded for this test
	var mockImageCache *imageprovider.BirdImageCache // = nil

	mockClient.On("IsConnected").Return(true).Once()
	// Expect Publish to be called, but don't worry about the exact JSON payload
	mockClient.On("Publish", mock.Anything, "test/topic", mock.AnythingOfType("string")).Return(nil).Once()

	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			MQTT: conf.MQTTSettings{
				Topic: "test/topic",
			},
		},
	}
	note := datastore.Note{
		CommonName:     "Robin",
		ScientificName: "Turdus migratorius",
	}

	// Create RetryConfig manually
	retryConfig := jobqueue.RetryConfig{
		Enabled:      false, // Not relevant for this test path, but set default
		MaxRetries:   0,
		InitialDelay: 0,
		MaxDelay:     0,
		Multiplier:   0,
	}

	action := &MqttAction{
		Settings:       settings,
		Note:           note,
		BirdImageCache: mockImageCache, // Pass nil cache
		MqttClient:     mockClient,
		EventTracker:   mockEventTracker,
		RetryConfig:    retryConfig, // Use the manually created config
	}

	// Act
	err := action.Execute(nil)

	// Assert
	require.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "Connect", mock.Anything)
}

func TestIsEOFError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "Direct io.EOF",
			err:      io.EOF,
			expected: true,
		},
		{
			name:     "Wrapped io.EOF",
			err:      fmt.Errorf("connection failed: %w", io.EOF),
			expected: true,
		},
		{
			name:     "String containing EOF (uppercase)",
			err:      fmt.Errorf("unexpected EOF"),
			expected: true,
		},
		{
			name:     "String containing EOF (lowercase)",
			err:      fmt.Errorf("connection terminated with eof"),
			expected: true,
		},
		{
			name:     "String containing EOF (mixed case)",
			err:      fmt.Errorf("TCP connection closed: Eof"),
			expected: true,
		},
		{
			name:     "Network error without EOF",
			err:      fmt.Errorf("connection refused"),
			expected: false,
		},
		{
			name:     "Timeout error",
			err:      fmt.Errorf("context deadline exceeded"),
			expected: false,
		},
		{
			name:     "DNS error",
			err:      fmt.Errorf("no such host"),
			expected: false,
		},
		{
			name:     "String containing EOF in the middle of word",
			err:      fmt.Errorf("buffereof data"),
			expected: true, // This will match because it contains "eof"
		},
		{
			name:     "Empty error message",
			err:      fmt.Errorf(""),
			expected: false,
		},
		{
			name:     "Nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isEOFError(tt.err)
			if result != tt.expected {
				t.Errorf("isEOFError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// TestIsEOFErrorBehaviorDocumentation tests edge cases and documents expected behavior
func TestIsEOFErrorBehaviorDocumentation(t *testing.T) {
	t.Run("StringMatchingIsCaseInsensitive", func(t *testing.T) {
		cases := []error{
			fmt.Errorf("EOF"),
			fmt.Errorf("eof"),
			fmt.Errorf("Eof"),
			fmt.Errorf("eOf"),
		}
		for _, err := range cases {
			if !isEOFError(err) {
				t.Errorf("Expected %v to be detected as EOF error", err)
			}
		}
	})

	t.Run("StringMatchingIsSubstringBased", func(t *testing.T) {
		// This documents that our string matching is substring-based
		// which may have false positives but is safer for wrapped errors
		err := fmt.Errorf("buffereof data") // contains "eof" 
		if !isEOFError(err) {
			t.Errorf("Expected buffereof data to be detected as EOF error")
		}
		t.Log("Note: This test documents that substring matching can have false positives")
		t.Log("The string 'buffereof data' contains 'eof' and will be detected as an EOF error")
		t.Log("This is acceptable as it's better to have false positives than miss wrapped EOF errors")
	})

	t.Run("ErrorsIsHasPriorityOverStringMatching", func(t *testing.T) {
		// Test that errors.Is correctly identifies wrapped io.EOF
		wrapped := fmt.Errorf("network error: %w", io.EOF)
		if !isEOFError(wrapped) {
			t.Errorf("Expected wrapped io.EOF to be detected: %v", wrapped)
		}
	})
}
