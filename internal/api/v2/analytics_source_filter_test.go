// analytics_source_filter_test.go: tests for the source_id query parameter on /analytics/*.
package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestParseOptionalSourceIDs covers the input parsing layer in isolation.
// The handler tests below cover end-to-end pass-through into the datastore call.
func TestParseOptionalSourceIDs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		query string
		want  []uint
	}{
		{name: "absent param returns nil", query: "", want: nil},
		{name: "single id", query: "?source_id=3", want: []uint{3}},
		{name: "comma-separated list preserves order", query: "?source_id=2,5,1", want: []uint{2, 5, 1}},
		{name: "whitespace tolerated", query: "?source_id= 2 , 5 ,  1 ", want: []uint{2, 5, 1}},
		{name: "duplicates collapsed", query: "?source_id=1,1,2,2,1", want: []uint{1, 2}},
		{name: "zero rejected", query: "?source_id=0", want: nil},
		{name: "negative rejected, valid kept", query: "?source_id=-1,4", want: []uint{4}},
		{name: "non-numeric rejected, valid kept", query: "?source_id=foo,7", want: []uint{7}},
		{name: "empty tokens skipped", query: "?source_id=,1,,3,", want: []uint{1, 3}},
		{name: "all invalid returns nil", query: "?source_id=foo,bar,0", want: nil},
		{name: "value with only whitespace returns nil", query: "?source_id=   ", want: nil},
	}

	c := &Controller{Settings: newValidTestSettings()}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/x"+tc.query, http.NoBody)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			got := c.parseOptionalSourceIDs(ctx, "source_id")
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestParseOptionalSourceIDs_Truncation ensures the maxSourceIDsPerRequest cap is enforced;
// supplying more than the cap returns exactly the cap's worth of leading valid IDs.
// Documented in utils.go as a defensive bound on the IN-clause size.
func TestParseOptionalSourceIDs_Truncation(t *testing.T) {
	t.Parallel()

	const overflow = maxSourceIDsPerRequest + 5

	// Build "1,2,3,...,N" where N > cap.
	var b strings.Builder
	for i := 1; i <= overflow; i++ {
		if i > 1 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(i))
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/x?source_id="+b.String(), http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	c := &Controller{Settings: newValidTestSettings()}
	got := c.parseOptionalSourceIDs(ctx, "source_id")
	require.Len(t, got, maxSourceIDsPerRequest)
	assert.Equal(t, uint(1), got[0], "leading IDs are preserved")
	assert.Equal(t, uint(maxSourceIDsPerRequest), got[len(got)-1])
}

// TestGetSpeciesSummary_PassesSourceIDsToDatastore verifies the API handler threads the
// parsed source_id values into the datastore call as a variadic argument. Uses mockery's
// EXPECT() pattern against MockInterface, matching the variadic with mock.Anything.
func TestGetSpeciesSummary_PassesSourceIDsToDatastore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		query       string
		wantSources []uint
	}{
		{name: "no source_id passes empty variadic", query: "?start_date=2026-01-01&end_date=2026-01-31", wantSources: nil},
		{name: "single source_id forwarded", query: "?start_date=2026-01-01&end_date=2026-01-31&source_id=7", wantSources: []uint{7}},
		{name: "comma list forwarded preserving order", query: "?start_date=2026-01-01&end_date=2026-01-31&source_id=11,22,33", wantSources: []uint{11, 22, 33}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, mockDS, controller := setupAnalyticsTestEnvironment(t)

			// Capture the variadic argument the handler passes so we can assert on it.
			var captured []uint
			mockDS.EXPECT().
				GetSpeciesSummaryData(mock.Anything, "2026-01-01", "2026-01-31", mock.Anything).
				Run(func(_ context.Context, _, _ string, src ...uint) {
					captured = append([]uint(nil), src...)
				}).
				Return([]datastore.SpeciesSummaryData{}, nil).
				Once()

			req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/summary"+tc.query, http.NoBody)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)

			err := controller.GetSpeciesSummary(ctx)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tc.wantSources, captured, "source_id query param must be passed through to the datastore")
		})
	}
}

