// mqtt.go: mqtt broker connection and reconnection logic
package processor

import (
	"context"
	"log"
	"time"

	"github.com/tphakala/birdnet-go/internal/mqtt"
)

// initializeMQTT initializes the MQTT client and attempts to connect
func (p *Processor) initializeMQTT() {
	if !p.Settings.Realtime.MQTT.Enabled {
		return
	}

	p.MqttClient = mqtt.New(p.Settings)
	p.mqttReconnectTimer = time.AfterFunc(time.Minute, p.reconnectMQTT)

	go p.connectMQTT()
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

// reconnectMQTT attempts to reconnect to the MQTT broker
func (p *Processor) reconnectMQTT() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := p.MqttClient.Connect(ctx); err != nil {
		log.Printf("Failed to reconnect to MQTT broker: %s", err)
		// Schedule next reconnection attempt
		p.mqttReconnectTimer.Reset(time.Minute)
	}
}
