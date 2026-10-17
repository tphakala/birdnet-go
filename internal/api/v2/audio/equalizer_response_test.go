// equalizer_response_test.go: tests for POST /api/v2/system/audio/equalizer/response.
package audio

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dspeq "github.com/tphakala/go-audio-dsp/equalizer"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/equalizer"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// eqResponseBody is the decoded 200 body.
type eqResponseBody struct {
	SampleRate int  `json:"sampleRate"`
	Enabled    bool `json:"enabled"`
	Points     []struct {
		Hz     float64 `json:"hz"`
		GainDB float64 `json:"gainDb"`
	} `json:"points"`
	Skipped []struct {
		Index   int    `json:"index"`
		Type    string `json:"type"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	} `json:"skipped"`
}

// postEQResponse sends body to the handler and returns the recorder.
func postEQResponse(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/audio/equalizer/response", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := h.Echo.NewContext(req, rec)
	require.NoError(t, h.ComputeEqualizerResponse(c))
	return rec
}

// decodeEQResponse requires a 200 and decodes the body.
func decodeEQResponse(t *testing.T, rec *httptest.ResponseRecorder) eqResponseBody {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out eqResponseBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

// eqBody builds a request body from a filter list plus extra top-level fields.
func eqBody(enabled bool, filters, extra string) string {
	if extra != "" {
		extra = "," + extra
	}
	return fmt.Sprintf(`{"equalizer":{"enabled":%t,"filters":[%s]}%s}`, enabled, filters, extra)
}

const (
	hp100Filter    = `{"type":"HighPass","frequency":100,"q":0.707,"passes":2}`
	hp30kFilter    = `{"type":"HighPass","frequency":30000,"q":0.707,"passes":1}`
	lp15kFilter    = `{"type":"LowPass","frequency":15000,"q":0.707,"passes":1}`
	measuredFilter = hp100Filter + `,{"type":"LowShelf","frequency":300,"q":0.707,"gain":-6,"passes":1}` +
		`,{"type":"Peaking","frequency":3000,"width":1000,"gain":4,"passes":1}` +
		`,{"type":"LowPass","frequency":12000,"q":0.707,"passes":1}`
)

func TestEqualizerResponse_DefaultsAndShape(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	rec := postEQResponse(t, h, eqBody(true, hp100Filter, ""))
	out := decodeEQResponse(t, rec)

	assert.Equal(t, conf.SampleRate, out.SampleRate)
	assert.True(t, out.Enabled)
	require.Len(t, out.Points, defaultEQResponsePoints+1, "sweep plus the filter center")
	assert.InDelta(t, 20.0, out.Points[0].Hz, 0)
	assert.InDelta(t, 20000.0, out.Points[len(out.Points)-1].Hz, 0)
	for i := 1; i < len(out.Points); i++ {
		require.Greater(t, out.Points[i].Hz, out.Points[i-1].Hz, "hz must ascend strictly at %d", i)
	}
	assert.Contains(t, rec.Body.String(), `"skipped":[]`, "skipped is an empty array, not null")
}

func TestEqualizerResponse_CurveMatchesMeasuredChain(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	var cfg conf.EqualizerSettings
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"filters":[`+measuredFilter+`]}`), &cfg))

	const (
		// minMeasuredHz keeps at least a few whole cycles in the measured half.
		minMeasuredHz = 50.0
		// minMeasuredGainDB drops points too quiet to measure against float32 noise.
		minMeasuredGainDB = -40.0
		// measuredToleranceDB is the library's own swept-sine tolerance.
		measuredToleranceDB = 0.3
		// measuredStride samples every Nth returned point.
		measuredStride = 24
	)
	for _, rate := range []int{48000, 96000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			out := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, measuredFilter, fmt.Sprintf(`"sampleRate":%d`, rate))))
			require.Equal(t, rate, out.SampleRate)
			require.Empty(t, out.Skipped)

			measured := 0
			for i := 0; i < len(out.Points); i += measuredStride {
				p := out.Points[i]
				if p.Hz < minMeasuredHz || p.GainDB < minMeasuredGainDB || p.Hz > float64(rate)*0.45 {
					continue
				}
				// The chain audio uses, built by the function the routes call.
				chain := equalizer.BuildFilterChain(cfg, rate)
				require.NotNil(t, chain)
				in := make([]float32, rate)
				for n := range in {
					in[n] = float32(0.5 * math.Sin(2*math.Pi*p.Hz*float64(n)/float64(rate)))
				}
				got := make([]float32, len(in))
				_, err := chain.ProcessInto(in, got)
				require.NoError(t, err)
				half := len(in) / 2
				db := 20 * math.Log10(rmsOf(got[half:])/rmsOf(in[half:]))
				assert.InDelta(t, p.GainDB, db, measuredToleranceDB, "rate %d hz %.1f", rate, p.Hz)
				measured++
			}
			assert.Greater(t, measured, 8, "enough points were measured")
		})
	}
}

