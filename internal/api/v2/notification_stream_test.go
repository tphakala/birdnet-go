package api

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// TestNotificationWebSocketDelivery tests WebSocket delivery of notifications
func TestNotificationWebSocketDelivery(t *testing.T) {
	// Create notification service
	config := notification.DefaultServiceConfig()
	notificationService := notification.NewService(config)

	// Create logger
	logger := log.New(log.Writer(), "test: ", log.LstdFlags)

	// Create controller
	controller := &Controller{
		notificationService: notificationService,
		logger:              logger,
		wsClients:           make(map[*Client]bool),
	}

	// Create Echo server
	e := echo.New()
	server := httptest.NewServer(e)
	defer server.Close()

	// Register WebSocket handler
	e.GET("/api/v2/streams/notifications", controller.HandleNotificationsStream)

	t.Run("notification delivery via WebSocket", func(t *testing.T) {
		// Connect to WebSocket
		wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v2/streams/notifications"
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("Failed to connect to WebSocket: %v", err)
		}
		defer ws.Close()

		// Channel to receive messages
		messages := make(chan []byte, 10)
		done := make(chan bool)

		// Start reading messages
		go func() {
			defer close(done)
			for {
				_, message, err := ws.ReadMessage()
				if err != nil {
					return
				}
				messages <- message
			}
		}()

		// Give connection time to establish
		time.Sleep(50 * time.Millisecond)

		// Subscribe to notification service
		subscriber, err := notificationService.Subscribe()
		if err != nil {
			t.Fatalf("Failed to subscribe: %v", err)
		}
		defer notificationService.Unsubscribe(subscriber)

		// Create test notification
		testNotification := &notification.Notification{
			ID:        "test-ws-123",
			Type:      notification.TypeWarning,
			Message:   "Test WebSocket notification",
			Component: "ws-test",
			Timestamp: time.Now(),
		}

		// Send notification
		select {
		case subscriber <- testNotification:
			t.Log("Notification sent to subscriber")
		case <-time.After(100 * time.Millisecond):
			t.Error("Failed to send notification to subscriber")
		}

		// Broadcast to WebSocket clients
		controller.broadcastNotification(testNotification)

		// Check if notification was received
		select {
		case msg := <-messages:
			t.Logf("Received WebSocket message: %s", string(msg))
			
			// Parse message
			var received map[string]interface{}
			if err := json.Unmarshal(msg, &received); err != nil {
				t.Errorf("Failed to parse message: %v", err)
			}
			
			// Verify content
			if id, ok := received["id"].(string); !ok || id != "test-ws-123" {
				t.Errorf("Wrong notification ID: got %v, want test-ws-123", received["id"])
			}
			
		case <-time.After(500 * time.Millisecond):
			t.Error("Timeout waiting for WebSocket notification")
		}
	})

	t.Run("multiple clients receive notifications", func(t *testing.T) {
		wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v2/streams/notifications"
		
		// Connect multiple clients
		numClients := 3
		clients := make([]*websocket.Conn, numClients)
		messageChannels := make([]chan []byte, numClients)
		
		for i := 0; i < numClients; i++ {
			ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatalf("Failed to connect client %d: %v", i, err)
			}
			defer ws.Close()
			
			clients[i] = ws
			messageChannels[i] = make(chan []byte, 1)
			
			// Start reading for each client
			ch := messageChannels[i]
			go func(conn *websocket.Conn, msgCh chan []byte) {
				for {
					_, message, err := conn.ReadMessage()
					if err != nil {
						return
					}
					select {
					case msgCh <- message:
					default:
						// Channel full, drop message
					}
				}
			}(ws, ch)
		}

		// Give connections time to establish
		time.Sleep(100 * time.Millisecond)

		// Broadcast notification
		notification := &notification.Notification{
			ID:        "broadcast-test",
			Type:      notification.TypeInfo,
			Message:   "Broadcast to all clients",
			Component: "broadcast-test",
			Timestamp: time.Now(),
		}
		
		controller.broadcastNotification(notification)

		// Verify all clients received the notification
		for i := 0; i < numClients; i++ {
			select {
			case msg := <-messageChannels[i]:
				var received map[string]interface{}
				if err := json.Unmarshal(msg, &received); err != nil {
					t.Errorf("Client %d: Failed to parse message: %v", i, err)
					continue
				}
				if id, ok := received["id"].(string); !ok || id != "broadcast-test" {
					t.Errorf("Client %d: Wrong notification ID: %v", i, received["id"])
				}
			case <-time.After(500 * time.Millisecond):
				t.Errorf("Client %d: Timeout waiting for notification", i)
			}
		}
	})
}

// broadcastNotification simulates the broadcast functionality
func (c *Controller) broadcastNotification(notif *notification.Notification) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Marshal notification
	data, err := json.Marshal(notif)
	if err != nil {
		c.logger.Printf("Error marshaling notification: %v", err)
		return
	}

	// Send to all connected clients
	for client := range c.wsClients {
		if client.streamType == "notifications" {
			select {
			case client.send <- data:
				// Sent successfully
			default:
				// Client's send channel is full, skip
				c.logger.Printf("Client %s send channel full, skipping", client.clientID)
			}
		}
	}
}