// config/config.go
package config

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
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
		Interval       int  // minimum interval between log messages in seconds
		ProcessingTime bool // true to report processing time for each prediction

		AudioExport struct {
			Enabled bool   // export audio clips containing indentified bird calls
			Path    string // path to audio clip export directory
			Type    string // audio file type, wav, mp3 or flac
		}

		Log struct {
			Enabled bool   // true to enable OBS chat log
			Path    string // path to OBS chat log
		}

		Birdweather struct {
			Enabled   bool    // true to enable birdweather uploads
			Debug     bool    // true to enable debug mode
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

	// Initialize viper and read config
	if err := initViper(); err != nil {
		return nil, fmt.Errorf("error initializing viper: %w", err)
	}

	// Unmarshal config into struct
	if err := viper.Unmarshal(ctx.Settings); err != nil {
		return nil, fmt.Errorf("error unmarshaling config into struct: %w", err)
	}

	// init custom confidence list
	speciesConfig, err := LoadSpeciesConfig()
	if err != nil {
		// print error is loading failed
		fmt.Println("error reading species conficende config:", err)
		// set customConfidence list as empty if file not found
		speciesConfig = SpeciesConfig{}
	}
	ctx.SpeciesConfig = speciesConfig

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

// LoadCustomSpeciesConfidence loads a list of custom thresholds for species from a CSV file.
func LoadSpeciesConfig() (SpeciesConfig, error) {
	var speciesConfig SpeciesConfig
	speciesConfig.Threshold = make(map[string]float32)

	configPaths, err := getDefaultConfigPaths()
	if err != nil {
		return SpeciesConfig{}, fmt.Errorf("error getting default config paths: %w", err)
	}

	fileName := "species_config.csv"

	// Search for the file in the provided config paths.
	for _, path := range configPaths {
		filePath := filepath.Join(path, fileName)
		file, err := os.Open(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				continue // file not found, try next path
			}
			return SpeciesConfig{}, fmt.Errorf("error opening file '%s': %w", filePath, err)
		}
		defer file.Close()

		// Read the CSV file using csv.Reader
		reader := csv.NewReader(file)

		// Customize the reader's settings
		reader.Comma = ','   // Default delimiter
		reader.Comment = '#' // Lines beginning with '#' will be ignored

		// Read the CSV file
		records, err := reader.ReadAll()
		if err != nil {
			return SpeciesConfig{}, fmt.Errorf("error reading CSV file '%s': %w", filePath, err)
		}

		// Process the records
		for _, record := range records {
			if len(record) != 2 {
				continue // skip malformed lines
			}

			species := strings.ToLower(strings.TrimSpace(record[0])) // Convert species to lowercase
			confidence, err := strconv.ParseFloat(strings.TrimSpace(record[1]), 32)
			if err != nil {
				continue // skip lines with invalid confidence values
			}

			speciesConfig.Threshold[species] = float32(confidence)
		}

		fmt.Println("Read species config file:", filePath)
		return speciesConfig, nil // Return on successful read.
	}

	// File not found in any of the config paths.
	return SpeciesConfig{}, fmt.Errorf("species confidence file '%s' not found", fileName)
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
  interval: 15		    # duplicate prediction interval in seconds
  processingtime: false # true to report processing time for each prediction
  audioexport:
    enabled: false 		# true to export audio clips containing indentified bird calls
    path: clips/   		# path to audio clip export directory
    type: wav      		# only wav supported for now
  log:
    enabled: false		# true to enable OBS chat log
    path: birdnet.txt	# path to OBS chat log
    
  birdweather:
    enabled: false		# true to enable birdweather uploads
    debug: false		# true to enable birdweather api debug mode
    id: 00000			# birdweather ID

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
