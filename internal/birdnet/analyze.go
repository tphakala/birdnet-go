package birdnet

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
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
	span, _ := StartSpan(ctx, "birdnet.predict", "Species prediction")
	defer span.Finish()

	start := time.Now()
	span.SetTag("model", bn.ModelInfo.ID)
	span.SetData("sample_count", len(sample))
	if len(sample) > 0 {
		span.SetData("sample_size", len(sample[0]))
	}

	fmt.Printf("sample count: %d\n", len(sample))
	fmt.Printf("sample size: %d\n", len(sample[0]))

	// implement locking to prevent concurrent access to the interpreter, not
	// necessarily best way to manage multiple audio sources but works for now
	bn.mu.Lock()
	defer bn.mu.Unlock()

// Get the input tensor from the interpreter
	spectrogramInputTensor := bn.SpectrogramInterpreter.GetInputTensor(0)
	if spectrogramInputTensor == nil {
		err := errors.New(fmt.Errorf("cannot get spectrogram input tensor")).
			Category(errors.CategoryModelInit).
			ModelContext(bn.Settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Context("interpreter_state", "initialized").
			Build()

		// Record error in metrics via span finish
		span.SetTag("error", "true")
		span.SetData("error_type", "input_tensor_nil")

		return nil, err
	}

	// Run the spectrogram model for each hop and aggregate outputs.
	hopSize := 128
	windowSize := 512
	sampleLen := len(sample[0])
	hops := 512
	//hops = (sampleLen-windowSize)/hopSize + 1

	// Prepare buffers for aggregated spectrogram formatted for the classifier.
	// Classifier expects NHWC (1,128,512,3) -> we'll create a float32 buffer
	// with shape (128, hops, 3) flattened as NHWC.
	height := 128
	channels := 3
	width := hops
	classBuf := make([]float32, height*width*channels)
	var colBuf []byte

	for hi := 0; hi < hops; hi++ {
		startIdx := hi * hopSize
		// prepare input window (zero-pad past end)
		input := spectrogramInputTensor.Float32s()
		for j := 0; j < len(input) && j < windowSize; j++ {
			srcIdx := startIdx + j
			if srcIdx < sampleLen {
				input[j] = sample[0][srcIdx]
			} else {
				input[j] = 0.0
			}
		}

		fmt.Printf("hop %d: startIdx=%d\n", hi, startIdx)

		if status := bn.SpectrogramInterpreter.Invoke(); status != tflite.OK {
			err := errors.Newf("spectrogram tensor invoke failed (hop=%d): %v", hi, status).
				Category(errors.CategoryAudio).
				ModelContext(bn.Settings.BirdNET.ModelPath, bn.ModelInfo.ID).
				Context("hop_index", hi).
				Context("status_code", status).
				Timing("spectrogram-invoke", time.Since(start)).
				Build()

			span.SetTag("error", "true")
			span.SetData("error_type", "spectrogram_invoke_failed")
			span.SetData("status_code", status)

			return nil, err
		}

		outTensor := bn.SpectrogramInterpreter.GetOutputTensor(0)
		if outTensor == nil {
			return nil, errors.New(fmt.Errorf("spectrogram output tensor nil")).
				Category(errors.CategoryModelInit).
				ModelContext(bn.Settings.BirdNET.ModelPath, bn.ModelInfo.ID).
				Context("hop_index", hi).
				Build()
		}

		specData := outTensor.Float32s()
		dims := make([]int, outTensor.NumDims())
		for i := 0; i < outTensor.NumDims(); i++ {
			dims[i] = outTensor.Dim(i)
		}

		fmt.Printf("Spectrogram output tensor dims: %v len=%d\n", dims, len(specData))

		output_size := outTensor.Dim(0)
		if len(colBuf) != output_size {
			colBuf = make([]byte, output_size)
		}
		outTensor.CopyToBuffer(&colBuf[0])

		// Fill classifier buffer: duplicate byte values into RGB channels.
		for y := 0; y < height; y++ {
			var v float32
			if y < output_size {
				v = float32(colBuf[y])
			} else {
				v = 0.0
			}
			base := ((y*width)+hi)*channels
			classBuf[base+0] = v
			classBuf[base+1] = v
			classBuf[base+2] = v
		}
	}

	// Write `classBuf` to PNG for debugging (dimensions: width=hops, height=128)
	if len(classBuf) > 0 {
		pngW := width
		pngH := height
		// file path can be changed as needed
		outPath := "/tmp/classbuf_debug.png"
		img := image.NewRGBA(image.Rect(0, 0, pngW, pngH))
		for y := 0; y < pngH; y++ {
			for x := 0; x < pngW; x++ {
				base := ((y*pngW)+x)*channels
				var r, g, b uint8
				if base+2 < len(classBuf) {
					rr := classBuf[base+0]
					gg := classBuf[base+1]
					bb := classBuf[base+2]
					if rr < 0 {
						rr = 0
					}
					if gg < 0 {
						gg = 0
					}
					if bb < 0 {
						bb = 0
					}
					if rr > 255 {
						rr = 255
					}
					if gg > 255 {
						gg = 255
					}
					if bb > 255 {
						bb = 255
					}
					r = uint8(rr)
					g = uint8(gg)
					b = uint8(bb)
				}
				img.SetRGBA(x, y, color.RGBA{r, g, b, 255})
			}
		}
		if f, err := os.Create(outPath); err == nil {
			_ = png.Encode(f, img)
			_ = f.Close()
			fmt.Printf("Wrote debug PNG: %s\n", outPath)
		} else {
			fmt.Printf("Failed to create debug PNG: %v\n", err)
		}
	}


	// Get the input tensor from the interpreter
	inputTensor := bn.AnalysisInterpreter.GetInputTensor(0)
	if inputTensor == nil {
		err := errors.New(fmt.Errorf("cannot get input tensor")).
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
	// Prefer the aggregated spectrogram (`classBuf`) formatted to NHWC if sizes match.
	pngTensor, _, _, pngErr := LoadPNGToTensor("/home/mikeyk730/src/merlin-bird-id/samples/spectrograms/lesgol.png")
	input := inputTensor.Float32s()
	if len(input) == len(classBuf) {
		copy(input, classBuf)
	} else if pngErr == nil && len(input) == len(pngTensor) {
		copy(input, pngTensor)
	} else {
		// Fallback to provided sample (if it matches length), otherwise zero-fill
		if len(input) == len(sample[0]) {
			copy(input, sample[0])
		} else {
			for i := range input {
				input[i] = 0.0
			}
		}
	}

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

	// Print predictions sorted descending by value: one per line with label and 3 decimals
	// type predItem struct {
	// 	idx   int
	// 	val   float32
	// 	label string
	// }

	// items := make([]predItem, 0, len(predictions))
	// for i, p := range predictions {
	// 	label := fmt.Sprintf("idx_%d", i)
	// 	if i < len(bn.Settings.BirdNET.Labels) {
	// 		label = bn.Settings.BirdNET.Labels[i]
	// 	}
	// 	items = append(items, predItem{idx: i, val: p, label: label})
	// }

	// sort.Slice(items, func(i, j int) bool { return items[i].val > items[j].val })
	// limit := 5
	// if len(items) < limit {
	// 	limit = len(items)
	// }
	// for i := 0; i < limit; i++ {
	// 	it := items[i]
	// 	fmt.Printf("%s: %.3f\n", it.label, it.val)
	// }



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
	span.SetTag("model", "birdnet") // Default model name for chunk processing
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

	// Get species occurrence scores once for all results (optimization)
	var speciesOccurrences map[string]float64
	if bn.Settings.BirdNET.Latitude != 0 && bn.Settings.BirdNET.Longitude != 0 {
		cachedScores, err := bn.getCachedSpeciesScores(predStart)
		if err == nil && len(cachedScores) > 0 {
			speciesOccurrences = cachedScores
		}
	}

	// Pre-allocate slice with capacity for all results
	notes := make([]datastore.Note, 0, len(results))
	for _, result := range results {
		// Look up occurrence score for this species (nil map reads are safe)
		occurrence := speciesOccurrences[result.Species]

		// Compute actual processing time
		processingTime := time.Since(start)

		note := observation.New(bn.Settings, predStart, predEnd, result.Species, float64(result.Confidence), source, clipName, processingTime, occurrence)
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
	//if len(buffer) != len(predictions) {
		// Fallback to allocation when buffer size doesn't match predictions length.
		// This ensures correctness when model output size differs from expected buffer size.
	//	return applySigmoidToPredictions(predictions, sensitivity)
	//}

	for i, pred := range predictions {
		//buffer[i] = float32(customSigmoid(float64(pred), sensitivity))
		buffer[i] = float32(pred)
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
