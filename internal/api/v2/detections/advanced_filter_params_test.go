// advanced_filter_params_test.go: coverage for the advanced filter parameters
// shared by GET /api/v2/detections and POST /api/v2/detections/batch/resolve.
//
// These parameters are what let the unified detections view serve the filters the
// standalone search page used to own (a confidence band, a three-state review
// verdict, sun-event time-of-day periods), so they are validated and mapped here
// rather than silently dropped.
package detections

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// newQueryParamsContext builds an echo.Context for a GET /api/v2/detections
// request carrying the given query parameters.
func newQueryParamsContext(t *testing.T, params map[string]string) echo.Context {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections", http.NoBody)
	q := req.URL.Query()
	for key, value := range params {
		q.Set(key, value)
	}
	req.URL.RawQuery = q.Encode()

	return echo.New().NewContext(req, httptest.NewRecorder())
}

// parseParams runs the real query-parameter parser, which is where the shared
// validator is invoked.
func parseParams(t *testing.T, params map[string]string) (*detectionQueryParams, error) {
	t.Helper()
	h := &Handler{}
	return h.parseDetectionQueryParams(newQueryParamsContext(t, params))
}

// httpStatus extracts the HTTP status from a validation error.
func httpStatus(t *testing.T, err error) int {
	t.Helper()
	httpErr, ok := err.(*echo.HTTPError) //nolint:errorlint // echo returns this concrete type directly
	require.True(t, ok, "validation errors must be echo.HTTPError so the handler maps them to a status")
	return httpErr.Code
}

func TestParseDetectionQueryParams_ConfidenceBounds(t *testing.T) {
	t.Parallel()

	t.Run("valid bounds are accepted and carried through", func(t *testing.T) {
		t.Parallel()
		params, err := parseParams(t, map[string]string{
			"confidenceMin": "60",
			"confidenceMax": "85",
		})
		require.NoError(t, err)
		assert.Equal(t, "60", params.ConfidenceMin)
		assert.Equal(t, "85", params.ConfidenceMax)
	})

	t.Run("bounds at the edges of the percentage range are valid", func(t *testing.T) {
		t.Parallel()
		_, err := parseParams(t, map[string]string{"confidenceMin": "0", "confidenceMax": "100"})
		assert.NoError(t, err)
	})

	invalid := []struct {
		name   string
		params map[string]string
	}{
		{name: "non-numeric minimum", params: map[string]string{"confidenceMin": "high"}},
		{name: "non-numeric maximum", params: map[string]string{"confidenceMax": "%%"}},
		{name: "minimum below zero", params: map[string]string{"confidenceMin": "-1"}},
		{name: "maximum above one hundred", params: map[string]string{"confidenceMax": "101"}},
		{name: "not a number at all", params: map[string]string{"confidenceMin": "NaN"}},
		{name: "inverted range", params: map[string]string{"confidenceMin": "90", "confidenceMax": "10"}},
	}
	for _, tc := range invalid {
		t.Run(tc.name+" is rejected", func(t *testing.T) {
			t.Parallel()
			_, err := parseParams(t, tc.params)
			require.Error(t, err, "a malformed bound must not be silently dropped")
			assert.Equal(t, http.StatusBadRequest, httpStatus(t, err))
		})
	}

	t.Run("equal bounds are a valid degenerate range", func(t *testing.T) {
		t.Parallel()
		_, err := parseParams(t, map[string]string{"confidenceMin": "50", "confidenceMax": "50"})
		assert.NoError(t, err)
	})
}

func TestParseDetectionQueryParams_VerifiedStatus(t *testing.T) {
	t.Parallel()

	valid := []string{
		"correct", "false_positive", "unverified", "any",
		"true", "false", "human", "yes", "no", "1", "0",
		"CORRECT", " unverified ",
	}
	for _, value := range valid {
		t.Run("accepts "+value, func(t *testing.T) {
			t.Parallel()
			_, err := parseParams(t, map[string]string{"verified": value})
			assert.NoError(t, err)
		})
	}

	invalid := []string{"maybe", "correct_ish", "verified", "2"}
	for _, value := range invalid {
		t.Run("rejects "+value, func(t *testing.T) {
			t.Parallel()
			_, err := parseParams(t, map[string]string{"verified": value})
			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, httpStatus(t, err))
		})
	}
}

func TestParseDetectionQueryParams_TimeOfDay(t *testing.T) {
	t.Parallel()

	valid := []string{"any", "day", "night", "sunrise", "sunset", "dawn", "dusk", "NIGHT"}
	for _, value := range valid {
		t.Run("accepts "+value, func(t *testing.T) {
			t.Parallel()
			_, err := parseParams(t, map[string]string{"timeOfDay": value})
			assert.NoError(t, err)
		})
	}

	invalid := []string{"evening", "twilight", "midnight"}
	for _, value := range invalid {
		t.Run("rejects "+value, func(t *testing.T) {
			t.Parallel()
			_, err := parseParams(t, map[string]string{"timeOfDay": value})
			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, httpStatus(t, err))
		})
	}
}

