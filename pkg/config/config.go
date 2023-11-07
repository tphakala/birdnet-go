// config/config.go
package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// OccurrenceMonitor to track species occurrences and manage state reset.
type OccurrenceMonitor struct {
	LastSpecies   string
	OccurrenceMap map[string]int
	ResetDuration time.Duration
	Mutex         sync.Mutex
	Timer         *time.Timer
}

// Context holds the overall application state, including the Settings and the OccurrenceMonitor.
type Context struct {
	Settings          *Settings
	OccurrenceMonitor *OccurrenceMonitor
}

type Settings struct {
	InputFile      string
	InputDirectory string
	RealtimeMode   bool
	ModelPath      string
	LabelFilePath  string
	Sensitivity    float64
	Overlap        float64
	Debug          bool
	CapturePath    string
	Threshold      float64
	Locale         string
	ProcessingTime bool   // true to report processing time for each prediction
	Recursive      bool   // true for recursive directory analysis
	OutputDir      string // directory to output results
	OutputFormat   string // table, csv
	LogPath        string
	LogFile        string
	Database       string // none, sqlite, mysql
	TimeAs24h      bool   // true 24-hour time format, false 12-hour time format
}

var Locales = map[string]string{
	"Afrikaans": "labels_af.txt",
	"Catalan":   "labels_ca.txt",
	"Czech":     "labels_cs.txt",
	"Chinese":   "labels_zh.txt",
	"Croatian":  "labels_hr.txt",
	"Danish":    "labels_da.txt",
	"Dutch":     "labels_nl.txt",
	"English":   "labels_en.txt",
	"Estonian":  "labels_et.txt",
	"Finnish":   "labels_fi.txt",
	"French":    "labels_fr.txt",
	"German":    "labels_de.txt",
	"Hungarian": "labels_hu.txt",
	"Icelandic": "labels_is.txt",
	"Indonesia": "labels_id.txt",
	"Italian":   "labels_it.txt",
	"Japanese":  "labels_ja.txt",
	"Latvian":   "labels_lv.txt",
	"Lithuania": "labels_lt.txt",
	"Norwegian": "labels_no.txt",
	"Polish":    "labels_pl.txt",
	"Portugues": "labels_pt.txt",
	"Russian":   "labels_ru.txt",
	"Slovak":    "labels_sk.txt",
	"Slovenian": "labels_sl.txt",
	"Spanish":   "labels_es.txt",
	"Swedish":   "labels_sv.txt",
	"Thai":      "labels_th.txt",
	"Ukrainian": "labels_uk.txt",
}

var LocaleCodes = map[string]string{
	"af": "Afrikaans",
	"ca": "Catalan",
	"cs": "Czech",
	"zh": "Chinese",
	"hr": "Croatian",
	"da": "Danish",
	"nl": "Dutch",
	"en": "English",
	"et": "Estonian",
	"fi": "Finnish",
	"fr": "French",
	"de": "German",
	"hu": "Hungarian",
	"is": "Icelandic",
	"id": "Indonesia",
	"it": "Italian",
	"ja": "Japanese",
	"lv": "Latvian",
	"lt": "Lithuania",
	"no": "Norwegian",
	"pl": "Polish",
	"pt": "Portugues",
	"ru": "Russian",
	"sk": "Slovak",
	"sl": "Slovenian",
	"es": "Spanish",
	"sv": "Swedish",
	"th": "Thai",
	"uk": "Ukrainian",
}

var (
	GlobalConfig  Settings
	globalContext *Context
)

// Load initializes the configuration by reading in the config file and environment variables.
func Load() error {
	setDefaults()
	initViper()

	if err := viper.Unmarshal(&GlobalConfig); err != nil {
		return fmt.Errorf("error unmarshaling config into struct: %w", err)
	}

	// Process the configuration
	processConfig()

	// Initialize the global context
	InitGlobalContext()

	return nil
}

// GetSettings returns a reference to the global application settings.
func GetSettings() *Settings {
	return &GlobalConfig
}

// BindFlags binds command line flags to configuration settings using Viper.
func BindFlags(cmd *cobra.Command, cfg *Settings) {
	if err := viper.BindPFlags(cmd.Flags()); err != nil {
		fmt.Printf("Error binding flags: %v\n", err)
	}
}

