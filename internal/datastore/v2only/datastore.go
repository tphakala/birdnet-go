// Package v2only provides a datastore implementation using only the v2 schema.
// This is used after migration completes when the legacy database is no longer needed.
package v2only

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
)

// Sentinel errors for operations not supported in v2-only mode.
var (
	// ErrOperationNotSupported indicates an operation is not available in v2-only mode.
	ErrOperationNotSupported = errors.NewStd("operation not supported in v2-only mode")
	// ErrNotImplemented indicates a feature requires implementation.
	ErrNotImplemented = errors.NewStd("not implemented in v2-only datastore")
)

// parseID converts a string ID to uint.
func parseID(id string) (uint, error) {
	parsed, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid ID %q: %w", id, err)
	}
	return uint(parsed), nil
}

// Datastore implements datastore.Interface using only v2 repositories.
type Datastore struct {
	manager      v2.Manager
	detection    repository.DetectionRepository
	label        repository.LabelRepository
	model        repository.ModelRepository
	source       repository.AudioSourceRepository
	weather      repository.WeatherRepository
	imageCache   repository.ImageCacheRepository
	threshold    repository.DynamicThresholdRepository
	notification repository.NotificationHistoryRepository
	log          logger.Logger
	metrics      *datastore.Metrics
	timezone     *time.Location
}

// Config configures the Datastore.
type Config struct {
	Manager      v2.Manager
	Detection    repository.DetectionRepository
	Label        repository.LabelRepository
	Model        repository.ModelRepository
	Source       repository.AudioSourceRepository
	Weather      repository.WeatherRepository
	ImageCache   repository.ImageCacheRepository
	Threshold    repository.DynamicThresholdRepository
	Notification repository.NotificationHistoryRepository
	Logger       logger.Logger
	Timezone     *time.Location
}

// New creates a new V2-only Datastore.
func New(cfg *Config) (*Datastore, error) {
	if cfg.Manager == nil {
		return nil, fmt.Errorf("manager is required")
	}
	if cfg.Detection == nil {
		return nil, fmt.Errorf("detection repository is required")
	}
	if cfg.Label == nil {
		return nil, fmt.Errorf("label repository is required")
	}
	if cfg.Model == nil {
		return nil, fmt.Errorf("model repository is required")
	}

	tz := cfg.Timezone
	if tz == nil {
		tz = time.Local
	}

	return &Datastore{
		manager:      cfg.Manager,
		detection:    cfg.Detection,
		label:        cfg.Label,
		model:        cfg.Model,
		source:       cfg.Source,
		weather:      cfg.Weather,
		imageCache:   cfg.ImageCache,
		threshold:    cfg.Threshold,
		notification: cfg.Notification,
		log:          cfg.Logger,
		timezone:     tz,
	}, nil
}

// Open is a no-op since the manager is already open.
func (ds *Datastore) Open() error {
	return nil
}

// Close closes the datastore.
func (ds *Datastore) Close() error {
	if ds.manager != nil {
		if !ds.manager.IsMySQL() {
			_ = ds.manager.CheckpointWAL()
		}
		return ds.manager.Close()
	}
	return nil
}

// SetMetrics sets the metrics instance.
func (ds *Datastore) SetMetrics(metrics *datastore.Metrics) {
	ds.metrics = metrics
}

// Manager returns the underlying database manager.
// This allows access to the manager for API endpoints that need it.
func (ds *Datastore) Manager() v2.Manager {
	return ds.manager
}

// SetSunCalcMetrics sets the SunCalc metrics instance.
func (ds *Datastore) SetSunCalcMetrics(_ any) {}

// Optimize performs database optimization.
func (ds *Datastore) Optimize(ctx context.Context) error {
	if !ds.manager.IsMySQL() {
		db := ds.manager.DB()
		if err := db.WithContext(ctx).Exec("VACUUM").Error; err != nil {
			return fmt.Errorf("VACUUM failed: %w", err)
		}
		return db.WithContext(ctx).Exec("ANALYZE").Error
	}
	return nil
}

// Transaction runs a function within a database transaction.
func (ds *Datastore) Transaction(fc func(tx *gorm.DB) error) error {
	return ds.manager.DB().Transaction(fc)
}

// GetDatabaseStats returns database statistics.
func (ds *Datastore) GetDatabaseStats() (*datastore.DatabaseStats, error) {
	ctx := context.Background()
	count, err := ds.detection.CountAll(ctx)
	if err != nil {
		return nil, err
	}

	dbType := "sqlite"
	if ds.manager.IsMySQL() {
		dbType = "mysql"
	}

	return &datastore.DatabaseStats{
		Type:            dbType,
		TotalDetections: count,
		Connected:       true,
		Location:        ds.manager.Path(),
	}, nil
}

// Save saves a note with its results.
func (ds *Datastore) Save(note *datastore.Note, results []datastore.Results) error {
	ctx := context.Background()

	label, err := ds.label.GetOrCreate(ctx, note.ScientificName, entities.LabelTypeSpecies)
	if err != nil {
		return fmt.Errorf("failed to get/create label: %w", err)
	}

	modelInfo := detection.DefaultModelInfo()
	model, err := ds.model.GetOrCreate(ctx, modelInfo.Name, modelInfo.Version, entities.ModelTypeBird)
	if err != nil {
		return fmt.Errorf("failed to get/create model: %w", err)
	}

	// Parse the date string and time string to get Unix timestamp
	var detectedAt int64
	if note.Date != "" && note.Time != "" {
		dateTimeStr := note.Date + " " + note.Time
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", dateTimeStr, ds.timezone); err == nil {
			detectedAt = t.Unix()
		}
	} else if note.Date != "" {
		if t, err := time.ParseInLocation("2006-01-02", note.Date, ds.timezone); err == nil {
			detectedAt = t.Unix()
		}
	}
	if detectedAt == 0 {
		detectedAt = time.Now().Unix()
	}

	det := &entities.Detection{
		LabelID:    label.ID,
		ModelID:    model.ID,
		DetectedAt: detectedAt,
		Confidence: note.Confidence,
	}

	if note.Latitude != 0 {
		det.Latitude = &note.Latitude
	}
	if note.Longitude != 0 {
		det.Longitude = &note.Longitude
	}
	if note.ClipName != "" {
		det.ClipName = &note.ClipName
	}

	if err := ds.detection.Save(ctx, det); err != nil {
		return fmt.Errorf("failed to save detection: %w", err)
	}

	if len(results) > 0 {
		preds := make([]*entities.DetectionPrediction, 0, len(results))
		for _, r := range results {
			predLabel, err := ds.label.GetOrCreate(ctx, r.Species, entities.LabelTypeSpecies)
			if err != nil {
				return fmt.Errorf("failed to get/create prediction label for %s: %w", r.Species, err)
			}
			preds = append(preds, &entities.DetectionPrediction{
				DetectionID: det.ID,
				LabelID:     predLabel.ID,
				Confidence:  float64(r.Confidence),
			})
		}
		if len(preds) > 0 {
			if err := ds.detection.SavePredictions(ctx, det.ID, preds); err != nil {
				return fmt.Errorf("failed to save predictions: %w", err)
			}
		}
	}

	return nil
}

// Delete deletes a note by ID.
func (ds *Datastore) Delete(id string) error {
	ctx := context.Background()
	noteID, err := parseID(id)
	if err != nil {
		return err
	}
	return ds.detection.Delete(ctx, noteID)
}

