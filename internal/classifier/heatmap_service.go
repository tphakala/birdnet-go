package classifier

import (
	"context"
	"sync"
	"sync/atomic"

	"os"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	heatmapIntraOpThreads = 4
	heatmapInterOpThreads = 2
	heatmapCoordWidth     = 2 // [lat, lon] per grid cell
)

// HeatmapInferenceService provides dedicated batch inference for heatmap
// computation, isolated from the real-time detection pipeline. It maintains
// its own ONNX session with multi-threaded configuration optimized for
// throughput rather than latency.
type HeatmapInferenceService struct {
	mu         sync.RWMutex
	session    atomic.Pointer[ort.DynamicAdvancedSession]
	labels     []string
	indexMap   map[string]int // species label -> index in output
	inputName  string         // ONNX input tensor name (from model metadata)
	outputName string         // ONNX output tensor name (from model metadata)
	inflight   sync.WaitGroup
	closed     atomic.Bool
}

// heatmapModelMeta holds tensor name metadata extracted from a geomodel ONNX file.
type heatmapModelMeta struct {
	inputNames  []string
	outputNames []string
	inputName   string // first input tensor name (for IoBinding)
	outputName  string // first output tensor name (for IoBinding)
}

func loadHeatmapModelMeta(modelPath string) (*heatmapModelMeta, error) {
	inputInfos, outputInfos, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, errors.Newf("heatmap service: failed to load model metadata: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelInit).
			Build()
	}
	if len(inputInfos) == 0 || len(outputInfos) == 0 {
		return nil, errors.Newf("heatmap service: model has no input or output tensors").
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelInit).
			Build()
	}

	inputNames := make([]string, len(inputInfos))
	for i := range inputInfos {
		inputNames[i] = inputInfos[i].Name
	}
	outputNames := make([]string, len(outputInfos))
	for i := range outputInfos {
		outputNames[i] = outputInfos[i].Name
	}

	return &heatmapModelMeta{
		inputNames:  inputNames,
		outputNames: outputNames,
		inputName:   inputInfos[0].Name,
		outputName:  outputInfos[0].Name,
	}, nil
}

// NewHeatmapInferenceService creates a heatmap inference service with a
// dedicated multi-threaded ONNX session.
func NewHeatmapInferenceService(modelPath string, labels []string) (*HeatmapInferenceService, error) {
	meta, err := loadHeatmapModelMeta(modelPath)
	if err != nil {
		return nil, err
	}

	session, err := createHeatmapSession(modelPath, meta.inputNames, meta.outputNames)
	if err != nil {
		return nil, err
	}

	s := &HeatmapInferenceService{
		labels:     labels,
		inputName:  meta.inputName,
		outputName: meta.outputName,
	}
	s.session.Store(session)
	s.indexMap = buildSpeciesIndexMap(labels)
	return s, nil
}

