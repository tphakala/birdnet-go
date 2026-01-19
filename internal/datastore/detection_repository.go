// Package datastore provides database operations for BirdNET-Go.
package datastore

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/mapper"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// detectionRepository implements DetectionRepository using the existing Interface.
// This is a bridge implementation that allows gradual migration to the new domain model
// while preserving compatibility with existing database operations.
type detectionRepository struct {
	store Interface
	tz    *time.Location
}

// NewDetectionRepository creates a new DetectionRepository wrapping an existing store.
func NewDetectionRepository(store Interface, tz *time.Location) DetectionRepository {
	if tz == nil {
		tz = time.Local
	}
	return &detectionRepository{
		store: store,
		tz:    tz,
	}
}

// Save persists a detection result and its additional predictions.
func (r *detectionRepository) Save(ctx context.Context, result *detection.Result, additionalResults []detection.AdditionalResult) error {
	// Convert domain model to legacy Note for existing Save method
	note := r.resultToNote(result)

	// Convert additional results to legacy Results
	results := make([]Results, 0, len(additionalResults))
	for _, ar := range additionalResults {
		results = append(results, Results{
			Species:    ar.Species.ScientificName + "_" + ar.Species.CommonName,
			Confidence: float32(ar.Confidence),
		})
	}

	// Use existing Save method
	if err := r.store.Save(&note, results); err != nil {
		return fmt.Errorf("failed to save detection: %w", err)
	}

	// Update the result's ID from the saved note
	result.ID = note.ID

	return nil
}

// Get retrieves a detection by ID.
func (r *detectionRepository) Get(ctx context.Context, id string) (*detection.Result, error) {
	note, err := r.store.Get(id)
	if err != nil {
		return nil, fmt.Errorf("failed to get detection %s: %w", id, err)
	}

	return r.noteToResult(&note)
}

// Delete removes a detection by ID.
func (r *detectionRepository) Delete(ctx context.Context, id string) error {
	return r.store.Delete(id)
}

// GetRecent retrieves the most recent detections.
func (r *detectionRepository) GetRecent(ctx context.Context, limit int) ([]*detection.Result, error) {
	notes, err := r.store.GetLastDetections(limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent detections: %w", err)
	}

	return r.notesToResults(notes)
}

// Search finds detections matching the given filters.
func (r *detectionRepository) Search(ctx context.Context, filters *DetectionFilters) ([]*detection.Result, int64, error) {
	// Convert to legacy AdvancedSearchFilters
	legacyFilters, err := r.convertFilters(filters)
	if err != nil {
		return nil, 0, err
	}

	notes, total, err := r.store.SearchNotesAdvanced(&legacyFilters)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search detections: %w", err)
	}

	results, err := r.notesToResults(notes)
	if err != nil {
		return nil, 0, err
	}

	return results, total, nil
}

// GetBySpecies retrieves detections for a specific species.
func (r *detectionRepository) GetBySpecies(ctx context.Context, species string, filters *DetectionFilters) ([]*detection.Result, int64, error) {
	limit := 100
	offset := 0
	if filters != nil {
		if filters.Limit > 0 {
			limit = filters.Limit
		}
		offset = filters.Offset
	}

	notes, err := r.store.SpeciesDetections(species, "", "", 1, false, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get species detections: %w", err)
	}

	total, err := r.store.CountSpeciesDetections(species, "", "", 1)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count species detections: %w", err)
	}

	results, err := r.notesToResults(notes)
	if err != nil {
		return nil, 0, err
	}

	return results, total, nil
}

// GetByDateRange retrieves detections within a date range.
func (r *detectionRepository) GetByDateRange(ctx context.Context, startDate, endDate string, limit, offset int) ([]*detection.Result, int64, error) {
	// Parse date strings to time.Time
	start, err := time.Parse(mapper.DateFormat, startDate)
	if err != nil && startDate != "" {
		return nil, 0, fmt.Errorf("invalid start date %q: %w", startDate, err)
	}

	var end time.Time
	if endDate != "" {
		end, err = time.Parse(mapper.DateFormat, endDate)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid end date %q: %w", endDate, err)
		}
	}
	if end.IsZero() {
		end = time.Now()
	}

	filters := &AdvancedSearchFilters{
		DateRange: &DateRange{
			Start: start,
			End:   end,
		},
		Limit:  limit,
		Offset: offset,
	}

	notes, total, err := r.store.SearchNotesAdvanced(filters)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get detections by date range: %w", err)
	}

	results, err := r.notesToResults(notes)
	if err != nil {
		return nil, 0, err
	}

	return results, total, nil
}