// Get retrieves a note by ID.
func (ds *Datastore) Get(id string) (datastore.Note, error) {
	ctx := context.Background()
	noteID, err := parseID(id)
	if err != nil {
		return datastore.Note{}, err
	}

	det, err := ds.detection.GetWithRelations(ctx, noteID)
	if err != nil {
		return datastore.Note{}, err
	}

	return ds.detectionToNote(det, nil), nil
}

// detectionToNote converts a v2 Detection to a legacy Note.
// If rawLabelsMap is provided, it will be used to look up the common name
// instead of making a database query (for batch efficiency).
func (ds *Datastore) detectionToNote(det *entities.Detection, rawLabelsMap map[string]string) datastore.Note {
	scientificName := ""
	// Try to get scientific name from preloaded Label first
	if det.Label != nil && det.Label.ScientificName != nil {
		scientificName = *det.Label.ScientificName
	} else if det.LabelID > 0 && ds.label != nil {
		// Label not preloaded, fetch it from the repository
		ctx := context.Background()
		if label, err := ds.label.GetByID(ctx, det.LabelID); err == nil && label != nil && label.ScientificName != nil {
			scientificName = *label.ScientificName
		}
	}

	// Resolve common name from raw_label
	commonName := scientificName // Default to scientific name
	if det.ModelID > 0 && det.LabelID > 0 {
		// Try to get from preloaded map first (batch mode)
		key := fmt.Sprintf("%d:%d", det.ModelID, det.LabelID)
		if rawLabelsMap != nil {
			if rawLabel, ok := rawLabelsMap[key]; ok && rawLabel != "" {
				if extracted := extractCommonName(rawLabel); extracted != "" {
					commonName = extracted
				}
			}
		} else if ds.label != nil {
			// Fallback to single query (for single detection lookups)
			ctx := context.Background()
			rawLabel, err := ds.label.GetRawLabelForLabel(ctx, det.ModelID, det.LabelID)
			if err == nil && rawLabel != "" {
				if extracted := extractCommonName(rawLabel); extracted != "" {
					commonName = extracted
				}
			}
		}
	}

	clipName := ""
	if det.ClipName != nil {
		clipName = *det.ClipName
	}

	lat := 0.0
	if det.Latitude != nil {
		lat = *det.Latitude
	}
	lon := 0.0
	if det.Longitude != nil {
		lon = *det.Longitude
	}

	// Convert Unix timestamp to date and time strings
	t := time.Unix(det.DetectedAt, 0).In(ds.timezone)
	dateStr := t.Format("2006-01-02")
	timeStr := t.Format("15:04:05")

	return datastore.Note{
		ID:             det.ID,
		Date:           dateStr,
		Time:           timeStr,
		ScientificName: scientificName,
		CommonName:     commonName,
		Confidence:     det.Confidence,
		Latitude:       lat,
		Longitude:      lon,
		ClipName:       clipName,
	}
}

// extractCommonName extracts the common name from a raw label string.
// The raw_label format is typically "ScientificName_CommonName" where
// the common name may contain underscores that should be preserved.
func extractCommonName(rawLabel string) string {
	// Split on underscore, but common name is everything after the first underscore
	// (since scientific names are always two words: "Genus species")
	parts := strings.SplitN(rawLabel, "_", 2)
	if len(parts) >= 2 {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// detectionsToNotes converts multiple detections to notes.
// Uses batch fetching of raw_labels to avoid N+1 query problem.
func (ds *Datastore) detectionsToNotes(dets []*entities.Detection) []datastore.Note {
	if len(dets) == 0 {
		return []datastore.Note{}
	}

	// Collect unique (modelID, labelID) pairs for batch fetching
	pairSet := make(map[string]repository.ModelLabelPair)
	for _, det := range dets {
		if det.ModelID > 0 && det.LabelID > 0 {
			key := fmt.Sprintf("%d:%d", det.ModelID, det.LabelID)
			if _, exists := pairSet[key]; !exists {
				pairSet[key] = repository.ModelLabelPair{
					ModelID: det.ModelID,
					LabelID: det.LabelID,
				}
			}
		}
	}

	// Batch fetch raw_labels
	var rawLabelsMap map[string]string
	if len(pairSet) > 0 && ds.label != nil {
		pairs := make([]repository.ModelLabelPair, 0, len(pairSet))
		for _, pair := range pairSet {
			pairs = append(pairs, pair)
		}
		ctx := context.Background()
		var err error
		rawLabelsMap, err = ds.label.GetRawLabelsForLabels(ctx, pairs)
		if err != nil {
			// Log error but continue - common names will fall back to scientific names
			if ds.log != nil {
				ds.log.Debug("failed to batch fetch raw_labels for common names",
					logger.Error(err))
			}
			rawLabelsMap = make(map[string]string)
		}
	}

	// Convert detections to notes using the preloaded map
	notes := make([]datastore.Note, 0, len(dets))
	for _, det := range dets {
		notes = append(notes, ds.detectionToNote(det, rawLabelsMap))
	}
	return notes
}

// GetAllNotes retrieves all notes.
func (ds *Datastore) GetAllNotes() ([]datastore.Note, error) {
	ctx := context.Background()
	filters := &repository.SearchFilters{
		Limit:    10000,
		SortBy:   "detected_at",
		SortDesc: true,
	}

	dets, _, err := ds.detection.Search(ctx, filters)
	if err != nil {
		return nil, err
	}

	return ds.detectionsToNotes(dets), nil
}

// GetTopBirdsData retrieves top birds data for a date.
func (ds *Datastore) GetTopBirdsData(selectedDate string, minConfidenceNormalized float64) ([]datastore.Note, error) {
	ctx := context.Background()

	t, err := time.ParseInLocation("2006-01-02", selectedDate, ds.timezone)
	if err != nil {
		return nil, err
	}
	startTime := t.Unix()
	endTime := t.Add(24 * time.Hour).Unix()

	filters := &repository.SearchFilters{
		StartTime:     &startTime,
		EndTime:       &endTime,
		MinConfidence: &minConfidenceNormalized,
		Limit:         100,
		SortBy:        "detected_at",
		SortDesc:      true,
	}

	dets, _, err := ds.detection.Search(ctx, filters)
	if err != nil {
		return nil, err
	}

	return ds.detectionsToNotes(dets), nil
}

// GetHourlyOccurrences retrieves hourly occurrences for a species on a date.
func (ds *Datastore) GetHourlyOccurrences(date, commonName string, minConfidenceNormalized float64) ([24]int, error) {
	ctx := context.Background()
	var hourly [24]int

	label, err := ds.label.GetByScientificName(ctx, commonName)
	if err != nil {
		if errors.Is(err, repository.ErrLabelNotFound) {
			return hourly, nil
		}
		return hourly, err
	}

	t, err := time.ParseInLocation("2006-01-02", date, ds.timezone)
	if err != nil {
		return hourly, fmt.Errorf("invalid date format: %w", err)
	}

	startTime := t.Unix()
	endTime := t.Add(24 * time.Hour).Unix()

	result, err := ds.detection.GetHourlyOccurrences(ctx, label.ID, startTime, endTime)
	if err != nil {
		return hourly, err
	}

	return result, nil
}

// SpeciesDetections retrieves detections for a species.
func (ds *Datastore) SpeciesDetections(species, date, hour string, duration int, sortAscending bool, limit, offset int) ([]datastore.Note, error) {
	ctx := context.Background()

	var startTime, endTime *int64
	if date != "" {
		t, err := time.ParseInLocation("2006-01-02", date, ds.timezone)
		if err == nil {
			if hour != "" {
				h, _ := strconv.Atoi(hour)
				t = t.Add(time.Duration(h) * time.Hour)
			}
			start := t.Unix()
			end := t.Add(time.Duration(duration) * time.Hour).Unix()
			if duration == 0 {
				end = t.Add(24 * time.Hour).Unix()
			}
			startTime = &start
			endTime = &end
		}
	}

	var labelIDs []uint
	if species != "" {
		label, err := ds.label.GetByScientificName(ctx, species)
		if err != nil {
			// Species not found - return empty results instead of all detections
			if errors.Is(err, repository.ErrLabelNotFound) {
				return []datastore.Note{}, nil
			}
			return nil, err
		}
		labelIDs = []uint{label.ID}
	}

	filters := &repository.SearchFilters{
		LabelIDs:  labelIDs,
		StartTime: startTime,
		EndTime:   endTime,
		Limit:     limit,
		Offset:    offset,
		SortBy:    "detected_at",
		SortDesc:  !sortAscending,
	}

	dets, _, err := ds.detection.Search(ctx, filters)
	if err != nil {
		return nil, err
	}

	return ds.detectionsToNotes(dets), nil
}

// GetLastDetections retrieves the last N detections.
func (ds *Datastore) GetLastDetections(numDetections int) ([]datastore.Note, error) {
	ctx := context.Background()
	dets, err := ds.detection.GetRecent(ctx, numDetections)
	if err != nil {
		return nil, err
	}
	return ds.detectionsToNotes(dets), nil
}

// GetAllDetectedSpecies retrieves all detected species.
func (ds *Datastore) GetAllDetectedSpecies() ([]datastore.Note, error) {
	ctx := context.Background()
	labels, err := ds.label.GetAllByType(ctx, entities.LabelTypeSpecies)
	if err != nil {
		return nil, err
	}

	notes := make([]datastore.Note, 0, len(labels))
	for i := range labels {
		if labels[i].ScientificName != nil {
			notes = append(notes, datastore.Note{
				ScientificName: *labels[i].ScientificName,
			})
		}
	}
	return notes, nil
}

// SearchNotes searches notes by query string.
func (ds *Datastore) SearchNotes(query string, sortAscending bool, limit, offset int) ([]datastore.Note, error) {
	ctx := context.Background()
	filters := &repository.SearchFilters{
		Query:    query,
		Limit:    limit,
		Offset:   offset,
		SortBy:   "detected_at",
		SortDesc: !sortAscending,
	}

	dets, _, err := ds.detection.Search(ctx, filters)
	if err != nil {
		return nil, err
	}

	return ds.detectionsToNotes(dets), nil
}

// SearchNotesAdvanced performs advanced search with filters.
// Converts all AdvancedSearchFilters fields to repository SearchFilters.
func (ds *Datastore) SearchNotesAdvanced(filters *datastore.AdvancedSearchFilters) ([]datastore.Note, int64, error) {
	ctx := context.Background()

	// Set up dependencies for entity lookups
	deps := &repository.FilterLookupDeps{
		LabelRepo:  ds.label,
		SourceRepo: ds.source,
	}

	// Convert API-level filters to repository filters
	repoFilters, err := repository.ConvertAdvancedFilters(ctx, filters, deps, ds.timezone)
	if err != nil {
		return nil, 0, err
	}

	// Execute search
	dets, total, err := ds.detection.Search(ctx, repoFilters)
	if err != nil {
		return nil, 0, err
	}

	return ds.detectionsToNotes(dets), total, nil
}

// GetNoteClipPath retrieves the clip path for a note.
func (ds *Datastore) GetNoteClipPath(noteID string) (string, error) {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return "", err
	}
	return ds.detection.GetClipPath(ctx, id)
}

// DeleteNoteClipPath deletes the clip path for a note.
func (ds *Datastore) DeleteNoteClipPath(noteID string) error {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return err
	}
	return ds.detection.Update(ctx, id, map[string]any{"clip_name": nil})
}

