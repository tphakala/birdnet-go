//go:build onnx

package onnx

import (
	"fmt"
	"slices"

	ort "github.com/yalue/onnxruntime_go"
)

// RangeFilter uses the BirdNET meta model to filter species by geographic location and date.
type RangeFilter struct {
	session    *ort.DynamicAdvancedSession
	labels     []string
	threshold  float32
	inputName  string
	outputName string
}

// NewRangeFilter creates a new RangeFilter from a BirdNET meta model ONNX file.
func NewRangeFilter(modelPath string, opts ...RangeFilterOption) (*RangeFilter, error) {
	if modelPath == "" {
		return nil, ErrModelPathRequired
	}

	cfg := &rangeFilterConfig{threshold: 0.03}
	for _, opt := range opts {
		opt(cfg)
	}

	// Load model metadata
	inputInfos, outputInfos, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to load range filter model metadata: %w", err)
	}

	if len(inputInfos) == 0 || len(outputInfos) == 0 {
		return nil, &ModelDetectionError{Reason: "range filter model has no input or output tensors"}
	}

	inputNames := make([]string, len(inputInfos))
	for i := range inputInfos {
		inputNames[i] = inputInfos[i].Name
	}
	outputNames := make([]string, len(outputInfos))
	for i := range outputInfos {
		outputNames[i] = outputInfos[i].Name
	}

	// Resolve labels
	labels, err := resolveRangeFilterLabels(cfg)
	if err != nil {
		return nil, err
	}

	// Validate label count against model output dimension
	if len(outputInfos) > 0 {
		outDims := outputInfos[0].Dimensions
		if len(outDims) >= 2 {
			modelSpecies := int(outDims[len(outDims)-1])
			if modelSpecies > 0 && len(labels) != modelSpecies {
				return nil, &LabelCountError{Expected: modelSpecies, Got: len(labels)}
			}
		}
	}

	// Create session options
	sessOpts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create session options: %w", err)
	}
	defer func() { _ = sessOpts.Destroy() }()

	if err := sessOpts.SetIntraOpNumThreads(1); err != nil {
		return nil, fmt.Errorf("birdnet: failed to set intra-op threads: %w", err)
	}
	if err := sessOpts.SetInterOpNumThreads(1); err != nil {
		return nil, fmt.Errorf("birdnet: failed to set inter-op threads: %w", err)
	}

	session, err := ort.NewDynamicAdvancedSession(modelPath, inputNames, outputNames, sessOpts)
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create range filter session: %w", err)
	}

	return &RangeFilter{
		session:    session,
		labels:     labels,
		threshold:  cfg.threshold,
		inputName:  inputNames[0],
		outputName: outputNames[0],
	}, nil
}

func resolveRangeFilterLabels(cfg *rangeFilterConfig) ([]string, error) {
	if len(cfg.labels) > 0 {
		return cfg.labels, nil
	}
	if cfg.labelsPath != "" {
		return loadLabels(cfg.labelsPath)
	}
	return nil, ErrLabelsRequired
}

// PredictRaw runs inference and returns raw species occurrence scores as a flat []float32 slice.
// This is used by adapters that handle their own label pairing and filtering.
func (r *RangeFilter) PredictRaw(latitude, longitude, week float32) ([]float32, error) {
	input := []float32{latitude, longitude, week}

	inputTensor, err := ort.NewTensor(ort.NewShape(1, 3), input)
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create range filter input tensor: %w", err)
	}
	defer func() { _ = inputTensor.Destroy() }()

	outputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(len(r.labels))))
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create range filter output tensor: %w", err)
	}
	defer func() { _ = outputTensor.Destroy() }()

	err = r.session.Run([]ort.Value{inputTensor}, []ort.Value{outputTensor})
	if err != nil {
		return nil, fmt.Errorf("birdnet: range filter inference failed: %w", err)
	}

	data := outputTensor.GetData()
	numSpecies := len(r.labels)
	if len(data) < numSpecies {
		return nil, fmt.Errorf("birdnet: range filter output has %d values but %d labels were provided", len(data), numSpecies)
	}
	scores := make([]float32, numSpecies)
	copy(scores, data[:numSpecies])
	return scores, nil
}

// Predict runs the range filter and returns labeled species occurrence scores.
func (r *RangeFilter) Predict(latitude, longitude float32, month, day int) ([]LocationScore, error) {
	if err := ValidateCoordinates(latitude, longitude); err != nil {
		return nil, err
	}
	if err := ValidateDate(month, day); err != nil {
		return nil, err
	}

	week := CalculateWeek(month, day)
	input := []float32{latitude, longitude, week}

	inputTensor, err := ort.NewTensor(ort.NewShape(1, 3), input)
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create range filter input tensor: %w", err)
	}
	defer func() { _ = inputTensor.Destroy() }()

	outputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(len(r.labels))))
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create range filter output tensor: %w", err)
	}
	defer func() { _ = outputTensor.Destroy() }()

	err = r.session.Run([]ort.Value{inputTensor}, []ort.Value{outputTensor})
	if err != nil {
		return nil, fmt.Errorf("birdnet: range filter inference failed: %w", err)
	}

	data := outputTensor.GetData()
	if len(data) < len(r.labels) {
		return nil, fmt.Errorf("birdnet: range filter output has %d values but %d labels were provided", len(data), len(r.labels))
	}
	scores := make([]LocationScore, len(r.labels))
	for i, label := range r.labels {
		scores[i] = LocationScore{
			Species: label,
			Score:   data[i],
			Index:   i,
		}
	}
	return scores, nil
}

