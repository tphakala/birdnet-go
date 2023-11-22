package main

import (
	"fmt"
	"os"

	"github.com/tphakala/birdnet-go/cmd"
	"github.com/tphakala/birdnet-go/internal/config"
)

func main() {
	ctx, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	rootCmd := cmd.RootCommand(ctx)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Command execution error: %v\n", err)
		os.Exit(1)
	}
}
