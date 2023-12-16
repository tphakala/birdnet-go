// config/config.go
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Settings struct {
	Debug bool // true to enable debug mode

	Node struct {
		Name      string // name of go-birdnet node, can be used to identify source of notes
		Locale    string // language to use for labels
		TimeAs24h bool   // true 24-hour time format, false 12-hour time format
		Threads   int    // number of CPU threads to use for analysis
	}

	BirdNET struct {
		Sensitivity float64 // birdnet analysis sigmoid sensitivity
		Threshold   float64 // threshold for prediction confidence to report
		Overlap     float64 // birdnet analysis overlap between chunks
		Longitude   float64 // longitude of recording location for prediction filtering
		Latitude    float64 // latitude of recording location for prediction filtering
	}

	Input struct {
		Path      string // path to input file or directory
		Recursive bool   // true for recursive directory analysis
	}

	Realtime struct {
		ProcessingTime bool // true to report processing time for each prediction

		AudioExport struct {
			Enabled bool   // export audio clips containing indentified bird calls
			Path    string // path to audio clip export directory
			Type    string // audio file type, wav, mp3 or flac
		}

		Log struct {
			Enabled  bool   // true to enable OBS chat log
			Path     string // path to OBS chat log
			Interval int    // minimum interval between log messages in seconds
		}

		Birdweather struct {
			Enabled   bool    // true to enable birdweather uploads
			ID        string  // birdweather ID
			Threshold float64 // threshold for prediction confidence for uploads
		}
	}

	Output struct {
		File struct {
			Enabled bool   // true to enable file output
			Path    string // directory to output results
			Type    string // table, csv
		}

		SQLite struct {
			Enabled bool   // true to enable sqlite output
			Path    string // path to sqlite database
		}

		MySQL struct {
			Enabled  bool   // true to enable mysql output
			Username string // username for mysql database
			Password string // password for mysql database
			Database string // database name for mysql database
			Host     string // host for mysql database
			Port     string // port for mysql database
		}
	}
}

// Load reads the configuration file and environment variables into GlobalConfig.
func Load() (*Context, error) {
	var settings Settings

	// Initialize the Context with the settings
	ctx := &Context{
		Settings: &settings,
		// set SpeciesListUpdated to yesterday to force update on first run
		SpeciesListUpdated: time.Now().AddDate(0, 0, -1),
	}

	// Initialize viper to read config
	if err := initViper(); err != nil {
		return nil, fmt.Errorf("error initializing viper: %w", err)
	}

	// Unmarshal config into struct
	if err := viper.Unmarshal(ctx.Settings); err != nil {
		return nil, fmt.Errorf("error unmarshaling config into struct: %w", err)
	}

	// init custom confidence list
	customConfidence, err := LoadCustomSpeciesConfidence("custom_confidence_list.txt")
	if err != nil {
		// set customConfidence list as empty if file not found
		customConfidence = SpeciesConfidence{}
	}
	ctx.CustomConfidence = customConfidence

	return ctx, nil
}

func initViper() error {
	// Set config file name and type
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	configPaths, err := getDefaultConfigPaths()
	if err != nil {
		return fmt.Errorf("error getting default config paths: %w", err)
	}

	for _, path := range configPaths {
		viper.AddConfigPath(path)
	}

	// Read configuration file
	err = viper.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// config file not found, create config with defaults
			return createDefaultConfig()
		}
		return fmt.Errorf("fatal error reading config file: %w", err)
	} else {
		fmt.Println("Read config file:", viper.ConfigFileUsed())
	}

	return nil
}

