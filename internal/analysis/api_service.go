package analysis

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api"
	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/monitor"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/suncalc"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// apiServerServiceName is the service name used for logging and diagnostics.
const apiServerServiceName = "api-server"

// APIServerService manages the HTTP API server, processor, and related subsystems
// as an app.Service. It owns the lifecycle of the API server, processor, bird image
// cache, SunCalc, OAuth2 server, system monitor, and the control/audio-level channels.
type APIServerService struct {
	settings   *conf.Settings
	bnAnalyzer *BirdNETAnalyzer
	dbService  *DatabaseService
	metrics    *observability.Metrics

	server         *api.Server
	proc           *processor.Processor
	birdImageCache *imageprovider.BirdImageCache
	sunCalc        *suncalc.SunCalc
	oauth2Server   *security.OAuth2Server
	systemMonitor  *monitor.SystemMonitor
	controlChan    chan string
	audioLevelChan chan myaudio.AudioLevelData
}

// NewAPIServerService creates a new APIServerService with the given dependencies.
// The service is not started; call Start() to initialize all subsystems.
func NewAPIServerService(settings *conf.Settings, bnAnalyzer *BirdNETAnalyzer, dbService *DatabaseService, metrics *observability.Metrics) *APIServerService {
	return &APIServerService{
		settings:   settings,
		bnAnalyzer: bnAnalyzer,
		dbService:  dbService,
		metrics:    metrics,
	}
}

// Name returns a human-readable identifier for logging and diagnostics.
func (s *APIServerService) Name() string {
	return apiServerServiceName
}

// Start initializes and starts the API server and all dependent subsystems.
// It fails fast if required dependencies (DataStore, BirdNET) are not available.
//
//nolint:gocognit // Orchestration function that initializes multiple subsystems in sequence.
func (s *APIServerService) Start(_ context.Context) error {
	// If Start fails after creating resources, clean up to prevent leaks.
	// The App framework only calls Stop() on services that started successfully,
	// so the failing service must clean up after itself.
	startSucceeded := false
	defer func() {
		if !startSucceeded {
			// Best-effort cleanup of partially initialized resources.
			if s.systemMonitor != nil {
				s.systemMonitor.Stop()
				s.systemMonitor = nil
			}
			if s.server != nil {
				_ = s.server.ShutdownWithContext(context.Background())
				s.server = nil
			}
			if s.proc != nil {
				_ = s.proc.ShutdownWithContext(context.Background())
				s.proc = nil
			}
		}
	}()

	// Fail fast: verify dependencies are initialized by upstream services.
	if s.dbService == nil || s.dbService.DataStore() == nil {
		return errors.Newf("api-server requires an initialized datastore; database service must be started first").
			Component("analysis.api_service").
			Category(errors.CategorySystem).
			Context("operation", "start_precondition_check").
			Build()
	}
	if s.bnAnalyzer == nil || s.bnAnalyzer.BirdNET() == nil {
		return errors.Newf("api-server requires an initialized birdnet model; birdnet-analyzer service must be started first").
			Component("analysis.api_service").
			Category(errors.CategorySystem).
			Context("operation", "start_precondition_check").
			Build()
	}

	dataStore := s.dbService.DataStore()
	bn := s.bnAnalyzer.BirdNET()

	// Update BirdNET model loaded metric.
	UpdateBirdNETModelLoadedMetric(s.metrics.BirdNET, bn)

	// Initialize bird image cache.
	s.birdImageCache = initBirdImageCache(s.settings, dataStore, s.metrics)

	// Create SunCalc for sunrise/sunset calculations.
	s.sunCalc = suncalc.NewSunCalc(s.settings.BirdNET.Latitude, s.settings.BirdNET.Longitude)

	// Create processor.
	s.proc = processor.New(s.settings, dataStore, bn, s.metrics, s.birdImageCache, GetLogger())
	s.proc.SetSunCalc(s.sunCalc)

	// Initialize backup system (optional — failure is non-fatal).
	backupLog := logger.Global().Module("backup")
	backupManager, backupScheduler, err := initializeBackupSystem(s.settings, backupLog)
	if err != nil {
		backupLog.Error("Failed to initialize backup system", logger.Error(err))
		GetLogger().Warn("backup system initialization failed",
			logger.Error(err),
			logger.String("operation", "backup_system_init"))
	} else {
		s.proc.SetBackupManager(backupManager)
		s.proc.SetBackupScheduler(backupScheduler)
	}

	// Initialize async services (event bus, notification workers, telemetry workers).
	// This is fatal — the system cannot operate without these.
	if err := telemetry.InitializeAsyncSystems(); err != nil {
		GetLogger().Error("failed to initialize critical async services",
			logger.Error(err),
			logger.String("operation", "initialize_async_systems"))
		return errors.New(err).
			Component("analysis.api_service").
			Category(errors.CategorySystem).
			Context("operation", "initialize_async_systems").
			Build()
	}

	// Initialize system monitor (optional — failure is non-fatal).
	s.systemMonitor = initializeSystemMonitor(s.settings)

	// Create channels.
	s.controlChan = make(chan string, 1)
	s.audioLevelChan = make(chan myaudio.AudioLevelData, 100)

	// Create OAuth2 server.
	s.oauth2Server = security.NewOAuth2Server()

	// Create and start the HTTP API server.
	GetLogger().Info("starting HTTP server")
	apiServer, err := api.New(
		s.settings,
		api.WithDataStore(dataStore),
		api.WithBirdImageCache(s.birdImageCache),
		api.WithProcessor(s.proc),
		api.WithMetrics(s.metrics),
		api.WithControlChannel(s.controlChan),
		api.WithAudioLevelChannel(s.audioLevelChan),
		api.WithOAuth2Server(s.oauth2Server),
		api.WithSunCalc(s.sunCalc),
		api.WithV2Manager(s.dbService.V2Manager()),
	)
	if err != nil {
		return errors.New(err).
			Component("analysis.api_service").
			Category(errors.CategorySystem).
			Context("operation", "create_http_server").
			Build()
	}
	s.server = apiServer
	s.server.Start()

	// Wire shutdown requester into API controller for restart endpoints.
	if appInstance := app.GetGlobal(); appInstance != nil {
		s.server.APIController().SetShutdownRequester(appInstance)
	}

	startSucceeded = true
	return nil
}

