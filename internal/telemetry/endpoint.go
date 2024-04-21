// endpoint.go: Prohmetheus compatible telemetry endpoint
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

// Endpoint handles all operations related to Prometehus compatible telemetry
type Endpoint struct {
	server        *http.Server
	ListenAddress string
}

// New creates a new instance of telemetry Endpoint
func NewEndpoint(settings *conf.Settings) (*Endpoint, error) {
	if !settings.Realtime.Telemetry.Enabled {
		return nil, fmt.Errorf("metrics not enabled")
	}

	return &Endpoint{
		ListenAddress: settings.Realtime.Telemetry.Listen,
	}, nil
}

// Start the HTTP server for telemetry endpoint and listen for the quit signal to shut down.
func (e *Endpoint) Start(metrics *Metrics, wg *sync.WaitGroup, quitChan <-chan struct{}) {
	mux := http.NewServeMux()
	RegisterMetricsHandlers(mux) // Registering metrics handlers
	RegisterDebugHandlers(mux)   // Registering debug handlers

	e.server = &http.Server{
		Addr:    e.ListenAddress,
		Handler: mux,
	}

	// Run the server in a separate goroutine so that it doesn't block.
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("Telemetry endpoint starting at %s", e.ListenAddress)
		if err := e.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start telemetry HTTP server at %s: %v", e.ListenAddress, err)
		}
	}()

	// Listen for quit signal
	go func() {
		<-quitChan
		log.Println("Quit signal received, stopping telemetry server.")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.server.Shutdown(ctx); err != nil {
			log.Printf("Failed to shutdown telemetry server gracefully: %v", err)
		}
	}()
}
