package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
)

// SyncSystemLists mirrors species lists defined in config.yaml (Extended Capture,
// Dog Bark, and Daylight filters) into GORM database-backed read-only system lists.
func SyncSystemLists(ctx context.Context, db *gorm.DB, settings *conf.Settings) error {
	systemLists := []struct {
		name string
		desc string
		spec []string
	}{
		{
			name: "YAML: Extended Capture",
			desc: "Configured via realtime.extendedcapture.species settings in config.yaml",
			spec: settings.Realtime.ExtendedCapture.Species,
		},
		{
			name: "YAML: Dog Bark Filter",
			desc: "Configured via realtime.dogbarkfilter.species settings in config.yaml",
			spec: settings.Realtime.DogBarkFilter.Species,
		},
		{
			name: "YAML: Daylight Filter",
			desc: "Configured via realtime.daylightfilter.species settings in config.yaml",
			spec: settings.Realtime.DaylightFilter.Species,
		},
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, sl := range systemLists {
			// Find or create the system list header
			var list entities.SpeciesList
			err := tx.Where("name = ? AND is_system = ?", sl.name, true).First(&list).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					list = entities.SpeciesList{
						Name:        sl.name,
						Description: sl.desc,
						IsSystem:    true,
					}
					if createErr := tx.Create(&list).Error; createErr != nil {
						return fmt.Errorf("failed to create system list %s: %w", sl.name, createErr)
					}
				} else {
					return fmt.Errorf("failed to check system list existence: %w", err)
				}
			}

			// Clean and de-duplicate species from config
			seen := make(map[string]struct{})
			var cleanSpecies []string
			for _, sp := range sl.spec {
				spNorm := strings.ToLower(strings.TrimSpace(sp))
				if spNorm == "" || strings.HasPrefix(spNorm, "list:") {
					continue
				}
				if _, ok := seen[spNorm]; !ok {
					seen[spNorm] = struct{}{}
					cleanSpecies = append(cleanSpecies, spNorm)
				}
			}

			// Delete existing members for this list
			if delErr := tx.Where("list_id = ?", list.ID).Delete(&entities.SpeciesListMember{}).Error; delErr != nil {
				return fmt.Errorf("failed to clear previous members of system list %s: %w", sl.name, delErr)
			}

			// Insert new members
			if len(cleanSpecies) > 0 {
				var members []entities.SpeciesListMember
				for _, sp := range cleanSpecies {
					members = append(members, entities.SpeciesListMember{
						ListID:         list.ID,
						ScientificName: sp,
					})
				}
				if insErr := tx.Create(&members).Error; insErr != nil {
					return fmt.Errorf("failed to insert members for system list %s: %w", sl.name, insErr)
				}
			}
		}
		return nil
	})
}