// Stop gracefully shuts down the API server and owned subsystems.
// It is safe to call before Start() or multiple times.
func (s *APIServerService) Stop(ctx context.Context) error {
	log := GetLogger()

	// Stop system monitor.
	if s.systemMonitor != nil {
		log.Info("stopping system monitor",
			logger.String("operation", "shutdown_system_monitor"))
		s.systemMonitor.Stop()
		s.systemMonitor = nil
	}

	// Shutdown HTTP server (drain in-flight requests before stopping processors).
	if s.server != nil {
		log.Info("shutting down HTTP server",
			logger.String("operation", "shutdown_http_server"))
		if err := s.server.ShutdownWithContext(ctx); err != nil {
			log.Warn("error shutting down HTTP server",
				logger.Error(err),
				logger.String("operation", "shutdown_http_server"))
		}
		s.server = nil
	}

	// Close control channel to signal goroutines selecting on it.
	if s.controlChan != nil {
		log.Info("closing control channel",
			logger.String("operation", "close_control_channel"))
		close(s.controlChan)
		s.controlChan = nil
	}

	// Shutdown processor (MQTT, job queue, thresholds).
	if s.proc != nil {
		log.Info("shutting down processor",
			logger.String("operation", "shutdown_processor"))
		if err := s.proc.ShutdownWithContext(ctx); err != nil {
			log.Warn("processor shutdown error",
				logger.Error(err),
				logger.String("operation", "shutdown_processor"))
		}
		s.proc = nil
	}

	// Stop notification service (after processor, which may send final notifications).
	if notification.IsInitialized() {
		log.Info("stopping notification service",
			logger.String("operation", "shutdown_notification_service"))
		if service := notification.GetService(); service != nil {
			service.Stop()
		}
	}

	// Close audio level channel for clean SSE client shutdown.
	if s.audioLevelChan != nil {
		close(s.audioLevelChan)
		s.audioLevelChan = nil
	}

	return nil
}

// Processor returns the detection processor, or nil if not yet started.
func (s *APIServerService) Processor() *processor.Processor {
	return s.proc
}

// Metrics returns the metrics instance, or nil if not configured.
func (s *APIServerService) Metrics() *observability.Metrics {
	return s.metrics
}

// APIController returns the v2 API controller, or nil if not yet started.
func (s *APIServerService) APIController() *apiv2.Controller {
	if s.server == nil {
		return nil
	}
	return s.server.APIController()
}

// ControlChan returns the control channel for restart/reload signaling,
// or nil if not yet started.
func (s *APIServerService) ControlChan() chan string {
	return s.controlChan
}

// AudioLevelChan returns the channel for audio level data updates,
// or nil if not yet started.
func (s *APIServerService) AudioLevelChan() chan myaudio.AudioLevelData {
	return s.audioLevelChan
}

// SunCalc returns the sunrise/sunset calculator, or nil if not yet started.
func (s *APIServerService) SunCalc() *suncalc.SunCalc {
	return s.sunCalc
}