// rmsOf returns the root mean square of samples, accumulated in float64.
func rmsOf(samples []float32) float64 {
	var sum float64
	for _, v := range samples {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func TestEqualizerResponse_PassesCountScaleDB(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	mk := func(passes int) eqResponseBody {
		f := fmt.Sprintf(`{"type":"HighPass","frequency":200,"q":0.707,"passes":%d}`, passes)
		return decodeEQResponse(t, postEQResponse(t, h, eqBody(true, f, "")))
	}
	p1, p3 := mk(1), mk(3)
	require.Len(t, p3.Points, len(p1.Points))
	for i := range p1.Points {
		assert.InDelta(t, 3*p1.Points[i].GainDB, p3.Points[i].GainDB, 1e-9, "point %d", i)
	}
}

// defaultEQFilters mirrors the equalizer filters in conf/defaults.go, which
// ship with passes 0 (off).
const defaultEQFilters = `{"type":"HighPass","frequency":100,"q":0.7,"passes":0},` +
	`{"type":"LowPass","frequency":15000,"q":0.7,"passes":0}`

func TestEqualizerResponse_DefaultConfigIsFlat(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	rec := postEQResponse(t, h, eqBody(true, defaultEQFilters, ""))
	out := decodeEQResponse(t, rec)

	assert.True(t, out.Enabled)
	assert.Len(t, out.Points, defaultEQResponsePoints, "no filter is applied, so no centers are added")
	for _, p := range out.Points {
		require.InDelta(t, 0, p.GainDB, 0)
	}
	assert.Contains(t, rec.Body.String(), `"skipped":[]`)
}

func TestEqualizerResponse_ZeroPassesMatchesRemoval(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	off := `{"type":"LowPass","frequency":15000,"q":0.707,"passes":0}`
	with := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, hp100Filter+","+off, "")))
	without := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, hp100Filter, "")))

	assert.Empty(t, with.Skipped)
	assert.Equal(t, without.Points, with.Points, "a passes-0 filter changes neither the curve nor the marks")
}

func TestEqualizerResponse_SampleRateMatters(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	at48 := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, hp30kFilter, `"sampleRate":48000`)))
	require.Len(t, at48.Skipped, 1)
	assert.Equal(t, "invalid_parameters", at48.Skipped[0].Reason)
	assert.Equal(t, 0, at48.Skipped[0].Index)
	assert.Equal(t, "HighPass", at48.Skipped[0].Type)
	assert.NotEmpty(t, at48.Skipped[0].Message)
	for _, p := range at48.Points {
		require.InDelta(t, 0, p.GainDB, 0, "a skipped filter leaves the curve flat")
	}

	at96 := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, hp30kFilter, `"sampleRate":96000`)))
	assert.Empty(t, at96.Skipped)
	assert.Less(t, at96.Points[0].GainDB, -20.0, "the 30 kHz high-pass is applied at 96 kHz")

	lp48 := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, lp15kFilter, `"sampleRate":48000,"minHz":18000,"maxHz":18000.5`)))
	lp96 := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, lp15kFilter, `"sampleRate":96000,"minHz":18000,"maxHz":18000.5`)))
	assert.Greater(t, math.Abs(lp48.Points[0].GainDB-lp96.Points[0].GainDB), 1.0, "bilinear warping differs by rate near 18 kHz")
}

