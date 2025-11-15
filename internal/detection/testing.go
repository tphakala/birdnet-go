package detection

import (
	"context"
	"errors"
	"sync"
)

// MockSpeciesRepository is a mock implementation of SpeciesRepository for testing.
// This is exported to allow other packages to use it during Phase 2 integration testing.
//
// Example usage:
//
//	mock := detection.NewMockSpeciesRepository()
//	mock.AddSpecies(&detection.Species{
//	    ID:             1,
//	    ScientificName: "Corvus brachyrhynchos",
//	    CommonName:     "American Crow",
//	})
//	cache := detection.NewSpeciesCache(mock, time.Hour)
type MockSpeciesRepository struct {
	species     map[string]*Species
	byID        map[uint]*Species
	byEbird     map[string]*Species
	callCounts  map[string]int
	mu          sync.Mutex
	shouldFail  bool
	listSpecies []*Species
}

// NewMockSpeciesRepository creates a new mock species repository for testing.
func NewMockSpeciesRepository() *MockSpeciesRepository {
	return &MockSpeciesRepository{
		species:    make(map[string]*Species),
		byID:       make(map[uint]*Species),
		byEbird:    make(map[string]*Species),
		callCounts: make(map[string]int),
	}
}

// GetByID retrieves species by database ID.
func (m *MockSpeciesRepository) GetByID(ctx context.Context, id uint) (*Species, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCounts["GetByID"]++

	if m.shouldFail {
		return nil, errors.New("mock error")
	}

	if sp, ok := m.byID[id]; ok {
		return sp, nil
	}
	return nil, errors.New("not found")
}

// GetByScientificName retrieves species by scientific name.
func (m *MockSpeciesRepository) GetByScientificName(ctx context.Context, name string) (*Species, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCounts["GetByScientificName"]++

	if m.shouldFail {
		return nil, errors.New("mock error")
	}

	if sp, ok := m.species[name]; ok {
		return sp, nil
	}
	return nil, errors.New("not found")
}

// GetByEbirdCode retrieves species by eBird taxonomy code.
func (m *MockSpeciesRepository) GetByEbirdCode(ctx context.Context, code string) (*Species, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCounts["GetByEbirdCode"]++

	if m.shouldFail {
		return nil, errors.New("mock error")
	}

	if sp, ok := m.byEbird[code]; ok {
		return sp, nil
	}
	return nil, errors.New("not found")
}

// GetOrCreate retrieves existing species or creates a new one.
func (m *MockSpeciesRepository) GetOrCreate(ctx context.Context, species *Species) (*Species, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCounts["GetOrCreate"]++

	if m.shouldFail {
		return nil, errors.New("mock error")
	}

	// Validate input
	if species == nil || species.ScientificName == "" {
		return nil, errors.New("scientificName is required")
	}

	// Check if exists
	if sp, ok := m.species[species.ScientificName]; ok {
		return sp, nil
	}

	// Create new
	species.ID = uint(len(m.species) + 1)
	m.species[species.ScientificName] = species
	m.byID[species.ID] = species
	if species.SpeciesCode != "" {
		m.byEbird[species.SpeciesCode] = species
	}
	m.listSpecies = append(m.listSpecies, species)

	return species, nil
}

// List returns all species with pagination.
func (m *MockSpeciesRepository) List(ctx context.Context, limit, offset int) ([]*Species, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCounts["List"]++

	if m.shouldFail {
		return nil, errors.New("mock error")
	}

	total := len(m.listSpecies)
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = total
	}
	if offset > total {
		return []*Species{}, nil
	}
	end := min(offset+limit, total)

	page := make([]*Species, end-offset)
	copy(page, m.listSpecies[offset:end])
	return page, nil
}

// InvalidateCache is a no-op for the mock.
func (m *MockSpeciesRepository) InvalidateCache() error {
	return nil
}

// AddSpecies adds a species to the mock repository.
// This is a test helper method. If sp.ID is 0, an ID is auto-assigned.
func (m *MockSpeciesRepository) AddSpecies(sp *Species) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Auto-assign ID if not set
	if sp.ID == 0 {
		sp.ID = uint(len(m.byID) + 1)
	}

	m.species[sp.ScientificName] = sp
	m.byID[sp.ID] = sp
	if sp.SpeciesCode != "" {
		m.byEbird[sp.SpeciesCode] = sp
	}
	m.listSpecies = append(m.listSpecies, sp)
}

// CallCount returns the number of times a method was called.
// Useful for verifying cache behavior.
func (m *MockSpeciesRepository) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCounts[method]
}

// SetShouldFail configures the mock to return errors.
// Useful for testing error handling.
func (m *MockSpeciesRepository) SetShouldFail(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shouldFail = fail
}

// Reset clears all data and call counts.
func (m *MockSpeciesRepository) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.species = make(map[string]*Species)
	m.byID = make(map[uint]*Species)
	m.byEbird = make(map[string]*Species)
	m.callCounts = make(map[string]int)
	m.listSpecies = nil
	m.shouldFail = false
}
