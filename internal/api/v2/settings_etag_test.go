package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// etagTestIntervalOutOfBand and etagTestIntervalRequested are realtime.interval
// values that differ from the controller's initial interval (15), so each row can
// tell whose write landed.
const (
	etagTestIntervalOutOfBand = 30
	etagTestIntervalRequested = 20
	etagTestSection           = "realtime"
	etagTestPutBody           = `{"realtime":{"interval":20}}`
	etagTestPatchBody         = `{"interval":20}`
	// etagTestMinEpochLength is the shortest epoch that still carries enough
	// randomness to tell two processes apart; crypto/rand.Text returns 26 characters.
	etagTestMinEpochLength = 16
)

// etagResult is the part of a settings response the ETag tests assert on.
type etagResult struct {
	code int
	etag string
	body string
}

// etagGetAll calls GET /settings and returns the response.
func etagGetAll(t *testing.T, e *echo.Echo, c *Controller) etagResult {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings", http.NoBody)
	require.NoError(t, c.GetAllSettings(e.NewContext(req, rec)))
	return etagResult{code: rec.Code, etag: rec.Header().Get(apicore.HeaderETag), body: rec.Body.String()}
}

// etagPut calls PUT /settings with the given If-Match value ("" sends none).
func etagPut(t *testing.T, e *echo.Echo, c *Controller, ifMatch string) etagResult {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings", bytes.NewReader([]byte(etagTestPutBody)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if ifMatch != "" {
		req.Header.Set(apicore.HeaderIfMatch, ifMatch)
	}
	rec := httptest.NewRecorder()
	require.NoError(t, c.UpdateSettings(e.NewContext(req, rec)))
	return etagResult{code: rec.Code, etag: rec.Header().Get(apicore.HeaderETag), body: rec.Body.String()}
}

// etagPatch calls PATCH /settings/realtime with the given If-Match value.
func etagPatch(t *testing.T, e *echo.Echo, c *Controller, ifMatch string) etagResult {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+etagTestSection, bytes.NewReader([]byte(etagTestPatchBody)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if ifMatch != "" {
		req.Header.Set(apicore.HeaderIfMatch, ifMatch)
	}
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues(etagTestSection)
	require.NoError(t, c.UpdateSectionSettings(ctx))
	return etagResult{code: rec.Code, etag: rec.Header().Get(apicore.HeaderETag), body: rec.Body.String()}
}

// publishOutOfBand publishes a client-writable change the way a model install
// does: through conf.UpdateSettings, outside any HTTP request.
func publishOutOfBand(t *testing.T) {
	t.Helper()
	publishInterval(t, etagTestIntervalOutOfBand)
}

// TestSettingsETag_IfMatch drives the real PUT and PATCH handlers against the
// global snapshot: GET hands out an ETag, an optional out-of-band publish moves
// the state, and then a write with or without If-Match is applied or refused.
// Not parallel: it publishes to the process-wide settings snapshot.
func TestSettingsETag_IfMatch(t *testing.T) {
	tests := []struct {
		name string
		// between runs after the GET and before the write.
		between func(t *testing.T)
		write   func(t *testing.T, e *echo.Echo, c *Controller, ifMatch string) etagResult
		// sendETag sends the GET's ETag as If-Match; otherwise none is sent.
		sendETag bool
		wantCode int
		// wantApplied is whether the write must land in the published snapshot.
		wantApplied bool
		// wantNewETag is whether the response ETag must differ from the GET's.
		wantNewETag bool
	}{
		{
			name: "PUT with the current ETag succeeds and returns a new ETag", sendETag: true,
			write: etagPut, wantCode: http.StatusOK, wantApplied: true, wantNewETag: true,
		},
		{
			name: "PUT with a stale ETag is refused", between: publishOutOfBand, sendETag: true,
			write: etagPut, wantCode: http.StatusPreconditionFailed,
		},
		{
			name: "PUT without If-Match still applies after an out-of-band publish", between: publishOutOfBand,
			write: etagPut, wantCode: http.StatusOK, wantApplied: true, wantNewETag: true,
		},
		{
			name: "PUT with the ETag survives a range filter publish",
			between: func(t *testing.T) {
				t.Helper()
				conf.UpdateIncludedSpecies([]string{"Parus major_Great Tit"})
			},
			sendETag: true, write: etagPut, wantCode: http.StatusOK, wantApplied: true, wantNewETag: true,
		},
		{
			name: "PATCH with a stale ETag is refused", between: publishOutOfBand, sendETag: true,
			write: etagPatch, wantCode: http.StatusPreconditionFailed,
		},
		{
			name: "PATCH without If-Match still applies after an out-of-band publish", between: publishOutOfBand,
			write: etagPatch, wantCode: http.StatusOK, wantApplied: true, wantNewETag: true,
		},
		{
			name: "PATCH with the current ETag succeeds", sendETag: true,
			write: etagPatch, wantCode: http.StatusOK, wantApplied: true, wantNewETag: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			initial := getTestSettings(t)
			c := newGlobalOwnerController(t, e, initial)
			// Publish once so the snapshot carries a real revision, as in production.
			publishInterval(t, initial.Realtime.Interval)

			got := etagGetAll(t, e, c)
			require.Equal(t, http.StatusOK, got.code)
			require.NotEmpty(t, got.etag, "GET /settings must send an ETag")

			if tt.between != nil {
				tt.between(t)
			}
			afterBetween := conf.GetSettings()

			ifMatch := ""
			if tt.sendETag {
				ifMatch = got.etag
			}
			res := tt.write(t, e, c, ifMatch)

			assert.Equal(t, tt.wantCode, res.code, "body: %s", res.body)
			final := conf.GetSettings()
			if tt.wantApplied {
				assert.Equal(t, etagTestIntervalRequested, final.Realtime.Interval)
				assert.NotSame(t, afterBetween, final)
			} else {
				assert.Same(t, afterBetween, final, "a refused write must publish nothing")
				assert.NotEqual(t, etagTestIntervalRequested, final.Realtime.Interval)
			}

			if tt.wantNewETag {
				require.NotEmpty(t, res.etag, "a successful write must send the new ETag")
				assert.NotEqual(t, got.etag, res.etag)
				assert.Equal(t, etagGetAll(t, e, c).etag, res.etag, "the response ETag must be what the next GET returns")
			}
			if tt.wantCode == http.StatusPreconditionFailed {
				assert.Equal(t, etagGetAll(t, e, c).etag, res.etag, "a 412 must carry the current ETag")
				assert.NotEqual(t, got.etag, res.etag)
			}
		})
	}
}

