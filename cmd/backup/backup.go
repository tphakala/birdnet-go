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
	"github.com/tphakala/birdnet-go/internal/backup/targets"
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

	// Add genkey subcommand
	genKeyCmd := &cobra.Command{
		Use:   "genkey",
		Short: "Generate a new encryption key for backups",
		Long:  `Generate a new encryption key for securing backups. The key will be saved to the default configuration directory.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			manager := backup.NewManager(&settings.Backup, log.Default())
			key, err := manager.GenerateEncryptionKey()
			if err != nil {
				return fmt.Errorf("failed to generate encryption key: %w", err)
			}
			log.Printf("Successfully generated new encryption key: %s", key)
			return nil
		},
	}

	cmd.AddCommand(genKeyCmd)
	return cmd
}

func runBackup(settings *conf.Settings) error {
	if !settings.Backup.Enabled {
		return fmt.Errorf("backup functionality is not enabled in configuration")
	}

	// Create a backup manager
	manager := backup.NewManager(&settings.Backup, log.Default())

	// Determine which database is in use and register appropriate source
	switch {
	case settings.Output.SQLite.Enabled:
		log.Println("Initializing SQLite backup source...")
		sqliteSource := sources.NewSQLiteSource(settings)

		// Validate the source configuration first
		if err := sqliteSource.Validate(); err != nil {
			return fmt.Errorf("invalid SQLite configuration: %w", err)
		}

		if err := manager.RegisterSource(sqliteSource); err != nil {
			return fmt.Errorf("failed to register SQLite source: %w", err)
		}
		log.Println("SQLite backup source initialized successfully")
	case settings.Output.MySQL.Enabled:
		log.Println("Warning: MySQL backups are not currently supported. Please use your database's native backup tools.")
		return nil
	default:
		log.Println("Warning: No supported database configuration found. Please check your configuration.")
		return nil
	}

	// Register configured backup targets
	log.Println("Initializing backup targets...")
	var registeredTargets int
	for _, targetConfig := range settings.Backup.Targets {
		if !targetConfig.Enabled {
			continue
		}

		var target backup.Target
		var err error

		switch targetConfig.Type {
		case "local":
			target, err = targets.NewLocalTarget(targets.LocalTargetConfig{
				Path:  targetConfig.Settings["path"].(string),
				Debug: settings.Backup.Debug,
			}, log.Default())
		case "ftp":
			targetConfig.Settings["logger"] = log.Default()
			target, err = targets.NewFTPTargetFromMap(targetConfig.Settings)
		case "sftp":
			targetConfig.Settings["logger"] = log.Default()
			target, err = targets.NewSFTPTarget(targetConfig.Settings)
		case "rsync":
			targetConfig.Settings["logger"] = log.Default()
			target, err = targets.NewRsyncTarget(targetConfig.Settings)
		case "gdrive":
			targetConfig.Settings["logger"] = log.Default()
			target, err = targets.NewGDriveTargetFromMap(targetConfig.Settings)
		default:
			log.Printf("Warning: Unsupported backup target type: %s", targetConfig.Type)
			continue
		}

		if err != nil {
			log.Printf("Warning: Failed to initialize %s backup target: %v", targetConfig.Type, err)
			continue
		}

		if err := manager.RegisterTarget(target); err != nil {
			log.Printf("Warning: Failed to register %s backup target: %v", targetConfig.Type, err)
			continue
		}

		registeredTargets++
		log.Printf("Successfully registered %s backup target", targetConfig.Type)
	}

	// Exit early if no targets were successfully registered
	if registeredTargets == 0 {
		return fmt.Errorf("no valid backup targets registered, backup cannot proceed")
	}

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	log.Println("Starting backup process...")

	// Run the backup
	if err := manager.RunBackup(ctx); err != nil {
		log.Printf("Backup failed: %v", err)
		return fmt.Errorf("backup failed: %w", err)
	}

	log.Println("Backup completed successfully")
	return nil
}
