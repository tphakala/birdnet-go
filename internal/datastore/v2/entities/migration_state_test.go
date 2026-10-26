package entities

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMigrationState_AuxiliaryDataOutcome(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	other := started.Add(time.Minute)
	state := func(status AuxiliaryDataStatus, s MigrationStatus, startedAt, auxStartedAt *time.Time) *MigrationState {
		return &MigrationState{State: s, StartedAt: startedAt, AuxiliaryDataStatus: status, AuxiliaryDataStartedAt: auxStartedAt}
	}

	tests := []struct {
		name  string
		state *MigrationState
		want  AuxiliaryDataStatus
	}{
		{"recorded for the current attempt", state(AuxiliaryDataStatusSucceeded, MigrationStatusCompleted, &started, &started), AuxiliaryDataStatusSucceeded},
		{"failed while validating", state(AuxiliaryDataStatusFailed, MigrationStatusValidating, &started, &started), AuxiliaryDataStatusFailed},
		{"not required", state(AuxiliaryDataStatusNotRequired, MigrationStatusCompleted, &started, &started), AuxiliaryDataStatusNotRequired},
		{"nothing stored", state(AuxiliaryDataStatusUnknown, MigrationStatusCompleted, &started, &started), AuxiliaryDataStatusUnknown},
		{"idle row with leftover columns", state(AuxiliaryDataStatusSucceeded, MigrationStatusIdle, &started, &started), AuxiliaryDataStatusUnknown},
		{"attempt marker differs", state(AuxiliaryDataStatusSucceeded, MigrationStatusCompleted, &other, &started), AuxiliaryDataStatusUnknown},
		{"no attempt marker", state(AuxiliaryDataStatusSucceeded, MigrationStatusCompleted, &started, nil), AuxiliaryDataStatusUnknown},
		{"no start time", state(AuxiliaryDataStatusSucceeded, MigrationStatusCompleted, nil, &started), AuxiliaryDataStatusUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.state.AuxiliaryDataOutcome())
		})
	}
}
