package processor

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"gorm.io/gorm"
)

// mockDatastore implements the datastore.Interface for testing
// We only implement the methods we actually use in tests
type mockDatastore struct {
	species []datastore.NewSpeciesData
}

// GetNewSpeciesDetections is the only method we need for tracker initialization
func (m *mockDatastore) GetNewSpeciesDetections(startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	return m.species, nil
}

// All other interface methods - stubbed out for testing
func (m *mockDatastore) Open() error { return nil }
func (m *mockDatastore) Close() error { return nil }
func (m *mockDatastore) Save(note *datastore.Note, results []datastore.Results) error { return nil }
func (m *mockDatastore) Delete(id string) error { return nil }
func (m *mockDatastore) Get(id string) (datastore.Note, error) { return datastore.Note{}, nil }
func (m *mockDatastore) SetMetrics(metrics *datastore.Metrics) {}
func (m *mockDatastore) SetSunCalcMetrics(suncalcMetrics any) {}
func (m *mockDatastore) Optimize(ctx context.Context) error { return nil }
func (m *mockDatastore) GetAllNotes() ([]datastore.Note, error) { return nil, nil }
func (m *mockDatastore) GetTopBirdsData(selectedDate string, minConfidenceNormalized float64) ([]datastore.Note, error) { return nil, nil }
func (m *mockDatastore) GetHourlyOccurrences(date, commonName string, minConfidenceNormalized float64) ([24]int, error) { return [24]int{}, nil }
func (m *mockDatastore) SpeciesDetections(species, date, hour string, duration int, sortAscending bool, limit, offset int) ([]datastore.Note, error) { return nil, nil }
func (m *mockDatastore) GetLastDetections(numDetections int) ([]datastore.Note, error) { return nil, nil }
func (m *mockDatastore) GetAllDetectedSpecies() ([]datastore.Note, error) { return nil, nil }
func (m *mockDatastore) SearchNotes(query string, sortAscending bool, limit, offset int) ([]datastore.Note, error) { return nil, nil }
func (m *mockDatastore) SearchNotesAdvanced(filters *datastore.AdvancedSearchFilters) ([]datastore.Note, int64, error) { return nil, 0, nil }
func (m *mockDatastore) GetNoteClipPath(noteID string) (string, error) { return "", nil }
func (m *mockDatastore) DeleteNoteClipPath(noteID string) error { return nil }
func (m *mockDatastore) GetNoteReview(noteID string) (*datastore.NoteReview, error) { return &datastore.NoteReview{}, nil }
func (m *mockDatastore) SaveNoteReview(review *datastore.NoteReview) error { return nil }
func (m *mockDatastore) GetNoteComments(noteID string) ([]datastore.NoteComment, error) { return nil, nil }
func (m *mockDatastore) SaveNoteComment(comment *datastore.NoteComment) error { return nil }
func (m *mockDatastore) UpdateNoteComment(commentID, entry string) error { return nil }
func (m *mockDatastore) DeleteNoteComment(commentID string) error { return nil }
func (m *mockDatastore) SaveDailyEvents(dailyEvents *datastore.DailyEvents) error { return nil }
func (m *mockDatastore) GetDailyEvents(date string) (datastore.DailyEvents, error) { return datastore.DailyEvents{}, nil }
func (m *mockDatastore) SaveHourlyWeather(hourlyWeather *datastore.HourlyWeather) error { return nil }
func (m *mockDatastore) GetHourlyWeather(date string) ([]datastore.HourlyWeather, error) { return nil, nil }
func (m *mockDatastore) LatestHourlyWeather() (*datastore.HourlyWeather, error) { return &datastore.HourlyWeather{}, nil }
func (m *mockDatastore) GetHourlyDetections(date, hour string, duration, limit, offset int) ([]datastore.Note, error) { return nil, nil }
func (m *mockDatastore) CountSpeciesDetections(species, date, hour string, duration int) (int64, error) { return 0, nil }
func (m *mockDatastore) CountSearchResults(query string) (int64, error) { return 0, nil }
func (m *mockDatastore) Transaction(fc func(tx *gorm.DB) error) error { return nil }
func (m *mockDatastore) LockNote(noteID string) error { return nil }
func (m *mockDatastore) UnlockNote(noteID string) error { return nil }
func (m *mockDatastore) GetNoteLock(noteID string) (*datastore.NoteLock, error) { return &datastore.NoteLock{}, nil }
func (m *mockDatastore) IsNoteLocked(noteID string) (bool, error) { return false, nil }
func (m *mockDatastore) GetImageCache(query datastore.ImageCacheQuery) (*datastore.ImageCache, error) { return &datastore.ImageCache{}, nil }
func (m *mockDatastore) GetImageCacheBatch(providerName string, scientificNames []string) (map[string]*datastore.ImageCache, error) { return make(map[string]*datastore.ImageCache), nil }
func (m *mockDatastore) SaveImageCache(cache *datastore.ImageCache) error { return nil }
func (m *mockDatastore) GetAllImageCaches(providerName string) ([]datastore.ImageCache, error) { return nil, nil }
func (m *mockDatastore) GetLockedNotesClipPaths() ([]string, error) { return nil, nil }
func (m *mockDatastore) CountHourlyDetections(date, hour string, duration int) (int64, error) { return 0, nil }
func (m *mockDatastore) GetSpeciesSummaryData(startDate, endDate string) ([]datastore.SpeciesSummaryData, error) { return nil, nil }
func (m *mockDatastore) GetHourlyAnalyticsData(date, species string) ([]datastore.HourlyAnalyticsData, error) { return nil, nil }
func (m *mockDatastore) GetDailyAnalyticsData(startDate, endDate, species string) ([]datastore.DailyAnalyticsData, error) { return nil, nil }
func (m *mockDatastore) GetDetectionTrends(period string, limit int) ([]datastore.DailyAnalyticsData, error) { return nil, nil }
func (m *mockDatastore) GetHourlyDistribution(startDate, endDate, species string) ([]datastore.HourlyDistributionData, error) { return nil, nil }
func (m *mockDatastore) SearchDetections(filters *datastore.SearchFilters) ([]datastore.DetectionRecord, int, error) { return nil, 0, nil }

