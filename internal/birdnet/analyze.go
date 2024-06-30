package birdnet

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/observation"
	"github.com/tphakala/go-tflite"
)

// Filter structure is used for filtering predictions based on certain criteria.
type Filter struct {
	Score float32
	Label string
}

// DetectionsMap maps species names to a list of their detection results.
type DetectionsMap map[string][]datastore.Results

// Predict performs inference on a given sample using the TensorFlow Lite interpreter.
// It processes the sample to predict species and their confidence levels.
func (bn *BirdNET) Predict(sample [][]float32) ([]datastore.Results, error) {
	// implement locking to prevent concurrent access to the interpreter, not
	// necessarily best way to manage multiple audio sources but works for now
	bn.mu.Lock()
	defer bn.mu.Unlock()

	// Get the input tensor from the interpreter
	inputTensor := bn.AnalysisInterpreter.GetInputTensor(0)
	if inputTensor == nil {
		return nil, fmt.Errorf("cannot get input tensor")
	}

	// Preparing input tensor with the sample data
	copy(inputTensor.Float32s(), sample[0])

	// DEBUG: Log the length of the sample data
	//log.Printf("Invoking tensor with sample length: %d", len(sample[0]))

	// Invoke the interpreter to perform inference
	if status := bn.AnalysisInterpreter.Invoke(); status != tflite.OK {
		return nil, fmt.Errorf("tensor invoke failed: %v", status)
	}

	// Read the results from the output tensor
	outputTensor := bn.AnalysisInterpreter.GetOutputTensor(0)
	predictions := extractPredictions(outputTensor)

	confidence := applySigmoidToPredictions(predictions, bn.Settings.BirdNET.Sensitivity)

	results, err := pairLabelsAndConfidence(bn.Labels, confidence)
	if err != nil {
		return nil, err
	}

	// Sorting results by confidence in descending order.
	sortResults(results)

	// Return the top 10 results
	return trimResultsToMax(results, 10), nil
}

// AnalyzeAudio processes audio data in chunks and predicts species using the BirdNET model.
// It returns a slice of observations with the identified species and their confidence levels.
func (bn *BirdNET) AnalyzeAudio(chunks [][]float32) ([]datastore.Note, error) {
	var observations []datastore.Note
	startTime := time.Now()
	predStart := 0.0

	for idx, chunk := range chunks {
		fmt.Printf("\r\033[KAnalyzing chunk [%d/%d] %s", idx+1, len(chunks), estimateTimeRemaining(startTime, idx, len(chunks)))

		chunkResults, err := bn.processChunk(chunk, predStart)
		if err != nil {
			return nil, err
		}

		observations = append(observations, chunkResults...)
		predStart += 3.0 - bn.Settings.BirdNET.Overlap // Adjust for overlap.
	}

	fmt.Printf("\r\033[KAnalysis completed in %s\n", formatDuration(time.Since(startTime)))
	return observations, nil
}

// processChunk handles the prediction for a single chunk of audio data.
func (bn *BirdNET) processChunk(chunk []float32, predStart float64) ([]datastore.Note, error) {
	results, err := bn.Predict([][]float32{chunk})
	if err != nil {
		return nil, fmt.Errorf("prediction failed: %w", err)
	}

	var source = ""
	var clipName = ""

	var notes []datastore.Note
	for _, result := range results {
		note := observation.New(bn.Settings, predStart, predStart+3.0, result.Species, float64(result.Confidence), source, clipName, 0)
		notes = append(notes, note)
	}
	return notes, nil
}

// customSigmoid applies a sigmoid function with sensitivity adjustment to a value.
func customSigmoid(x, sensitivity float64) float64 {
	return 1.0 / (1.0 + math.Exp(-sensitivity*x))
}

// sortResults sorts a slice of Result by their confidence in descending order.
func sortResults(results []datastore.Results) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Confidence > results[j].Confidence
	})
}

// pairLabelsAndConfidence pairs labels with their corresponding confidence values.
func pairLabelsAndConfidence(labels []string, preds []float32) ([]datastore.Results, error) {
	if len(labels) != len(preds) {
		return nil, fmt.Errorf("mismatched labels and predictions lengths: %d vs %d", len(labels), len(preds))
	}

	var results []datastore.Results
	for i, label := range labels {
		results = append(results, datastore.Results{Species: label, Confidence: preds[i]})
	}
	return results, nil
}

// Function to format duration in a readable way
func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60 // modulus to get remainder minutes after hours
	seconds := int(d.Seconds()) % 60 // modulus to get remainder seconds after minutes

	if hours >= 1 {
		return fmt.Sprintf("%d hour(s) %d minute(s)", hours, minutes)
	} else if minutes >= 1 {
		return fmt.Sprintf("%d minute(s) %d second(s)", minutes, seconds)
	} else {
		return fmt.Sprintf("%d second(s)", seconds)
	}
}

// estimateTimeRemaining estimates the time remaining for processing.
func estimateTimeRemaining(start time.Time, current, total int) string {
	if current == 0 {
		return "Estimating time..."
	}
	elapsed := time.Since(start)
	estimatedTotal := elapsed / time.Duration(current) * time.Duration(total)
	remaining := estimatedTotal - elapsed
	return fmt.Sprintf("(Estimated time remaining: %s)", formatDuration(remaining))
}

// extractPredictions extracts prediction results from a TensorFlow Lite tensor.
func extractPredictions(tensor *tflite.Tensor) []float32 {
	predSize := tensor.Dim(tensor.NumDims() - 1)
	predictions := make([]float32, predSize)
	copy(predictions, tensor.Float32s())
	return predictions
}

// applySigmoidToPredictions applies the sigmoid function to a slice of predictions.
func applySigmoidToPredictions(predictions []float32, sensitivity float64) []float32 {
	confidence := make([]float32, len(predictions))
	for i, pred := range predictions {
		confidence[i] = float32(customSigmoid(float64(pred), sensitivity))
	}
	return confidence
}

// trimResultsToMax trims the results to a maximum specified count.
func trimResultsToMax(results []datastore.Results, max int) []datastore.Results {
	if len(results) > max {
		return results[:max]
	}
	return results
}
