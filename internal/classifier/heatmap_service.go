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
)

// HeatmapInferenceService provides dedicated batch inference for heatmap
// computation, isolated from the real-time detection pipeline. It maintains
// its own ONNX session with multi-threaded configuration optimized for
// throughput rather than latency.
type HeatmapInferenceService struct {
	mu       sync.RWMutex
	session  atomic.Pointer[ort.DynamicAdvancedSession]
	labels   []string
	indexMap map[string]int // species label -> index in output
	inflight sync.WaitGroup
	closed   atomic.Bool
}

// NewHeatmapInferenceService creates a heatmap inference service with a
// dedicated multi-threaded ONNX session.
func NewHeatmapInferenceService(modelPath string, labels []string) (*HeatmapInferenceService, error) {
	session, err := createHeatmapSession(modelPath)
	if err != nil {
		return nil, err
	}

	s := &HeatmapInferenceService{
		labels: labels,
	}
	s.session.Store(session)
	s.indexMap = buildSpeciesIndexMap(labels)
	return s, nil
}

func createHeatmapSession(modelPath string) (*ort.DynamicAdvancedSession, error) {
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

// PredictBatchExtractSpecies runs batch inference and extracts only the target
// species scores, avoiding a full output copy. This is the primary hot path
// for heatmap computation.
func (s *HeatmapInferenceService) PredictBatchExtractSpecies(ctx context.Context, inputs []float32, batchSize, speciesIdx int, dst []float32) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	const inputWidth = 3
	if batchSize <= 0 || len(inputs) != batchSize*inputWidth {
		return errors.Newf("heatmap service: inputs length %d does not match batchSize %d * %d",
			len(inputs), batchSize, inputWidth).
			Component("classifier.heatmap_service").
			Category(errors.CategoryValidation).
			Build()
	}
	if len(dst) < batchSize {
		return errors.Newf("heatmap service: dst length %d < batchSize %d", len(dst), batchSize).
			Component("classifier.heatmap_service").
			Category(errors.CategoryValidation).
			Build()
	}

	s.inflight.Add(1)
	defer s.inflight.Done()

	// Re-check closed after inflight.Add to prevent use-after-free on shutdown
	if s.closed.Load() {
		return errors.Newf("heatmap service: session closed").
			Component("classifier.heatmap_service").
			Category(errors.CategoryValidation).
			Build()
	}

	session := s.session.Load()
	if session == nil {
		return errors.Newf("heatmap service: session not available").
			Component("classifier.heatmap_service").
			Category(errors.CategoryValidation).
			Build()
	}

	// Read numSpecies under RLock to prevent race with Rebuild
	s.mu.RLock()
	numSpecies := len(s.labels)
	s.mu.RUnlock()

	if speciesIdx < 0 || speciesIdx >= numSpecies {
		return errors.Newf("heatmap service: speciesIdx %d out of range [0, %d)", speciesIdx, numSpecies).
			Component("classifier.heatmap_service").
			Category(errors.CategoryValidation).
			Build()
	}

	inputTensor, err := ort.NewTensor(ort.NewShape(int64(batchSize), 3), inputs)
	if err != nil {
		return errors.Newf("heatmap service: failed to create input tensor: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelLoad).
			Build()
	}
	defer func() { _ = inputTensor.Destroy() }()

	outputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(int64(batchSize), int64(numSpecies)))
	if err != nil {
		return errors.Newf("heatmap service: failed to create output tensor: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelLoad).
			Build()
	}
	defer func() { _ = outputTensor.Destroy() }()

	if err := session.Run([]ort.Value{inputTensor}, []ort.Value{outputTensor}); err != nil {
		return errors.Newf("heatmap service: inference failed: %v", err).
			Component("classifier.heatmap_service").
			Category(errors.CategoryModelLoad).
			Build()
	}

	// Extract target species score directly from tensor-backed memory.
	// CRITICAL: complete extraction before outputTensor.Destroy() in defer.
	data := outputTensor.GetData()
	for i := range batchSize {
		dst[i] = data[i*numSpecies+speciesIdx]
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
	newSession, err := createHeatmapSession(modelPath)
	if err != nil {
		return err
	}

	s.mu.Lock()
	oldSession := s.session.Swap(newSession)
	s.labels = labels
	s.indexMap = buildSpeciesIndexMap(labels)
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
