package support

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/support"
)

// CollectCommand creates the support data collection subcommand
func CollectCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "collect",
		Short: "Collect system diagnostics for troubleshooting",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Collecting support data...")
			
			// Get config directory
			configPaths, err := conf.GetDefaultConfigPaths()
			if err != nil || len(configPaths) == 0 {
				configPaths = []string{"."}
			}
			
			// Get current settings for system ID
			settings := conf.GetSettings()
			systemID := "unknown"
			version := "unknown"
			if settings != nil {
				systemID = settings.SystemID
				version = settings.Version
			}
			
			// Create collector
			collector := support.NewCollector(
				configPaths[0], // Config directory
				".",            // Data directory
				systemID,
				version,
			)
			
			// Set collection options
			opts := support.CollectorOptions{
				IncludeLogs:       true,
				IncludeConfig:     true,
				IncludeSystemInfo: true,
				LogDuration:       7 * 24 * time.Hour, // 1 week
				MaxLogSize:        50 * 1024 * 1024,   // 50MB
				ScrubSensitive:    true,
			}
			
			// Collect data
			ctx := context.Background()
			dump, err := collector.Collect(ctx, opts)
			if err != nil {
				fmt.Printf("Error collecting support data: %v\n", err)
				os.Exit(1)
			}
			
			// Create archive
			archiveData, err := collector.CreateArchive(ctx, dump, opts)
			if err != nil {
				fmt.Printf("Error creating archive: %v\n", err)
				os.Exit(1)
			}
			
			// Save to file
			filename := fmt.Sprintf("birdnet-go-support-%s.zip", dump.ID)
			if err := os.WriteFile(filename, archiveData, 0o600); err != nil {
				fmt.Printf("Error saving archive: %v\n", err)
				os.Exit(1)
			}
			
			fmt.Printf("Support data collected and saved to: %s\n", filename)
		},
	}
}