// GetNoteReview retrieves the review for a note.
func (ds *Datastore) GetNoteReview(noteID string) (*datastore.NoteReview, error) {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return nil, err
	}

	review, err := ds.detection.GetReview(ctx, id)
	if err != nil {
		return nil, err
	}

	return &datastore.NoteReview{
		ID:        review.ID,
		NoteID:    id,
		Verified:  string(review.Verified),
		CreatedAt: review.CreatedAt,
		UpdatedAt: review.UpdatedAt,
	}, nil
}

// SaveNoteReview saves a review for a note.
func (ds *Datastore) SaveNoteReview(review *datastore.NoteReview) error {
	ctx := context.Background()

	v2Review := &entities.DetectionReview{
		DetectionID: review.NoteID,
		Verified:    entities.VerificationStatus(review.Verified),
	}

	return ds.detection.SaveReview(ctx, v2Review)
}

// GetNoteComments retrieves comments for a note.
func (ds *Datastore) GetNoteComments(noteID string) ([]datastore.NoteComment, error) {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return nil, err
	}

	comments, err := ds.detection.GetComments(ctx, id)
	if err != nil {
		return nil, err
	}

	result := make([]datastore.NoteComment, 0, len(comments))
	for _, c := range comments {
		result = append(result, datastore.NoteComment{
			ID:        c.ID,
			NoteID:    id,
			Entry:     c.Entry,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		})
	}

	return result, nil
}

// GetNoteResults retrieves additional predictions for a note.
func (ds *Datastore) GetNoteResults(noteID string) ([]datastore.Results, error) {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return nil, err
	}

	preds, err := ds.detection.GetPredictions(ctx, id)
	if err != nil {
		return nil, err
	}

	results := make([]datastore.Results, 0, len(preds))
	for _, pred := range preds {
		label, _ := ds.label.GetByID(ctx, pred.LabelID)
		scientificName := ""
		if label != nil && label.ScientificName != nil {
			scientificName = *label.ScientificName
		}

		results = append(results, datastore.Results{
			ID:         pred.ID,
			Species:    scientificName,
			Confidence: float32(pred.Confidence),
		})
	}

	return results, nil
}

// SaveNoteComment saves a comment for a note.
func (ds *Datastore) SaveNoteComment(comment *datastore.NoteComment) error {
	ctx := context.Background()

	v2Comment := &entities.DetectionComment{
		DetectionID: comment.NoteID,
		Entry:       comment.Entry,
		CreatedAt:   comment.CreatedAt,
	}

	return ds.detection.SaveComment(ctx, v2Comment)
}

// UpdateNoteComment updates a comment.
func (ds *Datastore) UpdateNoteComment(commentID, entry string) error {
	ctx := context.Background()
	id, err := parseID(commentID)
	if err != nil {
		return err
	}
	return ds.detection.UpdateComment(ctx, id, entry)
}

// DeleteNoteComment deletes a comment.
func (ds *Datastore) DeleteNoteComment(commentID string) error {
	ctx := context.Background()
	id, err := parseID(commentID)
	if err != nil {
		return err
	}
	return ds.detection.DeleteComment(ctx, id)
}

// ============================================================
// Weather Methods
// ============================================================

