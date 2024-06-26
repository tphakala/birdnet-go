// Package mqtt provides an abstraction for MQTT client functionality.
package mqtt

import (
	"context"
)

// Client defines the interface for MQTT client operations.
type Client interface {
	// Connect attempts to connect to the MQTT broker.
	// It returns an error if the connection fails.
	Connect(ctx context.Context) error

	// Publish sends a message to the specified topic on the MQTT broker.
	// It returns an error if the publish operation fails.
	Publish(ctx context.Context, topic string, payload string) error

	// IsConnected returns true if the client is currently connected to the MQTT broker.
	IsConnected() bool

	// Disconnect closes the connection to the MQTT broker.
	Disconnect()
}

// Config holds the configuration for the MQTT client.
type Config struct {
	Broker   string
	ClientID string
	Username string
	Password string
}
