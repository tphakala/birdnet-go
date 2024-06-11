// dogbarkfilter.go
package processor

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Assuming a predefined time limit for filtering detections after a dog bark.
var DogBarkFilterTimeLimit = time.Duration(conf.Setting().Realtime.DogBarkFilter.Remember) * time.Minute

// DogBarkFilter contains a list of species to be filtered within the time limit after a dog bark.
type DogBarkFilter struct {
	SpeciesList []string
}

// LoadDogBarkFilterConfig reads the dog bark filter configuration from a CSV file.
func LoadDogBarkFilterConfig(fileName string) (DogBarkFilter, error) {
	var config DogBarkFilter

	// Retrieve the default config paths from your application settings.
	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		return DogBarkFilter{}, err
	}

	var file *os.File
	// Attempt to open the file from one of the default config paths.
	for _, path := range configPaths {
		fullPath := filepath.Join(path, fileName)
		file, err = os.Open(fullPath)
		if err == nil {
			break
		}
	}

	if file == nil {
		// if file is not found just return empty config and error
		return DogBarkFilter{}, fmt.Errorf("file '%s' not found in default config paths", fileName)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	// Assuming no header and one species per line.
	records, err := reader.ReadAll()
	if err != nil {
		return DogBarkFilter{}, err
	}

	for _, record := range records {
		if len(record) == 0 {
			continue // Skip empty lines
		}
		// Assuming the species name is the only entry in each record.
		species := strings.ToLower(strings.TrimSpace(record[0]))
		config.SpeciesList = append(config.SpeciesList, species)
	}
	log.Println("Dog bark filter config loaded")

	return config, nil
}

// Check if the species should be filtered based on the last dog bark timestamp.
func (c DogBarkFilter) Check(species string, lastDogBark time.Time) bool {
	species = strings.ToLower(species)
	for _, s := range c.SpeciesList {
		if s == species {
			return time.Since(lastDogBark) <= DogBarkFilterTimeLimit
		}
	}
	return false
}
