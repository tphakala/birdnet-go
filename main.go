package main

import (
	"embed"
	"errors"
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
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	runtimectx "github.com/tphakala/birdnet-go/internal/runtime"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// buildTime is the time when the binary was built.
var buildDate string

// version holds the Git version tag
var version string

//go:embed assets/*
var assetsFs embed.FS

//go:embed views/*
var viewsFs embed.FS

//go:embed internal/imageprovider/data/latest.json
var imageDataFs embed.FS // Embed image provider data

// ImageProviderRegistry is a global registry for image providers
var imageProviderRegistry *imageprovider.ImageProviderRegistry

func main() {
	exitCode := mainWithExitCode()
	os.Exit(exitCode)
}

func mainWithExitCode() int {
	// Ensure all systems are properly shut down on exit
	defer func() {
		if err := telemetry.ShutdownSystem(5 * time.Second); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: system shutdown incomplete: %v\n", err)
		}
	}()

	// Check if profiling is enabled
	if os.Getenv("BIRDNET_GO_PROFILE") == "1" {
		fmt.Println("Profiling enabled")
		// Create a unique profile name with timestamp
		now := time.Now()
		profilePath := fmt.Sprintf("profile_%s.pprof", now.Format("20060102_150405"))

		f, err := os.Create(profilePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating profile file: %v\n", err)
			return 1
		}
		defer func() {
			if err := f.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "Error closing profile file: %v\n", err)
			}
		}()

		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "Error starting CPU profile: %v\n", err)
			return 1
		}
		defer pprof.StopCPUProfile()
	}

	// publish the embedded assets and views directories to controller package
	httpcontroller.AssetsFs = assetsFs
	httpcontroller.ViewsFs = viewsFs
	httpcontroller.ImageDataFs = imageDataFs

	// Initialize the image provider registry
	imageProviderRegistry = imageprovider.NewImageProviderRegistry()

	// Make registry available to the httpcontroller package
	httpcontroller.ImageProviderRegistry = imageProviderRegistry

	// Load the configuration
	config, validationResult, err := conf.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		return 1
	}

	// Handle validation warnings
	if validationResult != nil && validationResult.HasIssues() {
		for _, warning := range validationResult.Warnings {
			fmt.Printf("⚠️  Config warning: %s\n", warning)
		}
	}

	// Load or create system ID for telemetry
	systemID, err := telemetry.LoadOrCreateSystemID(filepath.Dir(viper.ConfigFileUsed()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to load system ID: %v\n", err)
		// Generate a temporary one for this session
		systemID, _ = telemetry.GenerateSystemID()
	}

	// Create runtime context with separated concerns
	runtimeContext := &runtimectx.Context{
		Version:   version,
		BuildDate: buildDate,
		SystemID:  systemID,
	}

	fmt.Printf("🐦 \033[37mBirdNET-Go %s (built: %s), using config file: %s\033[0m\n",
		runtimeContext.Version, runtimeContext.BuildDate, viper.ConfigFileUsed())

	// Initialize core systems (telemetry and notification)
	// TODO: telemetry.InitializeSystem should accept runtime context in future refactoring
	if err := telemetry.InitializeSystem(config); err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing core systems: %v\n", err)
		// Continue - these are not critical for basic operation
	}

	// Wait for core systems to be ready (with timeout)
	if err := telemetry.WaitForReady(5 * time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: core systems initialization incomplete: %v\n", err)
		// Continue - not critical for operation
	}

	// Enable runtime profiling if debug mode is enabled
	if config.Debug {
		// Enable mutex profiling for detecting lock contention
		runtime.SetMutexProfileFraction(1)

		// Enable block profiling for detecting blocking operations
		runtime.SetBlockProfileRate(1)

		fmt.Println("🐛 Runtime profiling enabled (mutex and block profiling active)")
	}

	// Process configuration validation warnings that occurred before Sentry initialization
	if validationResult != nil && len(validationResult.Warnings) > 0 {
		for _, warning := range validationResult.Warnings {
			parts := strings.SplitN(warning, ": ", 2)
			if len(parts) == 2 {
				component := parts[0]
				message := parts[1]
				telemetry.CaptureMessage(message, sentry.LevelWarning, component)
			}
		}
	}

	// Execute the root command
	rootCmd := cmd.RootCommand(config, runtimeContext)
	if err := rootCmd.Execute(); err != nil {
		if errors.Is(err, analysis.ErrAnalysisCanceled) {
			// Clean exit for user-initiated cancellation
			return 0
		}
		fmt.Fprintf(os.Stderr, "Command execution error: %v\n", err)
		return 1
	}

	return 0
}