// TestNeedsAdvancedRouting_ConfidenceBounds guards the routing decision: a
// confidence band must not be silently ignored by the simple handlers, which have
// no way to apply it.
func TestNeedsAdvancedRouting_ConfidenceBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		params detectionQueryParams
		want   bool
	}{
		{name: "no filters", params: detectionQueryParams{}, want: false},
		{name: "minimum only", params: detectionQueryParams{ConfidenceMin: "60"}, want: true},
		{name: "maximum only", params: detectionQueryParams{ConfidenceMax: "90"}, want: true},
		{name: "both bounds", params: detectionQueryParams{ConfidenceMin: "60", ConfidenceMax: "90"}, want: true},
		{name: "operator form still routes", params: detectionQueryParams{Confidence: ">85"}, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.params.needsAdvancedRouting())
		})
	}
}

func TestBuildAdvancedSearchFilters_ConfidenceRange(t *testing.T) {
	t.Parallel()

	h := &Handler{}

	t.Run("both bounds map to a range in fractions", func(t *testing.T) {
		t.Parallel()
		filters := h.buildAdvancedSearchFilters(&detectionQueryParams{
			ConfidenceMin: "60",
			ConfidenceMax: "85",
		})
		require.NotNil(t, filters.ConfidenceRange)
		assert.InDelta(t, 0.60, filters.ConfidenceRange.Min, 1e-9)
		assert.InDelta(t, 0.85, filters.ConfidenceRange.Max, 1e-9)
	})

	t.Run("a lone minimum leaves the maximum open", func(t *testing.T) {
		t.Parallel()
		filters := h.buildAdvancedSearchFilters(&detectionQueryParams{ConfidenceMin: "70"})
		require.NotNil(t, filters.ConfidenceRange)
		assert.InDelta(t, 0.70, filters.ConfidenceRange.Min, 1e-9)
		assert.InDelta(t, 1.0, filters.ConfidenceRange.Max, 1e-9,
			"an unspecified maximum must not clamp results to zero confidence")
	})

	t.Run("a lone maximum leaves the minimum at zero", func(t *testing.T) {
		t.Parallel()
		filters := h.buildAdvancedSearchFilters(&detectionQueryParams{ConfidenceMax: "40"})
		require.NotNil(t, filters.ConfidenceRange)
		assert.InDelta(t, 0.0, filters.ConfidenceRange.Min, 1e-9)
		assert.InDelta(t, 0.40, filters.ConfidenceRange.Max, 1e-9)
	})

	t.Run("no bounds leaves the range unset", func(t *testing.T) {
		t.Parallel()
		filters := h.buildAdvancedSearchFilters(&detectionQueryParams{})
		assert.Nil(t, filters.ConfidenceRange)
	})
}

func TestBuildAdvancedSearchFilters_VerifiedStatus(t *testing.T) {
	t.Parallel()

	h := &Handler{}

	t.Run("verdict names map to the three-state status", func(t *testing.T) {
		t.Parallel()
		for _, status := range []string{
			datastore.VerifiedStatusCorrect,
			datastore.VerifiedStatusFalsePositive,
			datastore.VerifiedStatusUnverified,
		} {
			filters := h.buildAdvancedSearchFilters(&detectionQueryParams{Verified: status})
			assert.Equal(t, status, filters.VerifiedStatus)
			assert.Nil(t, filters.Verified, "a verdict must not also set the legacy boolean")
		}
	})

	t.Run("any clears the filter", func(t *testing.T) {
		t.Parallel()
		filters := h.buildAdvancedSearchFilters(&detectionQueryParams{Verified: datastore.VerifiedStatusAny})
		assert.Empty(t, filters.VerifiedStatus)
		assert.Nil(t, filters.Verified)
	})

	t.Run("legacy booleans keep the two-state behaviour", func(t *testing.T) {
		t.Parallel()
		for value, want := range map[string]bool{
			"true": true, "human": true, "yes": true, "1": true,
			"false": false, "no": false, "0": false,
		} {
			filters := h.buildAdvancedSearchFilters(&detectionQueryParams{Verified: value})
			require.NotNil(t, filters.Verified, "value %q must set the legacy boolean", value)
			assert.Equal(t, want, *filters.Verified, "value %q", value)
			assert.Empty(t, filters.VerifiedStatus, "value %q must not set a verdict", value)
		}
	})
}

func TestBuildAdvancedSearchFilters_TimeOfDay(t *testing.T) {
	t.Parallel()

	h := &Handler{}

	tests := []struct {
		param string
		want  []string
	}{
		{param: "day", want: []string{datastore.TimeOfDayDay}},
		{param: "night", want: []string{datastore.TimeOfDayNight}},
		{param: "sunrise", want: []string{datastore.TimeOfDaySunrise}},
		{param: "sunset", want: []string{datastore.TimeOfDaySunset}},
		// Legacy spellings normalize rather than reaching the datastore as a
		// separate, differently-behaving vocabulary.
		{param: "dawn", want: []string{datastore.TimeOfDaySunrise}},
		{param: "dusk", want: []string{datastore.TimeOfDaySunset}},
		{param: "any", want: nil},
		{param: "", want: nil},
	}

	for _, tc := range tests {
		t.Run("timeOfDay="+tc.param, func(t *testing.T) {
			t.Parallel()
			filters := h.buildAdvancedSearchFilters(&detectionQueryParams{TimeOfDay: tc.param})
			assert.Equal(t, tc.want, filters.TimeOfDay)
		})
	}
}

