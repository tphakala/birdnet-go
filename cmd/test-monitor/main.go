// test-monitor is a simple program to test the system monitor functionality
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/monitor"
	"github.com/tphakala/birdnet-go/internal/notification"
)

func main() {
	// Initialize logging
	logging.Init()
	log.Println("Starting system monitor test...")

	// Load configuration
	settings := conf.Setting()
	if settings == nil {
		log.Fatal("Failed to load settings")
	}

	// Enable monitoring in settings
	settings.Realtime.Monitoring.Enabled = true
	settings.Realtime.Monitoring.CheckInterval = 10 // Check every 10 seconds
	settings.Realtime.Monitoring.Disk.Enabled = true
	settings.Realtime.Monitoring.Disk.Warning = 85.0
	settings.Realtime.Monitoring.Disk.Critical = 95.0
	settings.Realtime.Monitoring.Disk.Path = "/"

	log.Printf("Monitoring config: Enabled=%v, Interval=%ds, Disk Warning=%.1f%%, Critical=%.1f%%",
		settings.Realtime.Monitoring.Enabled,
		settings.Realtime.Monitoring.CheckInterval,
		settings.Realtime.Monitoring.Disk.Warning,
		settings.Realtime.Monitoring.Disk.Critical,
	)

	// Initialize event bus
	eventBusConfig := &events.Config{
		Enabled:    true,
		BufferSize: 1000,
		Workers:    2,
		Debug:      true,
	}
	
	eb, err := events.Initialize(eventBusConfig)
	if err != nil {
		log.Fatalf("Failed to initialize event bus: %v", err)
	}
	if eb == nil {
		log.Fatal("Event bus is nil")
	}
	log.Println("Event bus initialized")

	// Initialize notification system
	notification.Init(settings)
	log.Println("Notification system initialized")

	// Create and start system monitor
	systemMonitor := monitor.NewSystemMonitor(settings)
	systemMonitor.Start()
	log.Println("System monitor started")

	// Wait a moment for initialization
	time.Sleep(2 * time.Second)

	// Trigger an immediate check
	log.Println("Triggering manual resource check...")
	systemMonitor.TriggerCheck()

	// Wait for results
	time.Sleep(5 * time.Second)

	// Get resource status
	status := systemMonitor.GetResourceStatus()
	fmt.Println("\nResource Status:")
	for resource, info := range status {
		fmt.Printf("  %s: %v\n", resource, info)
	}

	// Keep running for a while to see periodic checks
	log.Println("\nMonitoring for 1 minute... (check logs for activity)")
	time.Sleep(60 * time.Second)

	// Shutdown
	log.Println("Shutting down...")
	systemMonitor.Stop()
	
	if err := eb.Shutdown(5 * time.Second); err != nil {
		log.Printf("Event bus shutdown error: %v", err)
	}
	
	log.Println("Test complete")
}