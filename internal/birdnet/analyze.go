package birdnet

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	tflite "github.com/tphakala/go-tflite"
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
	return bn.PredictWithContext(context.Background(), sample)
}

// PredictWithContext performs inference with tracing support
func (bn *BirdNET) PredictWithContext(ctx context.Context, sample [][]float32) ([]datastore.Results, error) {
	span, _ := StartSpan(ctx, "birdnet.predict", "Species prediction")
	defer span.Finish()

	start := time.Now()
	span.SetTag("model", bn.ModelInfo.ID)
	span.SetData("sample_count", len(sample))
	if len(sample) > 0 {
		span.SetData("sample_size", len(sample[0]))
	}

	// implement locking to prevent concurrent access to the interpreter, not
	// necessarily best way to manage multiple audio sources but works for now
	bn.mu.Lock()
	defer bn.mu.Unlock()

	// Get the input tensor from the interpreter
	inputTensor := bn.AnalysisInterpreter.GetInputTensor(0)
	if inputTensor == nil {
		err := errors.Newf("cannot get input tensor").
			Category(errors.CategoryModelInit).
			ModelContext(bn.Settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Context("interpreter_state", "initialized").
			Build()

		// Record error in metrics via span finish
		span.SetTag("error", "true")
		span.SetData("error_type", "input_tensor_nil")

		// Record error in metrics directly
		if globalMetrics != nil {
			globalMetrics.RecordPrediction(bn.ModelInfo.ID, time.Since(start).Seconds(), err)
		}

		return nil, err
	}

	// Preparing input tensor with the sample data
	copy(inputTensor.Float32s(), sample[0])

	// DEBUG: Log the length of the sample data
	//log.Printf("Invoking tensor with sample length: %d", len(sample[0]))

	// Invoke the interpreter to perform inference
	invokeStart := time.Now()
	if status := bn.AnalysisInterpreter.Invoke(); status != tflite.OK {
		err := errors.Newf("tensor invoke failed: %v", status).
			Category(errors.CategoryAudio).
			ModelContext(bn.Settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Context("sample_length", len(sample[0])).
			Context("status_code", status).
			Timing("prediction-invoke", time.Since(start)).
			Build()

		span.SetTag("error", "true")
		span.SetData("error_type", "invoke_failed")
		span.SetData("status_code", status)

		// Record error in metrics
		if globalMetrics != nil {
			globalMetrics.RecordPrediction(bn.ModelInfo.ID, time.Since(start).Seconds(), err)
		}

		return nil, err
	}

	invokeDuration := time.Since(invokeStart)
	span.SetData("invoke_duration_ms", invokeDuration.Milliseconds())

	// Record model invoke timing separately
	if globalMetrics != nil {
		globalMetrics.RecordModelInvoke(bn.ModelInfo.ID, invokeDuration.Seconds())
	}

	// Read the results from the output tensor
	outputTensor := bn.AnalysisInterpreter.GetOutputTensor(0)
	predictions := extractPredictions(outputTensor)

	// Use optimized sigmoid function with buffer reuse
	confidence := applySigmoidToPredictionsReuse(predictions, bn.Settings.BirdNET.Sensitivity, bn.confidenceBuffer)

	// Use the pre-allocated buffer to reduce memory allocations
	results, err := pairLabelsAndConfidenceReuse(bn.Settings.BirdNET.Labels, confidence, bn.resultsBuffer)
	if err != nil {
		err = errors.New(err).
			Category(errors.CategoryValidation).
			Context("label_count", len(bn.Settings.BirdNET.Labels)).
			Context("confidence_count", len(confidence)).
			Timing("prediction-total", time.Since(start)).
			Build()

		span.SetTag("error", "true")
		span.SetData("error_type", "label_mismatch")

		// Record error in metrics
		if globalMetrics != nil {
			globalMetrics.RecordPrediction(bn.ModelInfo.ID, time.Since(start).Seconds(), err)
		}

		return nil, err
	}

	// Use optimized top-k algorithm instead of full sort + trim
	topResults := getTopKResults(results, 10)

	// Log prediction timing for performance monitoring
	duration := time.Since(start)
	bn.Debug("Prediction completed in %v with %d results", duration, len(topResults))

	// Record metrics
	span.SetData("total_duration_ms", duration.Milliseconds())
	span.SetData("result_count", len(topResults))
	span.SetTag("error", "false")

	// The span.Finish() will automatically record the prediction metrics

	// Return the top 10 results
	return topResults, nil
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

	// Pre-allocate slice with known capacity
	results := make([]datastore.Results, 0, len(labels))
	for i, label := range labels {
		results = append(results, datastore.Results{Species: label, Confidence: preds[i]})
	}
	return results, nil
}

// pairLabelsAndConfidenceReuse pairs labels with confidence values, reusing a pre-allocated buffer.
// The buffer must have the same length as labels and preds.
func pairLabelsAndConfidenceReuse(labels []string, preds []float32, buffer []datastore.Results) ([]datastore.Results, error) {
	if len(labels) != len(preds) {
		return nil, fmt.Errorf("mismatched labels and predictions lengths: %d vs %d", len(labels), len(preds))
	}
	if len(buffer) != len(labels) {
		return nil, fmt.Errorf("buffer size mismatch: %d vs %d", len(buffer), len(labels))
	}

	// Reuse the buffer by updating values in place
	for i, label := range labels {
		buffer[i].Species = label
		buffer[i].Confidence = preds[i]
	}
	return buffer, nil
}

// FormatDuration formats duration in a human-readable way based on the total time
func FormatDuration(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	milliseconds := int(d.Milliseconds()) % 1000

	switch {
	case hours > 0:
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	case minutes > 0:
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	case seconds > 0:
		return fmt.Sprintf("%d.%03ds", seconds, milliseconds)
	default:
		return fmt.Sprintf("%dms", milliseconds)
	}
}

// Update EstimateTimeRemaining to use the new format
func EstimateTimeRemaining(start time.Time, current, total int) string {
	if current == 0 {
		return "Estimating time..."
	}
	elapsed := time.Since(start)
	estimatedTotal := elapsed / time.Duration(current) * time.Duration(total)
	remaining := estimatedTotal - elapsed
	return fmt.Sprintf("(Estimated time remaining: %s)", FormatDuration(remaining))
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

// applySigmoidToPredictionsReuse applies the sigmoid function to predictions using a pre-allocated buffer.
// Falls back to allocation if buffer size doesn't match predictions length to ensure correctness.
func applySigmoidToPredictionsReuse(predictions []float32, sensitivity float64, buffer []float32) []float32 {
	if len(buffer) != len(predictions) {
		// Fallback to allocation when buffer size doesn't match predictions length.
		// This ensures correctness when model output size differs from expected buffer size.
		return applySigmoidToPredictions(predictions, sensitivity)
	}

	for i, pred := range predictions {
		buffer[i] = float32(customSigmoid(float64(pred), sensitivity))
	}
	return buffer
}

// trimResultsToMax trims the results to a maximum specified count.
func trimResultsToMax(results []datastore.Results, maxResults int) []datastore.Results {
	if len(results) > maxResults {
		return results[:maxResults]
	}
	return results
}

// getTopKResults returns the top k results without fully sorting the array.
// Uses a partial sort algorithm that's more efficient than sorting all results.
func getTopKResults(results []datastore.Results, k int) []datastore.Results {
	if len(results) == 0 || k <= 0 {
		return []datastore.Results{}
	}

	if k >= len(results) {
		// If k is greater than or equal to the number of results, sort everything
		sortResults(results)
		return results
	}

	// Use partial sort to find top k elements
	partialSort(results, k)

	// Sort the top k elements in descending order
	sortResults(results[:k])

	return results[:k]
}

// partialSort performs a partial sort to move the top k elements to the front.
// This is more efficient than full sorting when k << len(results).
func partialSort(results []datastore.Results, k int) {
	n := len(results)
	if k >= n {
		return
	}

	// Use quickselect-like algorithm to partition the top k elements
	left, right := 0, n-1

partitionLoop:
	for left < right {
		pivotIndex := partition(results, left, right)

		switch {
		case pivotIndex == k-1:
			// Perfect partition - we have exactly k elements
			break partitionLoop
		case pivotIndex < k-1:
			// Need more elements, search right partition
			left = pivotIndex + 1
		default:
			// Too many elements, search left partition
			right = pivotIndex - 1
		}
	}
}

// partition partitions the array around a pivot for quickselect algorithm.
// Returns the final position of the pivot.
func partition(results []datastore.Results, left, right int) int {
	// Use the rightmost element as pivot
	pivot := results[right]
	i := left - 1

	for j := left; j < right; j++ {
		// Sort in descending order (higher confidence first)
		if results[j].Confidence > pivot.Confidence {
			i++
			results[i], results[j] = results[j], results[i]
		}
	}

	results[i+1], results[right] = results[right], results[i+1]
	return i + 1
}
