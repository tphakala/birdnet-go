// dynamic_threshold_migrate.go: one-time migration from per-model to per-species
// dynamic threshold tracking.
//
// Historically dynamic thresholds were keyed by (species_name, model_name) via the
// composite unique index idx_dt_species_model, so a species detected by several
// models produced one row per model. Tracking is now per species (the learned
// "confirmed present" state is model-independent), so this migration consolidates
// the existing per-model rows to one row per species and removes the model_name
// columns before AutoMigrate creates the new single-column unique index.
package datastore

import (
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
)

// legacyDynamicThresholdRow mirrors the pre-migration dynamic_thresholds schema,
// including the model_name column that per-species tracking removes. It is used
// only to read and rewrite existing rows during the migration.
type legacyDynamicThresholdRow struct {
	ID             uint
	SpeciesName    string
	ModelName      string
	ScientificName string
	Level          int
	CurrentValue   float64
	BaseThreshold  float64
	HighConfCount  int
	ValidHours     int
	ExpiresAt      time.Time
	LastTriggered  time.Time
	FirstCreated   time.Time
	UpdatedAt      time.Time
	TriggerCount   int
}

// TableName pins the row struct to the dynamic_thresholds table.
func (legacyDynamicThresholdRow) TableName() string { return "dynamic_thresholds" }

// migrateDynamicThresholdsToPerSpecies consolidates per-model dynamic threshold
// rows to one row per species and drops the model_name columns. It is idempotent
// (a no-op once model_name is gone) and a no-op on a fresh install. It must run
// after reconcileLegacyUniqueIndexes and before AutoMigrate, so AutoMigrate finds
// no duplicates when it creates the new unique(species_name) index.
func migrateDynamicThresholdsToPerSpecies(db *gorm.DB, log logger.Logger) error {
	m := db.Migrator()

	// Fresh install: nothing to migrate.
	if !m.HasTable("dynamic_thresholds") {
		return nil
	}
	// Already migrated: the model_name column is gone. HasColumn inspects the live
	// table by name (the model only resolves the table name), so the current
	// entity, which no longer declares model_name, is the right thing to pass.
	if !m.HasColumn(&DynamicThreshold{}, "model_name") {
		return nil
	}

	log.Info("Migrating dynamic thresholds to per-species tracking")

	// 1. Consolidate per-model rows to one row per species.
	if err := consolidateDynamicThresholdRows(db, log); err != nil {
		// Safety valve: dynamic thresholds are ephemeral, re-learnable state that
		// expires within ValidHours. Rather than brick startup on a later unique-index
		// creation failure, clear the table so AutoMigrate can succeed. The learned
		// state simply rebuilds from new detections.
		log.Warn("dynamic threshold consolidation failed; clearing table so migration can proceed",
			logger.Error(err))
		if derr := db.Exec("DELETE FROM dynamic_thresholds").Error; derr != nil {
			return fmt.Errorf("clear dynamic_thresholds after consolidation failure: %w", derr)
		}
	}

	// 2. Drop the composite unique index. Required before dropping model_name on
	//    SQLite, where a column referenced by an index cannot be dropped.
	if m.HasIndex(&DynamicThreshold{}, "idx_dt_species_model") {
		if err := m.DropIndex(&DynamicThreshold{}, "idx_dt_species_model"); err != nil {
			log.Warn("failed to drop legacy composite index idx_dt_species_model", logger.Error(err))
		}
	}

	// 3. Drop the model_name columns via native ALTER TABLE DROP COLUMN. Both SQLite
	//    (3.35+) and MySQL accept this identical syntax; it avoids GORM's SQLite
	//    table-recreation path, which mishandles a raw-created legacy table. Best-effort:
	//    a lingering column is harmless because the legacy schema declares it NOT NULL
	//    DEFAULT 'BirdNET', so inserts that omit it use the default.
	if err := db.Exec("ALTER TABLE dynamic_thresholds DROP COLUMN model_name").Error; err != nil {
		log.Warn("failed to drop model_name from dynamic_thresholds", logger.Error(err))
	}
	if m.HasColumn(&ThresholdEvent{}, "model_name") {
		if err := db.Exec("ALTER TABLE threshold_events DROP COLUMN model_name").Error; err != nil {
			log.Warn("failed to drop model_name from threshold_events", logger.Error(err))
		}
	}

	log.Info("Dynamic thresholds migrated to per-species tracking")
	return nil
}

