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

	fmt.Println("- Analyzing audio data")
	start := time.Now()

	// Start timestamp for the prediction. It will be adjusted for each chunk
	predStart := 0.0

	// Total number of chunks for progress indicator
	totalChunks := len(chunks)

	// Process each chunk of audio data
	for idx, c := range chunks {
		// Print progress indicator
		fmt.Printf("\r- Processing chunk [%d/%d]", idx+1, totalChunks)

		// Predict labels for the current audio data
		predictedResults, err := Predict([][]float32{c}, cfg.Sensitivity)
		if err != nil {
			return nil, fmt.Errorf("prediction failed: %v", err)
		}

		// Calculate the end timestamp for this prediction
		predEnd := predStart + 3.0

		for _, result := range predictedResults {
			obs := observation.New(result.Species, float64(result.Confidence), predStart, predEnd)
			observations = append(observations, obs)
		}

		// Adjust the start timestamp for the next prediction by considering the overlap
		predStart = predEnd - cfg.Overlap
	}

	// Move to a new line after the loop ends to avoid printing on the same line.
	fmt.Println("")

	elapsed := time.Since(start)
	fmt.Printf("Time %f seconds\n", elapsed.Seconds())

	return observations, nil
}

// analyzeAudioData processes chunks of audio data using a given interpreter to produce
// predictions. Each chunk is processed individually, and the results are aggregated
// into a map with timestamps as keys. The sensitivity and overlap values affect the
// prediction process and the timestamp calculation, respectively.
/*
func AnalyzeAudio(chunks [][]float32, cfg *config.Settings) (map[string][]Result, error) {
	// Initialize an empty map to hold the detection results
	detections := make(map[string][]Result)

	fmt.Println("- Analyzing audio data")
	start := time.Now()

	// Start timestamp for the prediction. It will be adjusted for each chunk
	predStart := 0.0

	// Total number of chunks for progress indicator
	totalChunks := len(chunks)

	// Process each chunk of audio data
	for idx, c := range chunks {
		// Print progress indicator
		fmt.Printf("\r- Processing chunk [%d/%d]", idx+1, totalChunks)

		// Add the current chunk to the accumulated audio samples
		sig := [][]float32{c}

		// Predict labels for the current audio data
		p, err := Predict(sig, cfg.Sensitivity)
		if err != nil {
			return nil, fmt.Errorf("prediction failed: %v", err)
		}

		// Calculate the end timestamp for this prediction
		predEnd := predStart + 3.0

		// Store the prediction results in the detections map with the timestamp range as the key
		detections[fmt.Sprintf("%5.1f;%5.1f", predStart, predEnd)] = p

		// Adjust the start timestamp for the next prediction by considering the overlap
		predStart = predEnd - cfg.Overlap
	}

	// Move to a new line after the loop ends to avoid printing on the same line.
	fmt.Println("")

	elapsed := time.Since(start)
	fmt.Printf("Time %f seconds\n", elapsed.Seconds())

	return detections, nil
}
*/
