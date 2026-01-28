package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Config holds the configuration for the export tool.
type Config struct {
	// Source database
	SQLitePath string

	// Target database - either DSN or individual components
	MySQLDSN      string
	MySQLHost     string
	MySQLPort     int
	MySQLUser     string
	MySQLPass     string
	MySQLDatabase string

	// Migration options
	BatchSize   int
	DropTables  bool
	Clean       bool
	AutoMigrate bool
	SkipVerify  bool
	Verbose     bool

	// Config file path for fallback
	ConfigPath string
}

// Load validates and loads the configuration, falling back to config.yaml if needed.
func (c *Config) Load() error {
	// Try to load from config.yaml if SQLite path is missing
	if c.SQLitePath == "" {
		if err := c.loadFromConfigFile(); err != nil {
			// Config file loading failed, check if we have enough from flags
			if c.SQLitePath == "" {
				return fmt.Errorf("--sqlite-path is required (or provide config.yaml)")
			}
		}
	}

	// Validate SQLite path exists
	if _, err := os.Stat(c.SQLitePath); os.IsNotExist(err) {
		return fmt.Errorf("SQLite database not found: %s", c.SQLitePath)
	}

	// Validate batch size
	if c.BatchSize < 1 {
		return fmt.Errorf("batch-size must be at least 1")
	}
	if c.BatchSize > 10000 {
		return fmt.Errorf("batch-size too large (max 10000)")
	}

	return nil
}

// loadFromConfigFile attempts to load configuration from config.yaml.
func (c *Config) loadFromConfigFile() error {
	v := viper.New()

	// Determine config file path
	configPath := c.ConfigPath
	if configPath == "" {
		// Try default locations, preferring home directory
		if homeDir, err := os.UserHomeDir(); err == nil {
			p := filepath.Join(homeDir, ".config", "birdnet-go", "config.yaml")
			if _, statErr := os.Stat(p); statErr == nil {
				configPath = p
			}
		}

		// Fall back to current directory if home config was not found/accessible
		if configPath == "" {
			configPath = "config.yaml"
		}
	}

	v.SetConfigFile(configPath)
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Load SQLite path if not provided via flags
	if c.SQLitePath == "" {
		sqlitePath := v.GetString("output.sqlite.path")
		if sqlitePath != "" {
			c.SQLitePath = sqlitePath
		}
	}

	// Load MySQL settings if not provided via flags
	if c.MySQLDSN == "" && c.MySQLHost == "" {
		if v.GetBool("output.mysql.enabled") {
			c.MySQLHost = v.GetString("output.mysql.host")
			c.MySQLPort = v.GetInt("output.mysql.port")
			if c.MySQLPort == 0 {
				c.MySQLPort = 3306
			}
			c.MySQLUser = v.GetString("output.mysql.username")
			c.MySQLPass = v.GetString("output.mysql.password")
			c.MySQLDatabase = v.GetString("output.mysql.database")
		}
	}

	return nil
}

// GetMySQLDSN returns the MySQL DSN string.
// If MySQLDSN is set directly, it's returned as-is.
// Otherwise, a DSN is constructed from individual components.
func (c *Config) GetMySQLDSN() string {
	if c.MySQLDSN != "" {
		return c.MySQLDSN
	}

	// Construct DSN from components
	// Format: user:password@tcp(host:port)/database?charset=utf8mb4&parseTime=True&loc=Local
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.MySQLUser,
		c.MySQLPass,
		c.MySQLHost,
		c.MySQLPort,
		c.MySQLDatabase,
	)

	return dsn
}

// GetSanitizedMySQLDSN returns the MySQL DSN with password masked for logging.
func (c *Config) GetSanitizedMySQLDSN() string {
	dsn := c.GetMySQLDSN()

	// Mask password in DSN
	// Format: user:password@tcp(host:port)/database
	if idx := strings.Index(dsn, ":"); idx != -1 {
		if atIdx := strings.Index(dsn, "@"); atIdx != -1 && atIdx > idx {
			return dsn[:idx+1] + "****" + dsn[atIdx:]
		}
	}

	return dsn
}