// publishInterval sets realtime.interval through conf.UpdateSettings, which
// stamps a new revision on the published snapshot.
func publishInterval(t *testing.T, interval int) {
	t.Helper()
	_, _, err := conf.UpdateSettings(func(_, updated *conf.Settings) error {
		updated.Realtime.Interval = interval
		return nil
	})
	require.NoError(t, err)
}

// TestSettingsETag_RangeFilterPublishKeepsETag pins that the runtime range
// filter writers leave the ETag alone, so an open Settings page is not made stale
// by the daily rebuild.
func TestSettingsETag_RangeFilterPublishKeepsETag(t *testing.T) {
	e := echo.New()
	c := newGlobalOwnerController(t, e, getTestSettings(t))
	publishInterval(t, etagTestIntervalOutOfBand)

	before := etagGetAll(t, e, c)
	conf.UpdateIncludedSpecies([]string{"Parus major_Great Tit"})
	conf.ResetRangeFilterUpdateFlag()
	after := etagGetAll(t, e, c)

	require.NotEmpty(t, before.etag)
	assert.Equal(t, before.etag, after.etag)
	assert.NotEqual(t, before.body, after.body, "the body does carry the new species list")
}

// TestSettingsETag_RollbackNeverReusesAnETag pins that a failed write that is
// rolled back leaves the settings content as it was but under a new ETag, so an
// If-Match built from a GET that ran during the failed write cannot match.
func TestSettingsETag_RollbackNeverReusesAnETag(t *testing.T) {
	e := echo.New()
	c := newGlobalOwnerController(t, e, getTestSettings(t))
	c.SetTestContext(t.Context(), func() {})
	c.DisableSaveSettings = false
	publishInterval(t, etagTestIntervalOutOfBand)
	failSettingsSave(t)

	before := etagGetAll(t, e, c)
	res := etagPut(t, e, c, before.etag)
	require.Equal(t, http.StatusInternalServerError, res.code)
	require.Contains(t, res.body, msgSaveFailedRolledBack)
	after := etagGetAll(t, e, c)

	require.NotEmpty(t, before.etag)
	assert.NotEqual(t, before.etag, after.etag, "the rolled-back state must not match an ETag issued before the rollback")
	assert.Equal(t, etagTestIntervalOutOfBand, conf.GetSettings().Realtime.Interval, "the content is the pre-request content")
	assert.Same(t, conf.GetSettings(), c.ControllerSettings())
}

// TestGetSectionSettings_UnexportedFieldIsNotASection pins that a section name
// that matches an unexported Settings field (GET /settings/revision) answers 404
// instead of panicking in the reflective lookup.
func TestGetSectionSettings_UnexportedFieldIsNotASection(t *testing.T) {
	e := echo.New()
	c := newGlobalOwnerController(t, e, getTestSettings(t))

	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/revision", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("revision")

	require.NotPanics(t, func() { require.NoError(t, c.GetSectionSettings(ctx)) })
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, rec.Header().Get(apicore.HeaderETag), "section GETs carry no ETag")
}

// TestSettingsETag_CarriesProcessEpoch pins that the ETag starts with the
// per-process epoch, which is what keeps an ETag issued before a restart from
// matching one issued after it (the revision counter starts over).
func TestSettingsETag_CarriesProcessEpoch(t *testing.T) {
	t.Parallel()

	etag := settingsETag(&conf.Settings{})

	assert.True(t, strings.HasPrefix(etag, `"`+settingsETagEpoch+"-"), "ETag %s must start with the process epoch", etag)
	assert.True(t, strings.HasSuffix(etag, `"`))
	assert.GreaterOrEqual(t, len(settingsETagEpoch), etagTestMinEpochLength, "the epoch must be random enough to differ between processes")
}

