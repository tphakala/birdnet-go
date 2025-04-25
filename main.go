package main

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"runtime/pprof"
	"time"

	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/cmd"
	"github.com/tphakala/birdnet-go/internal/analysis"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

// buildTime is the time when the binary was built.
var buildDate string

// version holds the Git version tag
var version string

//go:embed assets/*
var assetsFs embed.FS

//go:embed views/*
var viewsFs embed.FS

//go:embed internal/imageprovider/data/*
var imageDataFs embed.FS // Embed image provider data

// ImageProviderRegistry is a global registry for image providers
var imageProviderRegistry *imageprovider.ImageProviderRegistry

func main() {
	exitCode := mainWithExitCode()
	os.Exit(exitCode)
}

func mainWithExitCode() int {
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
		defer f.Close()

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
	settings := conf.Setting()
	if settings == nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration\n")
		return 1
	}

	// Set runtime values
	settings.Version = version
	settings.BuildDate = buildDate

	fmt.Printf("🐦 \033[37mBirdNET-Go %s (built: %s), using config file: %s\033[0m\n",
		settings.Version, settings.BuildDate, viper.ConfigFileUsed())

	// Execute the root command
	rootCmd := cmd.RootCommand(settings)
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
