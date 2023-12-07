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
	}

	BirdNET struct {
		Sensitivity float64 // birdnet analysis sigmoid sensitivity
		Threshold   float64 // threshold for prediction confidence to report
		Overlap     float64 // birdnet analysis overlap between chunks
		Longitude   float64 // longitude of recording location for prediction filtering
		Latitude    float64 // latitude of recording location for prediction filtering
	}

	Input struct {
		Path      string
		Recursive bool // true for recursive directory analysis
	}

	Output struct {
		File struct {
			Enabled bool
			Path    string // directory to output results
			Type    string // table, csv
		}

		Sqlite struct {
			Enabled bool
			Path    string
		}

		MySQL struct {
			Enabled  bool
			Username string
			Password string
			Host     string
		}
	}

	Realtime struct {
		ProcessingTime bool // true to report processing time for each prediction

		AudioExport struct {
			Enabled bool
			Path    string
			Type    string
		}

		Log struct {
			Enabled bool
			Path    string
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

debug: false

node:
  name: BirdNET-Go
  locale: en
  timeas24h: true

birdnet:
  sensitivity: 1
  threshold: 0.8
  overlap: 0.0
  latitude: 00.000
  longitude: 00.000

fileanalysis:
  dir: output/
  format: raven

realtime:
  processingtime: false
  audioexport:
    enabled: false
    path: clips/
    type: wav
  log:
    enabled: false
    path: log/birdnet.txt	

sqlite:
  enabled: false
  path: birdnet.db

mysql:
  enabled: false
  username: birdnet
  password: secret
  host: localhost
`
}