func TestEqualizerResponse_FlatCases(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	unknown := `{"type":"Nope","frequency":100,"q":1},{"type":"BandStop","frequency":200,"q":1}`
	tests := []struct {
		name        string
		body        string
		wantEnabled bool
		wantSkipped int
	}{
		{"disabled with filters", eqBody(false, hp100Filter, ""), false, 0},
		{"enabled without filters", eqBody(true, "", ""), true, 0},
		{"all unknown types", eqBody(true, unknown, ""), true, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := postEQResponse(t, h, tt.body)
			out := decodeEQResponse(t, rec)
			assert.Equal(t, tt.wantEnabled, out.Enabled)
			require.NotEmpty(t, out.Points)
			for _, p := range out.Points {
				require.InDelta(t, 0, p.GainDB, 0)
			}
			assert.Len(t, out.Skipped, tt.wantSkipped)
			for i, s := range out.Skipped {
				assert.Equal(t, i, s.Index)
				assert.Equal(t, "unknown_type", s.Reason)
			}
		})
	}
}

func TestEqualizerResponse_Validation(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	manyFilters := strings.TrimSuffix(strings.Repeat(hp100Filter+",", maxEQResponseFilters+1), ",")
	tests := []struct {
		name string
		body string
	}{
		{"not json", `not json`},
		{"missing equalizer", `{"sampleRate":48000}`},
		{"sampleRate too low", eqBody(true, "", `"sampleRate":7999`)},
		{"sampleRate too high", eqBody(true, "", `"sampleRate":384001`)},
		{"points too low", eqBody(true, "", `"points":1`)},
		{"points too high", eqBody(true, "", `"points":2049`)},
		{"points zero is explicit", eqBody(true, "", `"points":0`)},
		{"minHz zero", eqBody(true, "", `"minHz":0`)},
		{"minHz negative", eqBody(true, "", `"minHz":-5`)},
		{"maxHz below minHz", eqBody(true, "", `"minHz":100,"maxHz":50`)},
		{"maxHz equals minHz", eqBody(true, "", `"minHz":100,"maxHz":100`)},
		{"maxHz above Nyquist", eqBody(true, "", `"maxHz":24001`)},
		{"minHz above default max", eqBody(true, "", `"minHz":20000`)},
		{"too many filters", eqBody(true, manyFilters, "")},
		{"range too narrow for the point count", eqBody(true, "", `"minHz":1000,"maxHz":1000.000000000001`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, http.StatusBadRequest, postEQResponse(t, h, tt.body).Code)
		})
	}

	t.Run("exactly the filter cap is accepted", func(t *testing.T) {
		t.Parallel()
		ok := strings.TrimSuffix(strings.Repeat(hp100Filter+",", maxEQResponseFilters), ",")
		assert.Equal(t, http.StatusOK, postEQResponse(t, h, eqBody(true, ok, "")).Code)
	})
	t.Run("maxHz at Nyquist is accepted", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, http.StatusOK, postEQResponse(t, h, eqBody(true, "", `"maxHz":24000`)).Code)
	})
}

func TestEqualizerResponse_RequireFiniteGain(t *testing.T) {
	t.Parallel()
	require.NoError(t, requireFiniteGain(-12.5))
	require.NoError(t, requireFiniteGain(dspeq.SectionFloorDB))
	require.Error(t, requireFiniteGain(math.NaN()))
	require.Error(t, requireFiniteGain(math.Inf(-1)))
	require.Error(t, requireFiniteGain(math.Inf(1)))
}

