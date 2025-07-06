package birdnet

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Config contains minimal configuration for BirdNET initialization
// This is a simplified version that avoids requiring the full conf.Settings struct
type Config struct {
	// ModelPath is the path to custom TensorFlow Lite model file
	// If empty, uses embedded model
	ModelPath string

	// LabelPath is the path to custom labels file
	// If empty, uses embedded labels
	LabelPath string

	// Locale for species labels (e.g., "en", "de", "fr")
	// Falls back to model's default locale if unsupported
	Locale string

	// Labels array of species labels (populated from label file)
	Labels []string

	// Threads is the number of threads for inference
	// 0 means auto-detect optimal thread count
	Threads int

	// UseXNNPACK enables XNNPACK delegate for acceleration
	UseXNNPACK bool

	// Debug enables debug logging
	Debug bool

	// RangeFilterModel specifies range filter model version
	// "legacy" uses V1 meta model, any other value uses V2
	RangeFilterModel string
}

// NewBirdNETFromConfig initializes a new BirdNET instance with minimal configuration
func NewBirdNETFromConfig(config *Config) (*BirdNET, error) {
	// Validate config
	if config == nil {
		return nil, errors.New(nil).
			Component("birdnet").
			Category(errors.CategoryValidation).
			Context("error", "config cannot be nil").
			Build()
	}
	// ModelPath and LabelPath can be empty - BirdNET supports embedded resources
	// Locale can be empty - there is a default fallback
	
	// Convert to Settings for backward compatibility
	// This allows gradual migration without breaking existing code
	settings := &conf.Settings{
		BirdNET: conf.BirdNETConfig{
			ModelPath:  config.ModelPath,
			LabelPath:  config.LabelPath,
			Locale:     config.Locale,
			Labels:     config.Labels,
			Threads:    config.Threads,
			UseXNNPACK: config.UseXNNPACK,
			Debug:      config.Debug,
			RangeFilter: conf.RangeFilterSettings{
				Model: config.RangeFilterModel,
			},
		},
	}

	return NewBirdNET(settings)
}

// ConfigFromSettings extracts minimal BirdNET config from full settings
// This is useful for transitioning existing code
func ConfigFromSettings(settings *conf.Settings) *Config {
	if settings == nil {
		return nil
	}

	return &Config{
		ModelPath:        settings.BirdNET.ModelPath,
		LabelPath:        settings.BirdNET.LabelPath,
		Locale:           settings.BirdNET.Locale,
		Labels:           settings.BirdNET.Labels,
		Threads:          settings.BirdNET.Threads,
		UseXNNPACK:       settings.BirdNET.UseXNNPACK,
		Debug:            settings.BirdNET.Debug,
		RangeFilterModel: settings.BirdNET.RangeFilter.Model,
	}
}