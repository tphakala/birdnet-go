package main

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/cmd"
	"github.com/tphakala/birdnet-go/internal/analysis"
	"github.com/tphakala/birdnet-go/internal/api"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// buildTime is the time when the binary was built.
var buildDate string

// version holds the Git version tag
var version string

//go:embed internal/imageprovider/data/latest.json
var imageDataFs embed.FS // Embed image provider data

// ImageProviderRegistry is a global registry for image providers
var imageProviderRegistry *imageprovider.ImageProviderRegistry

func main() {
	exitCode := mainWithExitCode()
	os.Exit(exitCode)
}

func mainWithExitCode() int {
	// Create bootstrap logger for pre-initialization messages
	bootLog := logger.NewConsoleLogger("main", logger.LogLevelInfo)

	// Ensure all systems are properly shut down on exit
	defer func() {
		if err := telemetry.ShutdownSystem(5 * time.Second); err != nil {
			// Use fmt here as logger may be closed
			fmt.Fprintf(os.Stderr, "WARN  [main] System shutdown incomplete error=%v\n", err)
		}
	}()

	// Check if profiling is enabled
	if os.Getenv("BIRDNET_GO_PROFILE") == "1" {
		bootLog.Info("CPU profiling enabled")
		// Create a unique profile name with timestamp
		now := time.Now()
		profilePath := fmt.Sprintf("profile_%s.pprof", now.Format("20060102_150405"))

		f, err := os.Create(profilePath) //nolint:gosec // G304: profilePath is programmatically constructed with timestamp
		if err != nil {
			bootLog.Error("Failed to create profile file", logger.Error(err))
			return 1
		}
		defer func() {
			if err := f.Close(); err != nil {
				// Use fmt here as logger may be closed
				fmt.Fprintf(os.Stderr, "WARN  [main] Failed to close profile file error=%v\n", err)
			}
		}()

		if err := pprof.StartCPUProfile(f); err != nil {
			bootLog.Error("Failed to start CPU profile", logger.Error(err))
			return 1
		}
		defer pprof.StopCPUProfile()
	}

	// Publish the embedded image data to the API server
	api.ImageDataFs = imageDataFs

	// Initialize the image provider registry
	imageProviderRegistry = imageprovider.NewImageProviderRegistry()
	api.ImageProviderRegistry = imageProviderRegistry

	// Load the configuration
	settings := conf.Setting()
	if settings == nil {
		bootLog.Error("Failed to load configuration")
		return 1
	}

	// Set runtime values
	settings.Version = version
	settings.BuildDate = buildDate

	// Initialize the centralized logger
	centralLogger, err := logger.NewCentralLogger(&settings.Logging)
	if err != nil {
		bootLog.Error("Failed to initialize logger", logger.Error(err))
		return 1
	}
	logger.SetGlobal(centralLogger)
	defer func() {
		// Log application shutdown
		mainLog := centralLogger.Module("main")
		mainLog.Info("Application stopped")
		// Flush buffered logs before closing
		if err := centralLogger.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "WARN  [main] Failed to flush logger error=%v\n", err)
		}
		if err := centralLogger.Close(); err != nil {
			// Use fmt here as logger is being closed
			fmt.Fprintf(os.Stderr, "WARN  [main] Failed to close logger error=%v\n", err)
		}
	}()

	// Create main module logger
	mainLog := centralLogger.Module("main")

	// Load or create system ID for telemetry
	systemID, err := telemetry.LoadOrCreateSystemID(filepath.Dir(viper.ConfigFileUsed()))
	if err != nil {
		mainLog.Warn("Failed to load system ID, using temporary ID",
			logger.Error(err))
		// Generate a temporary one for this session
		systemID, _ = telemetry.GenerateSystemID()
	}
	settings.SystemID = systemID

	mainLog.Info("BirdNET-Go starting",
		logger.String("version", settings.Version),
		logger.String("build_date", settings.BuildDate),
		logger.String("config_file", viper.ConfigFileUsed()))

	// Initialize core systems (telemetry and notification)
	// Note: Telemetry is opt-in; errors here are expected if not enabled
	if err := telemetry.InitializeSystem(settings); err != nil {
		mainLog.Debug("Core systems not initialized",
			logger.Error(err))
		// Continue - these are not critical for basic operation
	}

	// Wait for core systems to be ready (with timeout)
	// Note: This is expected when telemetry is opt-in and not enabled
	if err := telemetry.WaitForReady(5 * time.Second); err != nil {
		mainLog.Debug("Core systems initialization incomplete",
			logger.Error(err))
		// Continue - not critical for operation
	}

	// Enable runtime profiling if debug mode is enabled
	if settings.Debug {
		// Enable mutex profiling for detecting lock contention
		runtime.SetMutexProfileFraction(1)

		// Enable block profiling for detecting blocking operations
		runtime.SetBlockProfileRate(1)

		mainLog.Debug("Runtime profiling enabled (mutex and block profiling active)")
	}

	// Process configuration validation warnings that occurred before Sentry initialization
	if len(settings.ValidationWarnings) > 0 {
		for _, warning := range settings.ValidationWarnings {
			parts := strings.SplitN(warning, ": ", 2)
			if len(parts) == 2 {
				component := parts[0]
				message := parts[1]
				telemetry.CaptureMessage(message, sentry.LevelWarning, component)
			}
		}
		// Clear the warnings as they've been processed
		settings.ValidationWarnings = nil
	}

	// Execute the root command
	rootCmd := cmd.RootCommand(settings)
	if err := rootCmd.Execute(); err != nil {
		if errors.Is(err, analysis.ErrAnalysisCanceled) {
			// Clean exit for user-initiated cancellation
			return 0
		}
		mainLog.Error("Command execution failed", logger.Error(err))
		return 1
	}

	return 0
}
