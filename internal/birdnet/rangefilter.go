// rangefilter.go

package birdnet

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/go-tflite"
)

// SpeciesScore holds a species label and its associated score.
type SpeciesScore struct {
	Score float64
	Label string
}

// ByScore implements sort.Interface for []SpeciesScore based on the Score field.
type ByScore []SpeciesScore

func (a ByScore) Len() int           { return len(a) }
func (a ByScore) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByScore) Less(i, j int) bool { return a[i].Score > a[j].Score } // For descending order

// GetProbableSpecies filters and sorts bird species based on their scores.
// It also updates the scores for species that have custom actions defined in the speciesConfigCSV.
func (bn *BirdNET) GetProbableSpecies() []string {
	// Skip filtering if location is not set
	if bn.Settings.BirdNET.Latitude == 0 && bn.Settings.BirdNET.Longitude == 0 {
		if bn.Settings.Debug {
			log.Println("Latitude and longitude not set, not using location based prediction filter")
		}
		return bn.Labels
	}

	// Apply prediction filter based on the context
	filters, _ := bn.predictFilter()

	// check bn.Settings.BirdNET.LocationFilterThreshold for valid value
	if bn.Settings.BirdNET.LocationFilterThreshold < 0 ||
		bn.Settings.BirdNET.LocationFilterThreshold > 1 {
		fmt.Println("Invalid LocationFilterThreshold value, using default value of 0.01")
		bn.Settings.BirdNET.LocationFilterThreshold = 0.01
	}

	// Collect species scores above a certain threshold
	var speciesScores []SpeciesScore
	for _, filter := range filters {
		if filter.Score >= bn.Settings.BirdNET.LocationFilterThreshold {
			// DEBUG print species which pass location threshold filter
			//fmt.Println("Filter: ", filter.Label, " Score: ", filter.Score)
			speciesScores = append(speciesScores, SpeciesScore{Score: float64(filter.Score), Label: filter.Label})
		}
	}

	// Load species from the CSV file containing species with custom actions
	speciesFromCSV, err := loadSpeciesFromCSV(conf.SpeciesConfigCSV)
	if err != nil {
		// Silently ignore the failure to load CSV
		speciesFromCSV = []string{} // Ensure speciesFromCSV is an empty slice
	}

	// Create a map for quick lookup of species in speciesScores
	speciesScoreMap := make(map[string]*SpeciesScore)
	for i := range speciesScores {
		speciesScoreMap[speciesScores[i].Label] = &speciesScores[i]
	}

	// Process species from CSV
	for _, species := range speciesFromCSV {
		for _, label := range bn.Labels {
			if strings.Contains(label, species) {
				updateOrAddSpecies(speciesScoreMap, &speciesScores, label) // Pass pointer to slice
				break
			}
		}
	}

	// Sort species scores in descending order
	sort.Sort(ByScore(speciesScores))

	// Extract labels from the sorted scores
	var labels []string
	for _, speciesScore := range speciesScores {
		labels = append(labels, speciesScore.Label)
	}

	// Update the SpeciesListUpdated time in the context
	bn.SpeciesListUpdated = time.Now().Truncate(24 * time.Hour)

	return labels
}

// predictFilter applies a TensorFlow Lite model to predict species based on the context.
func (bn *BirdNET) predictFilter() ([]Filter, error) {
	input := bn.RangeInterpreter.GetInputTensor(0)
	if input == nil {
		return nil, fmt.Errorf("cannot get input tensor")
	}

	// Calculate the week number for the filter model
	week := getWeekForFilter()

	// Prepare the input data
	data := []float32{float32(bn.Settings.BirdNET.Latitude), float32(bn.Settings.BirdNET.Longitude), week}

	// Retrieve the input tensor's underlying data slice
	float32s := input.Float32s()

	// Ensure the input tensor has enough capacity
	if len(float32s) < len(data) {
		return nil, fmt.Errorf("input tensor does not have enough capacity")
	}

	// Copy the data into the input tensor
	copy(float32s, data)

	// Execute the model inference
	status := bn.RangeInterpreter.Invoke()
	if status != tflite.OK {
		return nil, fmt.Errorf("tensor invoke failed")
	}

	// Retrieve the output tensor
	output := bn.RangeInterpreter.GetOutputTensor(0)
	outputSize := output.Dim(output.NumDims() - 1)

	// Collect the prediction results
	filter := make([]float32, outputSize)
	copy(filter, output.Float32s())

	// Filter and label the results
	var results []Filter
	for i, score := range filter {
		if score >= 0.03 {
			results = append(results, Filter{Score: score, Label: bn.Labels[i]})
		}
	}

	// Sort results by score in descending order
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// getWeekForFilter calculates the current week number for the filter model.
func getWeekForFilter() float32 {
	current := time.Now()
	month := int(current.Month())
	day := current.Day()
	weeksFromMonths := (month - 1) * 4
	weekInMonth := (day-1)/7 + 1

	// Calculate the week number
	return float32(weeksFromMonths + weekInMonth)
}

// Function to update the score of existing species or add new ones
func updateOrAddSpecies(scoreMap map[string]*SpeciesScore, scores *[]SpeciesScore, label string) {
	if score, exists := scoreMap[label]; exists {
		score.Score = 1.0 // Updates the score of the existing species
	} else {
		newScore := SpeciesScore{Score: 1.0, Label: label}
		*scores = append(*scores, newScore)          // Adds new species to the slice
		scoreMap[label] = &(*scores)[len(*scores)-1] // Update map with new score reference
	}
}

// loadSpeciesFromCSV reads species names from a CSV file located in one of the default config paths.
// Assumes that each row in the CSV file has the species name as the first element.
func loadSpeciesFromCSV(fileName string) ([]string, error) {
	// Retrieve the default config paths.
	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		return nil, fmt.Errorf("error getting default config paths: %v", err)
	}

	var file *os.File

	// Try to open the file in one of the default config paths.
	for _, path := range configPaths {
		fullPath := filepath.Join(path, fileName)
		file, err = os.Open(fullPath)
		if err == nil {
			break
		}
	}

	if file == nil {
		return nil, fmt.Errorf("file '%s' not found in default config paths", fileName)
	}
	defer file.Close()

	// Read from the CSV file
	reader := csv.NewReader(file)
	reader.Comment = '#'        // Set comment character
	reader.FieldsPerRecord = -1 // Allow a variable number of fields

	var speciesList []string
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("error reading CSV file: %v", err)
			continue // Skip this record and continue with the next
		}

		if len(record) > 0 {
			species := record[0] // Assuming species name is in the first column
			speciesList = append(speciesList, species)
		}
	}

	return speciesList, nil
}

// mergeSpeciesLists merges two slices of species, removing duplicates.
/*func mergeSpeciesLists(list1, list2 []string) []string {
	uniqueSpecies := make(map[string]struct{})
	for _, species := range list1 {
		uniqueSpecies[species] = struct{}{}
	}
	for _, species := range list2 {
		if _, exists := uniqueSpecies[species]; !exists {
			list1 = append(list1, species)
		}
	}
	return list1
}*/
