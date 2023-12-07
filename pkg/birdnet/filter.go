package birdnet

import (
	"fmt"
	"sort"
	"time"

	"github.com/tphakala/birdnet-go/internal/config"
	"github.com/tphakala/go-tflite"
)

type SpeciesScore struct {
	Score float64
	Label string
}

type ByScore []SpeciesScore

func (a ByScore) Len() int           { return len(a) }
func (a ByScore) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByScore) Less(i, j int) bool { return a[i].Score > a[j].Score } // For descending order

const locationFilterThreshold = 0.01

func GetProbableSpecies(ctx *config.Context) []string {

	// If latitude and longitude are not set, skip filtering
	if ctx.Settings.BirdNET.Latitude == 0 && ctx.Settings.BirdNET.Longitude == 0 {
		if ctx.Settings.Debug {
			fmt.Printf("Latitude and longitude not set, not using location based prediction filter\n")
		}
		return ctx.Labels
	}

	filters, _ := predictFilter(ctx)

	var speciesScores []SpeciesScore
	for _, filter := range filters {
		if filter.Score >= locationFilterThreshold {
			speciesScores = append(speciesScores, SpeciesScore{Score: float64(filter.Score), Label: filter.Label})
		}
	}

	sort.Sort(ByScore(speciesScores))

	var labels []string
	for _, speciesScore := range speciesScores {
		labels = append(labels, speciesScore.Label)
	}

	ctx.SpeciesListUpdated = time.Now().Truncate(24 * time.Hour)

	return labels
}

func predictFilter(ctx *config.Context) ([]Filter, error) {
	input := ctx.FilterInterpreter.GetInputTensor(0)
	if input == nil {
		return nil, fmt.Errorf("cannot get input tensor")
	}

	// Calculate week number for filter model, it assumes that there is
	// 4 weeks in a month and that the first week starts on the first day
	// of the month
	week := getWeekForFilter()

	// Create a slice with your data
	data := []float32{float32(ctx.Settings.BirdNET.Latitude), float32(ctx.Settings.BirdNET.Longitude), week}

	// Assuming input.Float32s() returns a slice of float32
	float32s := input.Float32s()

	// Ensure the input tensor has enough capacity
	if len(float32s) < len(data) {
		return nil, fmt.Errorf("input tensor does not have enough capacity")
	}

	// Copy the data into the input tensor
	copy(float32s, data)

	// Execute the inference using the interpreter
	status := ctx.FilterInterpreter.Invoke()
	if status != tflite.OK {
		return nil, fmt.Errorf("tensor invoke failed")
	}

	// Retrieve the output tensor from the interpreter
	output := ctx.FilterInterpreter.GetOutputTensor(0)
	outputSize := output.Dim(output.NumDims() - 1)

	// Create a slice to store the prediction results
	filter := make([]float32, outputSize)

	// Copy the data from the output tensor into the prediction slice
	copy(filter, output.Float32s())

	// Apply threshold and zip with labels
	var results []Filter
	for i, score := range filter {
		if score >= 0.03 {
			results = append(results, Filter{Score: score, Label: ctx.Labels[i]})
		}
	}

	// Sort by filter value
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

func getWeekForFilter() float32 {
	current := time.Now()
	month := int(current.Month())
	day := current.Day()
	weeksFromMonths := (month - 1) * 4
	weekInMonth := (day-1)/7 + 1

	// Week number is the sum of weeks from months and week number in the
	// current month
	return float32(weeksFromMonths + weekInMonth)
}
