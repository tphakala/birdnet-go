package analysis

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/analysis/privacyrollup"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// startPrivacyRollup starts the writer of the hourly privacy filter rollup and
// hands it to the processor. Only a v2-only process writes: the mode is fixed
// for the life of the process (a migration that finishes mid-run keeps the
// legacy store until restart), and in migration mode the v2 manager belongs to
// the migration. Without a writer the processor's recorder stays nil and every
// hit site is a no-op. Call it before the processor starts.
func (s *APIServerService) startPrivacyRollup() {
	if s.dbService == nil || !s.dbService.IsV2OnlyMode() || s.proc == nil {
		return
	}
	mgr := s.dbService.V2Manager()
	if mgr == nil {
		return
	}
	useV2Prefix := mgr.TablePrefix() != ""
	isMySQL := mgr.IsMySQL()
	rows := repository.NewPrivacyFilterHourlyRepository(mgr.DB(), nil, useV2Prefix, isMySQL)
	sources := repository.NewAudioSourceRepository(mgr.DB(), nil, useV2Prefix, isMySQL)

	// The resolver captures only the repository: Stop clears s.proc before the
	// writer's final flush.
	resolve := func(ctx context.Context, sourceURI, nodeName string, displayName *string) (uint, error) {
		src, err := sources.GetOrCreate(ctx, sourceURI, nodeName, displayName, entities.SourceType(""))
		if err != nil {
			return 0, err
		}
		return src.ID, nil
	}

	w := privacyrollup.NewWriter(rows, resolve, nil)
	w.Start()
	s.privacyRollup = w
	s.proc.SetPrivacyRollup(w)
	GetLogger().Debug("privacy filter rollup started",
		logger.String("operation", "privacy_rollup_start"))
}

// stopPrivacyRollup stops the writer after its final flush. Call it after the
// processor has shut down (the last flush cycle that can emit discards) and
// before the database closes.
func (s *APIServerService) stopPrivacyRollup(ctx context.Context) error {
	w := s.privacyRollup
	s.privacyRollup = nil
	return w.Stop(ctx)
}