// SaveDailyEvents saves daily weather events.
func (ds *Datastore) SaveDailyEvents(dailyEvents *datastore.DailyEvents) error {
	if ds.weather == nil {
		return fmt.Errorf("weather repository not configured")
	}
	ctx := context.Background()
	v2Events := &entities.DailyEvents{
		Date:     dailyEvents.Date,
		Sunrise:  dailyEvents.Sunrise,
		Sunset:   dailyEvents.Sunset,
		Country:  dailyEvents.Country,
		CityName: dailyEvents.CityName,
	}
	return ds.weather.SaveDailyEvents(ctx, v2Events)
}

// GetDailyEvents retrieves daily events for a date.
func (ds *Datastore) GetDailyEvents(date string) (datastore.DailyEvents, error) {
	if ds.weather == nil {
		return datastore.DailyEvents{}, fmt.Errorf("weather repository not configured")
	}
	ctx := context.Background()
	events, err := ds.weather.GetDailyEvents(ctx, date)
	if err != nil {
		return datastore.DailyEvents{}, err
	}
	return datastore.DailyEvents{
		ID:       events.ID,
		Date:     events.Date,
		Sunrise:  events.Sunrise,
		Sunset:   events.Sunset,
		Country:  events.Country,
		CityName: events.CityName,
	}, nil
}

// GetAllDailyEvents returns all daily events (used for migration, not needed in v2-only mode).
func (ds *Datastore) GetAllDailyEvents() ([]datastore.DailyEvents, error) {
	return nil, fmt.Errorf("GetAllDailyEvents: %w", ErrOperationNotSupported)
}

// GetAllHourlyWeather returns all hourly weather (used for migration, not needed in v2-only mode).
func (ds *Datastore) GetAllHourlyWeather() ([]datastore.HourlyWeather, error) {
	return nil, fmt.Errorf("GetAllHourlyWeather: %w", ErrOperationNotSupported)
}

// SaveHourlyWeather saves hourly weather data.
func (ds *Datastore) SaveHourlyWeather(hourlyWeather *datastore.HourlyWeather) error {
	if ds.weather == nil {
		return fmt.Errorf("weather repository not configured")
	}
	ctx := context.Background()
	v2Weather := &entities.HourlyWeather{
		DailyEventsID: hourlyWeather.DailyEventsID,
		Time:          hourlyWeather.Time,
		Temperature:   hourlyWeather.Temperature,
		FeelsLike:     hourlyWeather.FeelsLike,
		TempMin:       hourlyWeather.TempMin,
		TempMax:       hourlyWeather.TempMax,
		Pressure:      hourlyWeather.Pressure,
		Humidity:      hourlyWeather.Humidity,
		Visibility:    hourlyWeather.Visibility,
		WindSpeed:     hourlyWeather.WindSpeed,
		WindDeg:       hourlyWeather.WindDeg,
		WindGust:      hourlyWeather.WindGust,
		Clouds:        hourlyWeather.Clouds,
		WeatherMain:   hourlyWeather.WeatherMain,
		WeatherDesc:   hourlyWeather.WeatherDesc,
		WeatherIcon:   hourlyWeather.WeatherIcon,
	}
	return ds.weather.SaveHourlyWeather(ctx, v2Weather)
}

// GetHourlyWeather retrieves hourly weather for a date.
func (ds *Datastore) GetHourlyWeather(date string) ([]datastore.HourlyWeather, error) {
	if ds.weather == nil {
		return nil, fmt.Errorf("weather repository not configured")
	}
	ctx := context.Background()
	v2Weather, err := ds.weather.GetHourlyWeather(ctx, date)
	if err != nil {
		return nil, err
	}
	result := make([]datastore.HourlyWeather, 0, len(v2Weather))
	for i := range v2Weather {
		w := &v2Weather[i]
		result = append(result, datastore.HourlyWeather{
			ID:            w.ID,
			DailyEventsID: w.DailyEventsID,
			Time:          w.Time,
			Temperature:   w.Temperature,
			FeelsLike:     w.FeelsLike,
			TempMin:       w.TempMin,
			TempMax:       w.TempMax,
			Pressure:      w.Pressure,
			Humidity:      w.Humidity,
			Visibility:    w.Visibility,
			WindSpeed:     w.WindSpeed,
			WindDeg:       w.WindDeg,
			WindGust:      w.WindGust,
			Clouds:        w.Clouds,
			WeatherMain:   w.WeatherMain,
			WeatherDesc:   w.WeatherDesc,
			WeatherIcon:   w.WeatherIcon,
		})
	}
	return result, nil
}

// LatestHourlyWeather retrieves the most recent hourly weather record.
func (ds *Datastore) LatestHourlyWeather() (*datastore.HourlyWeather, error) {
	if ds.weather == nil {
		return nil, fmt.Errorf("weather repository not configured")
	}
	ctx := context.Background()
	w, err := ds.weather.LatestHourlyWeather(ctx)
	if err != nil {
		return nil, err
	}
	return &datastore.HourlyWeather{
		ID:            w.ID,
		DailyEventsID: w.DailyEventsID,
		Time:          w.Time,
		Temperature:   w.Temperature,
		FeelsLike:     w.FeelsLike,
		TempMin:       w.TempMin,
		TempMax:       w.TempMax,
		Pressure:      w.Pressure,
		Humidity:      w.Humidity,
		Visibility:    w.Visibility,
		WindSpeed:     w.WindSpeed,
		WindDeg:       w.WindDeg,
		WindGust:      w.WindGust,
		Clouds:        w.Clouds,
		WeatherMain:   w.WeatherMain,
		WeatherDesc:   w.WeatherDesc,
		WeatherIcon:   w.WeatherIcon,
	}, nil
}

// ============================================================
// Detection Count/Search Methods
// ============================================================

// GetHourlyDetections retrieves detections for a specific hour.
func (ds *Datastore) GetHourlyDetections(date, hour string, duration, limit, offset int) ([]datastore.Note, error) {
	ctx := context.Background()
	t, err := time.ParseInLocation("2006-01-02", date, ds.timezone)
	if err != nil {
		return nil, err
	}
	h, _ := strconv.Atoi(hour)
	t = t.Add(time.Duration(h) * time.Hour)
	startTime := t.Unix()
	endTime := t.Add(time.Duration(duration) * time.Hour).Unix()

	filters := &repository.SearchFilters{
		StartTime: &startTime,
		EndTime:   &endTime,
		Limit:     limit,
		Offset:    offset,
		SortBy:    "detected_at",
		SortDesc:  true,
	}
	dets, _, err := ds.detection.Search(ctx, filters)
	if err != nil {
		return nil, err
	}
	return ds.detectionsToNotes(dets), nil
}

// CountSpeciesDetections counts detections for a species.
func (ds *Datastore) CountSpeciesDetections(species, date, hour string, duration int) (int64, error) {
	ctx := context.Background()
	var startTime, endTime *int64
	if date != "" {
		t, err := time.ParseInLocation("2006-01-02", date, ds.timezone)
		if err == nil {
			if hour != "" {
				h, _ := strconv.Atoi(hour)
				t = t.Add(time.Duration(h) * time.Hour)
			}
			start := t.Unix()
			end := t.Add(time.Duration(duration) * time.Hour).Unix()
			if duration == 0 {
				end = t.Add(24 * time.Hour).Unix()
			}
			startTime = &start
			endTime = &end
		}
	}

	var labelIDs []uint
	if species != "" {
		label, err := ds.label.GetByScientificName(ctx, species)
		if err != nil {
			// Species not found - return zero count instead of all detections count
			if errors.Is(err, repository.ErrLabelNotFound) {
				return 0, nil
			}
			return 0, err
		}
		labelIDs = []uint{label.ID}
	}

	filters := &repository.SearchFilters{
		LabelIDs:  labelIDs,
		StartTime: startTime,
		EndTime:   endTime,
	}
	_, count, err := ds.detection.Search(ctx, filters)
	return count, err
}

