// config/config.go
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type Settings struct {
	NodeName       string // name of go-birdnet node, can be used to identify source of notes
	InputFile      string // audio file to analyze (overrides InputDirectory if set)
	InputDirectory string // directory to analyze
	RealtimeMode   bool   // true to analyze audio in realtime
	ModelPath      string
	LabelFilePath  string
	Sensitivity    float64 // birdnet analysis sigmoid sensitivity
	Overlap        float64 // birdnet analysis overlap between chunks
	Debug          bool    // true to enable debug mode
	ClipPath       string  // directory to store audio clips
	ClipType       string  // wav, flac, mp3
	Threshold      float64 // threshold for prediction confidence to report
	Locale         string  // language to use for labels
	ProcessingTime bool    // true to report processing time for each prediction
	Recursive      bool    // true for recursive directory analysis
	OutputDir      string  // directory to output results
	OutputFormat   string  // table, csv
	LogPath        string  // directory to store log files
	LogFile        string  // name of log file
	Database       string  // none, sqlite, mysql
	TimeAs24h      bool    // true 24-hour time format, false 12-hour time format
	Longitude      float64 // longitude of recording location for prediction filtering
	Latitude       float64 // latitude of recording location for prediction filtering
}

// Load reads the configuration file and environment variables into GlobalConfig.
func Load() (*Context, error) {
	var settings Settings

	// Initialize the Context with the settings
	ctx := &Context{
		Settings: &settings,
		// set SpeciesListUpdated to yesterday to force update on first run
		SpeciesListUpdated:  time.Now().AddDate(0, 0, -1),
		ExcludedSpeciesList: []string{"Engine", "Human vocal", "Human non-vocal", "Human whistle", "Gun", "Fireworks", "Siren", "Dog"},
	}

	// Initialize viper to read config
	if err := initViper(); err != nil {
		return nil, err // Handle viper initialization errors
	}

	// Unmarshal the configuration file directly into the Settings of the Context
	if err := viper.Unmarshal(ctx.Settings); err != nil {
		return nil, fmt.Errorf("error unmarshaling config into struct: %w", err)
	}

	// Initialize the OccurrenceMonitor or any other components as needed.
	ctx.OccurrenceMonitor = NewOccurrenceMonitor(10 * time.Second)

	return ctx, nil
}

// GetSettings returns a pointer to the global settings object.
func GetSettings(ctx *Context) *Settings {
	return ctx.Settings
}

// BindFlags binds command line flags to configuration settings using Viper.
func BindFlags(cmd *cobra.Command, settings *Settings) {
	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		fmt.Printf("Error binding flags: %v\n", err)
	}
}

// SyncViper updates the settings object with values from Viper.
func SyncViper(settings *Settings) {
	if err := viper.Unmarshal(settings); err != nil {
		fmt.Printf("Error unmarshalling Viper values to settings: %v\n", err)
	}
}

// initViper initializes viper with the configuration file.
func initViper() error {
	viper.SetConfigType("yaml")

	configPaths := getDefaultConfigPaths()
	configName := "config"

	for _, path := range configPaths {
		viper.AddConfigPath(path)
	}
	viper.SetConfigName(configName)

	return readConfig()
}

// getDefaultConfigPaths builds a list of paths to look for the configuration file.
func getDefaultConfigPaths() []string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Errorf("error fetching user directory: %v", err))
	}
	return []string{filepath.Join(homeDir, ".config", "birdnet-go"), "."}
}

// readConfig reads the configuration from files or creates a default if not found.
func readConfig() error {
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			configPath := filepath.Join(getDefaultConfigPaths()[0], "birdnet-go.yaml")
			createDefault(configPath)
		} else {
			return fmt.Errorf("fatal error reading config file: %w", err)
		}
	}
	return nil
}

// createDefault creates a default configuration file at the specified path.
func createDefault(configPath string) {
	defaultConfig := getDefaultConfig()

	// Ensure the directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		panic(fmt.Errorf("error creating directories for config file: %v", err))
	}

	// Write the default configuration
	if err := os.WriteFile(configPath, []byte(defaultConfig), 0644); err != nil {
		panic(fmt.Errorf("error writing default config file: %v", err))
	}

	fmt.Println("Created default config file at:", configPath)
	viper.ReadInConfig() // Re-read the config after creating the default
}

// getDefaultConfig returns the default configuration as a string.
func getDefaultConfig() string {
	return `# Default configuration
debug: false
nodename: go-birdnet
sensitivity: 1
locale: en
overlap: 0.0
clippath: ./clips
cliptype: mp3
threshold: 0.8
processingtime: false
logpath: ./log/
logfile: notes.log
outputdir:
outputformat:
database: none
timeas24h: true
`
}
