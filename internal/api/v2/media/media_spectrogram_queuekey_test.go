// media_spectrogram_queuekey_test.go: regression coverage for the spectrogram
// status-poll queue-key time-of-check/time-of-use bug.
//
// GetSpectrogramStatus used to re-derive the in-memory queue key from the live
// Realtime.Audio.Export.Path on every poll. GenerateSpectrogramByID enqueues an
// in-flight job under a key derived from the export path at request time, so a
// mid-flight Export.Path change made a later status poll compute a different key,
// miss the in-flight job, and report "not_started" even though generation was in
// progress. The fix keys the in-memory queue on an immutable identifier (note ID
// plus visual params) that does not depend on Export.Path, so enqueue, the worker,
// and the status poll always agree.
package media

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// TestGetSpectrogramStatusFindsInFlightJobAfterExportPathChange pins that a status
// poll finds an in-flight generation via the immutable queue key even after
// Realtime.Audio.Export.Path changed between enqueue and the poll. Before the fix
// the poll re-derived a path-based key from the changed export path and missed the
// in-flight entry, returning "not_started".
func TestGetSpectrogramStatusFindsInFlightJobAfterExportPathChange(t *testing.T) {
	withRestoredGlobalSettings(t)

	tmp := t.TempDir()
	sfs, err := securefs.New(tmp)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sfs.Close() })

	const (
		noteID = "42"
		width  = SpectrogramSizeLg
		raw    = true
	)

	settings := apitest.NewValidTestSettings()
	settings.Realtime.Audio.Export.Path = filepath.Join(tmp, "original")

	// A non-nil datastore satisfies requireDatastore; no Get expectation is set because
	// the queue-hit path must return BEFORE touching the datastore (the reordered fast
	// path). An unexpected Get call would fail this test, which is the point.
	mockDS := mocks.NewMockInterface(t)

	controllerCore := &apicore.Core{SFS: sfs, DS: mockDS}
	controllerCore.SetTestContext(t.Context(), nil)
	controller := &Handler{Core: controllerCore}
	controller.Settings.Store(settings)
	conftest.SetTestSettings(settings)

	// Simulate an in-flight generation enqueued under the immutable queue key.
	spec := settings.Realtime.Dashboard.Spectrogram
	queueKey := buildSpectrogramQueueKey(noteID, width, raw, spec.Style, spec.DynamicRange)
	status := &SpectrogramQueueStatus{}
	status.Update(spectrogramStatusGenerating, 0, "Generating spectrogram")
	spectrogramQueue.Store(queueKey, status)
	t.Cleanup(func() { spectrogramQueue.Delete(queueKey) })

	// Change Export.Path mid-flight: after enqueue, before the status poll.
	changed := conf.CloneSettings(settings)
	changed.Realtime.Audio.Export.Path = filepath.Join(tmp, "changed")
	conftest.SetTestSettings(changed)
	controller.Settings.Store(changed)

	// Poll the status endpoint.
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/spectrogram/"+noteID+"/status?size=lg&raw=true", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues(noteID)

	require.NoError(t, controller.GetSpectrogramStatus(ctx))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	data, ok := resp["data"].(map[string]any)
	require.True(t, ok, "response must contain a data object")
	assert.Equal(t, spectrogramStatusGenerating, data["status"],
		"status poll must find the in-flight job via the immutable queue key after Export.Path changed")
}

// TestGenerateSpectrogramFromRelRetainsFailedStatusForPolling pins that a failed
// generation leaves a "failed" entry in the queue so polling clients can see the error.
// Previously the worker's deferred cleanupQueueStatus deleted the still-"generating"
// entry on the way out, and the caller's later updateQueueStatus(..failed..) was a no-op
// on the now-absent key, so pollers saw "not_started" instead of the failure.
func TestGenerateSpectrogramFromRelRetainsFailedStatusForPolling(t *testing.T) {
	withRestoredGlobalSettings(t)

	tmp := t.TempDir()
	sfs, err := securefs.New(tmp)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sfs.Close() })

	// A cancelled controller context makes the worker fail fast on the missing audio
	// file instead of waiting out audioWaitTimeout.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	settings := apitest.NewValidTestSettings()
	controllerCore := &apicore.Core{SFS: sfs}
	controllerCore.SetTestContext(ctx, nil)
	controller := &Handler{Core: controllerCore}
	controller.Settings.Store(settings)
	conftest.SetTestSettings(settings)

	const queueKey = "queuekey-failtest:1026:true"
	t.Cleanup(func() { spectrogramQueue.Delete(queueKey) })

	// relAudioPath points at a file that does not exist, so generation fails.
	relAudioPath := "2024/06/04/Turdus_merula_80p.wav"
	_, genErr := controller.generateSpectrogramFromRel(
		t.Context(), relAudioPath, "clip.wav", queueKey, SpectrogramSizeLg, true, "", "", "")
	require.Error(t, genErr, "generation against a missing audio file must fail")

	statusValue, ok := spectrogramQueue.Load(queueKey)
	require.True(t, ok, "a failed generation must leave a queue entry so polling clients can see the failure")
	status, ok := statusValue.(*SpectrogramQueueStatus)
	require.True(t, ok)
	assert.Equal(t, spectrogramStatusFailed, status.GetStatus(),
		"the retained queue entry must report failed status, not be silently deleted")
}

// TestDeleteFailedStatusIfUnchangedPreservesRetryEntry pins that the failed-status
// retention timer only deletes the original failed entry: a retry that re-enqueues the
// same key within the retention window stores a fresh, active entry that the stale timer
// must not clobber (which would send a polling client back to "not_started" mid-retry).
func TestDeleteFailedStatusIfUnchangedPreservesRetryEntry(t *testing.T) {
	const key = "retry-race:1026:true"
	t.Cleanup(func() { spectrogramQueue.Delete(key) })

	// Original failed generation, captured the way the retention timer captures it.
	orig := &SpectrogramQueueStatus{}
	orig.Update(spectrogramStatusFailed, 0, "first attempt failed")
	spectrogramQueue.Store(key, orig)
	captured, ok := spectrogramQueue.Load(key)
	require.True(t, ok)

	// A retry within the retention window re-enqueues a fresh, active entry.
	fresh := &SpectrogramQueueStatus{}
	fresh.Update(spectrogramStatusGenerating, 0, "retry in progress")
	spectrogramQueue.Store(key, fresh)

	// The stale retention timer fires: it must not delete the retry's entry.
	deleteFailedStatusIfUnchanged(key, captured)

	v, ok := spectrogramQueue.Load(key)
	require.True(t, ok, "the retry's active entry must survive the stale retention timer")
	gotStatus, ok := v.(*SpectrogramQueueStatus)
	require.True(t, ok)
	assert.Equal(t, spectrogramStatusGenerating, gotStatus.GetStatus(),
		"stale retention timer must not delete the fresh retry entry")

	// When the entry is unchanged, the timer does delete it after the TTL.
	spectrogramQueue.Store(key, orig)
	captured2, ok := spectrogramQueue.Load(key)
	require.True(t, ok)
	deleteFailedStatusIfUnchanged(key, captured2)
	_, ok = spectrogramQueue.Load(key)
	assert.False(t, ok, "an unchanged failed entry must be deleted after its retention TTL")
}
