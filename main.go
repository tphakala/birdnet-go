package main

import (
	"embed"
	"fmt"
	"os"
	"runtime/pprof"
	"time"

	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/cmd"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
)

// buildTime is the time when the binary was built.
var buildDate string

// version holds the Git version tag
var version string

//go:embed assets/*
var assetsFs embed.FS

//go:embed views/*
var viewsFs embed.FS

func main() {
	// Check if profiling is enabled
	if os.Getenv("BIRDNET_GO_PROFILE") == "1" {
		fmt.Println("Profiling enabled")
		// Create a unique profile name with timestamp
		now := time.Now()
		profilePath := fmt.Sprintf("profile_%s.pprof", now.Format("20060102_150405"))

		f, err := os.Create(profilePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating profile file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()

		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "Error starting CPU profile: %v\n", err)
			os.Exit(1)
		}
		defer pprof.StopCPUProfile()
	}

	// publish the embedded assets and views directories to controller package
	httpcontroller.AssetsFs = assetsFs
	httpcontroller.ViewsFs = viewsFs

	// Load the configuration
	settings := conf.Setting()
	if settings == nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration\n")
		os.Exit(1)
	}

	// Set runtime values
	settings.Version = version
	settings.BuildDate = buildDate

	fmt.Printf("🐦 \033[37mBirdNET-Go v%s (built: %s), using config file: %s\033[0m\n",
		settings.Version, settings.BuildDate, viper.ConfigFileUsed())

	// Execute the root command
	rootCmd := cmd.RootCommand(settings)
	if err := rootCmd.Execute(); err != nil {
		if err.Error() == "error processing audio: context canceled" {
			// Clean exit for user-initiated cancellation
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Command execution error: %v\n", err)
		os.Exit(1)
	}
}
