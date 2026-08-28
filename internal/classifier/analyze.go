package classifier

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
)

// Filter structure is used for filtering predictions based on certain criteria.
type Filter struct {
	Score float32
	Label string
}

// DetectionsMap maps species names to a list of their detection results.
type DetectionsMap map[string][]datastore.Results

// Predict performs inference on a given sample using the classifier backend.
// Implements ModelInstance.
func (bn *BirdNET) Predict(ctx context.Context, sample [][]float32) ([]datastore.Results, error) {
	// Capture the model ID once via the lock-free identity snapshot, reused below, so
	// this hot path never reads bn.ModelInfo directly (reloadModelInternal writes it).
	modelID := bn.ModelID()
	span, _ := startPredictSpan(ctx, modelID, sample)
	defer span.Finish()

	settings := bn.currentSettings()
	start := time.Now()

	// Guard against empty sample slice. Pre-inference rejections are tagged but
	// not counted as predictions.
	if len(sample) == 0 || len(sample[0]) == 0 {
		span.markErrored(errTypeEmptySample)
		return nil, errors.Newf("empty audio sample").
			Category(errors.CategoryValidation).
			ModelContext(settings.BirdNET.ModelPath, modelID).
			Build()
	}

	// Lock to prevent concurrent access to the classifier backend and shared buffers
	bn.mu.Lock()
	defer bn.mu.Unlock()

	// Guard against nil classifier (e.g., after Delete() is called concurrently)
	if bn.classifier == nil {
		span.markErrored(errTypeClassifierNil)
		return nil, errors.Newf("classifier backend is not initialized").
			Category(errors.CategoryModelInit).
			ModelContext(settings.BirdNET.ModelPath, modelID).
			Build()
	}

	// Run inference via classifier backend
	invokeStart := time.Now()
	predictions, err := bn.classifier.Predict(sample[0])
	if err != nil {
		err = errors.New(err).
			Category(errors.CategoryAudio).
			ModelContext(settings.BirdNET.ModelPath, modelID).
			Context("sample_length", len(sample[0])).
			Timing("prediction-invoke", time.Since(invokeStart)).
			Build()

		recordPredictionFailure(span, modelID, errTypeInvokeFailed, start, err)
		return nil, err
	}

	invokeDuration := time.Since(invokeStart)
	span.SetData(dataKeyInvokeDurationMs, invokeDuration.Milliseconds())

	// Record model invoke timing separately
	if m := getMetrics(); m != nil {
		m.RecordModelInvoke(modelID, invokeDuration.Seconds())
	}

	// Use optimized sigmoid function with buffer reuse
	confidence := applySigmoidToPredictionsReuse(predictions, settings.BirdNET.Sensitivity, bn.confidenceBuffer)

	// Use the pre-allocated buffer to reduce memory allocations
	results, err := pairLabelsAndConfidenceReuse(settings.BirdNET.Labels, confidence, bn.resultsBuffer)
	if err != nil {
		err = errors.New(err).
			Category(errors.CategoryValidation).
			Context("label_count", len(settings.BirdNET.Labels)).
			Context("confidence_count", len(confidence)).
			Timing("prediction-total", time.Since(start)).
			Build()

		recordPredictionFailure(span, modelID, errTypeLabelMismatch, start, err)
		return nil, err
	}

	// Select the top predictions while retaining labels required by downstream filters.
	topResults := getTopKResults(results, DefaultTopKResults)

	// Log prediction timing for performance monitoring
	duration := time.Since(start)
	bn.Debug("Prediction completed in %v with %d results", duration, len(topResults))

	// Record metrics. Finish() records the single success because the span is not errored.
	recordPredictionSuccess(span, len(topResults), start)

	// Return the ranked results.
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

// getTopKResults returns the top k results plus the strongest lower-ranked label
// for each filter family not already represented in the top k. It avoids fully
// sorting the input array.
func getTopKResults(results []datastore.Results, k int) []datastore.Results {
	if len(results) == 0 || k <= 0 {
		return []datastore.Results{}
	}

	// Number of elements the caller will receive.
	n := min(k, len(results))

	if k >= len(results) {
		// If k is greater than or equal to the number of results, sort everything.
		sortResults(results)
	} else {
		// Use partial sort to move the top k elements to the front, then sort
		// just those k in descending order.
		partialSort(results, k)
		sortResults(results[:k])
	}

	// A human or dog label can score below the top-k boundary while still exceeding
	// its filter's configured threshold. One strongest signal per family is enough:
	// both filters compare every matching label against one family-wide threshold.
	// If the top-k prefix already contains that family, every tail score is less than
	// or equal to the retained score and cannot change the filter decision.
	hasHuman, hasDog := false, false
	for i := range n {
		hasHuman = hasHuman || nonbird.IsHumanVocalization(results[i].Species)
		hasDog = hasDog || nonbird.IsDogDetection(results[i].Species)
	}

	var bestHuman, bestDog datastore.Results
	haveHuman, haveDog := false, false
	for i := n; i < len(results); i++ {
		candidate := results[i]
		if !hasHuman && nonbird.IsHumanVocalization(candidate.Species) &&
			(!haveHuman || candidate.Confidence > bestHuman.Confidence) {
			bestHuman, haveHuman = candidate, true
		}
		if !hasDog && nonbird.IsDogDetection(candidate.Species) &&
			(!haveDog || candidate.Confidence > bestDog.Confidence) {
			bestDog, haveDog = candidate, true
		}
	}

	retained := n
	if haveHuman {
		retained++
	}
	if haveDog {
		retained++
	}

	// Return a freshly-allocated copy so the result never aliases the caller's
	// backing array. BirdNET.Predict passes a reused per-instance scratch buffer
	// (bn.resultsBuffer) that the next inference window overwrites in place via
	// pairLabelsAndConfidenceReuse; without this copy a top-K slice already handed
	// to classifier.ResultsQueue would be mutated concurrently with the queue
	// consumer reading it, an unsynchronized read/write data race that can corrupt
	// queued detections. The Bat and Perch Predict paths pass freshly-allocated
	// slices, so the copy is redundant-but-harmless there; doing it unconditionally
	// keeps the ownership contract uniform for every model. The copied set is
	// normally small, and the upstream large-buffer reuse optimization stays intact:
	// bn.resultsBuffer remains internal scratch that never escapes.
	out := make([]datastore.Results, retained)
	copy(out, results[:n])
	next := n
	if haveHuman {
		out[next] = bestHuman
		next++
	}
	if haveDog {
		out[next] = bestDog
	}

	// The top-k prefix is already sorted. Every retained tail result ranks at or
	// below that prefix, so sorting only the retained tail preserves the full
	// descending-order contract.
	sortResults(out[n:])
	return out
}

// SplitFilterSignals separates the normal top-k prediction set from the extra
// lower-ranked signals retained solely for the privacy and dog-bark filters.
// Model Predict implementations return the two slices as one ownership-safe
// allocation; the analysis queue splits it before persistence processing.
// normalResultCount must match the limit passed to getTopKResults by the model.
func SplitFilterSignals(results []datastore.Results, normalResultCount int) (topResults, filterSignals []datastore.Results) {
	n := min(max(normalResultCount, 0), len(results))
	// Cap the top slice at its length so an append by a future queue consumer
	// cannot overwrite the adjacent filter-signal tail.
	return results[:n:n], results[n:]
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