func createHeatmapSession(modelPath string, inputNames, outputNames []string) (*ort.DynamicAdvancedSession, error) {
	sessOpts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, errors.Newf("heatmap service: failed to create session options: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelInit).
			Build()
	}
	defer func() { _ = sessOpts.Destroy() }()

	if err := sessOpts.SetIntraOpNumThreads(heatmapIntraOpThreads); err != nil {
		return nil, errors.Newf("heatmap service: failed to set intra-op threads: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelInit).
			Build()
	}
	if err := sessOpts.SetInterOpNumThreads(heatmapInterOpThreads); err != nil {
		return nil, errors.Newf("heatmap service: failed to set inter-op threads: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelInit).
			Build()
	}

	session, err := ort.NewDynamicAdvancedSession(modelPath, inputNames, outputNames, sessOpts)
	if err != nil {
		return nil, errors.Newf("heatmap service: failed to create ONNX session: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelInit).
			Build()
	}

	return session, nil
}

func buildSpeciesIndexMap(labels []string) map[string]int {
	m := make(map[string]int, len(labels))
	for i, l := range labels {
		m[l] = i
	}
	return m
}

// ComputeGridWithBinding runs batch inference across all weeks for a grid,
// reusing pre-allocated tensors via IoBinding. The chunks-outer/weeks-inner
// loop order minimizes coordinate copies and partial-batch rebinding.
//
// coords contains [lat, lon] pairs (totalCells * 2 floats).
// result is the output buffer sized [weeksComputed * totalCells].
func (s *HeatmapInferenceService) ComputeGridWithBinding(
	ctx context.Context,
	coords []float32,
	totalCells int,
	speciesLabel string,
	stride int,
	totalWeeks int,
	batchSize int,
	result []float32,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	const coordWidth = heatmapCoordWidth
	if totalCells <= 0 || len(coords) != totalCells*coordWidth {
		return errors.Newf("heatmap service: coords length %d does not match totalCells %d * %d",
			len(coords), totalCells, coordWidth).
			Component("classifier.heatmap_service").
			Category(errors.CategoryValidation).
			Build()
	}

	if stride <= 0 || totalWeeks <= 0 || batchSize <= 0 {
		return errors.Newf("heatmap service: stride, totalWeeks, and batchSize must be > 0").
			Component("classifier.heatmap_service").
			Category(errors.CategoryValidation).
			Build()
	}

	weeksToCompute := (totalWeeks + stride - 1) / stride
	expectedResultLen := weeksToCompute * totalCells
	if len(result) < expectedResultLen {
		return errors.Newf("heatmap service: result length %d < expected %d", len(result), expectedResultLen).
			Component("classifier.heatmap_service").
			Category(errors.CategoryValidation).
			Build()
	}

	if s.closed.Load() {
		return errors.Newf("heatmap service: session closed").
			Component("classifier.heatmap_service").
			Category(errors.CategoryValidation).
			Build()
	}

	s.inflight.Add(1)
	defer s.inflight.Done()

	// Snapshot session and metadata together under RLock to prevent mismatch
	// during Rebuild (which swaps session and updates names atomically).
	s.mu.RLock()
	session := s.session.Load()
	numSpecies := len(s.labels)
	inputName := s.inputName
	outputName := s.outputName
	speciesIdx, speciesFound := s.indexMap[speciesLabel]
	s.mu.RUnlock()

	if session == nil {
		return errors.Newf("heatmap service: session not available").
			Component("classifier.heatmap_service").
			Category(errors.CategoryValidation).
			Build()
	}

	if !speciesFound {
		return errors.Newf("heatmap service: species %q not found in geomodel", speciesLabel).
			Component("classifier.heatmap_service").
			Category(errors.CategoryValidation).
			Build()
	}

	effectiveBatch := min(totalCells, batchSize)

	inputData := make([]float32, effectiveBatch*3)
	outputData := make([]float32, effectiveBatch*numSpecies)

	inputTensor, err := ort.NewTensor(ort.NewShape(int64(effectiveBatch), 3), inputData)
	if err != nil {
		return errors.Newf("heatmap service: failed to create input tensor: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelLoad).
			Build()
	}
	defer func() { _ = inputTensor.Destroy() }()

	outputTensor, err := ort.NewTensor(ort.NewShape(int64(effectiveBatch), int64(numSpecies)), outputData)
	if err != nil {
		return errors.Newf("heatmap service: failed to create output tensor: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelLoad).
			Build()
	}
	defer func() { _ = outputTensor.Destroy() }()

	binding, err := session.CreateIoBinding()
	if err != nil {
		return errors.Newf("heatmap service: failed to create IoBinding: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelLoad).
			Build()
	}
	defer func() { _ = binding.Destroy() }()

	if err := binding.BindInput(inputName, inputTensor); err != nil {
		return errors.Newf("heatmap service: failed to bind input: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelLoad).
			Build()
	}
	if err := binding.BindOutput(outputName, outputTensor); err != nil {
		return errors.Newf("heatmap service: failed to bind output: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelLoad).
			Build()
	}

	gp := &gridParams{
		session:    session,
		binding:    binding,
		inputName:  inputName,
		outputName: outputName,
		inputData:  inputData,
		outputData: outputData,
		coords:     coords,
		result:     result,
		totalCells: totalCells,
		numSpecies: numSpecies,
		speciesIdx: speciesIdx,
		stride:     stride,
		totalWeeks: totalWeeks,
	}

	for chunkStart := 0; chunkStart < totalCells; chunkStart += effectiveBatch {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.closed.Load() {
			return errSessionClosedDuringComputation
		}

		chunkEnd := min(chunkStart+effectiveBatch, totalCells)
		chunkSize := chunkEnd - chunkStart

		if err := s.processChunk(ctx, gp, chunkStart, chunkSize, effectiveBatch); err != nil {
			return err
		}
	}

	return nil
}

// gridParams groups the state shared across chunks during grid computation.
type gridParams struct {
	session    *ort.DynamicAdvancedSession
	binding    *ort.IoBinding
	inputName  string
	outputName string
	inputData  []float32
	outputData []float32
	coords     []float32
	result     []float32
	totalCells int
	numSpecies int
	speciesIdx int
	stride     int
	totalWeeks int
}

var errSessionClosedDuringComputation = errors.Newf("heatmap service: session closed during computation").
	Component("classifier.heatmap_service").
	Category(errors.CategoryValidation).
	Build()

// processChunk runs the inner week loop for one spatial chunk. For the last
// (potentially partial) chunk it creates smaller tensors and rebinds.
func (s *HeatmapInferenceService) processChunk(
	ctx context.Context,
	gp *gridParams,
	chunkStart, chunkSize, effectiveBatch int,
) error {
	const coordWidth = heatmapCoordWidth

	for i := range chunkSize {
		srcIdx := (chunkStart + i) * coordWidth
		dstIdx := i * 3
		gp.inputData[dstIdx] = gp.coords[srcIdx]
		gp.inputData[dstIdx+1] = gp.coords[srcIdx+1]
	}

	if chunkSize < effectiveBatch {
		return s.processPartialChunk(ctx, gp, chunkStart, chunkSize)
	}

	return s.runWeekLoop(ctx, gp, chunkStart, chunkSize)
}

// processPartialChunk handles the last chunk when it is smaller than the
// batch size. Creates smaller tensor wrappers over the existing buffers
// and rebinds them for inference. Does not restore the full-size binding
// because partial chunks are always the final iteration of the outer loop.
func (s *HeatmapInferenceService) processPartialChunk(
	ctx context.Context,
	gp *gridParams,
	chunkStart, chunkSize int,
) error {
	partialInput, err := ort.NewTensor(
		ort.NewShape(int64(chunkSize), 3), gp.inputData[:chunkSize*3])
	if err != nil {
		return errors.Newf("heatmap service: failed to create partial input tensor: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelLoad).
			Build()
	}
	defer func() { _ = partialInput.Destroy() }()

	partialOutput, err := ort.NewTensor(
		ort.NewShape(int64(chunkSize), int64(gp.numSpecies)),
		gp.outputData[:chunkSize*gp.numSpecies])
	if err != nil {
		return errors.Newf("heatmap service: failed to create partial output tensor: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelLoad).
			Build()
	}
	defer func() { _ = partialOutput.Destroy() }()

	if err := gp.binding.BindInput(gp.inputName, partialInput); err != nil {
		return errors.Newf("heatmap service: failed to bind partial input: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelLoad).
			Build()
	}
	if err := gp.binding.BindOutput(gp.outputName, partialOutput); err != nil {
		return errors.Newf("heatmap service: failed to bind partial output: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelLoad).
			Build()
	}

	return s.runWeekLoop(ctx, gp, chunkStart, chunkSize)
}

// runWeekLoop iterates over all weeks for a single chunk, running inference
// and extracting the target species scores.
func (s *HeatmapInferenceService) runWeekLoop(
	ctx context.Context,
	gp *gridParams,
	chunkStart, chunkSize int,
) error {
	weekIdx := 0
	for week := 1; week <= gp.totalWeeks; week += gp.stride {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.closed.Load() {
			return errSessionClosedDuringComputation
		}

		weekF := float32(week)
		limit := chunkSize * 3
		for i := 2; i < limit; i += 3 {
			gp.inputData[i] = weekF
		}

		if err := gp.session.RunWithBinding(gp.binding); err != nil {
			return errors.Newf("heatmap service: inference failed at week %d chunk %d: %v",
				week, chunkStart, err).
				Component("classifier.heatmap_service").
				Category(errors.CategoryModelLoad).
				Build()
		}

		weekOffset := weekIdx * gp.totalCells
		for i := range chunkSize {
			gp.result[weekOffset+chunkStart+i] = gp.outputData[i*gp.numSpecies+gp.speciesIdx]
		}

		weekIdx++
	}

	return nil
}

// NumSpecies returns the number of species in the geomodel output.
func (s *HeatmapInferenceService) NumSpecies() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.labels)
}

// SpeciesIndex returns the output index for a species label.
func (s *HeatmapInferenceService) SpeciesIndex(label string) (int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx, ok := s.indexMap[label]
	return idx, ok
}

// Rebuild replaces the ONNX session with a new one for an updated model.
// Waits for all in-flight inferences to complete before destroying the old session.
func (s *HeatmapInferenceService) Rebuild(modelPath string, labels []string) error {
	meta, err := loadHeatmapModelMeta(modelPath)
	if err != nil {
		return err
	}

	newSession, err := createHeatmapSession(modelPath, meta.inputNames, meta.outputNames)
	if err != nil {
		return err
	}

	s.mu.Lock()
	oldSession := s.session.Swap(newSession)
	s.labels = labels
	s.indexMap = buildSpeciesIndexMap(labels)
	s.inputName = meta.inputName
	s.outputName = meta.outputName
	s.mu.Unlock()

	// Wait for in-flight inferences on old session to finish
	s.inflight.Wait()

	if oldSession != nil {
		_ = oldSession.Destroy()
	}

	return nil
}

// Close shuts down the service and releases all resources.
// Sets closed flag first so new callers bail after inflight.Add,
// then swaps session to nil so very-late arrivals see nil,
// then waits for in-flight inferences before destroying.
func (s *HeatmapInferenceService) Close() error {
	s.closed.Store(true)

	oldSession := s.session.Swap(nil)

	// Wait for in-flight inferences to complete (they hold local session ref)
	s.inflight.Wait()

	if oldSession != nil {
		return oldSession.Destroy()
	}
	return nil
}

// heatmapService is the module-level singleton for heatmap inference.
var (
	heatmapServiceInstance *HeatmapInferenceService
	heatmapServiceMu       sync.Mutex
)

// GetHeatmapService returns the singleton heatmap inference service,
// creating it lazily on first use. Returns nil if the range filter model
// is not configured.
func (o *Orchestrator) GetHeatmapService() *HeatmapInferenceService {
	heatmapServiceMu.Lock()
	defer heatmapServiceMu.Unlock()

	if heatmapServiceInstance != nil {
		return heatmapServiceInstance
	}

	// Get model path and labels from current range filter config
	settings := o.Settings
	rfSettings := settings.BirdNET.RangeFilter

	if rfSettings.ModelPath == "" {
		return nil
	}

	o.mu.RLock()
	primary := o.primary
	o.mu.RUnlock()
	if primary == nil {
		return nil
	}

	// Get labels from the mapped range filter
	primary.mu.Lock()
	rf := primary.rangeFilter
	primary.mu.Unlock()
	if rf == nil {
		return nil
	}

	mrf, ok := rf.(*mappedRangeFilter)
	if !ok {
		return nil
	}

	labels := mrf.geomodelLabels
	if len(labels) == 0 {
		return nil
	}

	// Resolve model path (same logic as initializeV3GeoModel)
	modelPath := os.ExpandEnv(rfSettings.ModelPath)
	modelPath, err := conf.ExpandTildePath(modelPath)
	if err != nil {
		GetLogger().Error("Failed to expand heatmap model path", logger.Error(err))
		return nil
	}

	log := GetLogger()
	log.Info("Creating dedicated heatmap inference service",
		logger.String("model_path", modelPath),
		logger.Int("species_count", len(labels)),
		logger.Int("intra_op_threads", heatmapIntraOpThreads),
		logger.Int("inter_op_threads", heatmapInterOpThreads))

	svc, err := NewHeatmapInferenceService(modelPath, labels)
	if err != nil {
		log.Error("Failed to create heatmap inference service", logger.Error(err))
		return nil
	}

	heatmapServiceInstance = svc
	return svc
}

// RebuildHeatmapService rebuilds the heatmap inference service with updated model/labels.
// Called from the range filter reload callback.
func RebuildHeatmapService(modelPath string, labels []string) {
	heatmapServiceMu.Lock()
	defer heatmapServiceMu.Unlock()

	if heatmapServiceInstance == nil {
		return
	}

	log := GetLogger()
	log.Info("Rebuilding heatmap inference service",
		logger.String("model_path", modelPath),
		logger.Int("species_count", len(labels)))

	if err := heatmapServiceInstance.Rebuild(modelPath, labels); err != nil {
		log.Error("Failed to rebuild heatmap inference service", logger.Error(err))
		// Close broken service; next request will create a fresh one
		_ = heatmapServiceInstance.Close()
		heatmapServiceInstance = nil
	}
}

// CloseHeatmapService shuts down the heatmap inference service.
// Called from Orchestrator.Delete().
func CloseHeatmapService() {
	heatmapServiceMu.Lock()
	defer heatmapServiceMu.Unlock()

	if heatmapServiceInstance != nil {
		_ = heatmapServiceInstance.Close()
		heatmapServiceInstance = nil
	}
}
