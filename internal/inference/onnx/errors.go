//go:build onnx

package onnx

import (
	"errors"
	"fmt"
)

var (
	ErrModelPathRequired = errors.New("birdnet: model path is required")
	ErrLabelsRequired    = errors.New("birdnet: labels are required")
	ErrEmptyBatch        = errors.New("birdnet: batch must contain at least one segment")
)

type InputSizeError struct {
	Expected int
	Got      int
}

func (e *InputSizeError) Error() string {
	return fmt.Sprintf("birdnet: expected %d audio samples, got %d", e.Expected, e.Got)
}

type BatchInputSizeError struct {
	Index    int
	Expected int
	Got      int
}

func (e *BatchInputSizeError) Error() string {
	return fmt.Sprintf("birdnet: segment %d has %d audio samples, expected %d", e.Index, e.Got, e.Expected)
}

type LabelCountError struct {
	Expected int
	Got      int
}

func (e *LabelCountError) Error() string {
	return fmt.Sprintf("birdnet: label count mismatch: model has %d classes but %d labels were provided", e.Expected, e.Got)
}

type ModelDetectionError struct {
	Reason string
}

func (e *ModelDetectionError) Error() string {
	return fmt.Sprintf("birdnet: cannot detect model type: %s", e.Reason)
}

type LabelLoadError struct {
	Path   string
	Reason string
}

func (e *LabelLoadError) Error() string {
	return fmt.Sprintf("birdnet: failed to load labels from %s: %s", e.Path, e.Reason)
}

type InvalidCoordinatesError struct {
	Latitude  float32
	Longitude float32
	Reason    string
}

func (e *InvalidCoordinatesError) Error() string {
	return fmt.Sprintf("birdnet: invalid coordinates (%.2f, %.2f): %s", e.Latitude, e.Longitude, e.Reason)
}

type InvalidDateError struct {
	Month  int
	Day    int
	Reason string
}

func (e *InvalidDateError) Error() string {
	return fmt.Sprintf("birdnet: invalid date (month=%d, day=%d): %s", e.Month, e.Day, e.Reason)
}
