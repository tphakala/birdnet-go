// mqtt.go: mqtt broker connection and reconnection logic
package processor

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/tphakala/birdnet-go/internal/mqtt"
)

// initializeMQTT initializes the MQTT client and attempts to connect
func (p *Processor) initializeMQTT() error {
	if !p.Settings.Realtime.MQTT.Enabled {
		return nil
	}

	p.MqttClient = mqtt.New(p.Settings)
	p.mqttReconnectStop = make(chan struct{})
	p.startReconnectTimer()

	// Attempt to connect to the MQTT broker
	go p.connectMQTT()

	// Wait for initial connection attempt
	select {
	case <-time.After(5 * time.Second):
		if !p.MqttClient.IsConnected() {
			return errors.New("initial MQTT connection attempt failed")
		}
	}

	return nil
}

// startReconnectTimer starts the reconnect timer for the MQTT client
func (p *Processor) startReconnectTimer() {
	p.mqttReconnectTimer = time.AfterFunc(time.Minute, func() {
		select {
		case <-p.mqttReconnectStop:
			return
		default:
			p.reconnectMQTT()
		}
	})
}

// cleanupMQTT cleans up the MQTT client and reconnect timer
func (p *Processor) cleanupMQTT() {
	if p.mqttReconnectTimer != nil {
		p.mqttReconnectTimer.Stop()
	}
	if p.mqttReconnectStop != nil {
		close(p.mqttReconnectStop)
	}
}

// connectMQTT attempts to connect to the MQTT broker with retries
func (p *Processor) connectMQTT() {
	const maxRetries = 5
	retryDelay := time.Second

	for i := 0; i < maxRetries; i++ {
		log.Println("Connecting to MQTT broker")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := p.MqttClient.Connect(ctx)
		cancel()

		if err == nil {
			log.Println("Successfully connected to MQTT broker")
			return
		}

		log.Printf("Failed to connect to MQTT broker (attempt %d/%d): %s", i+1, maxRetries, err)

		if i < maxRetries-1 {
			log.Printf("Retrying in %v", retryDelay)
			time.Sleep(retryDelay)
			retryDelay *= 2 // Exponential backoff
		}
	}

	log.Println("Failed to connect to MQTT broker after maximum retries")
}

// reconnectMQTT attempts to reconnect to the MQTT broker with exponential backoff
func (p *Processor) reconnectMQTT() {
	backoff := time.Second
	maxBackoff := 5 * time.Minute

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := p.MqttClient.Connect(ctx)
		cancel()

		if err == nil {
			log.Println("Successfully reconnected to MQTT broker")
			p.startReconnectTimer()
			return
		}

		log.Printf("Failed to reconnect to MQTT broker: %s", err)
		log.Printf("Retrying in %v", backoff)

		// Exponential backoff with a maximum backoff time
		select {
		case <-time.After(backoff):
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		case <-p.mqttReconnectStop:
			return
		}
	}
}