// SyncViper is used to update the configuration values with those from Viper.
func SyncViper(cfg *Settings) {
	viper.Unmarshal(cfg)
}

// NormalizeLocale normalizes the input locale string and matches it to a known locale code or full name.
func NormalizeLocale(inputLocale string) (string, error) {
	inputLocale = strings.ToLower(inputLocale)

	if _, exists := Locales[LocaleCodes[inputLocale]]; exists {
		return inputLocale, nil
	}

	for code, fullName := range LocaleCodes {
		if strings.ToLower(fullName) == inputLocale {
			return code, nil
		}
	}

	fullLocale, exists := LocaleCodes[inputLocale]
	if !exists {
		return "", fmt.Errorf("unsupported locale: %s", inputLocale)
	}

	if _, exists := Locales[fullLocale]; !exists {
		return "", fmt.Errorf("locale code supported but no label file found: %s", fullLocale)
	}

	return inputLocale, nil
}

// unexported functions below

func initViper() {
	viper.SetConfigType("yaml")

	usr, err := user.Current()
	if err != nil {
		panic(fmt.Errorf("error fetching user directory: %v", err))
	}

	configPaths := []string{filepath.Join(usr.HomeDir, ".config", "go-birdnet"), "."}
	configName := "config"

	for _, path := range configPaths {
		viper.AddConfigPath(path)
	}

	viper.SetConfigName(configName)

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			createDefault(filepath.Join(usr.HomeDir, ".config", "go-birdnet", "config.yaml"))
		} else {
			panic(fmt.Errorf("fatal error reading config file: %s", err))
		}
	}
}

func setDefaults() {
	// Set default values for the configuration
	// ...
}

func processConfig() {
	// Any additional processing after loading configuration
	// ...
}

func createDefault(configPath string) {
	defaultConfig := `# Default configuration
debug: false
sensitivity: 1
locale: en
overlap: 0.0
capturepath: ./clips
threshold: 0.8
processingtime: false
logpath: ./log/
logfile: notes.log
outputdir:
outputformat:
database: none
timeas24h: true
`
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		panic(fmt.Errorf("error creating directories for config file: %v", err))
	}

	if err := os.WriteFile(configPath, []byte(defaultConfig), 0644); err != nil {
		panic(fmt.Errorf("error writing default config file: %v", err))
	}

	fmt.Println("Created default config file at:", configPath)
	viper.ReadInConfig()
}

// NewOccurrenceMonitor creates a new instance of OccurrenceMonitor with the given reset duration.
func NewOccurrenceMonitor(resetDuration time.Duration) *OccurrenceMonitor {
	return &OccurrenceMonitor{
		OccurrenceMap: make(map[string]int),
		ResetDuration: resetDuration,
	}
}

// NewContext creates a new instance of Context with the provided settings and occurrence monitor.
func NewContext(settings *Settings, occurrenceMonitor *OccurrenceMonitor) *Context {
	return &Context{
		Settings:          settings,
		OccurrenceMonitor: occurrenceMonitor,
	}
}

// TrackSpecies checks and updates the species occurrences in the OccurrenceMonitor.
func (om *OccurrenceMonitor) TrackSpecies(species string) bool {
	om.Mutex.Lock()
	defer om.Mutex.Unlock()

	if om.Timer == nil || om.LastSpecies != species {
		om.resetState(species)
		return false
	}

	om.OccurrenceMap[species]++

	if om.OccurrenceMap[species] > 1 {
		return true
	}

	return false
}

// resetState resets the state of the OccurrenceMonitor.
func (om *OccurrenceMonitor) resetState(species string) {
	om.OccurrenceMap = map[string]int{species: 1}
	om.LastSpecies = species
	if om.Timer != nil {
		om.Timer.Stop()
	}
	om.Timer = time.AfterFunc(om.ResetDuration, func() {
		om.Mutex.Lock()
		defer om.Mutex.Unlock()
		om.OccurrenceMap = make(map[string]int)
		om.Timer = nil
		om.LastSpecies = ""
	})
}

func InitGlobalContext() {
	// This function is supposed to be called after Load to make sure GlobalConfig is populated.
	globalContext = NewContext(&GlobalConfig, NewOccurrenceMonitor(10*time.Second)) // Set the duration as required
}

func GetGlobalContext() *Context {
	if globalContext == nil {
		InitGlobalContext() // Lazy initialization, if needed
	}
	return globalContext
}
