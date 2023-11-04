package main

import (
	"fmt"
	"os"

	"github.com/tphakala/go-birdnet/cmd"
	"github.com/tphakala/go-birdnet/pkg/config"
)

func main() {
	if err := config.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	rootCmd := cmd.RootCommand()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Command execution error: %v\n", err)
		os.Exit(1)
	}
}
