// Package equalizer builds go-audio-dsp equalizer filter chains from
// BirdNET-Go equalizer settings, resolving per-source overrides.
package equalizer

import (
	dspeq "github.com/tphakala/go-audio-dsp/equalizer"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

var eqLog = logger.Global().Module("audio").Module("equalizer")

// filterTypes maps the filter type names stored in conf.EqualizerFilter to the
// equalizer library's filter types.
var filterTypes = map[string]dspeq.FilterType{
	"LowPass":    dspeq.LowPass,
	"HighPass":   dspeq.HighPass,
	"AllPass":    dspeq.AllPass,
	"BandPass":   dspeq.BandPass,
	"BandReject": dspeq.BandReject,
	"LowShelf":   dspeq.LowShelf,
	"HighShelf":  dspeq.HighShelf,
	"Peaking":    dspeq.Peaking,
}

// BuildFilterChain creates a library FilterChain from the given equalizer
// settings. Returns nil when the equalizer is disabled, has no filters
// configured, or every filter was skipped. Unknown filter types and filters the
// library rejects for the given sample rate (for example a corner at or above
// Nyquist) are logged with the reason and skipped without aborting the chain.
// Every call returns a new chain, since chains hold per-route filter state.
func BuildFilterChain(settings conf.EqualizerSettings, sampleRate int) *dspeq.FilterChain {
	if !settings.Enabled || len(settings.Filters) == 0 {
		eqLog.Debug("EQ disabled or no filters configured",
			logger.Bool("enabled", settings.Enabled),
			logger.Int("filter_count", len(settings.Filters)))
		return nil
	}

	eqLog.Debug("building EQ filter chain",
		logger.Int("filter_count", len(settings.Filters)),
		logger.Int("sample_rate", sampleRate))

	chain := dspeq.NewFilterChain()

	for i := range settings.Filters {
		f := &settings.Filters[i]

		filterType, ok := filterTypes[f.Type]
		if !ok {
			eqLog.Warn("unknown EQ filter type, skipping",
				logger.String("type", f.Type))
			continue
		}

		band := dspeq.Band{
			Type:      filterType,
			Frequency: f.Frequency,
			Q:         f.Q,
			WidthHz:   f.Width,
			GainDB:    f.Gain,
			Passes:    max(f.Passes, 1),
		}
		filter, err := dspeq.NewFilter(band, sampleRate)
		if err != nil {
			eqLog.Warn("skipping EQ filter",
				logger.String("type", f.Type),
				logger.Float64("frequency", band.Frequency),
				logger.Float64("q", band.Q),
				logger.Float64("width", band.WidthHz),
				logger.Float64("gain", band.GainDB),
				logger.Int("passes", band.Passes),
				logger.Int("sample_rate", sampleRate),
				logger.Error(err))
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
			logger.Float64("frequency", band.Frequency),
			logger.Int("passes", band.Passes))
	}

	if chain.Len() == 0 {
		eqLog.Debug("EQ filter chain empty after building, all filters failed")
		return nil
	}
	eqLog.Debug("EQ filter chain built",
		logger.Int("active_filters", chain.Len()))
	return chain
}

// BuildFilterChainWithOverride builds a library FilterChain from the given EQ
// override settings, or from globalEQ if override is nil. This is the preferred
// entry point for callers that have already resolved the per-source/per-stream
// override via Settings.ResolveEQOverride.
func BuildFilterChainWithOverride(override *conf.EqualizerSettings, globalEQ conf.EqualizerSettings, sourceName string, sampleRate int) *dspeq.FilterChain {
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

// ResolveAndBuildFilterChain resolves the per-source EQ override for the given
// source name and builds the corresponding filter chain.
func ResolveAndBuildFilterChain(settings *conf.Settings, sourceName string, sampleRate int) *dspeq.FilterChain {
	override := settings.ResolveEQOverride(sourceName)
	return BuildFilterChainWithOverride(override, settings.Realtime.Audio.Equalizer, sourceName, sampleRate)
}
