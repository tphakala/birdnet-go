package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/cmd"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
)

// buildTime is the time when the binary was built.
var buildDate string

//go:embed assets/*
var assetsFs embed.FS

//go:embed views/*
var viewsFs embed.FS

func main() {
	// publish the embedded assets and views directories to controller package
	httpcontroller.AssetsFs = assetsFs
	httpcontroller.ViewsFs = viewsFs

	// Load the configuration
	settings, err := conf.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	} else {
		fmt.Printf("BirdNET-Go build date: %s, using config file: %s\n", buildDate, viper.ConfigFileUsed())
	}

	// Execute the root command
	rootCmd := cmd.RootCommand(settings)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Command execution error: %v\n", err)
		os.Exit(1)
	}
}
