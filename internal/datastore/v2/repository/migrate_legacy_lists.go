package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
	"gorm.io/gorm"
)

// MigrateLegacyAlertLists scans the database alert conditions and migrates any
// free-text species list values (comma, semicolon, or newline delimited) into
// standard database-backed Managed Species Lists. It resolves common names
// to scientific names using the provided locale.
func MigrateLegacyAlertLists(ctx context.Context, db *gorm.DB, locale string) error {
	log := logger.Global().Module("datastore")

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var conditions []entities.AlertCondition
		// Query conditions where property is species_name or scientific_name,
		// operator is in or not_in, and the value is not empty and does not start with list:
		err := tx.Where("(property = ? OR property = ?) AND (operator = ? OR operator = ?) AND value != ? AND value NOT LIKE ?",
			"species_name", "scientific_name", "in", "not_in", "", "list:%").
			Find(&conditions).Error
		if err != nil {
			return fmt.Errorf("failed to query legacy alert conditions: %w", err)
		}

		if len(conditions) == 0 {
			return nil
		}

		log.Info("found legacy alert conditions to migrate", logger.Int("count", len(conditions)))

		for _, cond := range conditions {
			if err := migrateSingleCondition(tx, log, cond, locale); err != nil {
				return err
			}
		}

		return nil
	})
}

func migrateSingleCondition(tx *gorm.DB, log logger.Logger, cond entities.AlertCondition, locale string) error {
	// Parse the list value into clean normalized species names
	var rawNames []string
	for item := range strings.FieldsFuncSeq(cond.Value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	}) {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			rawNames = append(rawNames, trimmed)
		}
	}

	if len(rawNames) == 0 {
		return nil
	}

	// Clean and de-duplicate
	seen := make(map[string]struct{})
	var cleanNames []string
	for _, name := range rawNames {
		nameNorm := strings.TrimSpace(name)
		if _, ok := seen[nameNorm]; !ok {
			seen[nameNorm] = struct{}{}
			cleanNames = append(cleanNames, nameNorm)
		}
	}

	// Fetch parent rule name to give the migrated list a helpful name
	var rule entities.AlertRule
	ruleName := "Migrated Alert List"
	if tx.First(&rule, cond.RuleID).Error == nil && rule.Name != "" {
		ruleName = fmt.Sprintf("Migrated List (%s)", rule.Name)
	}

	// Create the new SpeciesList
	list := entities.SpeciesList{
		Name:        ruleName,
		Description: fmt.Sprintf("Automatically migrated from legacy alert condition: %s %s", cond.Property, cond.Operator),
		IsSystem:    false, // User-owned, editable/fixable!
	}
	if err := tx.Create(&list).Error; err != nil {
		return fmt.Errorf("failed to create migrated species list: %w", err)
	}

	// Add members
	var members []entities.SpeciesListMember
	for _, name := range cleanNames {
		// Try to resolve as common name in specified locale
		resolved := openfauna.LookupScientificNames([]string{name}, locale)
		if sciNames, found := resolved[name]; found && len(sciNames) > 0 {
			for _, sci := range sciNames {
				members = append(members, entities.SpeciesListMember{
					ListID:         list.ID,
					ScientificName: strings.ToLower(sci),
				})
			}
		} else {
			nameLower := strings.ToLower(name)
			// Check if it's already a valid scientific name
			if _, foundMeta := openfauna.LookupMeta(nameLower); foundMeta {
				members = append(members, entities.SpeciesListMember{
					ListID:         list.ID,
					ScientificName: nameLower,
				})
			} else {
				// Unrecognized - keep it but emit warning
				log.Warn("migrated species name not recognized by OpenFauna; importing as unrecognized",
					logger.String("species", name),
					logger.String("list_name", ruleName))
				members = append(members, entities.SpeciesListMember{
					ListID:         list.ID,
					ScientificName: nameLower,
				})
			}
		}
	}

	if err := tx.Create(&members).Error; err != nil {
		return fmt.Errorf("failed to insert migrated list members: %w", err)
	}

	// Update the alert condition to point to the new list
	cond.Value = fmt.Sprintf("list:%d", list.ID)
	if err := tx.Save(&cond).Error; err != nil {
		return fmt.Errorf("failed to update alert condition %d: %w", cond.ID, err)
	}

	log.Info("successfully migrated legacy alert condition",
		logger.Int64("condition_id", int64(cond.ID)),
		logger.Int64("new_list_id", int64(list.ID)),
		logger.String("list_name", ruleName))

	return nil
}
