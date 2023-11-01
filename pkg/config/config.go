// config/config.go
package config

import (
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"

	"github.com/spf13/viper"
)

type Settings struct {
	InputFile      string
	InputDirectory string
	RealtimeMode   bool
	ModelPath      string
	Sensitivity    float64
	Overlap        float64
	Debug          bool
	CapturePath    string
	Threshold      float64
	Locale         string
	ProcessingTime bool
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

func Load() {
	// Get the user's home directory
	usr, err := user.Current()
	if err != nil {
		log.Fatalf("Error fetching user directory: %v", err)
	}

	// Initialize Viper
	viper.SetConfigType("yaml") // or "json", "toml"

	// Define config paths and names
	configPaths := []string{
		usr.HomeDir + "/.config/go-birdnet/",
		".",
	}
	configNames := []string{
		"config",
		"go-birdnet.conf",
	}

	// Look for the config in defined paths and names in the specific order
	found := false
	for _, path := range configPaths {
		for _, name := range configNames {
			viper.AddConfigPath(path)
			viper.SetConfigName(name)

			if err := viper.ReadInConfig(); err == nil {
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		// Config file not found in any of the paths; create a default one
		configPath := usr.HomeDir + "/.config/go-birdnet/config"
		createDefault(configPath)
		// Read the just created default config
		viper.AddConfigPath(filepath.Dir(configPath))
		viper.SetConfigName(filepath.Base(configPath))
		if err := viper.ReadInConfig(); err != nil {
			panic(fmt.Errorf("fatal error reading created default config file: %s", err))
		}
	}

	var cfg Settings

	// Unmarshal config file into struct
	if err := viper.Unmarshal(&cfg); err != nil {
		panic(fmt.Errorf("error unmarshaling config into struct: %s", err))
	}
}

func createDefault(path string) {
	defaultConfig := `
debug: false
modelpath: ./model/BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite
sensitivity: 1
locale: en
overlap: 0.0
savepath: ./clips
threshold: 0.8
processingtime: false
logpath: ./log/
logfile: notes.log
database: none
timeas24h: true
`
	// Create the directory structure if it doesn't exist
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("error creating directories for config file: %v", err)
		}
	}

	if err := os.WriteFile(path, []byte(defaultConfig), 0644); err != nil {
		log.Fatalf("error creating default config file: %v", err)
	}

	//fmt.Println("Created default config file at:", path)
}
