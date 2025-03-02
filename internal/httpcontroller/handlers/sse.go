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

	"github.com/google/uuid"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

type Notification struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type SSEHandler struct {
	clients         map[chan Notification]bool
	clientsMux      sync.Mutex
	debug           bool
	logger          *logger.Logger
	connectionCount int
}

func NewSSEHandler(baseLogger *logger.Logger) *SSEHandler {
	var componentLogger *logger.Logger
	if baseLogger != nil {
		componentLogger = baseLogger.Named("sse.handler")
	} else {
		componentLogger = nil
	}

	return &SSEHandler{
		clients:         make(map[chan Notification]bool),
		debug:           conf.Setting().WebServer.Debug,
		logger:          componentLogger,
		connectionCount: 0,
	}
}

// getConnectionLogger creates a connection-specific logger with the connection ID and client IP as fields
func (h *SSEHandler) getConnectionLogger(connectionID, clientIP string) *logger.Logger {
	if h.logger == nil {
		return nil
	}

	// Create a connection-specific logger with connection ID as part of the name hierarchy
	// and client IP as a permanent field
	return h.logger.Named("conn").With(
		"connection_id", connectionID,
		"client_ip", clientIP,
	)
}

// ServeSSE handles Server-Sent Events connections
// API: GET /api/v1/sse
func (h *SSEHandler) ServeSSE(c echo.Context) error {
	clientIP := c.Request().RemoteAddr
	connectionID := uuid.New().String()[:8] // Generate a short unique ID for this connection

	// Get a connection-specific logger
	connLogger := h.getConnectionLogger(connectionID, clientIP)

	// Convert if-else chain to switch statement
	switch {
	case connLogger != nil:
		connLogger.Debug("New connection request")
	case h.logger != nil:
		h.logger.Debug("New connection request",
			"client_ip", clientIP,
			"connection_id", connectionID)
	case h.debug:
		log.Printf("SSE: New connection request from %s", clientIP)
	}

	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	clientChan := make(chan Notification, 100)
	h.addClient(clientChan, connectionID, clientIP)

	// Use a context with cancel for cleanup
	ctx, cancel := context.WithCancel(c.Request().Context())
	defer func() {
		cancel()
		h.removeClient(clientChan, connectionID, clientIP)

		if connLogger != nil {
			connLogger.Debug("Connection closed")
		}
	}()

	// Add heartbeat
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	// Track connection start time for duration logging
	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			// Convert if-else chain to switch statement
			switch {
			case connLogger != nil:
				connLogger.Debug("Context cancelled",
					"duration_ms", time.Since(startTime).Milliseconds())
			case h.logger != nil:
				h.logger.Debug("Context cancelled",
					"client_ip", clientIP,
					"connection_id", connectionID,
					"duration_ms", time.Since(startTime).Milliseconds())
			case h.debug:
				log.Printf("SSE: Context cancelled for %s", clientIP)
			}
			return nil
		case notification := <-clientChan:
			data, err := json.Marshal(notification)
			if err != nil {
				// Convert if-else chain to switch statement
				switch {
				case connLogger != nil:
					connLogger.Error("Error marshaling notification", "error", err)
				case h.logger != nil:
					h.logger.Error("Error marshaling notification",
						"client_ip", clientIP,
						"connection_id", connectionID,
						"error", err)
				case h.debug:
					log.Printf("SSE: Error marshaling notification: %v", err)
				}
				continue
			}
			_, err = fmt.Fprintf(c.Response(), "data: %s\n\n", data)
			if err != nil {
				// Convert if-else chain to switch statement
				switch {
				case connLogger != nil:
					connLogger.Error("Write error", "error", err)
				case h.logger != nil:
					h.logger.Error("Write error",
						"client_ip", clientIP,
						"connection_id", connectionID,
						"error", err)
				case h.debug:
					log.Printf("SSE: Write error for %s: %v", clientIP, err)
				}
				return err
			}
			c.Response().Flush()
		case <-heartbeat.C:
			_, err := fmt.Fprintf(c.Response(), ":\n\n")
			if err != nil {
				// Convert if-else chain to switch statement
				switch {
				case connLogger != nil:
					connLogger.Error("Heartbeat error", "error", err)
				case h.logger != nil:
					h.logger.Error("Heartbeat error",
						"client_ip", clientIP,
						"connection_id", connectionID,
						"error", err)
				case h.debug:
					log.Printf("SSE: Heartbeat error for %s: %v", clientIP, err)
				}
				return err
			}
			c.Response().Flush()
		}
	}
}

