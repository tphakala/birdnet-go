package detection

import (
	"context"
	"log/slog"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/capture"
	"github.com/tphakala/birdnet-go/internal/audiocore/export"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// CaptureHandler triggers audio clip capture when detections occur
type CaptureHandler struct {
	id             string
	captureManager capture.Manager
	minConfidence  float32
	logger         *slog.Logger
}

// NewCaptureHandler creates a new capture handler
func NewCaptureHandler(id string, captureManager capture.Manager, minConfidence float32) Handler {
	// Get logger from logging package
	var logger *slog.Logger
	if l := logging.ForService("audiocore"); l != nil {
		logger = l.With("component", "capture_handler", "handler_id", id)
	} else {
		// Fallback to default slog if logging not initialized
		logger = slog.Default().With("component", "capture_handler", "handler_id", id)
	}

	return &CaptureHandler{
		id:             id,
		captureManager: captureManager,
		minConfidence:  minConfidence,
		logger:         logger,
	}
}

// ID returns the handler's identifier
func (h *CaptureHandler) ID() string {
	return h.id
}

// HandleDetection processes a single detection
func (h *CaptureHandler) HandleDetection(ctx context.Context, detection *Detection) error {
	// Check if detection meets confidence threshold
	if detection.Confidence < h.minConfidence {
		h.logger.Debug("detection below confidence threshold",
			"species", detection.Species,
			"confidence", detection.Confidence,
			"threshold", h.minConfidence)
		return nil
	}

	// Check if capture is enabled for this source
	if !h.captureManager.IsCaptureEnabled(detection.SourceID) {
		h.logger.Debug("capture not enabled for source",
			"source_id", detection.SourceID)
		return nil
	}

	// Calculate detection duration
	detectionDuration := time.Duration(detection.EndTime-detection.StartTime) * time.Second

	// Export clip
	result, err := h.captureManager.ExportClip(
		ctx,
		detection.SourceID,
		detection.Timestamp.Add(time.Duration(detection.StartTime)*time.Second),
		detectionDuration,
	)

	if err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("operation", "export_detection_clip").
			Context("source_id", detection.SourceID).
			Context("species", detection.Species).
			Build()
	}

	h.logger.Info("detection clip exported",
		"source_id", detection.SourceID,
		"species", detection.Species,
		"confidence", detection.Confidence,
		"file_path", result.FilePath,
		"duration", detectionDuration)

	return nil
}

// HandleAnalysisResult processes a complete analysis result
func (h *CaptureHandler) HandleAnalysisResult(ctx context.Context, result *AnalysisResult) error {
	// Check for nil result
	if result == nil {
		return nil
	}

	// Process each detection
	var firstError error
	for _, detection := range result.Detections {
		// Copy source ID from result if not set
		if detection.SourceID == "" {
			detection.SourceID = result.SourceID
		}

		if err := h.HandleDetection(ctx, &detection); err != nil {
			h.logger.Error("failed to handle detection",
				"species", detection.Species,
				"confidence", detection.Confidence,
				"error", err)
			// Keep first error but continue processing other detections
			if firstError == nil {
				firstError = err
			}
		}
	}

	return firstError
}

// Close releases any resources
func (h *CaptureHandler) Close() error {
	h.logger.Info("closing capture handler")
	return nil
}

// CaptureHandlerConfig contains configuration for the capture handler
type CaptureHandlerConfig struct {
	MinConfidence float32
	ExportConfig  *export.Config
}