// CountSearchResults counts search results.
func (ds *Datastore) CountSearchResults(query string) (int64, error) {
	ctx := context.Background()
	filters := &repository.SearchFilters{Query: query}
	_, count, err := ds.detection.Search(ctx, filters)
	return count, err
}

// CountHourlyDetections counts detections for a specific hour.
func (ds *Datastore) CountHourlyDetections(date, hour string, duration int) (int64, error) {
	ctx := context.Background()
	t, err := time.ParseInLocation("2006-01-02", date, ds.timezone)
	if err != nil {
		return 0, err
	}
	h, _ := strconv.Atoi(hour)
	t = t.Add(time.Duration(h) * time.Hour)
	startTime := t.Unix()
	endTime := t.Add(time.Duration(duration) * time.Hour).Unix()

	filters := &repository.SearchFilters{
		StartTime: &startTime,
		EndTime:   &endTime,
	}
	_, count, err := ds.detection.Search(ctx, filters)
	return count, err
}

// SearchDetections performs a detection search with filters.
func (ds *Datastore) SearchDetections(filters *datastore.SearchFilters) ([]datastore.DetectionRecord, int, error) {
	// This method requires complex conversion logic not yet implemented
	return nil, 0, fmt.Errorf("SearchDetections: %w", ErrNotImplemented)
}

// ============================================================
// Lock Methods
// ============================================================

// LockNote locks a note.
func (ds *Datastore) LockNote(noteID string) error {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return err
	}
	return ds.detection.Lock(ctx, id)
}

// UnlockNote unlocks a note.
func (ds *Datastore) UnlockNote(noteID string) error {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return err
	}
	return ds.detection.Unlock(ctx, id)
}

// GetNoteLock retrieves the lock for a note.
func (ds *Datastore) GetNoteLock(noteID string) (*datastore.NoteLock, error) {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return nil, err
	}

	// Single query to get the lock - check ErrRecordNotFound for missing lock
	var lock entities.DetectionLock
	err = ds.manager.DB().WithContext(ctx).
		Where("detection_id = ?", id).
		First(&lock).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, datastore.ErrNoteLockNotFound
		}
		return nil, err
	}

	return &datastore.NoteLock{
		ID:       lock.ID,
		NoteID:   id,
		LockedAt: lock.LockedAt,
	}, nil
}

// IsNoteLocked checks if a note is locked.
func (ds *Datastore) IsNoteLocked(noteID string) (bool, error) {
	ctx := context.Background()
	id, err := parseID(noteID)
	if err != nil {
		return false, err
	}
	return ds.detection.IsLocked(ctx, id)
}

// GetLockedNotesClipPaths retrieves clip paths for locked notes.
func (ds *Datastore) GetLockedNotesClipPaths() ([]string, error) {
	ctx := context.Background()
	return ds.detection.GetLockedClipPaths(ctx)
}

// ============================================================
// Image Cache Methods
// ============================================================

// GetImageCache retrieves an image cache entry.
func (ds *Datastore) GetImageCache(query datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	if ds.imageCache == nil {
		return nil, datastore.ErrImageCacheNotFound
	}
	ctx := context.Background()
	cache, err := ds.imageCache.GetImageCache(ctx, query.ProviderName, query.ScientificName)
	if err != nil {
		return nil, err
	}
	return &datastore.ImageCache{
		ID:             cache.ID,
		ProviderName:   cache.ProviderName,
		ScientificName: cache.ScientificName,
		SourceProvider: cache.SourceProvider,
		URL:            cache.URL,
		LicenseName:    cache.LicenseName,
		LicenseURL:     cache.LicenseURL,
		AuthorName:     cache.AuthorName,
		AuthorURL:      cache.AuthorURL,
		CachedAt:       cache.CachedAt,
	}, nil
}

// GetImageCacheBatch retrieves multiple image cache entries.
func (ds *Datastore) GetImageCacheBatch(providerName string, scientificNames []string) (map[string]*datastore.ImageCache, error) {
	if ds.imageCache == nil {
		return make(map[string]*datastore.ImageCache), nil
	}
	ctx := context.Background()
	v2Caches, err := ds.imageCache.GetImageCacheBatch(ctx, providerName, scientificNames)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*datastore.ImageCache)
	for name, cache := range v2Caches {
		result[name] = &datastore.ImageCache{
			ID:             cache.ID,
			ProviderName:   cache.ProviderName,
			ScientificName: cache.ScientificName,
			SourceProvider: cache.SourceProvider,
			URL:            cache.URL,
			LicenseName:    cache.LicenseName,
			LicenseURL:     cache.LicenseURL,
			AuthorName:     cache.AuthorName,
			AuthorURL:      cache.AuthorURL,
			CachedAt:       cache.CachedAt,
		}
	}
	return result, nil
}

// SaveImageCache saves an image cache entry.
func (ds *Datastore) SaveImageCache(cache *datastore.ImageCache) error {
	if ds.imageCache == nil {
		return fmt.Errorf("image cache repository not configured")
	}
	ctx := context.Background()
	v2Cache := &entities.ImageCache{
		ProviderName:   cache.ProviderName,
		ScientificName: cache.ScientificName,
		SourceProvider: cache.SourceProvider,
		URL:            cache.URL,
		LicenseName:    cache.LicenseName,
		LicenseURL:     cache.LicenseURL,
		AuthorName:     cache.AuthorName,
		AuthorURL:      cache.AuthorURL,
		CachedAt:       cache.CachedAt,
	}
	return ds.imageCache.SaveImageCache(ctx, v2Cache)
}

// GetAllImageCaches retrieves all image caches for a provider.
func (ds *Datastore) GetAllImageCaches(providerName string) ([]datastore.ImageCache, error) {
	if ds.imageCache == nil {
		return []datastore.ImageCache{}, nil
	}
	ctx := context.Background()
	v2Caches, err := ds.imageCache.GetAllImageCaches(ctx, providerName)
	if err != nil {
		return nil, err
	}
	result := make([]datastore.ImageCache, 0, len(v2Caches))
	for i := range v2Caches {
		cache := &v2Caches[i]
		result = append(result, datastore.ImageCache{
			ID:             cache.ID,
			ProviderName:   cache.ProviderName,
			ScientificName: cache.ScientificName,
			SourceProvider: cache.SourceProvider,
			URL:            cache.URL,
			LicenseName:    cache.LicenseName,
			LicenseURL:     cache.LicenseURL,
			AuthorName:     cache.AuthorName,
			AuthorURL:      cache.AuthorURL,
			CachedAt:       cache.CachedAt,
		})
	}
	return result, nil
}

// ============================================================
// Analytics Methods
// ============================================================

// parseDateRange parses start and end date strings to Unix timestamps.
// Returns (0, 0) if no dates provided, meaning no date filtering.
func (ds *Datastore) parseDateRange(startDate, endDate string) (start, end int64, err error) {
	if startDate != "" {
		t, parseErr := time.ParseInLocation("2006-01-02", startDate, ds.timezone)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("invalid start date format: %w", parseErr)
		}
		start = t.Unix()
	}
	if endDate != "" {
		t, parseErr := time.ParseInLocation("2006-01-02", endDate, ds.timezone)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("invalid end date format: %w", parseErr)
		}
		// End of day for end date
		end = t.Add(24*time.Hour - time.Second).Unix()
	}
	return start, end, nil
}

