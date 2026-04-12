// Package equalizer — builder.go converts conf.EqualizerSettings into a FilterChain.
package equalizer

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// BuildFilterChain creates a FilterChain from the given equalizer settings.
// Returns nil when the equalizer is disabled or has no filters configured.
// Unknown filter types are logged and skipped without aborting the chain.
func BuildFilterChain(settings conf.EqualizerSettings, sampleRate int) *FilterChain {
	if !settings.Enabled || len(settings.Filters) == 0 {
		return nil
	}

	log := logger.Global().Module("audio").Module("equalizer")
	chain := NewFilterChain()
	sr := float64(sampleRate)

	for i := range settings.Filters {
		f := &settings.Filters[i]
		passes := f.Passes
		if passes < 1 {
			passes = 1
		}

		filter, err := buildFilter(f, sr, passes)
		if err != nil {
			log.Warn("skipping EQ filter",
				logger.String("type", f.Type),
				logger.Float64("frequency", f.Frequency),
				logger.Error(err))
			continue
		}
		if filter == nil {
			log.Warn("unknown EQ filter type, skipping",
				logger.String("type", f.Type))
			continue
		}
		if addErr := chain.AddFilter(filter); addErr != nil {
			log.Warn("failed to add EQ filter to chain",
				logger.String("type", f.Type),
				logger.Error(addErr))
			continue
		}
	}

	if chain.Length() == 0 {
		return nil
	}
	return chain
}

// BuildFilterChainForSource resolves the effective EQ settings for a source
// (per-source override or global default) and builds a FilterChain.
// Returns nil when EQ is disabled or has no filters. Falls back to global
// settings if sourceCfg is nil or has no per-source override.
func BuildFilterChainForSource(sourceCfg *conf.AudioSourceConfig, globalEQ conf.EqualizerSettings, sampleRate int) *FilterChain {
	eqSettings := globalEQ
	if sourceCfg != nil && sourceCfg.Equalizer != nil {
		eqSettings = *sourceCfg.Equalizer
	}
	return BuildFilterChain(eqSettings, sampleRate)
}

// buildFilter creates a single Filter from a config entry.
// Returns (nil, nil) for unknown filter types.
func buildFilter(f *conf.EqualizerFilter, sampleRate float64, passes int) (*Filter, error) {
	switch f.Type {
	case "LowPass":
		return NewLowPass(sampleRate, f.Frequency, f.Q, passes)
	case "HighPass":
		return NewHighPass(sampleRate, f.Frequency, f.Q, passes)
	case "AllPass":
		return NewAllPass(sampleRate, f.Frequency, f.Q, passes)
	case "BandPass":
		return NewBandPass(sampleRate, f.Frequency, f.Width, passes)
	case "BandReject":
		return NewBandReject(sampleRate, f.Frequency, f.Width, passes)
	case "LowShelf":
		return NewLowShelf(sampleRate, f.Frequency, f.Q, f.Gain, passes)
	case "HighShelf":
		return NewHighShelf(sampleRate, f.Frequency, f.Q, f.Gain, passes)
	case "Peaking":
		return NewPeaking(sampleRate, f.Frequency, f.Width, f.Gain, passes)
	default:
		return nil, nil //nolint:nilnil // nil signals unknown filter type to caller
	}
}
