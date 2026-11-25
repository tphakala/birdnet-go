package analysis

import (
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

// wireClipDenoiseRecorder hands the processor the v2 detection repository that
// stores whether a saved clip was denoised. Only a v2-only process has the column:
// the mode is fixed for the life of the process (a migration that finishes mid-run
// keeps the legacy store until restart), and in migration mode the v2 manager
// belongs to the migration. Without a recorder the processor leaves the marker
// unwritten. Call it before the processor starts and before the HTTP server serves.
func (s *APIServerService) wireClipDenoiseRecorder() {
	if s.dbService == nil || !s.dbService.IsV2OnlyMode() || s.proc == nil {
		return
	}
	mgr := s.dbService.V2Manager()
	if mgr == nil {
		return
	}
	s.proc.SetClipDenoiseRecorder(repository.NewDetectionRepository(mgr.DB(), nil, mgr.TablePrefix() != "", mgr.IsMySQL()))
}
