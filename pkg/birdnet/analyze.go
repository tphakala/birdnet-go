package birdnet

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/tphakala/go-birdnet/pkg/config"
	"github.com/tphakala/go-birdnet/pkg/observation"
	"github.com/tphakala/go-tflite"
)

type Result struct {
	Species    string
	Confidence float32
}

type DetectionsMap map[string][]Result

// customSigmoid calculates the sigmoid of x adjusted by a sensitivity factor.
// Sensitivity modifies the steepness of the curve. A higher value for sensitivity
// makes the curve steeper. It returns a value between 0 and 1.
func customSigmoid(x, sensitivity float64) float64 {
	return 1.0 / (1.0 + math.Exp(-sensitivity*x))
}

// sortResults sorts a slice of Result based on the confidence value in descending order.
func sortResults(results []Result) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Confidence > results[j].Confidence // ">" for descending order
	})
}

// pairLabelsAndConfidence pairs labels with their corresponding confidence values.
// It returns a slice of Result where each Result contains a species and its confidence.
// An error is returned if the length of labels and predictions do not match.
func pairLabelsAndConfidence(labels []string, preds []float32) ([]Result, error) {
	if len(labels) != len(preds) {
		return nil, fmt.Errorf("length of labels (%d) and predictions (%d) do not match", len(labels), len(preds))
	}

	results := make([]Result, len(labels))
	for i := range labels {
		results[i] = Result{
			Species:    labels[i],
			Confidence: preds[i],
		}
	}

	return results, nil
}

// Predict uses a TensorFlow Lite interpreter to infer results from the provided sample.
// It then applies a custom sigmoid function to the raw predictions, pairs the results
// with their respective labels, sorts them by confidence, and returns the top results.
// The function returns an error if there's any issue during the prediction process.
func Predict(sample [][]float32, sensitivity float64) ([]Result, error) {
	// Get the input tensor from the interpreter
	input := interpreter.GetInputTensor(0)
	if input == nil {
		return nil, fmt.Errorf("cannot get input tensor")
	}

	// Copy the sample data into the input tensor
	copy(input.Float32s(), sample[0])

	// Execute the inference using the interpreter
	status := interpreter.Invoke()
	if status != tflite.OK {
		return nil, fmt.Errorf("tensor invoke failed")
	}

	// Retrieve the output tensor from the interpreter
	output := interpreter.GetOutputTensor(0)
	outputSize := output.Dim(output.NumDims() - 1)

	// Create a slice to store the prediction results
	prediction := make([]float32, outputSize)

	// Copy the data from the output tensor into the prediction slice
	copy(prediction, output.Float32s())

	// Apply the custom sigmoid function to the prediction values to
	// get the confidence values
	confidence := make([]float32, len(prediction))
	for i := range prediction {
		confidence[i] = float32(customSigmoid(float64(prediction[i]), sensitivity))
	}

	results, err := pairLabelsAndConfidence(labels, confidence)
	if err != nil {
		return nil, fmt.Errorf("error pairing labels and confidence: %v", err)
	}

	// Do a inplace sorting of the results
	sortResults(results)

	// Only return n number of results per signal
	const maxResults = 1
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	return results, nil
}

// AnalyzeAudio processes chunks of audio data using a given interpreter to produce
// predictions. Each chunk is processed individually, and the results are aggregated
// into a slice of Observations. The sensitivity and overlap values affect the
// prediction process and the timestamp calculation, respectively.
func AnalyzeAudio(chunks [][]float32, cfg *config.Settings) ([]observation.Note, error) {
	observations := []observation.Note{}

	start := time.Now()
	predStart := 0.0
	totalChunks := len(chunks)
	totalElapsed := time.Duration(0)

	// Initialize with an empty string, it will be calculated after the first prediction
	timeRemainingMessage := ""
	fmt.Printf("\r\033[K")

	for idx, chunk := range chunks {
		// Calculate and print the estimated time before processing each chunk
		if idx > 0 { // Skip the first iteration
			avgProcessingTimePerChunk := totalElapsed / time.Duration(idx)
			remainingChunks := totalChunks - idx
			estimatedTimeRemaining := avgProcessingTimePerChunk * time.Duration(remainingChunks)
			timeRemainingMessage = formatDuration(estimatedTimeRemaining)
		} else {
			// For the first chunk, we just indicate that we're calculating
			timeRemainingMessage = "Calculating..."
		}

		// Print the status message with the current chunk and estimated time remaining
		fmt.Printf("\r\033[KAnalyzing chunk [%d/%d] (Estimated time remaining: %s)", idx+1, totalChunks, timeRemainingMessage)

		// Take current time before prediction
		startTime := time.Now()

		// Predict labels for the current audio data
		predictedResults, err := Predict([][]float32{chunk}, cfg.Sensitivity)
		if err != nil {
			return nil, fmt.Errorf("prediction failed: %v", err)
		}

		elapsed := time.Since(startTime)
		totalElapsed += elapsed

		// Calculate the end timestamp for this prediction, chunk length is fixed 3.0 seconds
		predEnd := predStart + 3.0

		// Generate observations from predicted results
		for _, result := range predictedResults {
			obs := observation.New(cfg, predStart, predEnd, result.Species, float64(result.Confidence), 0.0, 0.0, "", elapsed)
			observations = append(observations, obs)
		}

		// Adjust for overlap for the next prediction
		predStart = predEnd - cfg.Overlap
	}

	// Clear the line and print a new line for completion message
	fmt.Printf("\r\033[K")
	elapsed := time.Since(start)
	fmt.Printf("Analysis completed, total time elapsed: %s\n", formatDuration(elapsed))

	return observations, nil
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