// getDefaultModelID retrieves the ID of the default BirdNET model.
// Returns 0 if not found (common name resolution will gracefully degrade).
func (ds *Datastore) getDefaultModelID(ctx context.Context) uint {
	modelInfo := detection.DefaultModelInfo()
	model, err := ds.model.GetByNameVersion(ctx, modelInfo.Name, modelInfo.Version)
	if err != nil || model == nil {
		return 0
	}
	return model.ID
}

// GetSpeciesSummaryData retrieves species summary data.
func (ds *Datastore) GetSpeciesSummaryData(ctx context.Context, startDate, endDate string) ([]datastore.SpeciesSummaryData, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	v2Data, err := ds.detection.GetSpeciesSummary(ctx, start, end, nil)
	if err != nil {
		return nil, err
	}

	// Batch fetch raw_labels for common names using the default model
	modelID := ds.getDefaultModelID(ctx)
	pairs := make([]repository.ModelLabelPair, 0, len(v2Data))
	for _, d := range v2Data {
		pairs = append(pairs, repository.ModelLabelPair{ModelID: modelID, LabelID: d.LabelID})
	}
	rawLabelsMap, _ := ds.label.GetRawLabelsForLabels(ctx, pairs)

	result := make([]datastore.SpeciesSummaryData, 0, len(v2Data))
	for _, d := range v2Data {
		commonName := d.ScientificName
		key := fmt.Sprintf("%d:%d", modelID, d.LabelID)
		if rawLabel, ok := rawLabelsMap[key]; ok && rawLabel != "" {
			if extracted := extractCommonName(rawLabel); extracted != "" {
				commonName = extracted
			}
		}

		result = append(result, datastore.SpeciesSummaryData{
			ScientificName: d.ScientificName,
			CommonName:     commonName,
			SpeciesCode:    "", // Not available in v2 schema
			Count:          int(d.TotalDetections),
			FirstSeen:      time.Unix(d.FirstDetection, 0).In(ds.timezone),
			LastSeen:       time.Unix(d.LastDetection, 0).In(ds.timezone),
			AvgConfidence:  d.AvgConfidence,
			MaxConfidence:  d.MaxConfidence,
		})
	}
	return result, nil
}

// GetHourlyAnalyticsData retrieves hourly analytics data for a specific date and species.
func (ds *Datastore) GetHourlyAnalyticsData(ctx context.Context, date, species string) ([]datastore.HourlyAnalyticsData, error) {
	start, end, err := ds.parseDateRange(date, date)
	if err != nil {
		return nil, err
	}

	labelID, err := ds.resolveLabelID(ctx, species)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return []datastore.HourlyAnalyticsData{}, nil
		}
		return nil, err
	}

	v2Data, err := ds.detection.GetHourlyDistribution(ctx, start, end, labelID, nil)
	if err != nil {
		return nil, err
	}

	result := make([]datastore.HourlyAnalyticsData, 0, len(v2Data))
	for _, d := range v2Data {
		result = append(result, datastore.HourlyAnalyticsData{
			Hour:  d.Hour,
			Count: int(d.Count),
		})
	}
	return result, nil
}

// resolveLabelID looks up a label ID for a species name.
// Returns (nil, nil) if species is empty (no filter).
// Returns (nil, errNotFound) if species not found.
// Returns (&id, nil) if found.
// Returns (nil, err) for other errors.
var errNotFound = errors.NewStd("species not found")

func (ds *Datastore) resolveLabelID(ctx context.Context, species string) (*uint, error) {
	if species == "" {
		return nil, nil //nolint:nilnil // nil means no filter, which is valid
	}
	label, err := ds.label.GetByScientificName(ctx, species)
	if err != nil {
		if errors.Is(err, repository.ErrLabelNotFound) {
			return nil, errNotFound
		}
		return nil, err
	}
	return &label.ID, nil
}

// GetDailyAnalyticsData retrieves daily analytics data.
func (ds *Datastore) GetDailyAnalyticsData(ctx context.Context, startDate, endDate, species string) ([]datastore.DailyAnalyticsData, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	labelID, err := ds.resolveLabelID(ctx, species)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return []datastore.DailyAnalyticsData{}, nil
		}
		return nil, err
	}

	v2Data, err := ds.detection.GetDailyAnalytics(ctx, start, end, labelID, nil)
	if err != nil {
		return nil, err
	}

	result := make([]datastore.DailyAnalyticsData, 0, len(v2Data))
	for _, d := range v2Data {
		result = append(result, datastore.DailyAnalyticsData{
			Date:  d.Date,
			Count: int(d.TotalDetections),
		})
	}
	return result, nil
}

// GetDetectionTrends retrieves detection trends.
func (ds *Datastore) GetDetectionTrends(ctx context.Context, period string, limit int) ([]datastore.DailyAnalyticsData, error) {
	v2Data, err := ds.detection.GetDetectionTrends(ctx, period, limit, nil)
	if err != nil {
		return nil, err
	}

	result := make([]datastore.DailyAnalyticsData, 0, len(v2Data))
	for _, d := range v2Data {
		result = append(result, datastore.DailyAnalyticsData{
			Date:  d.Date,
			Count: int(d.TotalDetections),
		})
	}
	return result, nil
}

// GetHourlyDistribution retrieves hourly distribution data.
func (ds *Datastore) GetHourlyDistribution(ctx context.Context, startDate, endDate, species string) ([]datastore.HourlyDistributionData, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	labelID, err := ds.resolveLabelID(ctx, species)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return []datastore.HourlyDistributionData{}, nil
		}
		return nil, err
	}

	v2Data, err := ds.detection.GetHourlyDistribution(ctx, start, end, labelID, nil)
	if err != nil {
		return nil, err
	}

	result := make([]datastore.HourlyDistributionData, 0, len(v2Data))
	for _, d := range v2Data {
		result = append(result, datastore.HourlyDistributionData{
			Hour:  d.Hour,
			Count: int(d.Count),
		})
	}
	return result, nil
}

// speciesFirstSeenInfo holds the common fields for species first detection data.
type speciesFirstSeenInfo struct {
	LabelID        uint
	ScientificName string
	FirstDetected  int64
}

// convertToNewSpeciesData converts species first-seen data to NewSpeciesData with common name resolution.
func (ds *Datastore) convertToNewSpeciesData(ctx context.Context, data []speciesFirstSeenInfo) []datastore.NewSpeciesData {
	if len(data) == 0 {
		return []datastore.NewSpeciesData{}
	}

	// Batch fetch raw_labels for common names using the default model
	modelID := ds.getDefaultModelID(ctx)
	pairs := make([]repository.ModelLabelPair, 0, len(data))
	for _, d := range data {
		pairs = append(pairs, repository.ModelLabelPair{ModelID: modelID, LabelID: d.LabelID})
	}
	rawLabelsMap, _ := ds.label.GetRawLabelsForLabels(ctx, pairs)

	result := make([]datastore.NewSpeciesData, 0, len(data))
	for _, d := range data {
		commonName := d.ScientificName
		key := fmt.Sprintf("%d:%d", modelID, d.LabelID)
		if rawLabel, ok := rawLabelsMap[key]; ok && rawLabel != "" {
			if extracted := extractCommonName(rawLabel); extracted != "" {
				commonName = extracted
			}
		}

		firstSeenDate := time.Unix(d.FirstDetected, 0).In(ds.timezone).Format("2006-01-02")
		result = append(result, datastore.NewSpeciesData{
			ScientificName: d.ScientificName,
			CommonName:     commonName,
			FirstSeenDate:  firstSeenDate,
			CountInPeriod:  0,
		})
	}
	return result
}

