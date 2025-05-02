package processor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
	assert.Error(t, err)
	assert.ErrorContains(t, err, "MQTT client not connected")
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
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "Connect", mock.Anything)
}
