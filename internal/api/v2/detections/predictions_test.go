package detections

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/detection"
)

const (
	predTestID            = "5"
	predTestMaxID         = "4294967295" // the largest id a 32-bit build holds
	predTestFirstTooBigID = "4294967296"
	predTestEmptyBody     = `{"detectionId":5,"modelPredictions":[]}`
	predTestPrimaryOld    = "Streptopelia senegalensis" // legacy alias of "Spilopelia senegalensis"
	predTestPrimaryNew    = "Spilopelia senegalensis"
	predTestBlackbird     = "Turdus merula"
	predTestRaven         = "Corvus corax"
	predTestMallard       = "Anas platyrhynchos"
	predTestPower         = "power"
	predTestDog           = "Dog"
	predTestBark          = "Bark"
	predTestModelName     = "BirdNET"
	predTestModelVer      = "2.4"
	predTestModelVar      = "default"
	predTestModelType     = "multi"
	predTestRavenCode     = "comrav"
	predTestDoveCode      = "laudov1"

	predTestHigh = 0.40
	predTestMid  = 0.30
	predTestLow  = 0.20
	predTestLow2 = 0.10
	predTestTie  = 0.05
	predTestDog1 = 0.02
)

// predictionsManagerStub satisfies the handler's V2Manager gate. It embeds the
// interface and is never called, so nothing is opened.
type predictionsManagerStub struct{ datastoreV2.Manager }

// predictionsFixture builds a handler on a strict repository mock with the enhanced
// database mode on and a V2 manager stub.
func predictionsFixture(t *testing.T) (*echo.Echo, *mocks.MockDetectionRepository, *Handler) {
	t.Helper()
	e, _, repo, h := detectionsRepoHandler(t)
	h.V2Manager = predictionsManagerStub{}
	datastoreV2.SetEnhancedDatabaseMode()
	t.Cleanup(datastoreV2.ResetDatabaseMode)
	return e, repo, h
}

// getPredictions calls the handler for id and returns the status and raw body.
func getPredictions(t *testing.T, e *echo.Echo, h *Handler, id string) (code int, body string) {
	t.Helper()
	return getPredictionsWith(t, e, h, id, func(r *http.Request) *http.Request { return r })
}

// getPredictionsWith is getPredictions with a hook that can replace the request,
// for example to give it a context that has already ended.
func getPredictionsWith(t *testing.T, e *echo.Echo, h *Handler, id string, adjust func(*http.Request) *http.Request) (code int, body string) {
	t.Helper()
	req := adjust(httptest.NewRequest(http.MethodGet, "/api/v2/detections/"+url.PathEscape(id)+"/predictions", http.NoBody))
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(id)
	require.NoError(t, h.GetDetectionPredictions(c))
	return rec.Code, rec.Body.String()
}

func predictionsPrimary(sci, modelType string) *detection.Result {
	return &detection.Result{
		ID:      5,
		Species: detection.Species{ScientificName: sci},
		Model: detection.ModelInfo{
			Name: predTestModelName, Version: predTestModelVer, Variant: predTestModelVar, ModelType: modelType,
		},
	}
}

func predRow(sci, common, code string, conf float64) detection.AdditionalResult {
	return detection.AdditionalResult{
		Species:    detection.Species{ScientificName: sci, CommonName: common, Code: code},
		Confidence: conf,
	}
}

// expectReads arms the strict mock with a primary and its stored alternates.
func expectReads(repo *mocks.MockDetectionRepository, primary *detection.Result, rows []detection.AdditionalResult) {
	repo.EXPECT().Get(mock.Anything, predTestID).Return(primary, nil).Once()
	repo.EXPECT().GetAdditionalResults(mock.Anything, predTestID).Return(rows, nil).Once()
}

func decodePredictions(t *testing.T, body string) DetectionPredictionsResponse {
	t.Helper()
	var resp DetectionPredictionsResponse
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	return resp
}

