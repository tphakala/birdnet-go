// Package backup provides the backup command for BirdNET-Go
package backup

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/spf13/cobra"
	"github.com/tphakala/birdnet-go/internal/backup"
	"github.com/tphakala/birdnet-go/internal/backup/sources"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Command creates and returns the backup command
func Command(settings *conf.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Perform an immediate backup of the database and configuration",
		Long:  `Backup command uses the configured backup settings to create an immediate backup of the SQLite database and configuration files.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBackup(settings)
		},
	}

	return cmd
}

func runBackup(settings *conf.Settings) error {
	if !settings.Backup.Enabled {
		return fmt.Errorf("backup functionality is not enabled in configuration")
	}

	// Create a backup manager
	manager := backup.NewManager(&settings.Backup, log.Default())

	// Register SQLite source
	sqliteSource := sources.NewSQLiteSource(settings)
	if err := manager.RegisterSource(sqliteSource); err != nil {
		return fmt.Errorf("failed to register SQLite source: %w", err)
	}

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Run the backup
	if err := manager.RunBackup(ctx); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	fmt.Println("Backup completed successfully")
	return nil
}