// TestSettingsETag_SectionGetSendsNone pins that a successful section GET sends
// no ETag: the revision covers all settings, so a section ETag would go stale on
// any unrelated change.
func TestSettingsETag_SectionGetSendsNone(t *testing.T) {
	e := echo.New()
	c := newGlobalOwnerController(t, e, getTestSettings(t))

	req := httptest.NewRequest(http.MethodGet, "/api/v2/settings/"+etagTestSection, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues(etagTestSection)

	require.NoError(t, c.GetSectionSettings(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get(apicore.HeaderETag))
}

// TestSettingsETag_MultipleIfMatchLines pins that several If-Match header lines
// are read as one comma-separated list (RFC 9110 section 5.3), so a matching tag
// on a later line is honoured.
func TestSettingsETag_MultipleIfMatchLines(t *testing.T) {
	e := echo.New()
	c := newGlobalOwnerController(t, e, getTestSettings(t))
	publishInterval(t, etagTestIntervalOutOfBand)
	current := etagGetAll(t, e, c).etag
	require.NotEmpty(t, current)

	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings", bytes.NewReader([]byte(etagTestPutBody)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Add(apicore.HeaderIfMatch, `"stale-0"`)
	req.Header.Add(apicore.HeaderIfMatch, current)
	rec := httptest.NewRecorder()
	require.NoError(t, c.UpdateSettings(e.NewContext(req, rec)))

	assert.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, etagTestIntervalRequested, conf.GetSettings().Realtime.Interval)
}

// TestSettingsETag_EmptyIfMatchIsRefused pins that an If-Match header that is
// present but empty is a precondition no ETag can satisfy: it answers 412 and
// publishes nothing, instead of being read as "no precondition" and applied.
func TestSettingsETag_EmptyIfMatchIsRefused(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		target  string
		body    string
		section string
		handler func(c *Controller, ctx echo.Context) error
	}{
		{
			name: "PUT", method: http.MethodPut, target: "/api/v2/settings", body: etagTestPutBody,
			handler: func(c *Controller, ctx echo.Context) error { return c.UpdateSettings(ctx) },
		},
		{
			name: "PATCH", method: http.MethodPatch, target: "/api/v2/settings/" + etagTestSection, body: etagTestPatchBody,
			section: etagTestSection,
			handler: func(c *Controller, ctx echo.Context) error { return c.UpdateSectionSettings(ctx) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			c := newGlobalOwnerController(t, e, getTestSettings(t))
			publishInterval(t, etagTestIntervalOutOfBand)
			before := conf.GetSettings()

			req := httptest.NewRequest(tt.method, tt.target, bytes.NewReader([]byte(tt.body)))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			req.Header.Set(apicore.HeaderIfMatch, "")
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			if tt.section != "" {
				ctx.SetParamNames("section")
				ctx.SetParamValues(tt.section)
			}
			require.NoError(t, tt.handler(c, ctx))

			assert.Equal(t, http.StatusPreconditionFailed, rec.Code, "body: %s", rec.Body.String())
			assert.Same(t, before, conf.GetSettings(), "a refused write must publish nothing")
			assert.Equal(t, etagGetAll(t, e, c).etag, rec.Header().Get(apicore.HeaderETag), "a 412 must carry the current ETag")
		})
	}
}

// TestSettingsETag_PreconditionIsCheckedUnderTheWriterLock pins that the If-Match
// comparison is made against the snapshot the write is applied to, inside the
// settings writer lock: a PUT that waits behind another writer must see that
// writer's publish and answer 412 rather than overwrite it.
func TestSettingsETag_PreconditionIsCheckedUnderTheWriterLock(t *testing.T) {
	e := echo.New()
	c := newGlobalOwnerController(t, e, getTestSettings(t))
	publishInterval(t, etagTestIntervalOutOfBand)
	current := etagGetAll(t, e, c).etag
	require.NotEmpty(t, current)

	release := conftest.HoldSettingsWriter(t, func(updated *conf.Settings) {
		updated.Models.Enabled = stalenessInstalledModels()
	})

	done := make(chan struct{})
	var res etagResult
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodPut, "/api/v2/settings", bytes.NewReader([]byte(etagTestPutBody)))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		req.Header.Set(apicore.HeaderIfMatch, current)
		rec := httptest.NewRecorder()
		if err := c.UpdateSettings(e.NewContext(req, rec)); err == nil {
			res = etagResult{code: rec.Code, etag: rec.Header().Get(apicore.HeaderETag), body: rec.Body.String()}
		}
	}()

	conftest.AssertStillBlocked(t, done, "PUT with If-Match")
	release()
	<-done

	assert.Equal(t, http.StatusPreconditionFailed, res.code, "body: %s", res.body)
	final := conf.GetSettings()
	assert.Equal(t, stalenessInstalledModels(), final.Models.Enabled, "the writer that held the lock must survive")
	assert.Equal(t, etagTestIntervalOutOfBand, final.Realtime.Interval, "the refused PUT must not be applied")
}