func TestGetDetectionPredictions(t *testing.T) {
	t.Run("filters, merges and orders the stored rows", func(t *testing.T) {
		e, repo, h := predictionsFixture(t)
		expectReads(repo, predictionsPrimary(predTestPrimaryOld, predTestModelType), []detection.AdditionalResult{
			// The primary through its alias and a case variant.
			predRow(predTestPrimaryNew, "", "", predTestHigh),
			predRow("streptopelia senegalensis", "", "", predTestHigh),
			// Human sound classes: all dropped, but the truncated Perch name is kept.
			predRow("Human vocal", "", "", predTestHigh),
			predRow("Male_speech_and_man_speaking", "", "", predTestHigh),
			predRow("Homo sapiens", "", "", predTestHigh),
			predRow("Cough", "", "", predTestHigh),
			predRow("Speech ", "", "", predTestHigh), // trailing space from ExtractScientificName
			predRow("Male", "", "", predTestHigh),
			// Case variants fed with the lower confidence first.
			predRow(predTestMallard, "", "", predTestLow2),
			predRow("anas platyrhynchos", "Mallard", "", predTestLow),
			predRow(predTestRaven, "Common Raven", predTestRavenCode, predTestMid),
			// A tie in reverse expected order with mixed leading case.
			predRow(predTestBlackbird, "", "", predTestTie),
			predRow(predTestPower, "", "", predTestTie),
		})

		code, body := getPredictions(t, e, h, predTestID)
		require.Equal(t, http.StatusOK, code)
		resp := decodePredictions(t, body)
		assert.Equal(t, uint(5), resp.DetectionID)
		require.Len(t, resp.ModelPredictions, 1)
		set := resp.ModelPredictions[0]
		assert.Equal(t, predTestModelName, set.ModelName)
		assert.Equal(t, predTestModelVer, set.ModelVersion)
		assert.Equal(t, predTestModelVar, set.ModelVariant)
		assert.Equal(t, predTestModelType, set.ModelType)
		assert.Equal(t, []PredictionResponse{
			{ScientificName: "Male", Confidence: predTestHigh},
			{ScientificName: predTestRaven, CommonName: "Common Raven", SpeciesCode: predTestRavenCode, Confidence: predTestMid},
			{ScientificName: "anas platyrhynchos", CommonName: "Mallard", Confidence: predTestLow},
			{ScientificName: predTestPower, Confidence: predTestTie},
			{ScientificName: predTestBlackbird, Confidence: predTestTie},
		}, set.Predictions)
	})

	t.Run("empty model type falls back to bird", func(t *testing.T) {
		e, repo, h := predictionsFixture(t)
		expectReads(repo, predictionsPrimary(predTestBlackbird, ""), []detection.AdditionalResult{
			predRow(predTestRaven, "", "", predTestMid),
		})
		code, body := getPredictions(t, e, h, predTestID)
		require.Equal(t, http.StatusOK, code)
		resp := decodePredictions(t, body)
		require.Len(t, resp.ModelPredictions, 1)
		assert.Equal(t, "bird", resp.ModelPredictions[0].ModelType)
	})

	t.Run("no rows and only filtered rows give an empty list", func(t *testing.T) {
		for name, rows := range map[string][]detection.AdditionalResult{
			"no rows":            nil,
			"only filtered rows": {predRow(predTestBlackbird, "", "", predTestMid), predRow("Human vocal", "", "", predTestLow)},
		} {
			t.Run(name, func(t *testing.T) {
				e, repo, h := predictionsFixture(t)
				expectReads(repo, predictionsPrimary(predTestBlackbird, predTestModelType), rows)
				code, body := getPredictions(t, e, h, predTestID)
				require.Equal(t, http.StatusOK, code)
				assert.JSONEq(t, predTestEmptyBody, body)
			})
		}
	})
}