func TestNewSpeciesTracker_NewSpecies(t *testing.T) {
	// Create mock datastore with some historical species data
	ds := &mockDatastore{
		species: []datastore.NewSpeciesData{
			{
				ScientificName: "Parus major",
				CommonName:     "Great Tit",
				FirstSeenDate:  time.Now().Add(-20 * 24 * time.Hour).Format("2006-01-02"), // 20 days ago
			},
			{
				ScientificName: "Turdus merula",
				CommonName:     "Common Blackbird",
				FirstSeenDate:  time.Now().Add(-5 * 24 * time.Hour).Format("2006-01-02"), // 5 days ago
			},
		},
	}

	// Create tracker with 14-day window
	tracker := NewSpeciesTrackerWithConfig(ds, 14, 60)
	
	// Initialize from database
	err := tracker.InitFromDatabase()
	if err != nil {
		t.Fatalf("Failed to initialize tracker: %v", err)
	}

	// Test new species (not in database)
	currentTime := time.Now()
	status := tracker.GetSpeciesStatus("Cyanistes caeruleus", currentTime)
	if !status.IsNew {
		t.Errorf("Expected Cyanistes caeruleus to be a new species")
	}
	if status.DaysSinceFirst != 0 {
		t.Errorf("Expected DaysSinceFirst to be 0 for new species, got %d", status.DaysSinceFirst)
	}

	// Test old species (outside window)
	status = tracker.GetSpeciesStatus("Parus major", currentTime)
	if status.IsNew {
		t.Errorf("Expected Parus major to not be a new species (20 days old)")
	}
	if status.DaysSinceFirst != 20 {
		t.Errorf("Expected DaysSinceFirst to be 20, got %d", status.DaysSinceFirst)
	}

	// Test recent species (within window)
	status = tracker.GetSpeciesStatus("Turdus merula", currentTime)
	if !status.IsNew {
		t.Errorf("Expected Turdus merula to be a new species (5 days old, within 14-day window)")
	}
	if status.DaysSinceFirst != 5 {
		t.Errorf("Expected DaysSinceFirst to be 5, got %d", status.DaysSinceFirst)
	}
}