// TestAdvancedSearchCacheKey_DistinguishesConfidenceBounds guards against cache
// collisions: two different confidence bands must not share a cached result set.
func TestAdvancedSearchCacheKey_DistinguishesConfidenceBounds(t *testing.T) {
	t.Parallel()

	base := detectionQueryParams{Search: "owl", NumResults: 25}

	withMin := base
	withMin.ConfidenceMin = "60"

	withMax := base
	withMax.ConfidenceMax = "60"

	withBoth := base
	withBoth.ConfidenceMin = "60"
	withBoth.ConfidenceMax = "90"

	keys := []string{
		base.advancedSearchCacheKey(),
		withMin.advancedSearchCacheKey(),
		withMax.advancedSearchCacheKey(),
		withBoth.advancedSearchCacheKey(),
	}

	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		_, duplicate := seen[key]
		assert.False(t, duplicate, "cache keys must be distinct, got a repeat of %q", key)
		seen[key] = struct{}{}
	}
}

// TestValidateAdvancedFilterParams_SharedByListAndResolve pins the invariant that
// makes "select all matching" safe: the batch-resolve endpoint and the list
// endpoint must accept and reject exactly the same filter sets, or a bulk action
// could target detections the list had filtered out of view.
func TestValidateAdvancedFilterParams_SharedByListAndResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		params  detectionQueryParams
		wantErr bool
	}{
		{name: "empty is valid", params: detectionQueryParams{}, wantErr: false},
		{
			name:   "full valid filter set",
			params: detectionQueryParams{ConfidenceMin: "50", ConfidenceMax: "95", Verified: "correct", TimeOfDay: "sunrise"},
		},
		{
			name:    "bad confidence bound",
			params:  detectionQueryParams{ConfidenceMin: "nope"},
			wantErr: true,
		},
		{
			name:    "bad verdict",
			params:  detectionQueryParams{Verified: "sometimes"},
			wantErr: true,
		},
		{
			name:    "bad period",
			params:  detectionQueryParams{TimeOfDay: "teatime"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The list endpoint reaches the validator through query-parameter parsing.
			listParams := map[string]string{}
			if tc.params.ConfidenceMin != "" {
				listParams["confidenceMin"] = tc.params.ConfidenceMin
			}
			if tc.params.ConfidenceMax != "" {
				listParams["confidenceMax"] = tc.params.ConfidenceMax
			}
			if tc.params.Verified != "" {
				listParams["verified"] = tc.params.Verified
			}
			if tc.params.TimeOfDay != "" {
				listParams["timeOfDay"] = tc.params.TimeOfDay
			}
			_, listErr := parseParams(t, listParams)

			// The resolve endpoint calls the validator directly.
			resolveParams := tc.params
			resolveErr := validateAdvancedFilterParams(&resolveParams)

			if tc.wantErr {
				require.Error(t, listErr, "list endpoint must reject")
				require.Error(t, resolveErr, "resolve endpoint must reject")
				return
			}
			require.NoError(t, listErr, "list endpoint must accept")
			require.NoError(t, resolveErr, "resolve endpoint must accept")
		})
	}
}

// TestIsValidVerifiedParam covers the recognizer used for both endpoints' error
// messages, including the legacy spellings kept for API compatibility.
func TestIsValidVerifiedParam(t *testing.T) {
	t.Parallel()

	assert.True(t, isValidVerifiedParam("correct"))
	assert.True(t, isValidVerifiedParam("false_positive"))
	assert.True(t, isValidVerifiedParam("unverified"))
	assert.True(t, isValidVerifiedParam("any"))
	assert.True(t, isValidVerifiedParam("human"))
	assert.False(t, isValidVerifiedParam("bogus"))
	assert.False(t, isValidVerifiedParam(""), "callers check for empty before validating")

	assert.NotEmpty(t, allowedVerifiedParams(), "error messages must list the accepted values")
	assert.Contains(t, allowedVerifiedParams(), "false_positive")
}

func TestIsValidTimeOfDayParam(t *testing.T) {
	t.Parallel()

	assert.True(t, isValidTimeOfDayParam("any"))
	assert.True(t, isValidTimeOfDayParam("day"))
	assert.True(t, isValidTimeOfDayParam("dawn"), "legacy alias stays accepted")
	assert.False(t, isValidTimeOfDayParam("evening"))

	assert.NotContains(t, allowedTimeOfDayParams, "dawn",
		"error text should steer callers to the canonical vocabulary")
}
