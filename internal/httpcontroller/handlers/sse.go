// File: internal/httpcontroller/handlers/sse.go

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
)

type Notification struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type SSEHandler struct {
	clients    map[chan Notification]bool
	clientsMux sync.Mutex
	debug      bool
}

func NewSSEHandler() *SSEHandler {
	return &SSEHandler{
		clients: make(map[chan Notification]bool),
		debug:   conf.Setting().WebServer.Debug,
	}
}

// ServeSSE handles Server-Sent Events connections
// API: GET /api/v1/sse
func (h *SSEHandler) ServeSSE(c echo.Context) error {
	h.Debug("SSE: New connection request from %s", c.Request().RemoteAddr)

	c.Response().Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	clientChan := make(chan Notification, 100)
	h.addClient(clientChan)

	// Use a context with cancel for cleanup
	ctx, cancel := context.WithCancel(c.Request().Context())
	defer func() {
		cancel()
		h.removeClient(clientChan)
		h.Debug("SSE: Connection closed for %s", c.Request().RemoteAddr)
	}()

	// Add heartbeat
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			h.Debug("SSE: Context cancelled for %s", c.Request().RemoteAddr)
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

func (h *SSEHandler) Debug(format string, v ...interface{}) {
	if h.debug {
		if len(v) == 0 {
			log.Print(format)
		} else {
			log.Printf(format, v...)
		}
	}
}
