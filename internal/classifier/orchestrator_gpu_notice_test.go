package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/notification"
)

func TestGPUNotice_FailedMoveSucceedsOnRetry(t *testing.T) {
	a := &recoveryBuilder{id: testSecondaryID, err: errors.NewStd("build failed")}
	env := newGPURecoveryEnv(t, "auto", "auto", map[string]*recoveryBuilder{testSecondaryID: a}, []string{testSecondaryID})
	env.faulted.Store(true)
	env.o.onOpenVINOGPUFault()
	env.settle()
	notices := gpuNoticesNow(t, env.svc)
	require.Len(t, notices, 1)
	require.Equal(t, notification.MsgOpenVINOGPUMovedPartialMessage, notices[0].MessageKey)

	a.err = nil
	env.o.ReconcileGPUFaultAfterSettingsReload()
	env.settle()

	notices = gpuNoticesNow(t, env.svc)
	require.Len(t, notices, 1)
	assert.Equal(t, notification.MsgOpenVINOGPUMovedTitle, notices[0].TitleKey, "a model that moved leaves the failed set")
	assert.False(t, env.o.moveFailed(testSecondaryID))
}

func TestGPUNotice_PartialNamesOnlyModelsAPassTried(t *testing.T) {
	env := newGPURecoveryEnv(t, "auto", "auto", nil, []string{testSecondaryID, gpuFaultHealthySecondaryID})
	env.faulted.Store(true)

	// One model failed, the other has not been tried: recovery is not done.
	env.o.setMoveFailed(testSecondaryID, true)
	env.o.syncInferenceHealth()
	assert.Empty(t, gpuNoticesNow(t, env.svc))

	env.o.setMoveFailed(gpuFaultHealthySecondaryID, true)
	env.o.syncInferenceHealth()
	notices := gpuNoticesNow(t, env.svc)
	require.Len(t, notices, 1)
	assert.Equal(t, notification.MsgOpenVINOGPUMovedPartialMessage, notices[0].MessageKey)
	assert.Contains(t, notices[0].MessageParams["models"], testSecondaryID)
	assert.Contains(t, notices[0].MessageParams["models"], gpuFaultHealthySecondaryID)
}
