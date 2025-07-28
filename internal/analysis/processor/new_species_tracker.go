// new_species_tracker.go
package processor

import (
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// SpeciesStatus represents the tracking status of a species
type SpeciesStatus struct {
	FirstSeenTime    time.Time
	IsNew            bool
	DaysSinceFirst   int
	LastUpdatedTime  time.Time // For cache management
}

// NewSpeciesTracker tracks species detections and identifies new species
// within a configurable time window. Designed for minimal memory allocations.
type NewSpeciesTracker struct {
	mu               sync.RWMutex
	speciesFirstSeen map[string]time.Time // scientificName -> first detection time
	windowDays       int                  // Days to consider a species "new"
	ds               datastore.Interface
	lastSyncTime     time.Time
	syncIntervalMins int
	
	// Pre-allocated for efficiency
	statusBuffer     SpeciesStatus // Reusable buffer for status calculations
}

// NewSpeciesTracker creates a new species tracker with the specified window
func NewSpeciesTrackerWithConfig(ds datastore.Interface, windowDays, syncIntervalMins int) *NewSpeciesTracker {
	if windowDays <= 0 {
		windowDays = 14 // Default to 14 days
	}
	if syncIntervalMins <= 0 {
		syncIntervalMins = 60 // Default to 1 hour
	}
	
	return &NewSpeciesTracker{
		speciesFirstSeen: make(map[string]time.Time, 100), // Pre-allocate for ~100 species
		windowDays:       windowDays,
		ds:               ds,
		syncIntervalMins: syncIntervalMins,
	}
}

// InitFromDatabase populates the tracker from historical data
// This should be called once during initialization
func (t *NewSpeciesTracker) InitFromDatabase() error {
	if t.ds == nil {
		return errors.Newf("datastore is nil").
			Component("new-species-tracker").
			Category(errors.CategoryConfiguration).
			Build()
	}

	// Calculate date range for initial load
	// Load data from 2x window period to ensure we catch all relevant species
	endDate := time.Now().Format("2006-01-02")
	startDate := time.Now().AddDate(0, 0, -t.windowDays*2).Format("2006-01-02")

	// Use existing analytics method to get new species data
	newSpeciesData, err := t.ds.GetNewSpeciesDetections(startDate, endDate, 1000, 0)
	if err != nil {
		return errors.New(err).
			Component("new-species-tracker").
			Category(errors.CategoryDatabase).
			Context("operation", "init_from_database").
			Build()
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Clear existing data
	for k := range t.speciesFirstSeen {
		delete(t.speciesFirstSeen, k)
	}

	// Populate map with first seen dates
	for _, species := range newSpeciesData {
		if species.FirstSeenDate != "" {
			firstSeen, err := time.Parse("2006-01-02", species.FirstSeenDate)
			if err == nil {
				t.speciesFirstSeen[species.ScientificName] = firstSeen
			}
		}
	}

	t.lastSyncTime = time.Now()
	return nil
}

// GetSpeciesStatus returns the tracking status for a species
// This method reuses a pre-allocated buffer to minimize allocations
func (t *NewSpeciesTracker) GetSpeciesStatus(scientificName string, currentTime time.Time) SpeciesStatus {
	t.mu.RLock()
	firstSeen, exists := t.speciesFirstSeen[scientificName]
	t.mu.RUnlock()

	// Reuse the pre-allocated status buffer
	status := &t.statusBuffer
	status.FirstSeenTime = firstSeen
	status.IsNew = false
	status.DaysSinceFirst = -1
	status.LastUpdatedTime = currentTime

	if exists {
		daysSince := int(currentTime.Sub(firstSeen).Hours() / 24)
		status.DaysSinceFirst = daysSince
		status.IsNew = daysSince <= t.windowDays
	} else {
		// Species not seen before
		status.IsNew = true
		status.DaysSinceFirst = 0
	}

	return *status
}

// UpdateSpecies updates the first seen time for a species if necessary
// Returns true if this is a new species detection
func (t *NewSpeciesTracker) UpdateSpecies(scientificName string, detectionTime time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	firstSeen, exists := t.speciesFirstSeen[scientificName]
	
	if !exists {
		// New species detected
		t.speciesFirstSeen[scientificName] = detectionTime
		return true
	}

	// Update if this detection is earlier than recorded
	if detectionTime.Before(firstSeen) {
		t.speciesFirstSeen[scientificName] = detectionTime
	}

	return false
}

// IsNewSpecies checks if a species is considered "new" within the configured window
func (t *NewSpeciesTracker) IsNewSpecies(scientificName string) bool {
	t.mu.RLock()
	firstSeen, exists := t.speciesFirstSeen[scientificName]
	t.mu.RUnlock()

	if !exists {
		return true // Never seen before
	}

	daysSince := int(time.Since(firstSeen).Hours() / 24)
	return daysSince <= t.windowDays
}

// SyncIfNeeded checks if a database sync is needed and performs it
// This helps keep the tracker updated with any database changes
func (t *NewSpeciesTracker) SyncIfNeeded() error {
	if time.Since(t.lastSyncTime).Minutes() < float64(t.syncIntervalMins) {
		return nil // No sync needed yet
	}

	return t.InitFromDatabase()
}

// GetWindowDays returns the configured window for new species
func (t *NewSpeciesTracker) GetWindowDays() int {
	return t.windowDays
}

// GetSpeciesCount returns the number of tracked species
func (t *NewSpeciesTracker) GetSpeciesCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.speciesFirstSeen)
}

// PruneOldEntries removes species entries older than 2x the window period
// This prevents unbounded memory growth over time
func (t *NewSpeciesTracker) PruneOldEntries() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	cutoffTime := time.Now().AddDate(0, 0, -t.windowDays*2)
	pruned := 0

	for scientificName, firstSeen := range t.speciesFirstSeen {
		if firstSeen.Before(cutoffTime) {
			delete(t.speciesFirstSeen, scientificName)
			pruned++
		}
	}

	return pruned
}