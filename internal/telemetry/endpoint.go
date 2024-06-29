// Package telemetry provides tools for monitoring and debugging the BirdNET-Go application.
package telemetry

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Endpoint handles all operations related to Prometheus-compatible telemetry.
type Endpoint struct {
	server        *http.Server
	listenAddress string
	metrics       *Metrics
}

// NewEndpoint creates a new instance of telemetry Endpoint.
//
// It initializes the Endpoint with the provided settings and metrics.
// If telemetry is not enabled in the settings, it returns an error.
//
// Parameters:
//   - settings: A pointer to the application settings.
//   - metrics: A pointer to the Metrics instance containing all telemetry metrics.
//
// Returns:
//   - A pointer to the new Endpoint instance and nil error on success.
//   - nil and an error if telemetry is not enabled in the settings.
//
// The function does not create new metrics but uses the provided Metrics instance.
// Ensure that the Metrics instance is properly initialized before calling this function.
func NewEndpoint(settings *conf.Settings, metrics *Metrics) (*Endpoint, error) {
	if !settings.Realtime.Telemetry.Enabled {
		return nil, fmt.Errorf("telemetry not enabled in settings")
	}

	return &Endpoint{
		listenAddress: settings.Realtime.Telemetry.Listen,
		metrics:       metrics,
	}, nil
}

// Start initializes and runs the HTTP server for the telemetry endpoint.
//
// It sets up the necessary routes, starts the server in a separate goroutine,
// and listens for a quit signal to shut down gracefully.
//
// Parameters:
//   - wg: A pointer to a WaitGroup for coordinating goroutine completion.
//   - quitChan: A channel for receiving the quit signal.
func (e *Endpoint) Start(wg *sync.WaitGroup, quitChan <-chan struct{}) {
	mux := http.NewServeMux()
	e.metrics.RegisterHandlers(mux)
	RegisterDebugHandlers(mux)

	e.server = &http.Server{
		Addr:    e.listenAddress,
		Handler: mux,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("Telemetry endpoint starting at %s", e.listenAddress)
		if err := e.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Telemetry HTTP server error: %v", err)
		}
	}()

	go e.gracefulShutdown(quitChan)
}

// gracefulShutdown waits for the quit signal and shuts down the server gracefully.
func (e *Endpoint) gracefulShutdown(quitChan <-chan struct{}) {
	<-quitChan
	log.Println("Stopping telemetry server...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.server.Shutdown(ctx); err != nil {
		log.Printf("Telemetry server shutdown error: %v", err)
	}
}

// GetMetrics returns the Metrics instance associated with this Endpoint.
//
// Returns:
//   - A pointer to the Metrics instance.
func (e *Endpoint) GetMetrics() *Metrics {
	return e.metrics
}
