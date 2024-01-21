package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/tphakala/birdnet-go/cmd"
	"github.com/tphakala/birdnet-go/internal/config"
	"github.com/tphakala/birdnet-go/internal/controller"
)

//go:embed assets/*
var assetsFs embed.FS

//go:embed views/*
var viewsFs embed.FS

func main() {
	// publish the embedded assets and views directories to controller package
	controller.AssetsFs = assetsFs
	controller.ViewsFs = viewsFs

	// Load the configuration
	ctx, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	// Execute the root command
	rootCmd := cmd.RootCommand(ctx)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Command execution error: %v\n", err)
		os.Exit(1)
	}
}