// GetNewSpeciesDetections retrieves new species detections (lifetime firsts).
func (ds *Datastore) GetNewSpeciesDetections(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	v2Data, err := ds.detection.GetNewSpecies(ctx, start, end, limit, offset)
	if err != nil {
		return nil, err
	}

	// Convert to common format
	data := make([]speciesFirstSeenInfo, len(v2Data))
	for i, d := range v2Data {
		data[i] = speciesFirstSeenInfo{
			LabelID:        d.LabelID,
			ScientificName: d.ScientificName,
			FirstDetected:  d.FirstDetected,
		}
	}

	return ds.convertToNewSpeciesData(ctx, data), nil
}

// GetSpeciesFirstDetectionInPeriod retrieves first detection of species in a period.
func (ds *Datastore) GetSpeciesFirstDetectionInPeriod(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	start, end, err := ds.parseDateRange(startDate, endDate)
	if err != nil {
		return nil, err
	}

	v2Data, err := ds.detection.GetSpeciesFirstDetectionInPeriod(ctx, start, end, limit, offset)
	if err != nil {
		return nil, err
	}

	// Convert to common format
	data := make([]speciesFirstSeenInfo, len(v2Data))
	for i, d := range v2Data {
		data[i] = speciesFirstSeenInfo{
			LabelID:        d.LabelID,
			ScientificName: d.ScientificName,
			FirstDetected:  d.FirstDetected,
		}
	}

	return ds.convertToNewSpeciesData(ctx, data), nil
}

// ============================================================
// Dynamic Threshold Methods
// ============================================================

// SaveDynamicThreshold saves a dynamic threshold.
func (ds *Datastore) SaveDynamicThreshold(threshold *datastore.DynamicThreshold) error {
	if ds.threshold == nil {
		return fmt.Errorf("threshold repository not configured")
	}
	ctx := context.Background()
	v2Threshold := &entities.DynamicThreshold{
		SpeciesName:    threshold.SpeciesName,
		ScientificName: threshold.ScientificName,
		Level:          threshold.Level,
		CurrentValue:   threshold.CurrentValue,
		BaseThreshold:  threshold.BaseThreshold,
		HighConfCount:  threshold.HighConfCount,
		ValidHours:     threshold.ValidHours,
		ExpiresAt:      threshold.ExpiresAt,
		LastTriggered:  threshold.LastTriggered,
		FirstCreated:   threshold.FirstCreated,
		UpdatedAt:      threshold.UpdatedAt,
		TriggerCount:   threshold.TriggerCount,
	}
	return ds.threshold.SaveDynamicThreshold(ctx, v2Threshold)
}

// GetDynamicThreshold retrieves a dynamic threshold.
func (ds *Datastore) GetDynamicThreshold(speciesName string) (*datastore.DynamicThreshold, error) {
	if ds.threshold == nil {
		return nil, fmt.Errorf("threshold repository not configured")
	}
	ctx := context.Background()
	t, err := ds.threshold.GetDynamicThreshold(ctx, speciesName)
	if err != nil {
		return nil, err
	}
	return &datastore.DynamicThreshold{
		ID:             t.ID,
		SpeciesName:    t.SpeciesName,
		ScientificName: t.ScientificName,
		Level:          t.Level,
		CurrentValue:   t.CurrentValue,
		BaseThreshold:  t.BaseThreshold,
		HighConfCount:  t.HighConfCount,
		ValidHours:     t.ValidHours,
		ExpiresAt:      t.ExpiresAt,
		LastTriggered:  t.LastTriggered,
		FirstCreated:   t.FirstCreated,
		UpdatedAt:      t.UpdatedAt,
		TriggerCount:   t.TriggerCount,
	}, nil
}

// GetAllDynamicThresholds retrieves all dynamic thresholds.
func (ds *Datastore) GetAllDynamicThresholds(limit ...int) ([]datastore.DynamicThreshold, error) {
	if ds.threshold == nil {
		return []datastore.DynamicThreshold{}, nil
	}
	ctx := context.Background()
	v2Thresholds, err := ds.threshold.GetAllDynamicThresholds(ctx, limit...)
	if err != nil {
		return nil, err
	}
	result := make([]datastore.DynamicThreshold, 0, len(v2Thresholds))
	for i := range v2Thresholds {
		t := &v2Thresholds[i]
		result = append(result, datastore.DynamicThreshold{
			ID:             t.ID,
			SpeciesName:    t.SpeciesName,
			ScientificName: t.ScientificName,
			Level:          t.Level,
			CurrentValue:   t.CurrentValue,
			BaseThreshold:  t.BaseThreshold,
			HighConfCount:  t.HighConfCount,
			ValidHours:     t.ValidHours,
			ExpiresAt:      t.ExpiresAt,
			LastTriggered:  t.LastTriggered,
			FirstCreated:   t.FirstCreated,
			UpdatedAt:      t.UpdatedAt,
			TriggerCount:   t.TriggerCount,
		})
	}
	return result, nil
}

// DeleteDynamicThreshold deletes a dynamic threshold.
func (ds *Datastore) DeleteDynamicThreshold(speciesName string) error {
	if ds.threshold == nil {
		return fmt.Errorf("threshold repository not configured")
	}
	ctx := context.Background()
	return ds.threshold.DeleteDynamicThreshold(ctx, speciesName)
}

// DeleteExpiredDynamicThresholds deletes expired thresholds.
func (ds *Datastore) DeleteExpiredDynamicThresholds(before time.Time) (int64, error) {
	if ds.threshold == nil {
		return 0, nil
	}
	ctx := context.Background()
	return ds.threshold.DeleteExpiredDynamicThresholds(ctx, before)
}

// UpdateDynamicThresholdExpiry updates the expiry of a threshold.
func (ds *Datastore) UpdateDynamicThresholdExpiry(speciesName string, expiresAt time.Time) error {
	if ds.threshold == nil {
		return fmt.Errorf("threshold repository not configured")
	}
	ctx := context.Background()
	return ds.threshold.UpdateDynamicThresholdExpiry(ctx, speciesName, expiresAt)
}

// BatchSaveDynamicThresholds saves multiple thresholds.
func (ds *Datastore) BatchSaveDynamicThresholds(thresholds []datastore.DynamicThreshold) error {
	if ds.threshold == nil {
		return fmt.Errorf("threshold repository not configured")
	}
	ctx := context.Background()
	v2Thresholds := make([]entities.DynamicThreshold, 0, len(thresholds))
	for i := range thresholds {
		t := &thresholds[i]
		v2Thresholds = append(v2Thresholds, entities.DynamicThreshold{
			SpeciesName:    t.SpeciesName,
			ScientificName: t.ScientificName,
			Level:          t.Level,
			CurrentValue:   t.CurrentValue,
			BaseThreshold:  t.BaseThreshold,
			HighConfCount:  t.HighConfCount,
			ValidHours:     t.ValidHours,
			ExpiresAt:      t.ExpiresAt,
			LastTriggered:  t.LastTriggered,
			FirstCreated:   t.FirstCreated,
			UpdatedAt:      t.UpdatedAt,
			TriggerCount:   t.TriggerCount,
		})
	}
	return ds.threshold.BatchSaveDynamicThresholds(ctx, v2Thresholds)
}

