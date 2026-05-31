package analysis

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// birdNETAnalyzerName is the service name used for logging and diagnostics.
const birdNETAnalyzerName = "birdnet-analyzer"

// BirdNETAnalyzer wraps BirdNET model initialization as an app.Service
// and implements app.Analyzer for source-to-analyzer routing.
type BirdNETAnalyzer struct {
	settings     *conf.Settings
	bn           *classifier.Orchestrator
	modelManager *classifier.ModelManager
}

// NewBirdNETAnalyzer creates a new BirdNETAnalyzer with the given settings.
// The analyzer is not started; call Start() to initialize the BirdNET model.
func NewBirdNETAnalyzer(settings *conf.Settings) *BirdNETAnalyzer {
	return &BirdNETAnalyzer{settings: settings}
}

// Name returns a human-readable identifier for logging and diagnostics.
func (a *BirdNETAnalyzer) Name() string {
	return birdNETAnalyzerName
}

// Start initializes the BirdNET interpreter and builds the species range filter.
// Model initialization failures are non-retryable (missing files, insufficient resources).
func (a *BirdNETAnalyzer) Start(_ context.Context) error {
	bn, err := classifier.NewOrchestrator(a.settings)
	if err != nil {
		return errors.New(err).
			Component("analysis").
			Category(errors.CategoryModelInit).
			Context("operation", "initialize_birdnet").
			Build()
	}

	if err := classifier.BuildRangeFilter(bn); err != nil {
		bn.Delete()
		return errors.New(err).
			Component("analysis").
			Category(errors.CategoryModelInit).
			Context("operation", "build_range_filter").
			Build()
	}

	a.bn = bn

	events.Emit(context.Background(), "detection", "model_loaded", "BirdNET model loaded", map[string]any{
		"species_count": len(bn.Settings.BirdNET.Labels),
	})

	// Initialize ModelManager for the model gallery. Failure is non-fatal
	// because the gallery is an optional feature; core detection still works.
	a.initModelManager(bn)

	return nil
}

// Stop releases BirdNET model resources. It is safe to call before Start()
// or multiple times.
func (a *BirdNETAnalyzer) Stop(_ context.Context) error {
	if a.bn != nil {
		log := GetLogger()
		log.Info("stopping BirdNET model",
			logger.String("service", birdNETAnalyzerName))
		a.bn.Delete()
		a.bn = nil
	}
	a.modelManager = nil
	return nil
}

// Compatible returns true if this analyzer can process audio from the given source.
// BirdNETAnalyzer handles all source types except ultrasonic (bat detection).
func (a *BirdNETAnalyzer) Compatible(source app.AudioSource) bool {
	return source.Type != app.SourceTypeUltrasonic
}

// BirdNET returns the underlying classifier orchestrator, or nil if the analyzer
// has not been started. Callers must not use the returned pointer after Stop().
func (a *BirdNETAnalyzer) BirdNET() *classifier.Orchestrator {
	return a.bn
}

// ModelManager returns the model gallery manager, or nil if initialization
// was skipped or failed. Callers must not use the returned pointer after Stop().
func (a *BirdNETAnalyzer) ModelManager() *classifier.ModelManager {
	return a.modelManager
}

// initModelManager creates and populates the ModelManager for the model gallery.
// If the models directory cannot be determined or the manager fails to scan,
// a warning is logged and the analyzer continues without gallery support.
func (a *BirdNETAnalyzer) initModelManager(bn *classifier.Orchestrator) {
	log := GetLogger()

	modelsDir, ok := a.settings.ResolveModelsDir()
	if !ok {
		log.Warn("could not determine config or home directory; model gallery disabled",
			logger.String("service", birdNETAnalyzerName))
		return
	}

	a.modelManager = classifier.NewModelManager(modelsDir, bn, a.settings)
	a.modelManager.ScanInstalled()

	log.Info("model manager initialized",
		logger.String("models_dir", modelsDir),
		logger.String("service", birdNETAnalyzerName))
}