// TestListAnalyticsSources_AnonymizesForUnauthenticated verifies the privacy contract:
// unauthenticated requests must receive an anonymized DisplayName and no SourceURI or
// NodeName, because SourceURIs can contain credentials (rtsp://user:pass@host) and
// display/node names can identify physical camera locations.
//
// The test controller has no authService configured, so isClientAuthenticated returns
// false — this matches the public/unauthenticated case.
func TestListAnalyticsSources_AnonymizesForUnauthenticated(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.EXPECT().
		ListAnalyticsSourcesData(mock.Anything).
		Return([]datastore.AnalyticsSourceInfo{
			{ID: 11, DisplayName: "Front Yard Camera", SourceURI: "rtsp://admin:supersecret@10.0.0.1/stream1", SourceType: "rtsp", NodeName: "rpi-living-room", DetectionCount: 100},
			{ID: 22, DisplayName: "Back Garden", SourceURI: "rtsp://10.0.0.2/stream2", SourceType: "rtsp", NodeName: "rpi-living-room", DetectionCount: 50},
		}, nil).
		Once()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/sources", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, controller.ListAnalyticsSources(ctx))
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	// IDs and counts are non-informational and should always be present.
	assert.Contains(t, body, `"id":11`)
	assert.Contains(t, body, `"id":22`)
	assert.Contains(t, body, `"detectionCount":100`)
	assert.Contains(t, body, `"detectionCount":50`)
	// Anonymized display names mirror the audio-level convention.
	assert.Contains(t, body, `"displayName":"source-11"`)
	assert.Contains(t, body, `"displayName":"source-22"`)
	// Sensitive fields must be absent.
	assert.NotContains(t, body, "supersecret", "credentials in source_uri must never leak to unauthenticated clients")
	assert.NotContains(t, body, "Front Yard Camera", "real display name must be hidden from unauthenticated clients")
	assert.NotContains(t, body, "Back Garden")
	assert.NotContains(t, body, "rpi-living-room", "node name reveals host topology; hide for unauthenticated clients")
	assert.NotContains(t, body, "10.0.0.1", "source_uri must not be exposed to unauthenticated clients")
	assert.NotContains(t, body, "10.0.0.2")
	// SourceType is also gated — exposing "rtsp" vs "alsa" reveals install topology.
	assert.NotContains(t, body, `"sourceType":"rtsp"`, "source type reveals install topology; hide for unauthenticated clients")
}

// TestListAnalyticsSources_EmptyAndError exercises the two non-happy branches: an empty list
// is returned as `{"sources":[]}` (frontend renders an "All sources" picker without
// special-casing), and a datastore error produces a 500.
func TestListAnalyticsSources_EmptyAndError(t *testing.T) {
	t.Parallel()

	t.Run("empty list", func(t *testing.T) {
		t.Parallel()
		e, mockDS, controller := setupAnalyticsTestEnvironment(t)
		mockDS.EXPECT().
			ListAnalyticsSourcesData(mock.Anything).
			Return([]datastore.AnalyticsSourceInfo{}, nil).
			Once()

		req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/sources", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)

		require.NoError(t, controller.ListAnalyticsSources(ctx))
		assert.Equal(t, http.StatusOK, rec.Code)
		// Must be a sources-array envelope, not bare null, so the frontend never has to
		// distinguish between missing and empty.
		assert.JSONEq(t, `{"sources":[]}`, rec.Body.String())
	})

	t.Run("datastore error becomes 500", func(t *testing.T) {
		t.Parallel()
		e, mockDS, controller := setupAnalyticsTestEnvironment(t)
		mockDS.EXPECT().
			ListAnalyticsSourcesData(mock.Anything).
			Return(nil, errors.New("simulated db failure")).
			Once()

		req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/sources", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)

		// The handler calls c.HandleError which writes the response and returns the wrapped error;
		// callers must not return the error from echo handlers since the response is already sent.
		// We accept either behavior — the assertion is on the HTTP status.
		_ = controller.ListAnalyticsSources(ctx)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}
