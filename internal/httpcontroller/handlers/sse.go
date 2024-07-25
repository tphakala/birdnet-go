// File: internal/httpcontroller/handlers/sse.go

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/labstack/echo/v4"
)

type Notification struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type SSEHandler struct {
	clients    map[chan Notification]bool
	clientsMux sync.Mutex
}

func NewSSEHandler() *SSEHandler {
	return &SSEHandler{
		clients: make(map[chan Notification]bool),
	}
}

func (h *SSEHandler) ServeSSE(c echo.Context) error {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	clientChan := make(chan Notification)
	h.addClient(clientChan)
	defer h.removeClient(clientChan)

	c.Response().Flush()

	for {
		select {
		case notification := <-clientChan:
			data, _ := json.Marshal(notification)
			fmt.Fprintf(c.Response(), "data: %s\n\n", data)
			c.Response().Flush()
		case <-c.Request().Context().Done():
			return nil
		}
	}
}

func (h *SSEHandler) SendNotification(notification Notification) {
	h.clientsMux.Lock()
	defer h.clientsMux.Unlock()
	for clientChan := range h.clients {
		clientChan <- notification
	}
}

func (h *SSEHandler) addClient(clientChan chan Notification) {
	h.clientsMux.Lock()
	h.clients[clientChan] = true
	h.clientsMux.Unlock()
}

func (h *SSEHandler) removeClient(clientChan chan Notification) {
	h.clientsMux.Lock()
	delete(h.clients, clientChan)
	close(clientChan)
	h.clientsMux.Unlock()
}