func TestNewSpeciesTracker_ConcurrentAccess(t *testing.T) {
	ds := &mockDatastore{
		species: []datastore.NewSpeciesData{},
	}

	tracker := NewSpeciesTrackerWithConfig(ds, 14, 60)
	_ = tracker.InitFromDatabase()

	// Test concurrent reads and writes
	var wg sync.WaitGroup
	species := []string{"Species1", "Species2", "Species3", "Species4", "Species5"}
	currentTime := time.Now()

	// Start multiple goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				speciesName := species[j%len(species)]
				if id%2 == 0 {
					// Read operation
					_ = tracker.GetSpeciesStatus(speciesName, currentTime)
				} else {
					// Write operation
					_ = tracker.UpdateSpecies(speciesName, currentTime)
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestNewSpeciesTracker_UpdateSpecies(t *testing.T) {
	ds := &mockDatastore{
		species: []datastore.NewSpeciesData{},
	}

	tracker := NewSpeciesTrackerWithConfig(ds, 14, 60)
	_ = tracker.InitFromDatabase()

	currentTime := time.Now()

	// Track a new species
	isNew := tracker.UpdateSpecies("Parus major", currentTime)
	if !isNew {
		t.Errorf("Expected UpdateSpecies to return true for new species")
	}

	// Verify it's now tracked
	status := tracker.GetSpeciesStatus("Parus major", currentTime)
	if !status.IsNew {
		t.Errorf("Expected newly tracked species to be marked as new")
	}
	if status.DaysSinceFirst != 0 {
		t.Errorf("Expected DaysSinceFirst to be 0 for just-tracked species")
	}

	// Update same species again
	isNew = tracker.UpdateSpecies("Parus major", currentTime.Add(time.Hour))
	if isNew {
		t.Errorf("Expected UpdateSpecies to return false for existing species")
	}
}

func TestNewSpeciesTracker_EdgeCases(t *testing.T) {
	// Create tracker with exactly 14 days old species
	ds := &mockDatastore{
		species: []datastore.NewSpeciesData{
			{
				ScientificName: "Parus major",
				FirstSeenDate:  time.Now().Add(-14 * 24 * time.Hour).Format("2006-01-02"), // Exactly 14 days ago
			},
		},
	}

	tracker := NewSpeciesTrackerWithConfig(ds, 14, 60)
	_ = tracker.InitFromDatabase()

	currentTime := time.Now()

	// Test species exactly at the window boundary
	status := tracker.GetSpeciesStatus("Parus major", currentTime)
	// Should be considered new since it's within the window (14 days is inclusive)
	if !status.IsNew {
		t.Errorf("Expected species at exact window boundary to be considered new")
	}

	// Test empty species name
	status = tracker.GetSpeciesStatus("", currentTime)
	if !status.IsNew {
		t.Errorf("Empty species name should be considered new (not in database)")
	}
	if status.DaysSinceFirst != 0 {
		t.Errorf("Expected DaysSinceFirst to be 0 for empty species name")
	}
}

func TestNewSpeciesTracker_PruneOldEntries(t *testing.T) {
	ds := &mockDatastore{
		species: []datastore.NewSpeciesData{
			{
				ScientificName: "Old Species",
				FirstSeenDate:  time.Now().Add(-30 * 24 * time.Hour).Format("2006-01-02"), // 30 days ago
			},
			{
				ScientificName: "Recent Species",
				FirstSeenDate:  time.Now().Add(-5 * 24 * time.Hour).Format("2006-01-02"), // 5 days ago
			},
		},
	}

	tracker := NewSpeciesTrackerWithConfig(ds, 14, 60)
	_ = tracker.InitFromDatabase()

	// Initial species count
	if tracker.GetSpeciesCount() != 2 {
		t.Errorf("Expected 2 species, got %d", tracker.GetSpeciesCount())
	}

	// Prune old entries (older than 28 days)
	pruned := tracker.PruneOldEntries()
	if pruned != 1 {
		t.Errorf("Expected 1 species to be pruned, got %d", pruned)
	}

	// Should only have recent species left
	if tracker.GetSpeciesCount() != 1 {
		t.Errorf("Expected 1 species after pruning, got %d", tracker.GetSpeciesCount())
	}

	// Recent species should still be there
	status := tracker.GetSpeciesStatus("Recent Species", time.Now())
	if !status.IsNew {
		t.Errorf("Recent species should still be marked as new after pruning")
	}
}

// Benchmark tests
func BenchmarkNewSpeciesTracker_GetSpeciesStatus(b *testing.B) {
	ds := &mockDatastore{
		species: []datastore.NewSpeciesData{},
	}

	tracker := NewSpeciesTrackerWithConfig(ds, 14, 60)
	_ = tracker.InitFromDatabase()

	// Pre-populate with some species
	currentTime := time.Now()
	species := make([]string, 100)
	for i := 0; i < 100; i++ {
		species[i] = fmt.Sprintf("Species%d", i)
		tracker.UpdateSpecies(species[i], currentTime.Add(time.Duration(-i)*24*time.Hour))
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = tracker.GetSpeciesStatus(species[i%100], currentTime)
	}
}

func BenchmarkNewSpeciesTracker_UpdateSpecies(b *testing.B) {
	ds := &mockDatastore{
		species: []datastore.NewSpeciesData{},
	}

	tracker := NewSpeciesTrackerWithConfig(ds, 14, 60)
	_ = tracker.InitFromDatabase()

	currentTime := time.Now()
	species := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		species[i] = fmt.Sprintf("Species%d", i)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tracker.UpdateSpecies(species[i], currentTime)
	}
}

func BenchmarkNewSpeciesTracker_ConcurrentOperations(b *testing.B) {
	ds := &mockDatastore{
		species: []datastore.NewSpeciesData{},
	}

	tracker := NewSpeciesTrackerWithConfig(ds, 14, 60)
	_ = tracker.InitFromDatabase()

	// Pre-populate with some species
	currentTime := time.Now()
	species := make([]string, 50)
	for i := 0; i < 50; i++ {
		species[i] = fmt.Sprintf("Species%d", i)
		tracker.UpdateSpecies(species[i], currentTime)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				_ = tracker.GetSpeciesStatus(species[i%50], currentTime)
			} else {
				tracker.UpdateSpecies(fmt.Sprintf("NewSpecies%d", i), currentTime)
			}
			i++
		}
	})
}

func BenchmarkNewSpeciesTracker_MapMemoryUsage(b *testing.B) {
	ds := &mockDatastore{
		species: []datastore.NewSpeciesData{},
	}

	tracker := NewSpeciesTrackerWithConfig(ds, 14, 60)
	_ = tracker.InitFromDatabase()

	currentTime := time.Now()
	b.ResetTimer()

	// Benchmark memory allocation when adding many species
	for i := 0; i < b.N; i++ {
		tracker.UpdateSpecies(fmt.Sprintf("UniqueSpecies%d", i), currentTime)
		if i%1000 == 0 {
			// Periodically check a species to prevent optimization
			_ = tracker.GetSpeciesStatus("UniqueSpecies0", currentTime)
		}
	}
}