// consolidateDynamicThresholdRows collapses all rows for the same species (matched
// case-insensitively) into a single row, run in one transaction.
func consolidateDynamicThresholdRows(db *gorm.DB, log logger.Logger) error {
	var rows []legacyDynamicThresholdRow
	if err := db.Find(&rows).Error; err != nil {
		return fmt.Errorf("load dynamic_thresholds for consolidation: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	groups := make(map[string][]legacyDynamicThresholdRow, len(rows))
	for i := range rows {
		key := strings.ToLower(rows[i].SpeciesName)
		groups[key] = append(groups[key], rows[i])
	}

	consolidated := 0
	err := db.Transaction(func(tx *gorm.DB) error {
		for key, group := range groups {
			if len(group) == 1 {
				// Single row: normalize its species_name to the lowercase key if it
				// differs, so the new case-sensitive unique(species_name) index and the
				// processor's lowercase lookups agree (a mixed-case legacy row would
				// otherwise let a later lowercase insert create a duplicate).
				if group[0].SpeciesName != key {
					if err := tx.Model(&legacyDynamicThresholdRow{}).
						Where("id = ?", group[0].ID).
						Update("species_name", key).Error; err != nil {
						return err
					}
				}
				continue
			}
			winner := consolidateDynamicThresholdGroup(group)

			ids := make([]uint, 0, len(group))
			for i := range group {
				ids = append(ids, group[i].ID)
			}
			// Delete every row in the group, then insert the merged winner, so
			// case-variant species names also collapse onto one lowercase row.
			if err := tx.Where("id IN ?", ids).Delete(&legacyDynamicThresholdRow{}).Error; err != nil {
				return err
			}
			winner.ID = 0
			if err := tx.Create(&winner).Error; err != nil {
				return err
			}
			consolidated++
		}
		return nil
	})
	if err != nil {
		return err
	}

	if consolidated > 0 {
		log.Info("Consolidated per-model dynamic thresholds to per-species",
			logger.Int("species_consolidated", consolidated))
	}
	return nil
}

// consolidateDynamicThresholdGroup merges a group of per-model rows for one species.
// The winner is the row with the highest learned level (ties broken by the later
// expiry, then the later update); counters and timestamps are merged across the
// group so the strongest confirmation from any model becomes the species' state.
func consolidateDynamicThresholdGroup(group []legacyDynamicThresholdRow) legacyDynamicThresholdRow {
	winner := group[0]
	for i := 1; i < len(group); i++ {
		r := group[i]
		switch {
		case r.Level != winner.Level:
			if r.Level > winner.Level {
				winner = r
			}
		case !r.ExpiresAt.Equal(winner.ExpiresAt):
			if r.ExpiresAt.After(winner.ExpiresAt) {
				winner = r
			}
		case r.UpdatedAt.After(winner.UpdatedAt):
			winner = r
		}
	}

	// Merge aggregate fields across the whole group. Counters use max (not sum):
	// each per-model row counted the same audio once per model, so summing would
	// inflate the learned state.
	for i := range group {
		r := &group[i]
		if r.ExpiresAt.After(winner.ExpiresAt) {
			winner.ExpiresAt = r.ExpiresAt
		}
		if r.HighConfCount > winner.HighConfCount {
			winner.HighConfCount = r.HighConfCount
		}
		if r.TriggerCount > winner.TriggerCount {
			winner.TriggerCount = r.TriggerCount
		}
		if r.LastTriggered.After(winner.LastTriggered) {
			winner.LastTriggered = r.LastTriggered
		}
		if !r.FirstCreated.IsZero() && (winner.FirstCreated.IsZero() || r.FirstCreated.Before(winner.FirstCreated)) {
			winner.FirstCreated = r.FirstCreated
		}
		if winner.ScientificName == "" && r.ScientificName != "" {
			winner.ScientificName = r.ScientificName
		}
	}

	winner.SpeciesName = strings.ToLower(winner.SpeciesName)
	winner.UpdatedAt = time.Now()
	return winner
}