func TestEqualizerResponse_ExactNullEncodesAsJSON(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	// A one-pass notch is an exact null at its center, which the library
	// reports as its per-section floor instead of -Inf.
	notch := `{"type":"BandReject","frequency":1000,"width":100,"passes":1}`
	rec := postEQResponse(t, h, eqBody(true, notch, ""))
	out := decodeEQResponse(t, rec)
	assert.NotContains(t, rec.Body.String(), "Inf")

	found := false
	for _, p := range out.Points {
		if p.Hz == 1000 {
			found = true
			assert.InDelta(t, dspeq.SectionFloorDB, p.GainDB, 0)
		}
	}
	assert.True(t, found, "the notch center is sampled")
}

func TestEqualizerResponse_NarrowNotchCenterReadsTrueDepth(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	// Q 30 at 15 kHz is a 500 Hz wide notch; 256 log points are about 400 Hz
	// apart there, so only the center mark can show its depth.
	const points = 256
	notch := `{"type":"BandReject","frequency":15000,"width":500,"passes":1}`
	out := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, notch, fmt.Sprintf(`"points":%d`, points))))
	assert.Len(t, out.Points, points+1)

	var center float64
	shallowest := math.Inf(1)
	for _, p := range out.Points {
		if p.Hz == 15000 {
			center = p.GainDB
		} else {
			shallowest = math.Min(shallowest, math.Abs(p.Hz-15000))
		}
	}
	assert.InDelta(t, dspeq.SectionFloorDB, center, 0, "the center reads the full depth")
	assert.Greater(t, shallowest, 100.0, "no sweep point comes near the notch center")
}

func TestEqualizerResponse_InsertsFilterCenters(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	// A 20 Hz wide notch at 15 kHz falls between two 512-point sweep points.
	notch := `{"type":"BandReject","frequency":15000,"width":20,"passes":1}`
	out := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, notch, "")))

	found := false
	nearest := math.Inf(1)
	for i, p := range out.Points {
		if i > 0 {
			require.Greater(t, p.Hz, out.Points[i-1].Hz)
		}
		if p.Hz == 15000 {
			found = true
			assert.LessOrEqual(t, p.GainDB, -40.0)
		} else {
			nearest = math.Min(nearest, math.Abs(p.Hz-15000))
		}
		require.False(t, math.IsInf(p.GainDB, 0) || math.IsNaN(p.GainDB))
	}
	assert.True(t, found, "the notch center is sampled")
	assert.Greater(t, nearest, 20.0, "the plain sweep misses the narrow notch")
	assert.Len(t, out.Points, defaultEQResponsePoints+1)

	// A center outside [minHz, maxHz] is not inserted.
	narrow := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, notch, `"maxHz":10000`)))
	assert.Len(t, narrow.Points, defaultEQResponsePoints)

	// A center that already is a sweep point is not duplicated.
	onSweep := `{"type":"HighPass","frequency":20,"q":0.707,"passes":1}`
	dup := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, onSweep, "")))
	assert.Len(t, dup.Points, defaultEQResponsePoints)
}

func TestEqualizerResponse_SkippedFilterCentersNotInserted(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	// Both skipped filters sit inside the default range; only the applied
	// high-pass center (100 Hz) may be added to the sweep.
	filters := hp100Filter +
		`,{"type":"Nope","frequency":1000,"q":1,"passes":1}` +
		`,{"type":"LowPass","frequency":2000,"q":0,"passes":1}`
	out := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, filters, "")))
	require.Len(t, out.Skipped, 2)
	assert.Len(t, out.Points, defaultEQResponsePoints+1)
	for _, p := range out.Points {
		assert.NotEqual(t, 1000.0, p.Hz)
		assert.NotEqual(t, 2000.0, p.Hz)
	}
}

func TestEqualizerResponse_DefaultMaxHzFollowsNyquist(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	out := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, "", `"sampleRate":16000`)))
	assert.Equal(t, 16000, out.SampleRate)
	require.Len(t, out.Points, defaultEQResponsePoints)
	assert.InDelta(t, 8000.0, out.Points[len(out.Points)-1].Hz, 0)
}