// DeleteAllDynamicThresholds deletes all thresholds.
func (ds *Datastore) DeleteAllDynamicThresholds() (int64, error) {
	if ds.threshold == nil {
		return 0, nil
	}
	ctx := context.Background()
	return ds.threshold.DeleteAllDynamicThresholds(ctx)
}

// GetDynamicThresholdStats returns threshold statistics.
func (ds *Datastore) GetDynamicThresholdStats() (totalCount, activeCount, atMinimumCount int64, levelDistribution map[int]int64, err error) {
	if ds.threshold == nil {
		return 0, 0, 0, make(map[int]int64), nil
	}
	ctx := context.Background()
	return ds.threshold.GetDynamicThresholdStats(ctx)
}

// ============================================================
// Threshold Event Methods
// ============================================================

// SaveThresholdEvent saves a threshold event.
func (ds *Datastore) SaveThresholdEvent(event *datastore.ThresholdEvent) error {
	if ds.threshold == nil {
		return fmt.Errorf("threshold repository not configured")
	}
	ctx := context.Background()
	v2Event := &entities.ThresholdEvent{
		SpeciesName:   event.SpeciesName,
		PreviousLevel: event.PreviousLevel,
		NewLevel:      event.NewLevel,
		PreviousValue: event.PreviousValue,
		NewValue:      event.NewValue,
		ChangeReason:  event.ChangeReason,
		Confidence:    event.Confidence,
		CreatedAt:     event.CreatedAt,
	}
	return ds.threshold.SaveThresholdEvent(ctx, v2Event)
}

// GetThresholdEvents retrieves threshold events for a species.
func (ds *Datastore) GetThresholdEvents(speciesName string, limit int) ([]datastore.ThresholdEvent, error) {
	if ds.threshold == nil {
		return []datastore.ThresholdEvent{}, nil
	}
	ctx := context.Background()
	v2Events, err := ds.threshold.GetThresholdEvents(ctx, speciesName, limit)
	if err != nil {
		return nil, err
	}
	result := make([]datastore.ThresholdEvent, 0, len(v2Events))
	for i := range v2Events {
		e := &v2Events[i]
		result = append(result, datastore.ThresholdEvent{
			ID:            e.ID,
			SpeciesName:   e.SpeciesName,
			PreviousLevel: e.PreviousLevel,
			NewLevel:      e.NewLevel,
			PreviousValue: e.PreviousValue,
			NewValue:      e.NewValue,
			ChangeReason:  e.ChangeReason,
			Confidence:    e.Confidence,
			CreatedAt:     e.CreatedAt,
		})
	}
	return result, nil
}

// GetRecentThresholdEvents retrieves recent threshold events.
func (ds *Datastore) GetRecentThresholdEvents(limit int) ([]datastore.ThresholdEvent, error) {
	if ds.threshold == nil {
		return []datastore.ThresholdEvent{}, nil
	}
	ctx := context.Background()
	v2Events, err := ds.threshold.GetRecentThresholdEvents(ctx, limit)
	if err != nil {
		return nil, err
	}
	result := make([]datastore.ThresholdEvent, 0, len(v2Events))
	for i := range v2Events {
		e := &v2Events[i]
		result = append(result, datastore.ThresholdEvent{
			ID:            e.ID,
			SpeciesName:   e.SpeciesName,
			PreviousLevel: e.PreviousLevel,
			NewLevel:      e.NewLevel,
			PreviousValue: e.PreviousValue,
			NewValue:      e.NewValue,
			ChangeReason:  e.ChangeReason,
			Confidence:    e.Confidence,
			CreatedAt:     e.CreatedAt,
		})
	}
	return result, nil
}

// DeleteThresholdEvents deletes threshold events for a species.
func (ds *Datastore) DeleteThresholdEvents(speciesName string) error {
	if ds.threshold == nil {
		return nil
	}
	ctx := context.Background()
	return ds.threshold.DeleteThresholdEvents(ctx, speciesName)
}

// DeleteAllThresholdEvents deletes all threshold events.
func (ds *Datastore) DeleteAllThresholdEvents() (int64, error) {
	if ds.threshold == nil {
		return 0, nil
	}
	ctx := context.Background()
	return ds.threshold.DeleteAllThresholdEvents(ctx)
}

// ============================================================
// Notification History Methods
// ============================================================

// SaveNotificationHistory saves a notification history entry.
func (ds *Datastore) SaveNotificationHistory(history *datastore.NotificationHistory) error {
	if ds.notification == nil {
		return fmt.Errorf("notification repository not configured")
	}
	ctx := context.Background()
	v2History := &entities.NotificationHistory{
		ScientificName:   history.ScientificName,
		NotificationType: history.NotificationType,
		LastSent:         history.LastSent,
		ExpiresAt:        history.ExpiresAt,
		CreatedAt:        history.CreatedAt,
		UpdatedAt:        history.UpdatedAt,
	}
	return ds.notification.SaveNotificationHistory(ctx, v2History)
}

// GetNotificationHistory retrieves a notification history entry.
func (ds *Datastore) GetNotificationHistory(scientificName, notificationType string) (*datastore.NotificationHistory, error) {
	if ds.notification == nil {
		return nil, datastore.ErrNotificationHistoryNotFound
	}
	ctx := context.Background()
	h, err := ds.notification.GetNotificationHistory(ctx, scientificName, notificationType)
	if err != nil {
		return nil, err
	}
	return &datastore.NotificationHistory{
		ID:               h.ID,
		ScientificName:   h.ScientificName,
		NotificationType: h.NotificationType,
		LastSent:         h.LastSent,
		ExpiresAt:        h.ExpiresAt,
		CreatedAt:        h.CreatedAt,
		UpdatedAt:        h.UpdatedAt,
	}, nil
}

// GetActiveNotificationHistory retrieves active notification history entries.
func (ds *Datastore) GetActiveNotificationHistory(after time.Time) ([]datastore.NotificationHistory, error) {
	if ds.notification == nil {
		return []datastore.NotificationHistory{}, nil
	}
	ctx := context.Background()
	v2Histories, err := ds.notification.GetActiveNotificationHistory(ctx, after)
	if err != nil {
		return nil, err
	}
	result := make([]datastore.NotificationHistory, 0, len(v2Histories))
	for i := range v2Histories {
		h := &v2Histories[i]
		result = append(result, datastore.NotificationHistory{
			ID:               h.ID,
			ScientificName:   h.ScientificName,
			NotificationType: h.NotificationType,
			LastSent:         h.LastSent,
			ExpiresAt:        h.ExpiresAt,
			CreatedAt:        h.CreatedAt,
			UpdatedAt:        h.UpdatedAt,
		})
	}
	return result, nil
}

// DeleteExpiredNotificationHistory deletes expired notification history entries.
func (ds *Datastore) DeleteExpiredNotificationHistory(before time.Time) (int64, error) {
	if ds.notification == nil {
		return 0, nil
	}
	ctx := context.Background()
	return ds.notification.DeleteExpiredNotificationHistory(ctx, before)
}
