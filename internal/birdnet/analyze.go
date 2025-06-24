package birdnet

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observation"
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
	span, ctx := StartSpan(ctx, "birdnet.predict", "Species prediction")
	defer span.Finish()
	
	start := time.Now()
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
		span.SetTag("error", "true")
		span.SetData("error_type", "input_tensor_nil")
		return nil, errors.New(fmt.Errorf("cannot get input tensor")).
			Category(errors.CategoryModelInit).
			ModelContext(bn.Settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Context("interpreter_state", "initialized").
			Build()
	}

	// Preparing input tensor with the sample data
	copy(inputTensor.Float32s(), sample[0])

	// DEBUG: Log the length of the sample data
	//log.Printf("Invoking tensor with sample length: %d", len(sample[0]))

	// Invoke the interpreter to perform inference
	invokeStart := time.Now()
	if status := bn.AnalysisInterpreter.Invoke(); status != tflite.OK {
		span.SetTag("error", "true")
		span.SetData("error_type", "invoke_failed")
		span.SetData("status_code", status)
		return nil, errors.Newf("tensor invoke failed: %v", status).
			Category(errors.CategoryAudio).
			ModelContext(bn.Settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Context("sample_length", len(sample[0])).
			Context("status_code", status).
			Timing("prediction-invoke", time.Since(start)).
			Build()
	}
	span.SetData("invoke_duration_ms", time.Since(invokeStart).Milliseconds())

	// Read the results from the output tensor
	outputTensor := bn.AnalysisInterpreter.GetOutputTensor(0)
	predictions := extractPredictions(outputTensor)

	confidence := applySigmoidToPredictions(predictions, bn.Settings.BirdNET.Sensitivity)

	results, err := pairLabelsAndConfidence(bn.Settings.BirdNET.Labels, confidence)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryValidation).
			Context("label_count", len(bn.Settings.BirdNET.Labels)).
			Context("confidence_count", len(confidence)).
			Timing("prediction-total", time.Since(start)).
			Build()
	}

	// Sorting results by confidence in descending order.
	sortResults(results)

	// Log prediction timing for performance monitoring
	duration := time.Since(start)
	bn.Debug("Prediction completed in %v with %d results", duration, len(results))
	
	// Record metrics
	span.SetData("total_duration_ms", duration.Milliseconds())
	span.SetData("result_count", len(results))
	span.SetTag("error", "false")
	
	RecordDuration("predict", duration)
	RecordMetric("birdnet.predict.results", float64(len(results)), map[string]string{"model": bn.ModelInfo.ID})

	// Return the top 10 results
	return trimResultsToMax(results, 10), nil
}

// AnalyzeAudio processes audio data in chunks and predicts species using the BirdNET model.
// It returns a slice of observations with the identified species and their confidence levels.
/*func (bn *BirdNET) AnalyzeAudio(chunks [][]float32) ([]datastore.Note, error) {
	var observations []datastore.Note
	startTime := time.Now()
	predStart := 0.0

	for idx, chunk := range chunks {
		fmt.Printf("\r\033[KAnalyzing chunk [%d/%d] %s", idx+1, len(chunks), EstimateTimeRemaining(startTime, idx, len(chunks)))

		chunkResults, err := bn.ProcessChunk(chunk, predStart)
		if err != nil {
			return nil, err
		}

		observations = append(observations, chunkResults...)
		predStart += 3.0 - bn.Settings.BirdNET.Overlap // Adjust for overlap.
	}

	fmt.Printf("\r\033[KAnalysis completed in %s\n", FormatDuration(time.Since(startTime)))
	return observations, nil
}*/

// processChunk handles the prediction for a single chunk of audio data.
func (bn *BirdNET) ProcessChunk(chunk []float32, predStart time.Time) ([]datastore.Note, error) {
	return bn.ProcessChunkWithContext(context.Background(), chunk, predStart)
}

// ProcessChunkWithContext handles prediction for a single chunk with tracing
func (bn *BirdNET) ProcessChunkWithContext(ctx context.Context, chunk []float32, predStart time.Time) ([]datastore.Note, error) {
	span, ctx := StartSpan(ctx, "birdnet.process_chunk", "Process audio chunk")
	defer span.Finish()
	
	start := time.Now()
	span.SetData("chunk_size", len(chunk))
	span.SetData("pred_start", predStart.Format(time.RFC3339))
	
	results, err := bn.PredictWithContext(ctx, [][]float32{chunk})
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryAudio).
			Context("chunk_start_time", predStart.Format(time.RFC3339)).
			Context("chunk_size", len(chunk)).
			Timing("chunk-prediction", time.Since(start)).
			Build()
	}

	// calculate predEnd time based on settings.BirdNET.Overlap
	predEnd := predStart.Add(time.Duration((3.0 - bn.Settings.BirdNET.Overlap) * float64(time.Second)))

	var source = ""
	var clipName = ""

	var notes []datastore.Note
	for _, result := range results {
		note := observation.New(bn.Settings, predStart, predEnd, result.Species, float64(result.Confidence), source, clipName, 0)
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

// trimResultsToMax trims the results to a maximum specified count.
func trimResultsToMax(results []datastore.Results, maxResults int) []datastore.Results {
	if len(results) > maxResults {
		return results[:maxResults]
	}
	return results
}