// GetHourly retrieves detections for a specific hour on a date.
func (r *detectionRepository) GetHourly(ctx context.Context, date, hour string, duration, limit, offset int) ([]*detection.Result, int64, error) {
	notes, err := r.store.GetHourlyDetections(date, hour, duration, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get hourly detections: %w", err)
	}

	total, err := r.store.CountHourlyDetections(date, hour, duration)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count hourly detections: %w", err)
	}

	results, err := r.notesToResults(notes)
	if err != nil {
		return nil, 0, err
	}

	return results, total, nil
}

// Lock sets a lock on a detection.
func (r *detectionRepository) Lock(ctx context.Context, id string) error {
	return r.store.LockNote(id)
}

// Unlock removes a lock from a detection.
func (r *detectionRepository) Unlock(ctx context.Context, id string) error {
	return r.store.UnlockNote(id)
}

// IsLocked checks if a detection is locked.
func (r *detectionRepository) IsLocked(ctx context.Context, id string) (bool, error) {
	return r.store.IsNoteLocked(id)
}

// SetReview sets the review status of a detection.
func (r *detectionRepository) SetReview(ctx context.Context, id, verified string) error {
	review := &NoteReview{
		NoteID:    r.parseID(id),
		Verified:  verified,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return r.store.SaveNoteReview(review)
}

// GetReview retrieves the review status of a detection.
func (r *detectionRepository) GetReview(ctx context.Context, id string) (string, error) {
	review, err := r.store.GetNoteReview(id)
	if err != nil {
		return "", err
	}
	if review == nil {
		return "", nil
	}
	return review.Verified, nil
}

// AddComment adds a comment to a detection.
func (r *detectionRepository) AddComment(ctx context.Context, id, comment string) error {
	noteComment := &NoteComment{
		NoteID:    r.parseID(id),
		Entry:     comment,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return r.store.SaveNoteComment(noteComment)
}

// GetComments retrieves comments for a detection.
func (r *detectionRepository) GetComments(ctx context.Context, id string) ([]detection.Comment, error) {
	noteComments, err := r.store.GetNoteComments(id)
	if err != nil {
		return nil, err
	}

	comments := make([]detection.Comment, len(noteComments))
	for i, nc := range noteComments {
		comments[i] = detection.Comment{
			ID:        nc.ID,
			Entry:     nc.Entry,
			CreatedAt: nc.CreatedAt,
			UpdatedAt: nc.UpdatedAt,
		}
	}
	return comments, nil
}

// UpdateComment updates a comment.
func (r *detectionRepository) UpdateComment(ctx context.Context, commentID uint, entry string) error {
	return r.store.UpdateNoteComment(strconv.FormatUint(uint64(commentID), 10), entry)
}

// DeleteComment deletes a comment.
func (r *detectionRepository) DeleteComment(ctx context.Context, commentID uint) error {
	return r.store.DeleteNoteComment(strconv.FormatUint(uint64(commentID), 10))
}

// GetClipPath returns the audio clip path for a detection.
func (r *detectionRepository) GetClipPath(ctx context.Context, id string) (string, error) {
	return r.store.GetNoteClipPath(id)
}

// GetAdditionalResults returns the secondary predictions for a detection.
func (r *detectionRepository) GetAdditionalResults(ctx context.Context, id string) ([]detection.AdditionalResult, error) {
	// This requires access to the Results table which isn't directly exposed
	// For now, this is a simplified implementation
	// Full implementation would require extending the Interface or accessing DB directly
	return nil, nil
}

// Helper methods

// resultToNote converts a domain Result to a legacy Note.
func (r *detectionRepository) resultToNote(result *detection.Result) Note {
	return Note{
		ID:             result.ID,
		SourceNode:     result.SourceNode,
		Date:           result.Timestamp.Format(mapper.DateFormat),
		Time:           result.Timestamp.Format(mapper.TimeFormat),
		BeginTime:      result.BeginTime,
		EndTime:        result.EndTime,
		SpeciesCode:    result.Species.Code,
		ScientificName: result.Species.ScientificName,
		CommonName:     result.Species.CommonName,
		Confidence:     result.Confidence,
		Latitude:       result.Latitude,
		Longitude:      result.Longitude,
		Threshold:      result.Threshold,
		Sensitivity:    result.Sensitivity,
		ClipName:       result.ClipName,
		ProcessingTime: result.ProcessingTime,
		Source: AudioSource{
			ID:          result.AudioSource.ID,
			SafeString:  result.AudioSource.SafeString,
			DisplayName: result.AudioSource.DisplayName,
		},
		Occurrence: result.Occurrence,
	}
}

// noteToResult converts a legacy Note to a domain Result.
func (r *detectionRepository) noteToResult(note *Note) (*detection.Result, error) {
	// Parse date and time strings to timestamp
	dateTime := note.Date + " " + note.Time
	timestamp, err := time.ParseInLocation(mapper.DateFormat+" "+mapper.TimeFormat, dateTime, r.tz)
	if err != nil {
		return nil, fmt.Errorf("failed to parse timestamp: %w", err)
	}

	result := &detection.Result{
		ID:         note.ID,
		Timestamp:  timestamp,
		SourceNode: note.SourceNode,
		AudioSource: detection.AudioSource{
			ID:          note.Source.ID,
			DisplayName: note.Source.DisplayName,
			SafeString:  note.Source.SafeString,
		},
		BeginTime: note.BeginTime,
		EndTime:   note.EndTime,
		Species: detection.Species{
			ScientificName: note.ScientificName,
			CommonName:     note.CommonName,
			Code:           note.SpeciesCode,
		},
		Confidence:     note.Confidence,
		Latitude:       note.Latitude,
		Longitude:      note.Longitude,
		Threshold:      note.Threshold,
		Sensitivity:    note.Sensitivity,
		ClipName:       note.ClipName,
		ProcessingTime: note.ProcessingTime,
		Occurrence:     note.Occurrence,
		Verified:       note.Verified,
		Locked:         note.Locked,
		Model:          detection.DefaultModelInfo(),
	}

	// Convert comments
	if len(note.Comments) > 0 {
		result.Comments = make([]detection.Comment, len(note.Comments))
		for i, c := range note.Comments {
			result.Comments[i] = detection.Comment{
				ID:        c.ID,
				Entry:     c.Entry,
				CreatedAt: c.CreatedAt,
				UpdatedAt: c.UpdatedAt,
			}
		}
	}

	return result, nil
}

// notesToResults converts a slice of Notes to Results.
func (r *detectionRepository) notesToResults(notes []Note) ([]*detection.Result, error) {
	results := make([]*detection.Result, 0, len(notes))
	for i := range notes {
		result, err := r.noteToResult(&notes[i])
		if err != nil {
			return nil, fmt.Errorf("failed to convert note ID=%d: %w", notes[i].ID, err)
		}
		results = append(results, result)
	}
	return results, nil
}

// convertFilters converts DetectionFilters to legacy AdvancedSearchFilters.
func (r *detectionRepository) convertFilters(filters *DetectionFilters) (AdvancedSearchFilters, error) {
	if filters == nil {
		return AdvancedSearchFilters{
			Limit:  100,
			Offset: 0,
		}, nil
	}

	legacy := AdvancedSearchFilters{
		TextQuery:     filters.Query,
		Species:       filters.Species,
		TimeOfDay:     filters.TimeOfDay,
		Location:      filters.Location,
		Limit:         filters.Limit,
		Offset:        filters.Offset,
		SortAscending: filters.SortAscending,
		Verified:      filters.Verified,
		Locked:        filters.Locked,
	}

	// Convert date range - parse strings to time.Time
	if filters.StartDate != "" || filters.EndDate != "" {
		var start, end time.Time
		var err error

		if filters.StartDate != "" {
			start, err = time.Parse(mapper.DateFormat, filters.StartDate)
			if err != nil {
				return AdvancedSearchFilters{}, fmt.Errorf("invalid start date %q: %w", filters.StartDate, err)
			}
		}
		if filters.EndDate != "" {
			end, err = time.Parse(mapper.DateFormat, filters.EndDate)
			if err != nil {
				return AdvancedSearchFilters{}, fmt.Errorf("invalid end date %q: %w", filters.EndDate, err)
			}
		}
		if end.IsZero() {
			end = time.Now()
		}
		legacy.DateRange = &DateRange{
			Start: start,
			End:   end,
		}
	} else if filters.Date != "" {
		date, err := time.Parse(mapper.DateFormat, filters.Date)
		if err != nil {
			return AdvancedSearchFilters{}, fmt.Errorf("invalid date %q: %w", filters.Date, err)
		}
		legacy.DateRange = &DateRange{
			Start: date,
			End:   date.Add(24*time.Hour - time.Second), // End of day
		}
	}

	// Convert hour range
	if filters.HourRange != nil {
		legacy.Hour = &HourFilter{
			Start: filters.HourRange.Start,
			End:   filters.HourRange.End,
		}
	}

	// Convert confidence
	if filters.Confidence != nil {
		legacy.Confidence = &ConfidenceFilter{
			Operator: filters.Confidence.Operator,
			Value:    filters.Confidence.Value,
		}
	}

	return legacy, nil
}

// parseID converts a string ID to uint.
func (r *detectionRepository) parseID(id string) uint {
	n, _ := strconv.ParseUint(id, 10, 64)
	return uint(n)
}
