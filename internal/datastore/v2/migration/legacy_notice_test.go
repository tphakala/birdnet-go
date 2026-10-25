package migration

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

func TestLegacyNotice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		legacy bool
		status entities.MigrationStatus
		want   LegacyNoticeKind
	}{
		{"legacy idle", true, entities.MigrationStatusIdle, LegacyNoticeNotStarted},
		{"legacy initializing", true, entities.MigrationStatusInitializing, LegacyNoticeInProgress},
		{"legacy dual_write", true, entities.MigrationStatusDualWrite, LegacyNoticeInProgress},
		{"legacy migrating", true, entities.MigrationStatusMigrating, LegacyNoticeInProgress},
		{"legacy validating", true, entities.MigrationStatusValidating, LegacyNoticeInProgress},
		{"legacy cutover", true, entities.MigrationStatusCutover, LegacyNoticeInProgress},
		{"legacy paused", true, entities.MigrationStatusPaused, LegacyNoticeNeedsAttention},
		{"legacy failed", true, entities.MigrationStatusFailed, LegacyNoticeNeedsAttention},
		{"legacy completed", true, entities.MigrationStatusCompleted, LegacyNoticeRestartRequired},
		{"migrated install, completed", false, entities.MigrationStatusCompleted, LegacyNoticeNone},
		{"fresh v2 install, idle", false, entities.MigrationStatusIdle, LegacyNoticeNone},
		{"legacy empty status", true, "", LegacyNoticeNone},
		{"legacy unexpected status", true, "something_new", LegacyNoticeNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, LegacyNotice(tt.legacy, tt.status))
		})
	}
}
