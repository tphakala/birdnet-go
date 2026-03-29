package analysis

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// birdNETAnalyzerName is the service name used for logging and diagnostics.
const birdNETAnalyzerName = "birdnet-analyzer"

// BirdNETAnalyzer wraps BirdNET model initialization as an app.Service
// and implements app.Analyzer for source-to-analyzer routing.
type BirdNETAnalyzer struct {
	settings *conf.Settings
	bn       *classifier.BirdNET
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

// Start initializes the BirdNET interpreter, builds the species range filter,
// and initializes the audio conversion pool.
// Model initialization failures are non-retryable (missing files, insufficient resources).
func (a *BirdNETAnalyzer) Start(_ context.Context) error {
	bn, err := classifier.NewBirdNET(a.settings)
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

	if err := InitFloat32Pool(); err != nil {
		bn.Delete()
		return errors.New(err).
			Component("analysis").
			Category(errors.CategoryAudio).
			Context("operation", "initialize_float32_pool").
			Build()
	}

	a.bn = bn
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
	return nil
}

// Compatible returns true if this analyzer can process audio from the given source.
// BirdNETAnalyzer handles all source types except ultrasonic (bat detection).
func (a *BirdNETAnalyzer) Compatible(source app.AudioSource) bool {
	return source.Type != app.SourceTypeUltrasonic
}

// BirdNET returns the underlying BirdNET interpreter, or nil if the analyzer
// has not been started. Callers must not use the returned pointer after Stop().
func (a *BirdNETAnalyzer) BirdNET() *classifier.BirdNET {
	return a.bn
}
