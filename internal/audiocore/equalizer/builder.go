// Package equalizer builds go-audio-dsp equalizer filter chains from
// BirdNET-Go equalizer settings, resolving per-source overrides.
package equalizer

import (
	dspeq "github.com/tphakala/go-audio-dsp/equalizer"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

var eqLog = logger.Global().Module("audio").Module("equalizer")

// bypassPasses is the passes value that turns a filter off: buildFilterChain
// leaves a filter of a known type with this value out of the chain and does not
// report it as skipped.
const bypassPasses = 0

// SkipReason says why buildFilterChain rejected a configured filter. A filter
// of a known type with passes 0 is bypassed, not skipped, and has no SkipReason.
type SkipReason string

const (
	// SkipUnknownType means the filter type is not one conf.EQFilterType knows.
	SkipUnknownType SkipReason = "unknown_type"
	// SkipInvalidParameters means the library rejected the filter's parameters
	// for the sample rate, for example a corner at or above Nyquist, a
	// non-positive Q or width, negative passes, or more than dspeq.MaxPasses passes.
	SkipInvalidParameters SkipReason = "invalid_parameters"
	// SkipAddFailed means the library built the filter but refused to add it to
	// the chain.
	SkipAddFailed SkipReason = "add_failed"
)

// SkippedFilter describes one configured filter that was left out of a chain.
type SkippedFilter struct {
	// Index is the filter's position in the settings' Filters slice.
	Index int
	// Type is the configured filter type name, as written in the settings.
	Type string
	// Reason says why the filter was skipped.
	Reason SkipReason
	// Band is the library band built from the filter. It is the zero value for
	// SkipUnknownType, where no band is built.
	Band dspeq.Band
	// Err is the library error. It is nil for SkipUnknownType.
	Err error
}

// BuildFilterChain creates a library FilterChain from the given equalizer
// settings. Returns nil when the equalizer is disabled, has no filters
// configured, or no filter was applied. Unknown filter types and filters the
// library rejects for the given sample rate (for example a corner at or above
// Nyquist) are logged with the reason and skipped without aborting the chain.
// Every call returns a new chain, since chains hold per-route filter state.
func BuildFilterChain(settings conf.EqualizerSettings, sampleRate int) *dspeq.FilterChain {
	return buildFilterChain(settings, sampleRate, func(s *SkippedFilter) { warnSkippedFilter(s, sampleRate) })
}

// BuildFilterChainReport builds the same chain as BuildFilterChain, with the
// same nil returns, and also returns the filters it rejected; a bypassed filter
// is not one of them. It does not log a Warn for a skipped filter, since a
// caller that reports skips to its own client (the equalizer response preview)
// runs on every edit of a half-typed value. The skipped slice is nil when
// nothing was skipped.
func BuildFilterChainReport(settings conf.EqualizerSettings, sampleRate int) (chain *dspeq.FilterChain, skipped []SkippedFilter) {
	chain = buildFilterChain(settings, sampleRate, func(s *SkippedFilter) {
		skipped = append(skipped, *s)
	})
	return chain, skipped
}

// warnSkippedFilter writes the Warn line for one skipped filter.
func warnSkippedFilter(s *SkippedFilter, sampleRate int) {
	switch s.Reason {
	case SkipUnknownType:
		eqLog.Warn("unknown EQ filter type, skipping",
			logger.String("type", s.Type))
	case SkipInvalidParameters:
		eqLog.Warn("skipping EQ filter",
			logger.String("type", s.Type),
			logger.Float64("frequency", s.Band.Frequency),
			logger.Float64("q", s.Band.Q),
			logger.Float64("width", s.Band.WidthHz),
			logger.Float64("gain", s.Band.GainDB),
			logger.Int("passes", s.Band.Passes),
			logger.Int("sample_rate", sampleRate),
			logger.Error(s.Err))
	case SkipAddFailed:
		eqLog.Warn("failed to add EQ filter to chain",
			logger.String("type", s.Type),
			logger.Error(s.Err))
	}
}

// buildFilterChain is the shared body of BuildFilterChain and
// BuildFilterChainReport. onSkip is called once per skipped filter, at the
// point where the filter is rejected.
func buildFilterChain(settings conf.EqualizerSettings, sampleRate int, onSkip func(*SkippedFilter)) *dspeq.FilterChain {
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

		filterType, ok := conf.EQFilterType(f.Type)
		if !ok {
			onSkip(&SkippedFilter{Index: i, Type: f.Type, Reason: SkipUnknownType})
			continue
		}

		if f.Passes == bypassPasses {
			eqLog.Debug("EQ filter bypassed, passes is 0",
				logger.Int("index", i),
				logger.String("type", f.Type),
				logger.Float64("frequency", f.Frequency))
			continue
		}

		band := dspeq.Band{
			Type:      filterType,
			Frequency: f.Frequency,
			Q:         f.Q,
			WidthHz:   f.Width,
			GainDB:    f.Gain,
			Passes:    f.Passes,
		}
		filter, err := dspeq.NewFilter(band, sampleRate)
		if err != nil {
			onSkip(&SkippedFilter{Index: i, Type: f.Type, Reason: SkipInvalidParameters, Band: band, Err: err})
			continue
		}
		if addErr := chain.AddFilter(filter); addErr != nil {
			onSkip(&SkippedFilter{Index: i, Type: f.Type, Reason: SkipAddFailed, Band: band, Err: addErr})
			continue
		}
		eqLog.Debug("added EQ filter",
			logger.String("type", f.Type),
			logger.Float64("frequency", band.Frequency),
			logger.Int("passes", band.Passes))
	}

	if chain.Len() == 0 {
		eqLog.Debug("EQ filter chain empty after building, no filter applied")
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
