// Package events provides an asynchronous event bus for decoupling components
package events

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// DetectionEvent represents a bird detection event that can be processed asynchronously
type DetectionEvent interface {
	// GetSpeciesName returns the common name of the detected species
	GetSpeciesName() string

	// GetScientificName returns the scientific name of the detected species
	GetScientificName() string

	// GetConfidence returns the confidence level of the detection
	GetConfidence() float64

	// GetTimestamp returns when the detection occurred
	GetTimestamp() time.Time

	// GetLocation returns the detection location/source
	GetLocation() string

	// GetMetadata returns additional context data
	GetMetadata() map[string]interface{}

	// IsNewSpecies returns true if this is the first detection of this species
	IsNewSpecies() bool

	// GetDaysSinceFirstSeen returns days since species was first detected
	GetDaysSinceFirstSeen() int
}

// detectionEventImpl is the concrete implementation of DetectionEvent
type detectionEventImpl struct {
	speciesName        string
	scientificName     string
	confidence         float64
	timestamp          time.Time
	location           string
	metadata           map[string]interface{}
	isNewSpecies       bool
	daysSinceFirstSeen int
}

// NewDetectionEvent creates a new detection event with input validation
func NewDetectionEvent(
	speciesName string,
	scientificName string,
	confidence float64,
	location string,
	isNewSpecies bool,
	daysSinceFirstSeen int,
) (DetectionEvent, error) {
	// Validate input parameters to prevent invalid DetectionEvent instances
	if speciesName == "" {
		return nil, errors.Newf("NewDetectionEvent: speciesName cannot be empty").
			Component("events").
			Category(errors.CategoryValidation).
			Build()
	}
	if scientificName == "" {
		return nil, errors.Newf("NewDetectionEvent: scientificName cannot be empty").
			Component("events").
			Category(errors.CategoryValidation).
			Build()
	}
	if confidence < 0.0 || confidence > 1.0 {
		return nil, errors.Newf("NewDetectionEvent: confidence must be between 0 and 1, got %f", confidence).
			Component("events").
			Category(errors.CategoryValidation).
			Context("confidence", confidence).
			Build()
	}
	if daysSinceFirstSeen < 0 {
		return nil, errors.Newf("NewDetectionEvent: daysSinceFirstSeen cannot be negative, got %d", daysSinceFirstSeen).
			Component("events").
			Category(errors.CategoryValidation).
			Context("daysSinceFirstSeen", daysSinceFirstSeen).
			Build()
	}

	return &detectionEventImpl{
		speciesName:        speciesName,
		scientificName:     scientificName,
		confidence:         confidence,
		timestamp:          time.Now(),
		location:           location,
		metadata:           make(map[string]interface{}),
		isNewSpecies:       isNewSpecies,
		daysSinceFirstSeen: daysSinceFirstSeen,
	}, nil
}

// GetSpeciesName returns the common name of the detected species
func (e *detectionEventImpl) GetSpeciesName() string {
	return e.speciesName
}

// GetScientificName returns the scientific name of the detected species
func (e *detectionEventImpl) GetScientificName() string {
	return e.scientificName
}

// GetConfidence returns the confidence level of the detection
func (e *detectionEventImpl) GetConfidence() float64 {
	return e.confidence
}

// GetTimestamp returns when the detection occurred
func (e *detectionEventImpl) GetTimestamp() time.Time {
	return e.timestamp
}

// GetLocation returns the detection location/source
func (e *detectionEventImpl) GetLocation() string {
	return e.location
}

// GetMetadata returns additional context data
func (e *detectionEventImpl) GetMetadata() map[string]interface{} {
	return e.metadata
}

// IsNewSpecies returns true if this is the first detection of this species
func (e *detectionEventImpl) IsNewSpecies() bool {
	return e.isNewSpecies
}

// GetDaysSinceFirstSeen returns days since species was first detected
func (e *detectionEventImpl) GetDaysSinceFirstSeen() int {
	return e.daysSinceFirstSeen
}

// String returns a string representation of the detection event
func (e *detectionEventImpl) String() string {
	return fmt.Sprintf("Detection: %s (%.2f%%) at %s, new=%v",
		e.speciesName, e.confidence*100, e.timestamp.Format(time.RFC3339), e.isNewSpecies)
}

// DetectionEventConsumer represents a consumer that processes detection events
type DetectionEventConsumer interface {
	EventConsumer

	// ProcessDetectionEvent processes a single detection event
	ProcessDetectionEvent(event DetectionEvent) error
}
