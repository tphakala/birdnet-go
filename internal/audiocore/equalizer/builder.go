// Package equalizer — builder.go converts conf.EqualizerSettings into a FilterChain.
package equalizer

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

var eqLog = logger.Global().Module("audio").Module("equalizer")

// BuildFilterChain creates a FilterChain from the given equalizer settings.
// Returns nil when the equalizer is disabled or has no filters configured.
// Unknown filter types are logged and skipped without aborting the chain.
func BuildFilterChain(settings conf.EqualizerSettings, sampleRate int) *FilterChain {
	if !settings.Enabled || len(settings.Filters) == 0 {
		eqLog.Debug("EQ disabled or no filters configured",
			logger.Bool("enabled", settings.Enabled),
			logger.Int("filter_count", len(settings.Filters)))
		return nil
	}

	eqLog.Debug("building EQ filter chain",
		logger.Int("filter_count", len(settings.Filters)),
		logger.Int("sample_rate", sampleRate))

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
			eqLog.Warn("skipping EQ filter",
				logger.String("type", f.Type),
				logger.Float64("frequency", f.Frequency),
				logger.Error(err))
			continue
		}
		if filter == nil {
			eqLog.Warn("unknown EQ filter type, skipping",
				logger.String("type", f.Type))
			continue
		}
		if addErr := chain.AddFilter(filter); addErr != nil {
			eqLog.Warn("failed to add EQ filter to chain",
				logger.String("type", f.Type),
				logger.Error(addErr))
			continue
		}
		eqLog.Debug("added EQ filter",
			logger.String("type", f.Type),
			logger.Float64("frequency", f.Frequency),
			logger.Int("passes", passes))
	}

	if chain.Length() == 0 {
		eqLog.Debug("EQ filter chain empty after building, all filters failed")
		return nil
	}
	eqLog.Debug("EQ filter chain built",
		logger.Int("active_filters", chain.Length()))
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
		eqLog.Debug("using per-source EQ override",
			logger.String("source", sourceCfg.Name),
			logger.Bool("enabled", eqSettings.Enabled),
			logger.Int("filter_count", len(eqSettings.Filters)))
	} else {
		sourceName := "unknown"
		if sourceCfg != nil {
			sourceName = sourceCfg.Name
		}
		eqLog.Debug("using global EQ for source",
			logger.String("source", sourceName),
			logger.Bool("enabled", globalEQ.Enabled),
			logger.Int("filter_count", len(globalEQ.Filters)))
	}
	return BuildFilterChain(eqSettings, sampleRate)
}

// BuildFilterChainWithOverride builds a FilterChain from the given EQ override
// settings, or from globalEQ if override is nil. This is the preferred entry
// point for callers that have already resolved the per-source/per-stream override
// via Settings.ResolveEQOverride.
func BuildFilterChainWithOverride(override *conf.EqualizerSettings, globalEQ conf.EqualizerSettings, sourceName string, sampleRate int) *FilterChain {
	eqSettings := globalEQ
	if override != nil {
		eqSettings = *override
		eqLog.Debug("using per-source EQ override",
			logger.String("source", sourceName),
			logger.Bool("enabled", eqSettings.Enabled),
			logger.Int("filter_count", len(eqSettings.Filters)))
	} else {
		eqLog.Debug("using global EQ for source",
			logger.String("source", sourceName),
			logger.Bool("enabled", globalEQ.Enabled),
			logger.Int("filter_count", len(globalEQ.Filters)))
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
