// File: internal/httpcontroller/handlers/sse.go

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// SSE connection configuration
const (
	// Connection timeouts
	maxSSEConnectionDuration = 30 * time.Minute // Maximum connection duration to prevent resource leaks
	sseHeartbeatIntervalV1   = 30 * time.Second // Heartbeat interval for v1 SSE connections
	
	// Buffer sizes
	sseClientChannelBuffer   = 100 // Buffer size for SSE client channels
)

type Notification struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type SSEHandler struct {
	clients          map[chan Notification]bool
	clientsMux       sync.Mutex
	debug            bool
	activeConnections int64 // Track total active connections
}

func NewSSEHandler() *SSEHandler {
	return &SSEHandler{
		clients: make(map[chan Notification]bool),
		debug:   conf.Setting().WebServer.Debug,
	}
}

// ServeSSE handles Server-Sent Events connections with proper timeout management
// API: GET /api/v1/sse
func (h *SSEHandler) ServeSSE(c echo.Context) error {
	// Track active connections
	atomic.AddInt64(&h.activeConnections, 1)
	
	h.Debug("SSE: New connection request from %s (total active: %d)", c.Request().RemoteAddr, atomic.LoadInt64(&h.activeConnections))

	c.Response().Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	// Use buffered channel to prevent blocking on cleanup
	clientChan := make(chan Notification, sseClientChannelBuffer)
	h.addClient(clientChan)

	// Single defer function to handle cleanup in correct order
	defer func() {
		h.removeClient(clientChan)
		atomic.AddInt64(&h.activeConnections, -1)
		h.Debug("SSE: Connection closed for %s (total active: %d)", c.Request().RemoteAddr, atomic.LoadInt64(&h.activeConnections))
	}()

	// Create a context with timeout for maximum connection duration
	timeoutCtx, cancel := context.WithTimeout(c.Request().Context(), maxSSEConnectionDuration)
	defer cancel()
	
	// Track connection start time
	connectionStart := time.Now()

	// Add heartbeat
	heartbeat := time.NewTicker(sseHeartbeatIntervalV1)
	defer heartbeat.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			// Context timeout or cancellation
			if timeoutCtx.Err() == context.DeadlineExceeded {
				h.Debug("SSE: Connection exceeded max duration for %s (duration: %v)", c.Request().RemoteAddr, time.Since(connectionStart))
			} else {
				h.Debug("SSE: Context cancelled for %s", c.Request().RemoteAddr)
			}
			return nil
		case notification := <-clientChan:
			data, err := json.Marshal(notification)
			if err != nil {
				h.Debug("SSE: Error marshaling notification: %v", err)
				continue
			}
			_, err = fmt.Fprintf(c.Response(), "data: %s\n\n", data)
			if err != nil {
				h.Debug("SSE: Write error for %s: %v", c.Request().RemoteAddr, err)
				return err
			}
			c.Response().Flush()
		case <-heartbeat.C:
			// Check if connection has exceeded maximum duration
			if time.Since(connectionStart) > maxSSEConnectionDuration {
				h.Debug("SSE: Maximum connection duration exceeded for %s, closing connection", c.Request().RemoteAddr)
				return nil
			}
			
			_, err := fmt.Fprintf(c.Response(), ":\n\n")
			if err != nil {
				h.Debug("SSE: Heartbeat error for %s: %v", c.Request().RemoteAddr, err)
				return err
			}
			c.Response().Flush()
		}
	}
}

func (h *SSEHandler) SendNotification(notification Notification) {
	h.clientsMux.Lock()
	clientCount := len(h.clients)
	log.Printf("SSE: Starting to broadcast notification to %d clients", clientCount)

	for clientChan := range h.clients {
		select {
		case clientChan <- notification:
			h.Debug("SSE: Successfully sent notification to client channel")
		default:
			h.Debug("SSE: Warning - Client channel is blocked, skipping notification")
			// Optionally, you might want to remove blocked clients
			// go h.removeClient(clientChan)
		}
	}
	h.clientsMux.Unlock()
	h.Debug("SSE: Finished broadcasting notification to all clients")
}

func (h *SSEHandler) addClient(clientChan chan Notification) {
	h.clientsMux.Lock()
	defer h.clientsMux.Unlock()
	h.clients[clientChan] = true
	h.Debug("SSE: New client connected. Channel buffer remaining: %d. Total clients: %d", cap(clientChan)-len(clientChan), len(h.clients))
}

func (h *SSEHandler) removeClient(clientChan chan Notification) {
	h.clientsMux.Lock()
	delete(h.clients, clientChan)
	close(clientChan)
	h.clientsMux.Unlock()

	h.Debug("SSE: Client disconnected. Total clients: %d", len(h.clients))
}

func (h *SSEHandler) Debug(format string, v ...any) {
	if h.debug {
		if len(v) == 0 {
			log.Print(format)
		} else {
			log.Printf(format, v...)
		}
	}
}
