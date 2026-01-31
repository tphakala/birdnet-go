package repository

import (
	"context"

	"gorm.io/gorm"
)

// Transactor provides transaction support for repository operations.
// Use this when you need to execute multiple repository operations atomically.
//
// Example usage:
//
//	err := transactor.WithTransaction(ctx, func(tx *gorm.DB) error {
//	    // Create repositories with the transaction
//	    detRepo := NewDetectionRepository(tx, false)
//	    labelRepo := NewLabelRepository(tx, false)
//
//	    // All operations use the same transaction
//	    if err := detRepo.Save(ctx, detection); err != nil {
//	        return err // Rolls back
//	    }
//	    return labelRepo.GetOrCreate(ctx, ...) // Commits if successful
//	})
type Transactor interface {
	// WithTransaction executes fn within a database transaction.
	// If fn returns an error, the transaction is rolled back.
	// If fn returns nil, the transaction is committed.
	WithTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error

	// DB returns the underlying database connection.
	// Use this to create repository instances outside of transactions.
	DB() *gorm.DB
}

// transactor implements Transactor.
type transactor struct {
	db *gorm.DB
}

// NewTransactor creates a new Transactor with the given database connection.
func NewTransactor(db *gorm.DB) Transactor {
	return &transactor{db: db}
}

// WithTransaction executes fn within a database transaction.
func (t *transactor) WithTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return t.db.WithContext(ctx).Transaction(fn)
}

// DB returns the underlying database connection.
func (t *transactor) DB() *gorm.DB {
	return t.db
}
