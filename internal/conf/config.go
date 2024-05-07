// conf/config.go
package conf

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

type Settings struct {
	Debug bool // true to enable debug mode

	Main struct {
		Name      string // name of go-birdnet node, can be used to identify source of notes
		TimeAs24h bool   // true 24-hour time format, false 12-hour time format
		Log       LogConfig
	}

	BirdNET struct {
		Sensitivity             float64 // birdnet analysis sigmoid sensitivity
		Threshold               float64 // threshold for prediction confidence to report
		Overlap                 float64 // birdnet analysis overlap between chunks
		Longitude               float64 // longitude of recording location for prediction filtering
		Latitude                float64 // latitude of recording location for prediction filtering
		Threads                 int     // number of CPU threads to use for analysis
		Locale                  string  // language to use for labels
		LocationFilterThreshold float32 // threshold for prediction confidence to report
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
			Enabled          bool    // true to enable birdweather uploads
			Debug            bool    // true to enable debug mode
			ID               string  // birdweather ID
			Threshold        float64 // threshold for prediction confidence for uploads
			LocationAccuracy float64 // accuracy of location in meters
		}

		PrivacyFilter struct {
			Enabled bool // true to enable privacy filter
		}

		DogBarkFilter struct {
			Enabled bool // true to enable dog bark filter
		}

		Retention struct {
			MinEvictionHours   int // minimum number of hours to keep audio clips
			MinClipsPerSpecies int // minimum number of clips per species to keep
		}

		RTSP struct {
			Url       string // RTSP stream URL
			Transport string // RTSP Transport Protocol
		}

		MQTT struct {
			Enabled  bool   // true to enable MQTT
			Broker   string // MQTT (tcp://host:port)
			Topic    string // MQTT topic
			Username string // MQTT username
			Password string // MQTT password
		}

		Telemetry struct {
			Enabled bool   // true to enable Prometheus compatible telemetry endpoint
			Listen  string // IP address and port to listen on
		}
	}

	WebServer struct {
		Enabled bool   // true to enable web server
		Port    string // port for web server
		AutoTLS bool   // true to enable auto TLS
		Log     LogConfig
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

// LogConfig defines the configuration for a log file
type LogConfig struct {
	Enabled     bool         // true to enable this log
	Path        string       // Path to the log file
	Rotation    RotationType // Type of log rotation
	MaxSize     int64        // Max size in bytes for RotationSize
	RotationDay time.Weekday // Day of the week for RotationWeekly
}

// RotationType defines different types of log rotations.
type RotationType string

const (
	RotationDaily  RotationType = "daily"
	RotationWeekly RotationType = "weekly"
	RotationSize   RotationType = "size"
)

// buildTime is the time when the binary was built.
var buildDate string

// Load reads the configuration file and environment variables into GlobalConfig.
func Load() (*Settings, error) {
	// Create a new settings struct
	settings := &Settings{}

	// Initialize viper and read config
	if err := initViper(); err != nil {
		return nil, fmt.Errorf("error initializing viper: %w", err)
	}

	// Unmarshal config into struct
	if err := viper.Unmarshal(settings); err != nil {
		return nil, fmt.Errorf("error unmarshaling config into struct: %w", err)
	}

	// Validate MQTT settings
	if settings.Realtime.MQTT.Enabled {
		if settings.Realtime.MQTT.Broker == "" {
			return nil, errors.New("MQTT broker URL is required when MQTT is enabled")
		}
	}
	return settings, nil
}

func initViper() error {
	// Set config file name and type
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	configPaths, err := GetDefaultConfigPaths()
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
		// Print build date and config file used
		fmt.Printf("BirdNET-Go build date: %s, using config file: %s\n", buildDate, viper.ConfigFileUsed())
	}

	return nil
}

// createDefaultConfig creates a default config file and writes it to the default config path
func createDefaultConfig() error {
	configPaths, err := GetDefaultConfigPaths() // Again, adjusted for error handling
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
main:
  name: BirdNET-Go		# name of node, can be used to identify source of notes
  timeas24h: true		# true for 24-hour time format, false for 12-hour time format
  log:
    enabled: true		# true to enable log file
    path: birdnet.log	# path to log file
    rotation: daily		# daily, weekly or size
    maxsize: 1048576	# max size in bytes for size rotation
    rotationday: 0		# day of the week for weekly rotation, 0 = Sunday

# BirdNET model specific settings
birdnet:
  sensitivity: 1.0			# sigmoid sensitivity, 0.1 to 1.5
  threshold: 0.8			# threshold for prediction confidence to report, 0.0 to 1.0
  overlap: 0.0				# overlap between chunks, 0.0 to 2.9
  threads: 0				# 0 to use all available CPU threads
  locale: en				# language to use for labels
  latitude: 00.000			# latitude of recording location for prediction filtering
  longitude: 00.000			# longitude of recording location for prediction filtering
  locationfilterthreshold: 0.01 # rangefilter species occurrence threshold

# Realtime processing settings
realtime:
  interval: 15		    # duplicate prediction interval in seconds
  processingtime: false # true to report processing time for each prediction
  
  audioexport:
    enabled: true 		# true to export audio clips containing indentified bird calls
    path: clips/   		# path to audio clip export directory
    type: wav      		# only wav supported for now
  
  log:
    enabled: false		# true to enable OBS chat log
    path: birdnet.txt	# path to OBS chat log

  birdweather:
    enabled: false		  # true to enable birdweather uploads
    locationaccuracy: 500 # accuracy of location in meters
    debug: false		  # true to enable birdweather api debug mode
    id: 00000			  # birdweather ID

  rtsp:
    url:				# RTSP stream URL
    transport: tcp		# RTSP Transport Protocol

  mqtt:
    enabled: false					# true to enable MQTT
    broker: tcp://localhost:1883	# MQTT (tcp://host:port)
    topic: birdnet					# MQTT topic
    username: birdnet				# MQTT username
    password: secret       			# MQTT password

  privacyfilter:        # Privacy filter prevents audio clip saving if human voice 
    enabled: true       # is detected durin audio capture

  dogbarkfilter:
    enabled: true

  telemetry:
    enabled: false			# true to enable Prometheus compatible telemetry endpoint
    listen: "0.0.0.0:8090"	# IP address and port to listen on

  retention:
    enabled: true           # true to enable retention policy of clips
    minEvictionHours: 72	# minumum number of hours before considering clip for eviction
    minClipsPerSpecies: 10	# minumum number of clips per species to keep before starting evictions

webserver:
  enabled: true		# true to enable web server
  port: 8080		# port for web server
  autotls: false	# true to enable auto TLS
  log:
    enabled: true	# true to enable log file
    path: webui.log	# path to log file
    rotation: daily	# daily, weekly or size
    maxsize: 1048576	# max size in bytes for size rotation
    rotationday: 0	# day of the week for weekly rotation, 0 = Sunday

# Ouput settings
output:
  file:
    enabled: true		# true to enable file output for file and directory analysis
    path: output/		# path to output directory
    type: table			# ouput format, Raven table or csv
  # Only one database is supported at a time
  # if both are enabled, SQLite will be used.
  sqlite:
    enabled: true		# true to enable sqlite output
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
