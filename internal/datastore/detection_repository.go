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

const (
	// allHoursDuration indicates no hour-based filtering (all hours).
	allHoursDuration = 1
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
	note := NoteFromResult(result)

	// Convert additional results to legacy Results
	results := AdditionalResultsToDatastoreResults(additionalResults)

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
	if err := r.store.Delete(id); err != nil {
		return fmt.Errorf("failed to delete detection %s: %w", id, err)
	}
	return nil
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
	limit := defaultDetectionLimit
	offset := 0
	if filters != nil {
		if filters.Limit > 0 {
			limit = filters.Limit
		}
		offset = filters.Offset
	}

	// SpeciesDetections params: species, date, hour, duration, sortAscending, limit, offset
	// Empty date/hour means all dates/hours; allHoursDuration means no hour-based filtering
	notes, err := r.store.SpeciesDetections(species, "", "", allHoursDuration, false, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get species detections: %w", err)
	}

	// CountSpeciesDetections params: species, date, hour, duration
	total, err := r.store.CountSpeciesDetections(species, "", "", allHoursDuration)
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
	start, err := r.parseDateWithDefault(startDate, false)
	if err != nil {
		return nil, 0, err
	}

	end, err := r.parseDateWithDefault(endDate, true)
	if err != nil {
		return nil, 0, err
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
	if err := r.store.LockNote(id); err != nil {
		return fmt.Errorf("failed to lock detection %s: %w", id, err)
	}
	return nil
}

// Unlock removes a lock from a detection.
func (r *detectionRepository) Unlock(ctx context.Context, id string) error {
	if err := r.store.UnlockNote(id); err != nil {
		return fmt.Errorf("failed to unlock detection %s: %w", id, err)
	}
	return nil
}

// IsLocked checks if a detection is locked.
func (r *detectionRepository) IsLocked(ctx context.Context, id string) (bool, error) {
	locked, err := r.store.IsNoteLocked(id)
	if err != nil {
		return false, fmt.Errorf("failed to check lock status for detection %s: %w", id, err)
	}
	return locked, nil
}

// SetReview sets the review status of a detection.
func (r *detectionRepository) SetReview(ctx context.Context, id, verified string) error {
	noteID, err := r.parseID(id)
	if err != nil {
		return err
	}
	review := &NoteReview{
		NoteID:    noteID,
		Verified:  verified,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := r.store.SaveNoteReview(review); err != nil {
		return fmt.Errorf("failed to set review for detection %s: %w", id, err)
	}
	return nil
}

// GetReview retrieves the review status of a detection.
func (r *detectionRepository) GetReview(ctx context.Context, id string) (string, error) {
	review, err := r.store.GetNoteReview(id)
	if err != nil {
		return "", fmt.Errorf("failed to get review for detection %s: %w", id, err)
	}
	if review == nil {
		return "", nil
	}
	return review.Verified, nil
}

// AddComment adds a comment to a detection.
func (r *detectionRepository) AddComment(ctx context.Context, id, comment string) error {
	noteID, err := r.parseID(id)
	if err != nil {
		return err
	}
	noteComment := &NoteComment{
		NoteID:    noteID,
		Entry:     comment,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := r.store.SaveNoteComment(noteComment); err != nil {
		return fmt.Errorf("failed to add comment to detection %s: %w", id, err)
	}
	return nil
}

// GetComments retrieves comments for a detection.
func (r *detectionRepository) GetComments(ctx context.Context, id string) ([]detection.Comment, error) {
	noteComments, err := r.store.GetNoteComments(id)
	if err != nil {
		return nil, fmt.Errorf("failed to get comments for detection %s: %w", id, err)
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
	if err := r.store.UpdateNoteComment(strconv.FormatUint(uint64(commentID), 10), entry); err != nil {
		return fmt.Errorf("failed to update comment %d: %w", commentID, err)
	}
	return nil
}

// DeleteComment deletes a comment.
func (r *detectionRepository) DeleteComment(ctx context.Context, commentID uint) error {
	if err := r.store.DeleteNoteComment(strconv.FormatUint(uint64(commentID), 10)); err != nil {
		return fmt.Errorf("failed to delete comment %d: %w", commentID, err)
	}
	return nil
}

// GetClipPath returns the audio clip path for a detection.
func (r *detectionRepository) GetClipPath(ctx context.Context, id string) (string, error) {
	path, err := r.store.GetNoteClipPath(id)
	if err != nil {
		return "", fmt.Errorf("failed to get clip path for detection %s: %w", id, err)
	}
	return path, nil
}

// GetAdditionalResults returns the secondary predictions for a detection.
func (r *detectionRepository) GetAdditionalResults(ctx context.Context, id string) ([]detection.AdditionalResult, error) {
	results, err := r.store.GetNoteResults(id)
	if err != nil {
		return nil, fmt.Errorf("failed to get additional results for detection %s: %w", id, err)
	}

	if len(results) == 0 {
		return nil, nil
	}

	additional := make([]detection.AdditionalResult, len(results))
	for i, res := range results {
		additional[i] = detection.AdditionalResult{
			Species:    detection.ParseSpeciesString(res.Species),
			Confidence: float64(res.Confidence),
		}
	}
	return additional, nil
}

// NoteFromResult converts a detection.Result to a datastore.Note.
// This is exported for use by action structs (DatabaseAction, MqttAction, SSEAction)
// that need to convert the domain model to the legacy Note type for persistence
// or JSON serialization.
//
// Note: The Results field is not populated. Callers should use
// AdditionalResultsToDatastoreResults separately if needed.
func NoteFromResult(result *detection.Result) Note {
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
		Verified:   result.Verified,
		Locked:     result.Locked,
	}
}

// AdditionalResultsToDatastoreResults converts a slice of detection.AdditionalResult
// to datastore.Results for database persistence or JSON serialization.
func AdditionalResultsToDatastoreResults(results []detection.AdditionalResult) []Results {
	if len(results) == 0 {
		return nil
	}

	dsResults := make([]Results, len(results))
	for i, r := range results {
		speciesStr := r.Species.ScientificName + "_" + r.Species.CommonName
		if r.Species.Code != "" {
			speciesStr += "_" + r.Species.Code
		}
		dsResults[i] = Results{
			Species:    speciesStr,
			Confidence: float32(r.Confidence),
		}
	}
	return dsResults
}

// Helper methods

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
			Type:        detection.DetermineSourceType(note.Source.SafeString),
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
			Limit:  defaultDetectionLimit,
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

	// Convert date range - parse strings to time.Time using repository timezone
	if filters.StartDate != "" || filters.EndDate != "" {
		start, err := r.parseDateWithDefault(filters.StartDate, false)
		if err != nil {
			return AdvancedSearchFilters{}, err
		}
		end, err := r.parseDateWithDefault(filters.EndDate, true)
		if err != nil {
			return AdvancedSearchFilters{}, err
		}
		legacy.DateRange = &DateRange{
			Start: start,
			End:   end,
		}
	} else if filters.Date != "" {
		date, err := r.parseDateWithDefault(filters.Date, false)
		if err != nil {
			return AdvancedSearchFilters{}, err
		}
		legacy.DateRange = &DateRange{
			Start: date,
			End:   date.Add(24*time.Hour - time.Nanosecond), // End of day (23:59:59.999999999)
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
func (r *detectionRepository) parseID(id string) (uint, error) {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid detection ID %q: %w", id, err)
	}
	return uint(n), nil
}

// parseDateWithDefault parses a date string, returning a default if empty.
// If defaultToNow is true and the string is empty, returns current time in the timezone.
func (r *detectionRepository) parseDateWithDefault(dateStr string, defaultToNow bool) (time.Time, error) {
	if dateStr == "" {
		if defaultToNow {
			return time.Now().In(r.tz), nil
		}
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation(mapper.DateFormat, dateStr, r.tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: %w", dateStr, err)
	}
	return t, nil
}
