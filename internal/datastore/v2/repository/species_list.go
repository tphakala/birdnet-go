package repository

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// SpeciesListRepository handles species list CRUD operations.
type SpeciesListRepository interface {
	ListSpeciesLists(ctx context.Context) ([]entities.SpeciesList, error)
	GetSpeciesList(ctx context.Context, id uint) (*entities.SpeciesList, error)
	CreateSpeciesList(ctx context.Context, list *entities.SpeciesList) error
	UpdateSpeciesList(ctx context.Context, list *entities.SpeciesList) error
	DeleteSpeciesList(ctx context.Context, id uint) error
	ResolveSpeciesList(ctx context.Context, id uint) ([]string, error)
}
