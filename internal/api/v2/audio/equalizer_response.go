// internal/api/v2/audio/equalizer_response.go
package audio

import (
	"fmt"
	"math"
	"net/http"
	"slices"

	"github.com/labstack/echo/v4"
	dspeq "github.com/tphakala/go-audio-dsp/equalizer"

	"github.com/tphakala/birdnet-go/internal/audiocore/equalizer"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Limits and defaults of the equalizer response endpoint.
const (
	defaultEQResponsePoints = 512
	minEQResponsePoints     = 2
	maxEQResponsePoints     = 2048
	defaultEQResponseMinHz  = 20.0
	defaultEQResponseMaxHz  = 20000.0
	// minEQResponseSampleRate and maxEQResponseSampleRate bound an explicit
	// sampleRate. The top is the highest entry of conf.ValidSampleRates. A range
	// rather than the list, because a bat stream can run at a probed native rate
	// that is not in the list.
	minEQResponseSampleRate = 8000
	maxEQResponseSampleRate = 384000
	// maxEQResponseFilters caps the filters in one request. conf has no filter
	// count limit, so this only bounds the cost of a preview.
	maxEQResponseFilters = 64
	// eqResponseFloorDB and eqResponseCeilingDB bound the gain written to the
	// response, since encoding/json cannot encode -Inf, which the library
	// reports for an exact null.
	eqResponseFloorDB   = -300.0
	eqResponseCeilingDB = 300.0
)

// equalizerResponseRequest is the body of POST /system/audio/equalizer/response.
// Optional numbers are pointers so an absent field means the default while an
// explicit zero is rejected.
type equalizerResponseRequest struct {
	// Equalizer is the unsaved equalizer configuration to evaluate.
	Equalizer *conf.EqualizerSettings `json:"equalizer"`
	// SourceID names a registered audio source. When it resolves, the curve is
	// computed at the rate that source's EQ chain is built with.
	SourceID string `json:"sourceId"`
	// SampleRate is used when SourceID is absent or unknown.
	SampleRate *int     `json:"sampleRate"`
	Points     *int     `json:"points"`
	MinHz      *float64 `json:"minHz"`
	MaxHz      *float64 `json:"maxHz"`
}

// equalizerResponsePoint is one point of the response curve.
type equalizerResponsePoint struct {
	Hz     float64 `json:"hz"`
	GainDB float64 `json:"gainDb"`
}

// equalizerSkippedFilter reports a filter left out of the curve, as audio
// leaves it out of the chain.
type equalizerSkippedFilter struct {
	// Index is the filter's position in the request's equalizer.filters.
	Index   int    `json:"index"`
	Type    string `json:"type"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// equalizerResponseBody is the 200 response.
type equalizerResponseBody struct {
	// SampleRate is the rate the curve was computed at.
	SampleRate int                      `json:"sampleRate"`
	Enabled    bool                     `json:"enabled"`
	Points     []equalizerResponsePoint `json:"points"`
	Skipped    []equalizerSkippedFilter `json:"skipped"`
}

// eqResponseRange is the validated sweep range and size.
type eqResponseRange struct {
	points       int
	minHz, maxHz float64
}

// ComputeEqualizerResponse handles POST /api/v2/system/audio/equalizer/response.
// It builds the equalizer chain for the posted (possibly unsaved) configuration
// with the builder the audio routes use, and returns the chain's magnitude at
// log-spaced frequencies plus the center of every applied filter (none when
// the equalizer is disabled). Nothing is read from or written to the saved
// settings.
func (c *Handler) ComputeEqualizerResponse(ctx echo.Context) error {
	var req equalizerResponseRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "Invalid request body", http.StatusBadRequest)
	}
	if req.Equalizer == nil {
		return c.HandleError(ctx, errors.NewStd("equalizer is required"), "Invalid request body", http.StatusBadRequest)
	}
	if len(req.Equalizer.Filters) > maxEQResponseFilters {
		return c.HandleError(ctx, fmt.Errorf("%d filters, at most %d", len(req.Equalizer.Filters), maxEQResponseFilters),
			"Too many equalizer filters", http.StatusBadRequest)
	}
	if req.SampleRate != nil && (*req.SampleRate < minEQResponseSampleRate || *req.SampleRate > maxEQResponseSampleRate) {
		return c.HandleError(ctx, fmt.Errorf("sampleRate %d outside [%d, %d]", *req.SampleRate, minEQResponseSampleRate, maxEQResponseSampleRate),
			"Invalid sampleRate", http.StatusBadRequest)
	}

	rate := c.resolveEQResponseRate(&req)
	sweep, err := validateEQResponseRange(&req, rate)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid frequency range", http.StatusBadRequest)
	}

	chain, skipped := equalizer.BuildFilterChainReport(*req.Equalizer, rate)
	if chain == nil {
		// Audio runs no chain here, so the curve is flat. An empty chain is the
		// library's own identity.
		chain = dspeq.NewFilterChain()
	}

	freqs, err := dspeq.LogSweep(sweep.minHz, sweep.maxHz, sweep.points)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid frequency range", http.StatusBadRequest)
	}
	if req.Equalizer.Enabled {
		freqs = withFilterCenters(freqs, req.Equalizer.Filters, skipped, sweep)
	}

	body := equalizerResponseBody{
		SampleRate: rate,
		Enabled:    req.Equalizer.Enabled,
		Points:     make([]equalizerResponsePoint, len(freqs)),
		Skipped:    make([]equalizerSkippedFilter, 0, len(skipped)),
	}
	for i, p := range chain.Response(freqs) {
		gain, clampErr := clampEQResponseDB(p.GainDB)
		if clampErr != nil {
			return c.HandleError(ctx, clampErr, "Equalizer response is not finite", http.StatusInternalServerError)
		}
		body.Points[i] = equalizerResponsePoint{Hz: p.Hz, GainDB: gain}
	}
	for i := range skipped {
		body.Skipped = append(body.Skipped, skippedFilterBody(&skipped[i]))
	}

	c.LogDebugIfEnabled("computed equalizer response",
		logger.Int("filter_count", len(req.Equalizer.Filters)),
		logger.Int("sample_rate", rate),
		logger.Int("points", len(body.Points)),
		logger.Int("skipped", len(body.Skipped)))
	return ctx.JSON(http.StatusOK, body)
}

// resolveEQResponseRate picks the sample rate to evaluate at. A source that is
// in the registry wins, with the same rule the analysis pipeline applies when it
// builds that source's EQ chain: the source's rate when positive, else the
// configured rate (internal/analysis/audio_pipeline_service.go:817-820). The
// probed SourceSampleRate is an ingest detail and is not used. For an absent or
// unknown source the explicit sampleRate applies, else the configured rate.
func (c *Handler) resolveEQResponseRate(req *equalizerResponseRequest) int {
	if req.SourceID != "" {
		if eng := c.Engine.Load(); eng != nil {
			if registry := eng.Registry(); registry != nil {
				if src, ok := registry.Get(req.SourceID); ok {
					if src.SampleRate > 0 {
						return src.SampleRate
					}
					return conf.SampleRate
				}
			}
		}
		c.LogDebugIfEnabled("equalizer response source not in registry, using fallback rate",
			logger.String("source_id", privacy.SanitizeRTSPUrl(req.SourceID)))
	}
	if req.SampleRate != nil {
		return *req.SampleRate
	}
	return conf.SampleRate
}

// validateEQResponseRange applies the defaults and checks points and the
// frequency range against the Nyquist frequency of rate.
func validateEQResponseRange(req *equalizerResponseRequest, rate int) (eqResponseRange, error) {
	nyquist := float64(rate) / 2
	out := eqResponseRange{
		points: defaultEQResponsePoints,
		minHz:  defaultEQResponseMinHz,
		maxHz:  math.Min(defaultEQResponseMaxHz, nyquist),
	}
	if req.Points != nil {
		out.points = *req.Points
	}
	if req.MinHz != nil {
		out.minHz = *req.MinHz
	}
	if req.MaxHz != nil {
		out.maxHz = *req.MaxHz
	}
	switch {
	case out.points < minEQResponsePoints || out.points > maxEQResponsePoints:
		return out, fmt.Errorf("points %d outside [%d, %d]", out.points, minEQResponsePoints, maxEQResponsePoints)
	case math.IsNaN(out.minHz) || math.IsInf(out.minHz, 0) || out.minHz <= 0:
		return out, fmt.Errorf("minHz must be finite and above 0, got %g", out.minHz)
	case math.IsNaN(out.maxHz) || math.IsInf(out.maxHz, 0) || out.maxHz <= out.minHz:
		return out, fmt.Errorf("maxHz must be finite and above minHz (%g), got %g", out.minHz, out.maxHz)
	case out.maxHz > nyquist:
		return out, fmt.Errorf("maxHz %g above the Nyquist frequency %g of %d Hz", out.maxHz, nyquist, rate)
	}
	return out, nil
}

// withFilterCenters adds the center frequency of every filter that was applied
// and lies in the sweep range, so a narrow notch or peak between two sweep
// points is still sampled at its center. The result is sorted ascending with
// exact duplicates removed.
func withFilterCenters(freqs []float64, filters []conf.EqualizerFilter, skipped []equalizer.SkippedFilter, sweep eqResponseRange) []float64 {
	skippedIdx := make(map[int]struct{}, len(skipped))
	for i := range skipped {
		skippedIdx[skipped[i].Index] = struct{}{}
	}
	for i := range filters {
		if _, isSkipped := skippedIdx[i]; isSkipped {
			continue
		}
		if f := filters[i].Frequency; f >= sweep.minHz && f <= sweep.maxHz {
			freqs = append(freqs, f)
		}
	}
	slices.Sort(freqs)
	return slices.Compact(freqs)
}

// clampEQResponseDB limits a gain to [eqResponseFloorDB, eqResponseCeilingDB]
// so it encodes as JSON. NaN is an error: the library returns it only for a
// non-finite frequency, which validation excludes.
func clampEQResponseDB(gainDB float64) (float64, error) {
	if math.IsNaN(gainDB) {
		return 0, errors.NewStd("equalizer gain is NaN")
	}
	return min(max(gainDB, eqResponseFloorDB), eqResponseCeilingDB), nil
}

// skippedFilterBody converts a builder skip into its response form.
func skippedFilterBody(s *equalizer.SkippedFilter) equalizerSkippedFilter {
	msg := "unknown filter type"
	if s.Err != nil {
		msg = s.Err.Error()
	}
	return equalizerSkippedFilter{Index: s.Index, Type: s.Type, Reason: string(s.Reason), Message: msg}
}
