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
	"github.com/tphakala/birdnet-go/internal/observation"
	tflite "github.com/tphakala/go-tflite"
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

// BuildRangeFilter updates the range filter with current probable species
func BuildRangeFilter(bn *BirdNET) error {
	// Get date for Range Filter week calculation
	today := time.Now().Truncate(24 * time.Hour)

	// Update location based species list
	speciesScores, err := bn.GetProbableSpecies(today, 0.0)
	if err != nil {
		return err
	}

	// Convert the speciesScores slice to a slice of species labels
	var includedSpecies []string
	for _, speciesScore := range speciesScores {
		includedSpecies = append(includedSpecies, speciesScore.Label)
	}

	if conf.Setting().BirdNET.RangeFilter.Debug {
		// Debug: Write included species to file
		debugFile := "debug_included_species.txt"
		content := fmt.Sprintf("Updated at: %s\nSpecies count: %d\n\nSpecies list:\n",
			time.Now().Format("2006-01-02 15:04:05"),
			len(includedSpecies))
		for _, species := range includedSpecies {
			content += species + "\n"
		}
		if err := os.WriteFile(debugFile, []byte(content), 0o644); err != nil {
			log.Printf("❌ [range_filter/rebuild] Warning: Failed to write included species file: %v\n", err)
		}
	}

	conf.Setting().UpdateIncludedSpecies(includedSpecies)

	return nil
}

// GetProbableSpecies filters and sorts bird species based on their scores.
// It also updates the scores for species that have custom actions defined in the speciesConfigCSV.
func (bn *BirdNET) GetProbableSpecies(date time.Time, week float32) ([]SpeciesScore, error) {
	bn.Debug("Applying range filter")
	// Skip filtering if location is not set
	if bn.Settings.BirdNET.Latitude == 0 && bn.Settings.BirdNET.Longitude == 0 {
		bn.Debug("Latitude and longitude not set, not using location based prediction filter")
		var speciesScores []SpeciesScore
		for _, label := range bn.Settings.BirdNET.Labels {
			speciesScores = append(speciesScores, SpeciesScore{Score: 0.0, Label: label})
		}
		return speciesScores, nil
	}

	// Apply prediction filter based on the context
	filters, err := bn.predictFilter(date, week)
	if err != nil {
		return nil, fmt.Errorf("error during prediction filter: %w", err)
	}

	// check bn.Settings.BirdNET.LocationFilterThreshold for valid value
	if bn.Settings.BirdNET.RangeFilter.Threshold < 0 ||
		bn.Settings.BirdNET.RangeFilter.Threshold > 1 {
		fmt.Println("Invalid LocationFilterThreshold value, using default value of 0.01")
		bn.Settings.BirdNET.RangeFilter.Threshold = 0.01
	}

	// Collect species scores above a certain threshold
	var speciesScores []SpeciesScore
	for _, filter := range filters {
		if filter.Score >= bn.Settings.BirdNET.RangeFilter.Threshold {
			// Check if species is in exclude list before adding
			if !isSpeciesExcluded(filter.Label, bn.Settings.Realtime.Species.Exclude) {
				speciesScores = append(speciesScores, SpeciesScore{Score: float64(filter.Score), Label: filter.Label})
			} else {
				bn.Debug("Excluding species from range filter: %s", filter.Label)
			}
		}
	}

	// Add included species and species with actions with maximum score
	processedSpecies := make(map[string]bool)

	// Process explicitly included species
	for _, includedSpecies := range bn.Settings.Realtime.Species.Include {
		bn.Debug("Processing included species: %s", includedSpecies)
		addSpeciesWithMaxScore(bn, &speciesScores, includedSpecies, processedSpecies)
	}

	// Process species with configured actions
	for species := range bn.Settings.Realtime.Species.Config {
		bn.Debug("Processing species with actions: %s", species)
		addSpeciesWithMaxScore(bn, &speciesScores, species, processedSpecies)
	}

	// Sort species scores in descending order
	sort.Sort(ByScore(speciesScores))

	return speciesScores, nil
}

