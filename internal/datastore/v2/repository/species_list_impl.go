package repository

import (
	"context"
	"fmt"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
)

type speciesListRepository struct {
	db      *gorm.DB
	metrics *datastore.Metrics
}

// NewSpeciesListRepository creates a new SpeciesListRepository.
func NewSpeciesListRepository(db *gorm.DB, metrics *datastore.Metrics) SpeciesListRepository {
	return &speciesListRepository{db: db, metrics: metrics}
}

// ListSpeciesLists returns all species lists, preloading their members.
func (r *speciesListRepository) ListSpeciesLists(ctx context.Context) ([]entities.SpeciesList, error) {
	var lists []entities.SpeciesList
	if err := r.db.WithContext(ctx).Preload("Members").Order("id ASC").Find(&lists).Error; err != nil {
		return nil, fmt.Errorf("failed to list species lists: %w", err)
	}
	return lists, nil
}

// GetSpeciesList returns a single species list by ID, preloading its members.
func (r *speciesListRepository) GetSpeciesList(ctx context.Context, id uint) (*entities.SpeciesList, error) {
	var list entities.SpeciesList
	if err := r.db.WithContext(ctx).Preload("Members").First(&list, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSpeciesListNotFound
		}
		return nil, fmt.Errorf("failed to get species list %d: %w", id, err)
	}
	return &list, nil
}

// CreateSpeciesList creates a new species list and its members.
func (r *speciesListRepository) CreateSpeciesList(ctx context.Context, list *entities.SpeciesList) error {
	return datastore.RetryOnLock(ctx, "v2_create_species_list", func() error {
		if err := r.db.WithContext(ctx).Create(list).Error; err != nil {
			return fmt.Errorf("failed to create species list: %w", err)
		}
		return nil
	}, r.metrics)
}

// UpdateSpeciesList updates a species list, deleting old members first.
func (r *speciesListRepository) UpdateSpeciesList(ctx context.Context, list *entities.SpeciesList) error {
	if list.ID == 0 {
		return fmt.Errorf("failed to update species list: missing list ID")
	}
	return datastore.RetryTransactionOnLock(ctx, r.db, "v2_update_species_list", func(tx *gorm.DB) error {
		if err := tx.Where("list_id = ?", list.ID).Delete(&entities.SpeciesListMember{}).Error; err != nil {
			return fmt.Errorf("failed to delete old list members: %w", err)
		}
		// Reset IDs of members to prevent stale primary keys on retry, and set ListID
		for i := range list.Members {
			list.Members[i].ID = 0
			list.Members[i].ListID = list.ID
		}
		// Update header fields explicitly to avoid overwriting CreatedAt and other database columns with zero values.
		if err := tx.Model(&entities.SpeciesList{}).Where("id = ?", list.ID).Updates(map[string]any{
			"name":        list.Name,
			"description": list.Description,
		}).Error; err != nil {
			return fmt.Errorf("failed to update species list header: %w", err)
		}
		// Insert members separately
		if len(list.Members) > 0 {
			if err := tx.Create(&list.Members).Error; err != nil {
				return fmt.Errorf("failed to insert new list members: %w", err)
			}
		}
		return nil
	}, r.metrics)
}

// DeleteSpeciesList deletes a species list (members are cascade deleted).
func (r *speciesListRepository) DeleteSpeciesList(ctx context.Context, id uint) error {
	return datastore.RetryOnLock(ctx, "v2_delete_species_list", func() error {
		result := r.db.WithContext(ctx).Delete(&entities.SpeciesList{}, id)
		if result.Error != nil {
			return fmt.Errorf("failed to delete species list %d: %w", id, result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrSpeciesListNotFound
		}
		return nil
	}, r.metrics)
}

// ResolveSpeciesList returns the slice of scientific names in the list.
func (r *speciesListRepository) ResolveSpeciesList(ctx context.Context, id uint) ([]string, error) {
	var names []string
	if err := r.db.WithContext(ctx).Model(&entities.SpeciesListMember{}).Where("list_id = ?", id).Pluck("scientific_name", &names).Error; err != nil {
		return nil, fmt.Errorf("failed to resolve species list %d: %w", id, err)
	}
	return names, nil
}
