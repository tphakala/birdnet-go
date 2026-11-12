package analysis

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/analysis/noisebank"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// startNoiseExamples loads the learned noise examples and hands the service to the
// processor, which compares every bat window with them, and to the API, which marks
// detections as noise through it. Only a v2-only process has the table: the mode is
// fixed for the life of the process (a migration that finishes mid-run keeps the
// legacy store until restart), and in migration mode the v2 manager belongs to the
// migration. When the examples cannot be loaded the service is not published, so
// the feature is unavailable for this run and the gate stays off; nothing is
// deleted. Call it after the processor is created and before it starts and before
// the HTTP server serves, so no request and no analysis window sees a half
// initialised service.
func (s *APIServerService) startNoiseExamples(ctx context.Context, embedder noisebank.Embedder) {
	if s.dbService == nil || !s.dbService.IsV2OnlyMode() || s.proc == nil || embedder == nil {
		return
	}
	mgr := s.dbService.V2Manager()
	if mgr == nil {
		return
	}
	useV2Prefix := mgr.TablePrefix() != ""
	svc := noisebank.NewService(noisebank.Config{
		Embedder:   embedder,
		Examples:   repository.NewNoiseExampleRepository(mgr.DB(), nil, useV2Prefix),
		Detections: repository.NewDetectionRepository(mgr.DB(), nil, useV2Prefix, mgr.IsMySQL()),
	})
	if err := svc.Start(ctx); err != nil {
		GetLogger().Error("noise examples unavailable: loading the stored examples failed",
			logger.Error(err),
			logger.String("operation", "noise_examples_start"))
		return
	}
	s.noiseExamples = svc
	s.proc.SetNoiseBank(svc.Bank())
	s.proc.SetNoiseExamples(svc)
}

// stopNoiseExamples stops the learning worker. Call it after the HTTP server has
// stopped and before the database closes.
func (s *APIServerService) stopNoiseExamples(ctx context.Context) error {
	svc := s.noiseExamples
	s.noiseExamples = nil
	if svc == nil {
		return nil
	}
	return svc.Stop(ctx)
}