// addSpeciesWithMaxScore adds all matching species to the scores list with maximum score
func addSpeciesWithMaxScore(bn *BirdNET, speciesScores *[]SpeciesScore, speciesName string, processedSpecies map[string]bool) {
	// Skip if already processed
	if processedSpecies[speciesName] {
		return
	}

	matchFound := false
	for _, label := range bn.Settings.BirdNET.Labels {
		if matchesSpecies(label, speciesName) {
			bn.Debug("Adding species with max score: %s (matched with: %s)", label, speciesName)
			*speciesScores = append(*speciesScores, SpeciesScore{Score: 1.0, Label: label})
			matchFound = true
		}
	}

	if matchFound {
		processedSpecies[speciesName] = true
	}
}

// isSpeciesExcluded checks if a species should be excluded based on its label
func isSpeciesExcluded(label string, excludeList []string) bool {
	for _, excludedSpecies := range excludeList {
		if matchesSpecies(label, excludedSpecies) {
			return true
		}
	}
	return false
}

// matchesSpecies checks if a label matches a species name (either common or scientific)
func matchesSpecies(label, speciesName string) bool {
	scientificName, commonName, _ := observation.ParseSpeciesString(label)
	return strings.EqualFold(scientificName, speciesName) || strings.EqualFold(commonName, speciesName)
}

// predictFilter applies a TensorFlow Lite model to predict species based on the context.
func (bn *BirdNET) predictFilter(date time.Time, week float32) ([]Filter, error) {
	input := bn.RangeInterpreter.GetInputTensor(0)
	if input == nil {
		return nil, fmt.Errorf("cannot get input tensor")
	}

	// If week is not set, use current date to get week
	if week == 0 {
		week = getWeekForFilter(date)
	}

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

	// Filter and label the results, but only for indices that exist in bn.Labels
	var results []Filter
	for i, score := range filter {
		if score >= bn.Settings.BirdNET.RangeFilter.Threshold && i < len(bn.Settings.BirdNET.Labels) {
			results = append(results, Filter{Score: score, Label: bn.Settings.BirdNET.Labels[i]})
		}
	}

	// Sort results by score in descending order
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// getWeekForFilter calculates the current week number for the filter model.
func getWeekForFilter(date time.Time) float32 {
	var month int
	var day int

	if date.IsZero() {
		date = time.Now()
	}

	month = int(date.Month())
	day = date.Day()

	// Calculate the week number
	weeksFromMonths := (month - 1) * 4
	weekInMonth := (day-1)/7 + 1

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
		return nil, fmt.Errorf("error getting default config paths: %w", err)
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

// debug functions

// RunFilterProcess executes the filter process on demand and prints the results.
func (bn *BirdNET) RunFilterProcess(dateStr string, week float32) {
	// If dateStr is not empty, parse the date
	var parsedDate time.Time
	var err error
	if dateStr != "" {
		parsedDate, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			fmt.Printf("Error parsing date: %s\n", err)
			return
		}
	}

	// Get the probable species
	speciesScores, err := bn.GetProbableSpecies(parsedDate, week)
	if err != nil {
		fmt.Printf("Error during species prediction: %s\n", err)
		return
	}

	PrintSpeciesScores(parsedDate, speciesScores)
}

// PrintSpeciesScores prints out the list of species scores in a human-readable format.
func PrintSpeciesScores(date time.Time, speciesScores []SpeciesScore) {
	// Get settings
	threshold := conf.Setting().BirdNET.RangeFilter.Threshold
	lat := conf.Setting().BirdNET.Latitude
	lon := conf.Setting().BirdNET.Longitude

	week := int(getWeekForFilter(date))
	fmt.Printf("Included species for %v, %v on date %s, week %d, threshold %.6f\n\n", lat, lon, date.Format("2006-01-02"), week, threshold)

	// Get number of species in speciesScores slice
	numSpecies := len(speciesScores)

	// Print header
	fmt.Printf("%-33s %-33s %-6s\n", "Scientific Name", "Common Name", "Score")
	fmt.Println(strings.Repeat("-", 33), strings.Repeat("-", 33), strings.Repeat("-", 6))

	for _, speciesScore := range speciesScores {
		scientificName, commonName, _ := observation.ParseSpeciesString(speciesScore.Label)
		fmt.Printf("%-33s %-33s %.4f\n", scientificName, commonName, speciesScore.Score)
	}

	fmt.Printf("\nTotal number of species: %d\n", numSpecies)
}