// Load list of custom thresholds for species
func LoadCustomSpeciesConfidence(filePath string) (SpeciesConfidence, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return SpeciesConfidence{}, fmt.Errorf("error opening custom species confidence file: %w", err)
	}
	defer file.Close()

	var speciesConfidence SpeciesConfidence
	speciesConfidence.Thresholds = make(map[string]float32)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, ",")
		if len(parts) != 2 {
			continue // skip malformed lines
		}

		species := strings.TrimSpace(parts[0])
		confidence, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 32)
		if err != nil {
			continue // skip lines with invalid confidence values
		}

		speciesConfidence.Thresholds[species] = float32(confidence)
	}

	if err := scanner.Err(); err != nil {
		return SpeciesConfidence{}, fmt.Errorf("error reading custom species confidence file: %w", err)
	}

	return speciesConfidence, nil
}

// getDefaultConfigPaths returns a list of default config paths for the current OS
func getDefaultConfigPaths() ([]string, error) {
	var configPaths []string

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("error fetching user directory: %v", err)
	}

	switch runtime.GOOS {
	case "windows":
		// Windows path, usually in "C:\Users\Username\AppData\Roaming"
		configPaths = []string{
			".",
			filepath.Join(homeDir, "AppData", "Local", "birdnet-go"),
		}
	default:
		// Linux and macOS path
		configPaths = []string{
			filepath.Join(homeDir, ".config", "birdnet-go"),
			"/etc/birdnet-go",
			".",
		}
	}

	return configPaths, nil
}

// createDefaultConfig creates a default config file and writes it to the default config path
func createDefaultConfig() error {
	configPaths, err := getDefaultConfigPaths() // Again, adjusted for error handling
	if err != nil {
		return fmt.Errorf("error getting default config paths: %w", err)
	}
	configPath := filepath.Join(configPaths[0], "config.yaml")
	defaultConfig := getDefaultConfig()

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("error creating directories for config file: %w", err)
	}

	if err := os.WriteFile(configPath, []byte(defaultConfig), 0644); err != nil {
		return fmt.Errorf("error writing default config file: %w", err)
	}

	fmt.Println("Created default config file at:", configPath)
	return viper.ReadInConfig()
}

// getDefaultConfig returns the default configuration as a string.
func getDefaultConfig() string {
	return `# BirdNET-Go configuration

debug: false			# print debug messages, can help with problem solving

# Node specific settings
node:
  name: BirdNET-Go		# name of node, can be used to identify source of notes
  locale: en			# language to use for labels
  timeas24h: true		# true for 24-hour time format, false for 12-hour time format
  threads: 0			# 0 to use all available CPU threads

# BirdNET model specific settings
birdnet:
  sensitivity: 1.0		# sigmoid sensitivity, 0.1 to 1.5
  threshold: 0.8		# threshold for prediction confidence to report, 0.0 to 1.0
  overlap: 0.0			# overlap between chunks, 0.0 to 2.9
  latitude: 00.000		# latitude of recording location for prediction filtering
  longitude: 00.000		# longitude of recording location for prediction filtering

# Realtime processing settings
realtime:
  processingtime: false # true to report processing time for each prediction
  audioexport:
    enabled: false 		# true to export audio clips containing indentified bird calls
    path: clips/   		# path to audio clip export directory
    type: wav      		# audio file type, wav, mp3 or flac
  log:
    enabled: false		# true to enable OBS chat log
    path: birdnet.txt	# path to OBS chat log
	interval: 15		# minimum interval between repeating log messages in seconds
  birdweather:
    enabled: false		# true to enable birdweather uploads
    id: 00000			# birdweather ID
    threshold: 0.9		# threshold of prediction confidence for uploads, 0.0 to 1.0

# Ouput settings
output:
  file:
    enabled: true		# true to enable file output for file and directory analysis
    path: output/		# path to output directory
    type: table			# ouput format, Raven table or csv
  # Only one database is supported at a time
  # if both are enabled, SQLite will be used.
  sqlite:
    enabled: false		# true to enable sqlite output
    path: birdnet.db	# path to sqlite database
  mysql:
    enabled: false		# true to enable mysql output
    username: birdnet	# mysql database username
    password: secret	# mysql database user password
    database: birdnet	# mysql database name
    host: localhost		# mysql database host
    port: 3306			# mysql database port
`
}
