package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestModelsEnabledChanged pins the reconcile detector: it compares the SET of known models
// (by registry ID), so a reorder, a case change, or an entry that resolves to no known model
// is not a change, while adding or removing a known model is (Phase 4).
func TestModelsEnabledChanged(t *testing.T) {
	t.Parallel()

	mk := func(ids ...string) *conf.Settings {
		s := &conf.Settings{}
		s.Models.Enabled = ids
		return s
	}
	nilEnabled := &conf.Settings{}
	nilEnabled.Models.Enabled = nil

	tests := []struct {
		name     string
		old, cur *conf.Settings
		want     bool
	}{
		{"same set, different order", mk("birdnet", "perch_v2"), mk("perch_v2", "birdnet"), false},
		{"same set, different case", mk("birdnet"), mk("BirdNet"), false},
		{"added known model", mk("birdnet"), mk("birdnet", "perch_v2"), true},
		{"removed known model", mk("birdnet", "perch_v2"), mk("birdnet"), true},
		{"added unknown model is ignored", mk("birdnet"), mk("birdnet", "nonexistent_model"), false},
		{"nil vs empty is no change", nilEnabled, mk(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, modelsEnabledChanged(tt.old, tt.cur))
		})
	}
}