func (h *SSEHandler) SendNotification(notification Notification) {
	h.clientsMux.Lock()
	clientCount := len(h.clients)

	// Create a specific logger based on notification type
	var notificationLogger *logger.Logger
	if h.logger != nil {
		switch notification.Type {
		case "audio":
			notificationLogger = h.logger.Named("audio")
		case "detection":
			notificationLogger = h.logger.Named("detection")
		default:
			notificationLogger = h.logger
		}

		notificationLogger.Info("Broadcasting notification",
			"total_clients", clientCount,
			"message_type", notification.Type)
	} else {
		log.Printf("SSE: Starting to broadcast notification to %d clients", clientCount)
	}

	for clientChan := range h.clients {
		select {
		case clientChan <- notification:
			if h.logger != nil && h.debug {
				if notificationLogger != nil {
					notificationLogger.Debug("Sent notification to client")
				} else {
					h.logger.Debug("Sent notification to client")
				}
			}
		default:
			if h.logger != nil {
				if notificationLogger != nil {
					notificationLogger.Warn("Client channel blocked, skipping notification")
				} else {
					h.logger.Warn("Client channel blocked, skipping notification")
				}
			} else if h.debug {
				log.Printf("SSE: Warning - Client channel is blocked, skipping notification")
			}
			// Optionally, you might want to remove blocked clients
			// go h.removeClient(clientChan)
		}
	}
	h.clientsMux.Unlock()

	if h.logger != nil && h.debug {
		if notificationLogger != nil {
			notificationLogger.Debug("Finished broadcasting notification", "total_clients", clientCount)
		} else {
			h.logger.Debug("Finished broadcasting notification", "total_clients", clientCount)
		}
	} else if h.debug {
		log.Printf("SSE: Finished broadcasting notification to all clients")
	}
}

func (h *SSEHandler) addClient(clientChan chan Notification, connectionID, clientIP string) {
	h.clientsMux.Lock()
	defer h.clientsMux.Unlock()

	h.clients[clientChan] = true
	h.connectionCount++

	// Get a connection-specific logger
	connLogger := h.getConnectionLogger(connectionID, clientIP)

	// Convert if-else chain to switch statement
	switch {
	case connLogger != nil:
		connLogger.Info("Client connected",
			"buffer_remaining", cap(clientChan)-len(clientChan),
			"total_clients", len(h.clients))
	case h.logger != nil:
		h.logger.Info("Client connected",
			"client_ip", clientIP,
			"connection_id", connectionID,
			"buffer_remaining", cap(clientChan)-len(clientChan),
			"total_clients", len(h.clients))
	case h.debug:
		log.Printf("SSE: New client connected. Channel buffer remaining: %d. Total clients: %d",
			cap(clientChan)-len(clientChan), len(h.clients))
	}
}

func (h *SSEHandler) removeClient(clientChan chan Notification, connectionID, clientIP string) {
	h.clientsMux.Lock()
	delete(h.clients, clientChan)
	close(clientChan)
	totalClients := len(h.clients)
	h.clientsMux.Unlock()

	// Get a connection-specific logger
	connLogger := h.getConnectionLogger(connectionID, clientIP)

	// Convert if-else chain to switch statement
	switch {
	case connLogger != nil:
		connLogger.Info("Client disconnected", "total_clients", totalClients)
	case h.logger != nil:
		h.logger.Info("Client disconnected",
			"client_ip", clientIP,
			"connection_id", connectionID,
			"total_clients", totalClients)
	case h.debug:
		log.Printf("SSE: Client disconnected. Total clients: %d", totalClients)
	}
}

// Debug logs a debug message if debug is enabled
// Deprecated: Use structured logging methods instead
func (h *SSEHandler) Debug(format string, v ...interface{}) {
	if h.debug {
		if len(v) == 0 {
			log.Print(format)
		} else {
			log.Printf(format, v...)
		}
	}
}

// LogDebug logs a debug message using the structured logger
func (h *SSEHandler) LogDebug(msg string, fields ...interface{}) {
	// Convert if-else chain to switch statement
	switch {
	case h.logger != nil && h.debug:
		h.logger.Debug(msg, fields...)
	case h.debug:
		if len(fields) > 0 {
			log.Printf("SSE DEBUG: %s %v", msg, fields)
		} else {
			log.Printf("SSE DEBUG: %s", msg)
		}
	}
}

// LogInfo logs an info message using the structured logger
func (h *SSEHandler) LogInfo(msg string, fields ...interface{}) {
	if h.logger != nil {
		h.logger.Info(msg, fields...)
	} else {
		if len(fields) > 0 {
			log.Printf("SSE INFO: %s %v", msg, fields)
		} else {
			log.Printf("SSE INFO: %s", msg)
		}
	}
}

// LogError logs an error message using the structured logger
func (h *SSEHandler) LogError(msg string, err error, fields ...interface{}) {
	if h.logger != nil {
		if err != nil {
			h.logger.Error(msg, append(fields, "error", err)...)
		} else {
			h.logger.Error(msg, fields...)
		}
	} else {
		if err != nil {
			log.Printf("SSE ERROR: %s: %v %v", msg, err, fields)
		} else {
			log.Printf("SSE ERROR: %s %v", msg, fields)
		}
	}
}
