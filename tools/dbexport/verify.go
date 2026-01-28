package main

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"gorm.io/gorm"
)

// Verifier performs post-migration verification.
type Verifier struct {
	sourceDB *gorm.DB
	targetDB *gorm.DB
}

// NewVerifier creates a new Verifier.
func NewVerifier(sourceDB, targetDB *gorm.DB) *Verifier {
	return &Verifier{
		sourceDB: sourceDB,
		targetDB: targetDB,
	}
}

// Verify performs all verification checks.
func (v *Verifier) Verify() error {
	// Count verification
	if err := v.verifyCounts(); err != nil {
		return fmt.Errorf("count verification failed: %w", err)
	}

	// Sample verification for critical tables
	if err := v.verifySamples(); err != nil {
		return fmt.Errorf("sample verification failed: %w", err)
	}

	return nil
}

// verifyCounts compares record counts between source and target.
func (v *Verifier) verifyCounts() error {
	fmt.Println("\nVerifying record counts...")

	tables := []struct {
		name  string
		model any
	}{
		{"daily_events", &datastore.DailyEvents{}},
		{"hourly_weathers", &datastore.HourlyWeather{}},
		{"notes", &datastore.Note{}},
		{"results", &datastore.Results{}},
		{"note_reviews", &datastore.NoteReview{}},
		{"note_comments", &datastore.NoteComment{}},
		{"note_locks", &datastore.NoteLock{}},
		{"image_caches", &datastore.ImageCache{}},
		{"dynamic_thresholds", &datastore.DynamicThreshold{}},
		{"threshold_events", &datastore.ThresholdEvent{}},
		{"notification_histories", &datastore.NotificationHistory{}},
	}

	allMatch := true
	fmt.Printf("%-25s %12s %12s %8s\n", "Table", "Source", "Target", "Match")
	fmt.Println(string(make([]byte, 60)))

	for _, t := range tables {
		var sourceCount, targetCount int64

		if err := v.sourceDB.Model(t.model).Count(&sourceCount).Error; err != nil {
			return fmt.Errorf("failed to count source %s: %w", t.name, err)
		}

		if err := v.targetDB.Model(t.model).Count(&targetCount).Error; err != nil {
			return fmt.Errorf("failed to count target %s: %w", t.name, err)
		}

		match := "✓"
		if sourceCount != targetCount {
			match = "✗"
			allMatch = false
		}

		fmt.Printf("%-25s %12d %12d %8s\n", t.name, sourceCount, targetCount, match)
	}

	if !allMatch {
		return fmt.Errorf("record counts do not match")
	}

	fmt.Println("\nAll counts match!")
	return nil
}

// verifySamples verifies random samples from critical tables.
func (v *Verifier) verifySamples() error {
	fmt.Println("\nVerifying sample records...")

	// Sample Notes (most critical table)
	if err := v.sampleNotes(5); err != nil {
		return fmt.Errorf("notes sampling failed: %w", err)
	}

	// Sample Results
	if err := v.sampleResults(5); err != nil {
		return fmt.Errorf("results sampling failed: %w", err)
	}

	fmt.Println("Sample verification passed!")
	return nil
}

// sampleNotes verifies random Note records.
func (v *Verifier) sampleNotes(count int) error {
	// Get random IDs from source
	var sourceNotes []datastore.Note
	if err := v.sourceDB.Order("RANDOM()").Limit(count).Find(&sourceNotes).Error; err != nil {
		return fmt.Errorf("failed to fetch source samples: %w", err)
	}

	if len(sourceNotes) == 0 {
		fmt.Println("  Notes: no records to sample")
		return nil
	}

	// Verify each in target using index to avoid copying large struct
	for i := range sourceNotes {
		src := &sourceNotes[i]
		var target datastore.Note
		if err := v.targetDB.First(&target, src.ID).Error; err != nil {
			return fmt.Errorf("note ID %d not found in target: %w", src.ID, err)
		}

		// Verify critical fields
		if src.ScientificName != target.ScientificName {
			return fmt.Errorf("note ID %d: ScientificName mismatch (%s vs %s)",
				src.ID, src.ScientificName, target.ScientificName)
		}
		if src.CommonName != target.CommonName {
			return fmt.Errorf("note ID %d: CommonName mismatch (%s vs %s)",
				src.ID, src.CommonName, target.CommonName)
		}
		if src.Date != target.Date {
			return fmt.Errorf("note ID %d: Date mismatch (%s vs %s)",
				src.ID, src.Date, target.Date)
		}
		if src.Confidence != target.Confidence {
			return fmt.Errorf("note ID %d: Confidence mismatch (%f vs %f)",
				src.ID, src.Confidence, target.Confidence)
		}
	}

	fmt.Printf("  Notes: %d samples verified\n", len(sourceNotes))
	return nil
}

// sampleResults verifies random Results records.
func (v *Verifier) sampleResults(count int) error {
	// Get random IDs from source
	var sourceResults []datastore.Results
	if err := v.sourceDB.Order("RANDOM()").Limit(count).Find(&sourceResults).Error; err != nil {
		return fmt.Errorf("failed to fetch source samples: %w", err)
	}

	if len(sourceResults) == 0 {
		fmt.Println("  Results: no records to sample")
		return nil
	}

	// Verify each in target
	for _, src := range sourceResults {
		var target datastore.Results
		if err := v.targetDB.First(&target, src.ID).Error; err != nil {
			return fmt.Errorf("result ID %d not found in target: %w", src.ID, err)
		}

		// Verify critical fields
		if src.NoteID != target.NoteID {
			return fmt.Errorf("result ID %d: NoteID mismatch (%d vs %d)",
				src.ID, src.NoteID, target.NoteID)
		}
		if src.Species != target.Species {
			return fmt.Errorf("result ID %d: Species mismatch (%s vs %s)",
				src.ID, src.Species, target.Species)
		}
		if src.Confidence != target.Confidence {
			return fmt.Errorf("result ID %d: Confidence mismatch (%f vs %f)",
				src.ID, src.Confidence, target.Confidence)
		}
	}

	fmt.Printf("  Results: %d samples verified\n", len(sourceResults))
	return nil
}