func TestGetDetectionPredictions_DogRule(t *testing.T) {
	tests := []struct {
		name string
		rows []detection.AdditionalResult
		want []string
	}{
		{"dog below every other row is dropped", []detection.AdditionalResult{
			predRow(predTestRaven, "", "", predTestMid), predRow(predTestMallard, "", "", predTestLow), predRow(predTestDog, "", "", predTestDog1),
		}, []string{predTestRaven, predTestMallard}},
		{"dog equal to the lowest other row is kept", []detection.AdditionalResult{
			predRow(predTestRaven, "", "", predTestMid), predRow(predTestMallard, "", "", predTestLow), predRow(predTestDog, "", "", predTestLow),
		}, []string{predTestRaven, predTestMallard, predTestDog}},
		{"dog between two rows is kept", []detection.AdditionalResult{
			predRow(predTestRaven, "", "", predTestMid), predRow(predTestMallard, "", "", predTestLow2), predRow(predTestDog, "", "", predTestLow),
		}, []string{predTestRaven, predTestDog, predTestMallard}},
		{"one dog does not shield another", []detection.AdditionalResult{
			predRow(predTestRaven, "", "", predTestMid), predRow(predTestDog, "", "", predTestTie), predRow(predTestBark, "", "", predTestDog1),
		}, []string{predTestRaven}},
		{"a human row does not shield the dog", []detection.AdditionalResult{
			predRow(predTestRaven, "", "", predTestMid), predRow(predTestDog, "", "", predTestTie), predRow("Human vocal", "", "", predTestDog1),
		}, []string{predTestRaven}},
		{"only dog rows are kept", []detection.AdditionalResult{
			predRow(predTestDog, "", "", predTestTie), predRow(predTestBark, "", "", predTestDog1),
		}, []string{predTestDog, predTestBark}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, repo, h := predictionsFixture(t)
			expectReads(repo, predictionsPrimary(predTestBlackbird, predTestModelType), tt.rows)
			code, body := getPredictions(t, e, h, predTestID)
			require.Equal(t, http.StatusOK, code)
			resp := decodePredictions(t, body)
			require.Len(t, resp.ModelPredictions, 1)
			got := make([]string, 0, len(resp.ModelPredictions[0].Predictions))
			for _, p := range resp.ModelPredictions[0].Predictions {
				got = append(got, p.ScientificName)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// permutations returns every ordering of rows.
func permutations(rows []detection.AdditionalResult) [][]detection.AdditionalResult {
	if len(rows) <= 1 {
		return [][]detection.AdditionalResult{slices.Clone(rows)}
	}
	var out [][]detection.AdditionalResult
	for i := range rows {
		rest := make([]detection.AdditionalResult, 0, len(rows)-1)
		rest = append(rest, rows[:i]...)
		rest = append(rest, rows[i+1:]...)
		for _, p := range permutations(rest) {
			out = append(out, append([]detection.AdditionalResult{rows[i]}, p...))
		}
	}
	return out
}

func TestGetDetectionPredictions_InputOrderDoesNotMatter(t *testing.T) {
	rows := []detection.AdditionalResult{
		predRow("Spilopelia senegalensis", "", "", predTestLow),
		predRow("Streptopelia senegalensis", "", predTestDoveCode, predTestLow),
		predRow("spilopelia senegalensis", "", "other1", predTestLow2),
		predRow(predTestRaven, "", "", predTestMid),
	}
	var first string
	perms := permutations(rows)
	require.Len(t, perms, 24)
	for i, p := range perms {
		e, repo, h := predictionsFixture(t)
		expectReads(repo, predictionsPrimary(predTestBlackbird, predTestModelType), p)
		code, body := getPredictions(t, e, h, predTestID)
		require.Equal(t, http.StatusOK, code)
		if i == 0 {
			first = body
			resp := decodePredictions(t, body)
			require.Len(t, resp.ModelPredictions, 1)
			assert.Equal(t, []PredictionResponse{
				{ScientificName: predTestRaven, Confidence: predTestMid},
				{ScientificName: predTestPrimaryNew, SpeciesCode: predTestDoveCode, Confidence: predTestLow},
			}, resp.ModelPredictions[0].Predictions, "the merged row keeps the code of the best-ranked row that has one")
		}
		assert.Equal(t, first, body, "permutation %d", i)
	}
}

func TestGetDetectionPredictions_Errors(t *testing.T) {
	t.Run("not the enhanced database is 409", func(t *testing.T) {
		e, repo, h := predictionsFixture(t)
		datastoreV2.ResetDatabaseMode()
		code, body := getPredictions(t, e, h, predTestID)
		assert.Equal(t, http.StatusConflict, code)
		assert.Contains(t, body, predictionsV2RequiredMessage)
		repo.AssertNotCalled(t, "Get", mock.Anything, mock.Anything)
	})

	t.Run("no v2 manager is 409", func(t *testing.T) {
		e, _, h := predictionsFixture(t)
		h.V2Manager = nil
		code, _ := getPredictions(t, e, h, predTestID)
		assert.Equal(t, http.StatusConflict, code)
	})

	// Signs, spaces, zero and ids that do not fit in 32 bits are rejected before any
	// read; the strict mock fails the test if a repository method is called.
	for _, id := range []string{"abc", "0", "-1", "+5", " 5", predTestFirstTooBigID, "5000000000"} {
		t.Run("invalid id "+id+" is 400", func(t *testing.T) {
			e, _, h := predictionsFixture(t)
			code, _ := getPredictions(t, e, h, id)
			assert.Equal(t, http.StatusBadRequest, code)
		})
	}

	t.Run("largest 32-bit id is read as its validated number", func(t *testing.T) {
		e, repo, h := predictionsFixture(t)
		repo.EXPECT().Get(mock.Anything, predTestMaxID).Return(predictionsPrimary(predTestBlackbird, ""), nil).Once()
		repo.EXPECT().GetAdditionalResults(mock.Anything, predTestMaxID).Return(nil, nil).Once()
		code, body := getPredictions(t, e, h, "0"+predTestMaxID)
		assert.Equal(t, http.StatusOK, code)
		assert.JSONEq(t, `{"detectionId":`+predTestMaxID+`,"modelPredictions":[]}`, body)
	})

	t.Run("unknown detection is 404", func(t *testing.T) {
		e, repo, h := predictionsFixture(t)
		repo.EXPECT().Get(mock.Anything, predTestID).
			Return(nil, fmt.Errorf("failed to get detection %s: %w", predTestID, repository.ErrDetectionNotFound)).Once()
		code, _ := getPredictions(t, e, h, predTestID)
		assert.Equal(t, http.StatusNotFound, code)
	})

	t.Run("other Get error is 500", func(t *testing.T) {
		e, repo, h := predictionsFixture(t)
		repo.EXPECT().Get(mock.Anything, predTestID).Return(nil, assert.AnError).Once()
		code, _ := getPredictions(t, e, h, predTestID)
		assert.Equal(t, http.StatusInternalServerError, code)
	})

	t.Run("predictions read error is 500", func(t *testing.T) {
		e, repo, h := predictionsFixture(t)
		repo.EXPECT().Get(mock.Anything, predTestID).Return(predictionsPrimary(predTestBlackbird, ""), nil).Once()
		repo.EXPECT().GetAdditionalResults(mock.Anything, predTestID).Return(nil, assert.AnError).Once()
		code, _ := getPredictions(t, e, h, predTestID)
		assert.Equal(t, http.StatusInternalServerError, code)
	})
}

// TestGetDetectionPredictions_SkipsUnusableRows verifies that rows with a blank or
// whitespace name and rows with a NaN or infinite confidence never reach the
// response: a non-finite confidence would also make the JSON encoding fail.
func TestGetDetectionPredictions_SkipsUnusableRows(t *testing.T) {
	e, repo, h := predictionsFixture(t)
	expectReads(repo, predictionsPrimary(predTestBlackbird, predTestModelType), []detection.AdditionalResult{
		predRow("", "", "", predTestHigh),
		predRow("   ", "", "", predTestHigh),
		predRow(predTestMallard, "", "", math.NaN()),
		predRow(predTestMallard, "", "", math.Inf(1)),
		predRow(predTestMallard, "", "", math.Inf(-1)),
		predRow(predTestRaven, "", predTestRavenCode, predTestMid),
	})
	code, body := getPredictions(t, e, h, predTestID)
	require.Equal(t, http.StatusOK, code)
	resp := decodePredictions(t, body)
	require.Len(t, resp.ModelPredictions, 1)
	assert.Equal(t, []PredictionResponse{
		{ScientificName: predTestRaven, SpeciesCode: predTestRavenCode, Confidence: predTestMid},
	}, resp.ModelPredictions[0].Predictions)
}

// TestGetDetectionPredictions_RequestEnded verifies that a read failing because the
// request itself ended is answered 499 (canceled) or 408 (deadline), not 500, for
// each of the two reads.
func TestGetDetectionPredictions_RequestEnded(t *testing.T) {
	canceled := func(t *testing.T) context.Context {
		t.Helper()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		return ctx
	}
	expired := func(t *testing.T) context.Context {
		t.Helper()
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		t.Cleanup(cancel)
		return ctx
	}
	cases := []struct {
		name     string
		endedCtx func(*testing.T) context.Context
		readErr  error
		failRead string // "get" or "rows"
		want     int
	}{
		{"canceled during Get", canceled, context.Canceled, "get", apicore.StatusClientClosedRequest},
		{"deadline during Get", expired, context.DeadlineExceeded, "get", http.StatusRequestTimeout},
		{"canceled during GetAdditionalResults", canceled, context.Canceled, "rows", apicore.StatusClientClosedRequest},
		{"deadline during GetAdditionalResults", expired, context.DeadlineExceeded, "rows", http.StatusRequestTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, repo, h := predictionsFixture(t)
			if tc.failRead == "get" {
				repo.EXPECT().Get(mock.Anything, predTestID).Return(nil, fmt.Errorf("read: %w", tc.readErr)).Once()
			} else {
				repo.EXPECT().Get(mock.Anything, predTestID).Return(predictionsPrimary(predTestBlackbird, ""), nil).Once()
				repo.EXPECT().GetAdditionalResults(mock.Anything, predTestID).Return(nil, fmt.Errorf("read: %w", tc.readErr)).Once()
			}
			ended := tc.endedCtx(t)
			code, _ := getPredictionsWith(t, e, h, predTestID, func(r *http.Request) *http.Request {
				return r.WithContext(ended)
			})
			assert.Equal(t, tc.want, code)
		})
	}
}

// TestGetDetectionPredictions_PublicRoute verifies the route is registered outside
// the authenticated group: with an auth middleware that always answers 401, the
// handler's own 409 still comes back.
func TestGetDetectionPredictions_PublicRoute(t *testing.T) {
	e, _, h := setupTestEnvironment(t)
	h.AuthMiddleware = func(echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			return c.NoContent(http.StatusUnauthorized)
		}
	}
	datastoreV2.ResetDatabaseMode()
	t.Cleanup(datastoreV2.ResetDatabaseMode)
	h.V2Manager = nil
	h.RegisterDetectionRoutes(h.Group)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/5/predictions", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusConflict, rec.Code)
}
