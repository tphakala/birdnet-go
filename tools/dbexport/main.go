// Package main provides a CLI tool for exporting data from SQLite to MySQL.
// This tool is used to populate MySQL databases with legacy data from SQLite
// to enable MySQL migration testing for the database normalization effort.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version information (can be set via ldflags during build)
var version = "dev"

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "dbexport",
	Short: "Export BirdNET-Go data from SQLite to MySQL",
	Long: `A tool for migrating BirdNET-Go legacy database data from SQLite to MySQL.

This tool is primarily used for testing the database normalization migration
on MySQL backends by populating MySQL with real-world data from SQLite.

The migration preserves original IDs and handles foreign key relationships
correctly by temporarily disabling FK checks during the migration.`,
	RunE: runExport,
}

var cfg Config

func init() {
	// Source database flags
	rootCmd.Flags().StringVar(&cfg.SQLitePath, "sqlite-path", "", "Path to source SQLite database file")

	// Target database flags - DSN or individual components
	rootCmd.Flags().StringVar(&cfg.MySQLDSN, "mysql-dsn", "", "MySQL connection string (e.g., user:pass@tcp(host:3306)/dbname)")
	rootCmd.Flags().StringVar(&cfg.MySQLHost, "mysql-host", "localhost", "MySQL host (alternative to DSN)")
	rootCmd.Flags().IntVar(&cfg.MySQLPort, "mysql-port", 3306, "MySQL port")
	rootCmd.Flags().StringVar(&cfg.MySQLUser, "mysql-user", "birdnet", "MySQL username")
	rootCmd.Flags().StringVar(&cfg.MySQLPass, "mysql-pass", "birdnet", "MySQL password")
	rootCmd.Flags().StringVar(&cfg.MySQLDatabase, "mysql-database", "birdnet", "MySQL database name")

	// Migration options
	rootCmd.Flags().IntVar(&cfg.BatchSize, "batch-size", 1000, "Number of records per batch")
	rootCmd.Flags().BoolVar(&cfg.DropTables, "drop-tables", false, "Drop all tables before migration (fresh start)")
	rootCmd.Flags().BoolVar(&cfg.Clean, "clean", false, "Truncate target tables before migration (keeps table structure)")
	rootCmd.Flags().BoolVar(&cfg.AutoMigrate, "auto-migrate", true, "Create tables in target database before migration (use --auto-migrate=false to disable)")
	rootCmd.Flags().BoolVar(&cfg.SkipVerify, "skip-verify", false, "Skip post-migration verification")
	rootCmd.Flags().BoolVar(&cfg.Verbose, "verbose", false, "Enable verbose output")

	// Config file fallback
	rootCmd.Flags().StringVar(&cfg.ConfigPath, "config", "", "Path to config.yaml (for connection fallback)")

	// Version flag
	rootCmd.Flags().BoolP("version", "v", false, "Print version information")
}

func runExport(cmd *cobra.Command, args []string) error {
	// Handle version flag
	if v, _ := cmd.Flags().GetBool("version"); v {
		fmt.Printf("dbexport version %s\n", version)
		return nil
	}

	// Load and validate configuration
	if err := cfg.Load(); err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	if cfg.Verbose {
		fmt.Printf("Source: %s\n", cfg.SQLitePath)
		fmt.Printf("Target: %s\n", cfg.GetSanitizedMySQLDSN())
		fmt.Printf("Batch size: %d\n", cfg.BatchSize)
		fmt.Printf("Clean mode: %v\n", cfg.Clean)
	}

	// Create and run migrator
	migrator, err := NewMigrator(&cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize migrator: %w", err)
	}
	defer migrator.Close()

	// Run migration
	stats, err := migrator.Run()
	if err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	// Print summary
	stats.Print()

	// Run verification unless skipped
	if !cfg.SkipVerify {
		fmt.Println("\n--- Verification ---")
		verifier := NewVerifier(migrator.sourceDB, migrator.targetDB)
		if err := verifier.Verify(); err != nil {
			return fmt.Errorf("verification failed: %w", err)
		}
		fmt.Println("Verification passed!")
	}

	return nil
}
