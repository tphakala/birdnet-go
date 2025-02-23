package analysis

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// ControlMonitor handles control signals for realtime analysis mode
type ControlMonitor struct {
	wg               *sync.WaitGroup
	controlChan      chan string
	quitChan         chan struct{}
	restartChan      chan struct{}
	notificationChan chan handlers.Notification
	bufferManager    *BufferManager
	proc             *processor.Processor
	audioLevelChan   chan myaudio.AudioLevelData
	bn               *birdnet.BirdNET
}

// NewControlMonitor creates a new ControlMonitor instance
func NewControlMonitor(wg *sync.WaitGroup, controlChan chan string, quitChan, restartChan chan struct{}, notificationChan chan handlers.Notification, bufferManager *BufferManager, proc *processor.Processor) *ControlMonitor {
	return &ControlMonitor{
		wg:               wg,
		controlChan:      controlChan,
		quitChan:         quitChan,
		restartChan:      restartChan,
		notificationChan: notificationChan,
		bufferManager:    bufferManager,
		proc:             proc,
		audioLevelChan:   make(chan myaudio.AudioLevelData),
		bn:               proc.Bn,
	}
}

// Start begins monitoring control signals
func (cm *ControlMonitor) Start() {
	go cm.monitor()
}

// monitor listens for control signals and handles them
func (cm *ControlMonitor) monitor() {
	for {
		select {
		case signal := <-cm.controlChan:
			cm.handleControlSignal(signal)
		case <-cm.quitChan:
			return
		}
	}
}

// handleControlSignal processes different control signals
func (cm *ControlMonitor) handleControlSignal(signal string) {
	switch signal {
	case "rebuild_range_filter":
		cm.handleRebuildRangeFilter()
	case "reload_birdnet":
		cm.handleReloadBirdnet()
	case "reconfigure_mqtt":
		cm.handleReconfigureMQTT()
	case "reconfigure_rtsp_sources":
		cm.handleReconfigureRTSP()
	default:
		log.Printf("Received unknown control signal: %v", signal)
	}
}

// handleRebuildRangeFilter rebuilds the range filter
func (cm *ControlMonitor) handleRebuildRangeFilter() {
	if err := birdnet.BuildRangeFilter(cm.bn); err != nil {
		log.Printf("\033[31m❌ Error handling range filter rebuild: %v\033[0m", err)
		cm.notifyError("Failed to rebuild range filter", err)
	} else {
		log.Printf("\033[32m🔄 Range filter rebuilt successfully\033[0m")
		cm.notifySuccess("Range filter rebuilt successfully")
	}
}

// handleReloadBirdnet reloads the BirdNET model
func (cm *ControlMonitor) handleReloadBirdnet() {
	if err := cm.bn.ReloadModel(); err != nil {
		log.Printf("\033[31m❌ Error reloading BirdNET model: %v\033[0m", err)
		cm.notifyError("Failed to reload BirdNET model", err)
		return
	}

	log.Printf("\033[32m✅ BirdNET model reloaded successfully\033[0m")
	cm.notifySuccess("BirdNET model reloaded successfully")

	// Rebuild range filter after model reload
	if err := birdnet.BuildRangeFilter(cm.bn); err != nil {
		log.Printf("\033[31m❌ Error rebuilding range filter after model reload: %v\033[0m", err)
		cm.notifyError("Failed to rebuild range filter", err)
	} else {
		log.Printf("\033[32m✅ Range filter rebuilt successfully\033[0m")
		cm.notifySuccess("Range filter rebuilt successfully")
	}
}

// handleReconfigureMQTT reconfigures the MQTT connection
func (cm *ControlMonitor) handleReconfigureMQTT() {
	log.Printf("\033[32m🔄 Reconfiguring MQTT connection...\033[0m")
	settings := conf.Setting()

	if cm.proc == nil {
		log.Printf("\033[31m❌ Error: Processor not available\033[0m")
		cm.notifyError("Failed to reconfigure MQTT", fmt.Errorf("processor not available"))
		return
	}

	// If MQTT is enabled, initialize and connect
	if settings.Realtime.MQTT.Enabled {
		var err error
		cm.proc.MqttClient, err = mqtt.NewClient(settings, cm.proc.Metrics)
		if err != nil {
			log.Printf("\033[31m❌ Error creating MQTT client: %v\033[0m", err)
			cm.notifyError("Failed to create MQTT client", err)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := cm.proc.MqttClient.Connect(ctx); err != nil {
			cancel()
			log.Printf("\033[31m❌ Error connecting to MQTT broker: %v\033[0m", err)
			cm.notifyError("Failed to connect to MQTT broker", err)
			return
		}
		cancel()

		log.Printf("\033[32m✅ MQTT connection configured successfully\033[0m")
		cm.notifySuccess("MQTT connection configured successfully")
	} else if cm.proc.MqttClient != nil {
		// If MQTT is disabled, disconnect if client exists
		cm.proc.MqttClient.Disconnect()
		cm.proc.MqttClient = nil
		log.Printf("\033[32m✅ MQTT connection disabled\033[0m")
		cm.notifySuccess("MQTT connection disabled")
	}
}

// handleReconfigureRTSP reconfigures RTSP sources
func (cm *ControlMonitor) handleReconfigureRTSP() {
	log.Printf("\033[32m🔄 Reconfiguring RTSP sources...\033[0m")
	settings := conf.Setting()

	// Prepare the list of active sources
	var sources []string
	if len(settings.Realtime.RTSP.URLs) > 0 {
		sources = append(sources, settings.Realtime.RTSP.URLs...)
	}
	if settings.Realtime.Audio.Source != "" {
		sources = append(sources, "malgo")
	}

	// Update the analysis buffer monitors
	cm.bufferManager.UpdateMonitors(sources)

	// Reconfigure RTSP streams
	myaudio.ReconfigureRTSPStreams(settings, cm.wg, cm.quitChan, cm.restartChan, cm.audioLevelChan)

	log.Printf("\033[32m✅ RTSP sources reconfigured successfully\033[0m")
	cm.notifySuccess("Audio capture reconfigured successfully")
}

// notifySuccess sends a success notification
func (cm *ControlMonitor) notifySuccess(message string) {
	cm.notificationChan <- handlers.Notification{
		Message: message,
		Type:    "success",
	}
}

// notifyError sends an error notification
func (cm *ControlMonitor) notifyError(message string, err error) {
	cm.notificationChan <- handlers.Notification{
		Message: fmt.Sprintf("%s: %v", message, err),
		Type:    "error",
	}
}
