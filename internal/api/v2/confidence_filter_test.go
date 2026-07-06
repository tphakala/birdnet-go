// confidence_filter_test.go: tests for the confidence filter parser and the
// datastore-disabled search route guard (backport of #3723).
package api

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// confidenceValueDelta is the tolerance used when comparing parsed confidence
// fractions, since values like 0.1 have no exact binary float representation.
const confidenceValueDelta = 1e-9

// TestParseConfidenceFilter pins the operator/value parsing of the confidence
// filter, including the previously-broken explicit "=N" form. Before the fix a
// bare "50" parsed as equality but the documented "=50" fell through to the
// default case with value "=50", which strconv.ParseFloat rejected, silently
// dropping the whole filter. Both forms must now parse to operator "=".
func TestParseConfidenceFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		param        string
		wantNil      bool
		wantOperator string
		wantValue    float64 // fraction, i.e. param/100
	}{
		{name: "explicit equals operator", param: "=50", wantOperator: "=", wantValue: 0.5},
		{name: "bare number defaults to equals", param: "50", wantOperator: "=", wantValue: 0.5},
		{name: "greater than or equal", param: ">=80", wantOperator: ">=", wantValue: 0.8},
		{name: "less than or equal", param: "<=20", wantOperator: "<=", wantValue: 0.2},
		{name: "greater than", param: ">10", wantOperator: ">", wantValue: 0.1},
		{name: "less than", param: "<90", wantOperator: "<", wantValue: 0.9},
		{name: "explicit equals zero boundary", param: "=0", wantOperator: "=", wantValue: 0.0},
		{name: "explicit equals hundred boundary", param: "=100", wantOperator: "=", wantValue: 1.0},
		{name: "empty string returns nil", param: "", wantNil: true},
		{name: "explicit equals with non-numeric value returns nil", param: "=abc", wantNil: true},
		{name: "lone equals returns nil", param: "=", wantNil: true},
		{name: "explicit equals out of range high returns nil", param: "=150", wantNil: true},
		{name: "explicit equals negative returns nil", param: "=-5", wantNil: true},
		{name: "explicit equals NaN returns nil", param: "=NaN", wantNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := parseConfidenceFilter(tt.param)

			if tt.wantNil {
				assert.Nil(t, got, "expected nil result for param %q", tt.param)
				return
			}

			require.NotNil(t, got, "expected non-nil result for param %q", tt.param)
			assert.Equal(t, tt.wantOperator, got.Operator, "operator mismatch for param %q", tt.param)
			assert.InDelta(t, tt.wantValue, got.Value, confidenceValueDelta, "value mismatch for param %q", tt.param)
		})
	}
}

// TestInitSearchRoutesSkippedWhenDatastoreDisabled pins the datastore-disabled
// guard for the search routes: when DS is nil (the "datastore disabled" mode
// NewWithOptions permits), initSearchRoutes registers no /search route instead
// of wiring HandleSearch, which dereferences a nil datastore via
// SearchDetections and would panic on the first request.
func TestInitSearchRoutesSkippedWhenDatastoreDisabled(t *testing.T) {
	t.Parallel()

	e := echo.New()
	c := &Controller{Echo: e, Group: e.Group("/api/v2")}

	// Must not panic and must not register any /search route.
	assert.NotPanics(t, func() {
		c.initSearchRoutes()
	}, "initSearchRoutes must not panic when the datastore is disabled")

	for _, r := range e.Routes() {
		assert.NotContains(t, r.Path, "/search",
			"search routes must not register when the datastore is disabled: %s %s", r.Method, r.Path)
	}
}