// Filter removes predictions for species below the location score threshold.
// If rerank is true, confidence is multiplied by location score and re-sorted.
func (r *RangeFilter) Filter(predictions []Prediction, scores []LocationScore, rerank bool) []Prediction {
	return filterPredictions(predictions, scores, r.threshold, rerank)
}

// FilterBatch applies Filter to each batch of predictions.
func (r *RangeFilter) FilterBatch(batches [][]Prediction, scores []LocationScore, rerank bool) [][]Prediction {
	return filterBatchPredictions(batches, scores, r.threshold, rerank)
}

// Close releases the ONNX session and associated resources.
func (r *RangeFilter) Close() error {
	if r.session != nil {
		err := r.session.Destroy()
		r.session = nil
		return err
	}
	return nil
}

// CalculateWeek returns the BirdNET 48-week year week number for a given month and day.
// BirdNET assumes 4 weeks per month. Days 29-31 are clamped to week 4 of their month.
// Result is always in [1, 48].
func CalculateWeek(month, day int) float32 {
	weeksFromMonths := (month - 1) * 4
	weekInMonth := min((day-1)/7+1, 4)
	return float32(weeksFromMonths + weekInMonth)
}

// ValidateCoordinates checks that latitude is in [-90, 90] and longitude is in [-180, 180].
func ValidateCoordinates(latitude, longitude float32) error {
	if latitude < -90 || latitude > 90 {
		return &InvalidCoordinatesError{
			Latitude: latitude, Longitude: longitude,
			Reason: "latitude must be between -90 and 90",
		}
	}
	if longitude < -180 || longitude > 180 {
		return &InvalidCoordinatesError{
			Latitude: latitude, Longitude: longitude,
			Reason: "longitude must be between -180 and 180",
		}
	}
	return nil
}

// ValidateDate checks that month is in [1, 12] and day is in [1, 31].
func ValidateDate(month, day int) error {
	if month < 1 || month > 12 {
		return &InvalidDateError{Month: month, Day: day, Reason: "month must be between 1 and 12"}
	}
	if day < 1 || day > 31 {
		return &InvalidDateError{Month: month, Day: day, Reason: "day must be between 1 and 31"}
	}
	return nil
}

// filterPredictions removes predictions for species with location scores below threshold.
// If rerank is true, confidence is multiplied by location score and results are re-sorted.
func filterPredictions(predictions []Prediction, scores []LocationScore, threshold float32, rerank bool) []Prediction {
	scoreMap := make(map[string]float32, len(scores))
	for _, s := range scores {
		scoreMap[s.Species] = s.Score
	}

	var result []Prediction
	for _, p := range predictions {
		locScore, ok := scoreMap[p.Species]
		if !ok || locScore < threshold {
			continue
		}
		pred := p
		if rerank {
			pred.Confidence = p.Confidence * locScore
		}
		result = append(result, pred)
	}

	if rerank {
		slices.SortFunc(result, func(a, b Prediction) int {
			if a.Confidence > b.Confidence {
				return -1
			}
			if a.Confidence < b.Confidence {
				return 1
			}
			return 0
		})
	}
	return result
}

// filterBatchPredictions applies filterPredictions to each batch of predictions.
func filterBatchPredictions(batches [][]Prediction, scores []LocationScore, threshold float32, rerank bool) [][]Prediction {
	result := make([][]Prediction, len(batches))
	for i, batch := range batches {
		result[i] = filterPredictions(batch, scores, threshold, rerank)
	}
	return result
}

// RangeFilterOption configures a RangeFilter.
type RangeFilterOption func(*rangeFilterConfig)

type rangeFilterConfig struct {
	labels     []string
	labelsPath string
	threshold  float32
}

// WithRangeFilterLabels provides labels directly.
func WithRangeFilterLabels(labels []string) RangeFilterOption {
	return func(c *rangeFilterConfig) { c.labels = labels }
}

// WithRangeFilterLabelsPath loads labels from a file.
func WithRangeFilterLabelsPath(path string) RangeFilterOption {
	return func(c *rangeFilterConfig) { c.labelsPath = path }
}

// WithRangeFilterThreshold sets the minimum location score for filtering.
func WithRangeFilterThreshold(threshold float32) RangeFilterOption {
	return func(c *rangeFilterConfig) { c.threshold = threshold }
}

// WithRangeFilterFromClassifierLabels uses the same labels as a Classifier.
func WithRangeFilterFromClassifierLabels(labels []string) RangeFilterOption {
	return func(c *rangeFilterConfig) {
		cp := make([]string, len(labels))
		copy(cp, labels)
		c.labels = cp
	}
}
