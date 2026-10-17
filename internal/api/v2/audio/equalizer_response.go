// internal/api/v2/audio/equalizer_response.go
package audio

import (
	"fmt"
	"math"
	"net/http"

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
	points, minHz, maxHz, err := validateEQResponseRange(&req, rate)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid frequency range", http.StatusBadRequest)
	}

	chain, skipped := equalizer.BuildFilterChainReport(*req.Equalizer, rate)
	if chain == nil {
		// Audio runs no chain here, so the curve is flat. An empty chain is the
		// library's own identity.
		chain = dspeq.NewFilterChain()
	}

	// The chain's marks put every applied filter's frequency on the grid, so a
	// narrow notch or peak between two sweep points is still sampled at its
	// center. An empty chain has none, so a flat curve has exactly points entries.
	freqs, err := dspeq.LogSweepWithMarks(minHz, maxHz, points, chain.Marks())
	if err != nil {
		return c.HandleError(ctx, err, "Invalid frequency range", http.StatusBadRequest)
	}

	body := equalizerResponseBody{
		SampleRate: rate,
		Enabled:    req.Equalizer.Enabled,
		Points:     make([]equalizerResponsePoint, len(freqs)),
		Skipped:    make([]equalizerSkippedFilter, 0, len(skipped)),
	}
	for i, p := range chain.Response(freqs) {
		if gainErr := requireFiniteGain(p.GainDB); gainErr != nil {
			return c.HandleError(ctx, gainErr, "Equalizer response is not finite", http.StatusInternalServerError)
		}
		body.Points[i] = equalizerResponsePoint{Hz: p.Hz, GainDB: p.GainDB}
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
// configured rate (the sourceSampleRate rule in the analysis pipeline's route
// setup in internal/analysis/audio_pipeline_service.go). The
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
func validateEQResponseRange(req *equalizerResponseRequest, rate int) (points int, minHz, maxHz float64, err error) {
	nyquist := float64(rate) / 2
	points, minHz, maxHz = defaultEQResponsePoints, defaultEQResponseMinHz, math.Min(defaultEQResponseMaxHz, nyquist)
	if req.Points != nil {
		points = *req.Points
	}
	if req.MinHz != nil {
		minHz = *req.MinHz
	}
	if req.MaxHz != nil {
		maxHz = *req.MaxHz
	}
	switch {
	case points < minEQResponsePoints || points > maxEQResponsePoints:
		err = fmt.Errorf("points %d outside [%d, %d]", points, minEQResponsePoints, maxEQResponsePoints)
	case math.IsNaN(minHz) || math.IsInf(minHz, 0) || minHz <= 0:
		err = fmt.Errorf("minHz must be finite and above 0, got %g", minHz)
	case math.IsNaN(maxHz) || math.IsInf(maxHz, 0) || maxHz <= minHz:
		err = fmt.Errorf("maxHz must be finite and above minHz (%g), got %g", minHz, maxHz)
	case maxHz > nyquist:
		err = fmt.Errorf("maxHz %g above the Nyquist frequency %g of %d Hz", maxHz, nyquist, rate)
	}
	return points, minHz, maxHz, err
}

// requireFiniteGain returns an error for a gain encoding/json cannot write.
// The library reports a finite gain at every finite frequency, so this is a
// guard on its contract, not a clamp.
func requireFiniteGain(gainDB float64) error {
	if math.IsNaN(gainDB) || math.IsInf(gainDB, 0) {
		return errors.NewStd("equalizer gain is not finite")
	}
	return nil
}

// skippedFilterBody converts a builder skip into its response form.
func skippedFilterBody(s *equalizer.SkippedFilter) equalizerSkippedFilter {
	msg := "unknown filter type"
	if s.Err != nil {
		msg = s.Err.Error()
	}
	return equalizerSkippedFilter{Index: s.Index, Type: s.Type, Reason: string(s.Reason), Message: msg}
}