func TestEqualizerResponse_DisabledAddsNoCenters(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	// 100 Hz is not a sweep point, so a stray center would add a 513th point.
	out := decodeEQResponse(t, postEQResponse(t, h, eqBody(false, hp100Filter, "")))
	assert.Len(t, out.Points, defaultEQResponsePoints)
}

func TestEqualizerResponse_Deterministic(t *testing.T) {
	t.Parallel()
	_, h := setupSystemTestEnvironment(t)

	body := eqBody(true, measuredFilter, "")
	first := postEQResponse(t, h, body).Body.String()
	_ = postEQResponse(t, h, eqBody(true, lp15kFilter, `"sampleRate":96000`))
	second := postEQResponse(t, h, body).Body.String()
	assert.Equal(t, first, second)
}

// registerEQTestSource registers a source with the given pipeline rate and
// probed source rate and returns its ID.
func registerEQTestSource(t *testing.T, sampleRate, probedRate int) (h *Handler, sourceID string) {
	t.Helper()
	_, h = setupAudioSourcesTestEnv(t, []*audiocore.SourceConfig{{
		DisplayName:      "EQ Test Mic",
		Type:             audiocore.SourceTypeAudioCard,
		ConnectionString: "hw:9,0",
		SampleRate:       sampleRate,
		SourceSampleRate: probedRate,
		BitDepth:         16,
		Channels:         1,
	}})
	list := h.Engine.Load().Registry().List()
	require.Len(t, list, 1)
	sourceID = list[0].ID
	return h, sourceID
}

func TestEqualizerResponse_SourceIDResolvesRouteRate(t *testing.T) {
	t.Parallel()
	// The pipeline rate is 96 kHz; the probed rate must not be used.
	h, id := registerEQTestSource(t, 96000, 48000)

	// A 30 kHz corner is only valid at 96 kHz. The explicit sampleRate is
	// ignored because the source's rate takes precedence.
	body := eqBody(true, hp30kFilter, fmt.Sprintf(`"sourceId":%q,"sampleRate":48000`, id))
	out := decodeEQResponse(t, postEQResponse(t, h, body))
	assert.Equal(t, 96000, out.SampleRate)
	assert.Empty(t, out.Skipped)
}

func TestEqualizerResponse_SourceIDWithoutRateUsesConfiguredRate(t *testing.T) {
	t.Parallel()
	// The route falls back to conf.SampleRate when the source has no rate.
	h, id := registerEQTestSource(t, 0, 96000)
	// An explicit sampleRate must not replace the route's fallback rate.
	out := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, hp30kFilter, fmt.Sprintf(`"sourceId":%q,"sampleRate":96000`, id))))
	assert.Equal(t, conf.SampleRate, out.SampleRate)
	require.Len(t, out.Skipped, 1)
}

func TestEqualizerResponse_UnknownSourceFallsBack(t *testing.T) {
	t.Parallel()
	h, _ := registerEQTestSource(t, 96000, 0)

	// Unknown source with an explicit rate: the explicit rate is used.
	out := decodeEQResponse(t, postEQResponse(t, h, eqBody(true, "", `"sourceId":"nope","sampleRate":44100`)))
	assert.Equal(t, 44100, out.SampleRate)

	// Unknown source without a rate: the configured rate.
	out = decodeEQResponse(t, postEQResponse(t, h, eqBody(true, "", `"sourceId":"nope"`)))
	assert.Equal(t, conf.SampleRate, out.SampleRate)

	// No engine at all behaves the same.
	_, noEngine := setupSystemTestEnvironment(t)
	out = decodeEQResponse(t, postEQResponse(t, noEngine, eqBody(true, "", `"sourceId":"nope"`)))
	assert.Equal(t, conf.SampleRate, out.SampleRate)
}
