package classifier

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// gpuRecoveryCPUID is a third synthetic model that runs on the OpenVINO CPU,
// so a recovery pass must leave it alone.
const gpuRecoveryCPUID = "TestZZZOpenVINOCPU_OV"

// gpuRecoveryWait bounds every wait on the recovery goroutine.
const gpuRecoveryWait = 5 * time.Second

// recoveryBuilder counts its builds and returns a fresh ORT-reporting fake per call,
// optionally blocking or failing.
type recoveryBuilder struct {
	id      string
	builds  atomic.Int32
	entered chan struct{} // closed on the first build when non-nil
	release chan struct{} // the build waits on it when non-nil
	err     error
	// order, when non-nil, receives the model ID of every build.
	order *[]string
	once  sync.Once
	// releaseOnce makes closing release idempotent for the tests that also release in a cleanup.
	releaseOnce sync.Once
	mu          sync.Mutex
	built       []*reloadFakeModel
}

func (b *recoveryBuilder) build(*Orchestrator, *conf.Settings, int) (ModelInstance, error) {
	b.builds.Add(1)
	if b.order != nil {
		b.mu.Lock()
		*b.order = append(*b.order, b.id)
		b.mu.Unlock()
	}
	if b.entered != nil {
		b.once.Do(func() { close(b.entered) })
	}
	if b.release != nil {
		<-b.release
	}
	if b.err != nil {
		return nil, b.err
	}
	m := &reloadFakeModel{id: b.id}
	b.mu.Lock()
	b.built = append(b.built, m)
	b.mu.Unlock()
	return m, nil
}

// gpuRecoveryEnv is an orchestrator with GPU-reporting models and the seams a
// recovery test drives.
type gpuRecoveryEnv struct {
	o         *Orchestrator
	svc       *notification.Service
	faulted   *atomic.Bool
	callbacks atomic.Int32
	old       map[string]ModelInstance
}

// newGPURecoveryEnv builds a published orchestrator under (backend, device) with a
// healthy v2.4 stand-in and the given models already on the OpenVINO GPU (cpuIDs
// on the OpenVINO CPU), each with its builder.
func newGPURecoveryEnv(t *testing.T, backend, device string, builders map[string]*recoveryBuilder, gpuIDs []string, cpuIDs ...string) *gpuRecoveryEnv {
	t.Helper()
	env := &gpuRecoveryEnv{svc: setupTestNotification(t), old: map[string]ModelInstance{}}
	setGlobalBackend(t, backend, device, "/opt/ov")
	env.faulted = setGPUFaultSeam(t)
	env.o = newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
	env.o.published.Store(true)
	t.Cleanup(env.o.inferenceHealth.gpuNotice.Stop)
	for id, b := range builders {
		registerTestSecondaryBuilder(t, id, b.build)
	}
	add := func(id, dev string) {
		m := &gpuFaultFakeModel{device: dev}
		m.id = id
		env.old[id] = m
		env.o.models[id] = &modelEntry{instance: m}
	}
	for _, id := range gpuIDs {
		add(id, "")
	}
	for _, id := range cpuIDs {
		add(id, deviceCPU)
	}
	env.o.SetGPURecoveryCallback(func() { env.callbacks.Add(1) })
	return env
}

// settle waits for the recovery goroutines and the notice reconciles to finish.
func (e *gpuRecoveryEnv) settle() {
	e.o.gpuRecovery.wg.Wait()
	e.o.inferenceHealth.inFlight.Wait()
}

// gpuNoticesNow lists every OpenVINO GPU notice in the store: the moved variant and
// the stopped one, whose title the partial variant shares.
func gpuNoticesNow(t *testing.T, svc *notification.Service) []*notification.Notification {
	t.Helper()
	all, err := svc.List(nil)
	require.NoError(t, err)
	var out []*notification.Notification
	for _, n := range all {
		if n.TitleKey == notification.MsgOpenVINOGPUStoppedTitle || n.TitleKey == notification.MsgOpenVINOGPUMovedTitle {
			out = append(out, n)
		}
	}
	return out
}

func TestGPURecovery_AutoMovesGPUModels(t *testing.T) {
	a := &recoveryBuilder{id: testSecondaryID, entered: make(chan struct{}), release: make(chan struct{})}
	b := &recoveryBuilder{id: gpuFaultHealthySecondaryID}
	c := &recoveryBuilder{id: gpuRecoveryCPUID}
	env := newGPURecoveryEnv(t, "auto", "auto",
		map[string]*recoveryBuilder{testSecondaryID: a, gpuFaultHealthySecondaryID: b, gpuRecoveryCPUID: c},
		[]string{testSecondaryID, gpuFaultHealthySecondaryID}, gpuRecoveryCPUID)

	env.faulted.Store(true)
	env.o.onOpenVINOGPUFault()
	select {
	case <-a.entered:
	case <-time.After(gpuRecoveryWait):
		require.FailNow(t, "the recovery pass did not rebuild the GPU model")
	}

	env.o.syncInferenceHealth()
	assert.Empty(t, gpuNoticesNow(t, env.svc), "no notice while the pass runs")

	close(a.release)
	env.settle()

	assert.Equal(t, int32(1), a.builds.Load())
	assert.Equal(t, int32(1), b.builds.Load())
	assert.Equal(t, int32(0), c.builds.Load(), "the OpenVINO CPU model is untouched")
	for _, id := range []string{testSecondaryID, gpuFaultHealthySecondaryID} {
		assert.Equal(t, int32(1), env.old[id].(*gpuFaultFakeModel).closes.Load(), "old %s closed", id)
		assert.False(t, builtOnFailedOpenVINOGPU(env.o.instanceFor(id)), "%s now runs off the GPU", id)
	}
	assert.Equal(t, int32(0), env.old[gpuRecoveryCPUID].(*gpuFaultFakeModel).closes.Load())
	assert.Equal(t, int32(1), env.callbacks.Load(), "one callback after the pass")

	notices := gpuNoticesNow(t, env.svc)
	require.Len(t, notices, 1)
	assert.Equal(t, notification.MsgOpenVINOGPUMovedTitle, notices[0].TitleKey)
	assert.Equal(t, notification.MsgOpenVINOGPUMovedMessage, notices[0].MessageKey)

	env.o.onOpenVINOGPUFault()
	env.settle()
	assert.Equal(t, int32(1), a.builds.Load(), "nothing left on the GPU, nothing rebuilt")
	assert.Equal(t, int32(1), env.callbacks.Load())
}

func TestGPURecovery_ExplicitDoesNothing(t *testing.T) {
	for _, sel := range []struct{ backend, device string }{{"auto", "gpu"}, {"openvino", "auto"}} {
		t.Run(sel.backend+"/"+sel.device, func(t *testing.T) {
			a := &recoveryBuilder{id: testSecondaryID}
			env := newGPURecoveryEnv(t, sel.backend, sel.device, map[string]*recoveryBuilder{testSecondaryID: a}, []string{testSecondaryID})

			env.faulted.Store(true)
			env.o.onOpenVINOGPUFault()
			env.settle()

			assert.Equal(t, int32(0), a.builds.Load(), "an explicit selection never rebuilds off the GPU")
			notices := gpuNoticesNow(t, env.svc)
			require.Len(t, notices, 1)
			assert.Equal(t, notification.MsgOpenVINOGPUStoppedTitle, notices[0].TitleKey)
			assert.Equal(t, notification.MsgOpenVINOGPUStoppedMessage, notices[0].MessageKey)
			assert.Equal(t, int32(0), env.callbacks.Load())
		})
	}
}

func TestGPURecovery_FailedRebuildRetriedOnNextTrigger(t *testing.T) {
	a := &recoveryBuilder{id: testSecondaryID, err: errors.NewStd("build failed")}
	env := newGPURecoveryEnv(t, "auto", "auto", map[string]*recoveryBuilder{testSecondaryID: a}, []string{testSecondaryID})

	env.faulted.Store(true)
	env.o.onOpenVINOGPUFault()
	env.settle()

	assert.Equal(t, int32(1), a.builds.Load(), "one attempt per trigger")
	assert.Same(t, env.old[testSecondaryID], env.o.instanceFor(testSecondaryID), "the failed rebuild keeps the old instance")
	notices := gpuNoticesNow(t, env.svc)
	require.Len(t, notices, 1)
	assert.Equal(t, notification.MsgOpenVINOGPUStoppedTitle, notices[0].TitleKey, "the neutral title")
	assert.Equal(t, notification.MsgOpenVINOGPUMovedPartialMessage, notices[0].MessageKey)
	assert.Contains(t, notices[0].MessageParams["models"], testSecondaryID)
	assert.Equal(t, int32(0), env.callbacks.Load(), "nothing moved, no callback")

	env.o.ReconcileGPUFaultAfterSettingsReload()
	env.settle()
	assert.Equal(t, int32(2), a.builds.Load(), "the next trigger retries once")
}

func TestGPURecovery_ExplicitToAutoMovesLeftovers(t *testing.T) {
	a := &recoveryBuilder{id: testSecondaryID}
	env := newGPURecoveryEnv(t, "auto", "gpu", map[string]*recoveryBuilder{testSecondaryID: a}, []string{testSecondaryID})

	env.faulted.Store(true)
	env.o.onOpenVINOGPUFault()
	env.settle()
	require.Equal(t, int32(0), a.builds.Load())
	require.Len(t, gpuNoticesNow(t, env.svc), 1)

	s := conftest.GetTestSettings()
	s.BirdNET.Backend, s.BirdNET.OpenVINODevice = "auto", "auto"
	conftest.SetTestSettings(s)
	env.o.ReconcileGPUFaultAfterSettingsReload()
	env.settle()

	assert.Equal(t, int32(1), a.builds.Load(), "the leftover on the GPU is rebuilt")
	notices := gpuNoticesNow(t, env.svc)
	require.Len(t, notices, 1, "the stopped notice is replaced, not duplicated")
	assert.Equal(t, notification.MsgOpenVINOGPUMovedTitle, notices[0].TitleKey)
}

func TestGPURecovery_DueBeforePublish(t *testing.T) {
	a := &recoveryBuilder{id: testSecondaryID}
	env := newGPURecoveryEnv(t, "auto", "auto", map[string]*recoveryBuilder{testSecondaryID: a}, []string{testSecondaryID})
	env.o.published.Store(false)

	env.faulted.Store(true)
	env.o.onOpenVINOGPUFault()
	env.settle()
	assert.Equal(t, int32(0), a.builds.Load(), "no pass before the orchestrator is published")
	assert.Empty(t, gpuNoticesNow(t, env.svc), "no notice while a recovery is due")

	env.o.published.Store(true)
	env.o.runGPURecoveryAfterPublish()
	assert.Equal(t, int32(1), a.builds.Load(), "the post-publish pass runs inline")
	env.settle()
	require.Len(t, gpuNoticesNow(t, env.svc), 1)

	// A second orchestrator that exists after the latch tripped (the observer was
	// already consumed) runs its own pass.
	b := &recoveryBuilder{id: gpuFaultHealthySecondaryID}
	registerTestSecondaryBuilder(t, gpuFaultHealthySecondaryID, b.build)
	o2 := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
	o2.published.Store(true)
	m := &gpuFaultFakeModel{}
	m.id = gpuFaultHealthySecondaryID
	o2.models[gpuFaultHealthySecondaryID] = &modelEntry{instance: m}
	o2.runGPURecoveryAfterPublish()
	o2.gpuRecovery.wg.Wait()
	o2.inferenceHealth.inFlight.Wait()
	assert.Equal(t, int32(1), b.builds.Load())
}

func TestGPURecovery_DeleteStops(t *testing.T) {
	// A moves first and finishes, B blocks: the pass is in flight with a move to report.
	a := &recoveryBuilder{id: testSecondaryID}
	b := &recoveryBuilder{id: gpuFaultHealthySecondaryID, entered: make(chan struct{}), release: make(chan struct{})}
	env := newGPURecoveryEnv(t, "auto", "auto",
		map[string]*recoveryBuilder{testSecondaryID: a, gpuFaultHealthySecondaryID: b},
		[]string{testSecondaryID, gpuFaultHealthySecondaryID})
	t.Cleanup(func() { releaseOnce(b) })

	env.faulted.Store(true)
	env.o.onOpenVINOGPUFault()
	select {
	case <-b.entered:
	case <-time.After(gpuRecoveryWait):
		require.FailNow(t, "the recovery pass did not reach the second builder")
	}

	deleted := make(chan struct{})
	go func() {
		env.o.Delete()
		close(deleted)
	}()
	// Delete closes the models, the old instance of B among them, before it waits for
	// the pass; once that happened, a Delete that does not wait would have returned.
	oldB := env.old[gpuFaultHealthySecondaryID].(*gpuFaultFakeModel)
	require.Eventually(t, func() bool { return oldB.closes.Load() == 1 }, gpuRecoveryWait, gpuFaultTestPoll)
	select {
	case <-deleted:
		require.FailNow(t, "Delete returned while a recovery build was in flight")
	default:
	}
	releaseOnce(b)
	select {
	case <-deleted:
	case <-time.After(gpuRecoveryWait):
		require.FailNow(t, "Delete did not return after the build finished")
	}
	assert.Equal(t, int32(0), env.callbacks.Load(), "no callback once Delete began")
	// Delete cleared the callback before the pass finished, so a move reported after it
	// would only be remembered for a later registration: nothing may be remembered.
	env.o.gpuRecovery.callbackMu.Lock()
	remembered := env.o.gpuRecovery.moved
	env.o.gpuRecovery.callbackMu.Unlock()
	assert.False(t, remembered, "a move finished after Delete began is not reported or remembered")

	env.o.onOpenVINOGPUFault()
	env.settle()
	assert.Equal(t, int32(1), b.builds.Load(), "a stopped orchestrator starts no pass")
}

func TestGPURecovery_FailedModelDoesNotStopLaterOnesAndOrderIsSorted(t *testing.T) {
	var order []string
	a := &recoveryBuilder{id: testSecondaryID, err: errors.NewStd("build failed"), order: &order}
	b := &recoveryBuilder{id: gpuFaultHealthySecondaryID, order: &order}
	c := &recoveryBuilder{id: "TestZZZZLast_OV", order: &order}
	env := newGPURecoveryEnv(t, "auto", "auto",
		map[string]*recoveryBuilder{gpuFaultHealthySecondaryID: b, "TestZZZZLast_OV": c, testSecondaryID: a},
		[]string{"TestZZZZLast_OV", testSecondaryID, gpuFaultHealthySecondaryID})

	env.faulted.Store(true)
	env.o.onOpenVINOGPUFault()
	env.settle()

	assert.Equal(t, []string{testSecondaryID, gpuFaultHealthySecondaryID, "TestZZZZLast_OV"}, order, "models are rebuilt in registry ID order")
	assert.False(t, builtOnFailedOpenVINOGPU(env.o.instanceFor(gpuFaultHealthySecondaryID)), "a model after the failed one is still moved")
	assert.False(t, builtOnFailedOpenVINOGPU(env.o.instanceFor("TestZZZZLast_OV")))
	assert.True(t, builtOnFailedOpenVINOGPU(env.o.instanceFor(testSecondaryID)), "the failed model keeps its instance")
	assert.Equal(t, int32(1), env.callbacks.Load(), "a partial move still reports")
}

func TestSetGPURecoveryCallback(t *testing.T) {
	a := &recoveryBuilder{id: testSecondaryID}
	env := newGPURecoveryEnv(t, "auto", "auto", map[string]*recoveryBuilder{testSecondaryID: a}, []string{testSecondaryID})
	env.o.SetGPURecoveryCallback(nil)

	env.faulted.Store(true)
	env.o.onOpenVINOGPUFault()
	env.settle()
	require.Equal(t, int32(1), a.builds.Load())

	var calls atomic.Int32
	env.o.SetGPURecoveryCallback(func() { calls.Add(1) })
	assert.Equal(t, int32(1), calls.Load(), "registering after a move replays it once")
	env.o.SetGPURecoveryCallback(func() { calls.Add(1) })
	assert.Equal(t, int32(1), calls.Load(), "the replay is consumed")

	// Clearing the callback waits for an in-flight call.
	inCallback := make(chan struct{})
	release := make(chan struct{})
	env.o.SetGPURecoveryCallback(func() {
		close(inCallback)
		<-release
	})
	env.o.gpuRecovery.callbackMu.Lock()
	env.o.gpuRecovery.moved = true
	env.o.gpuRecovery.callbackMu.Unlock()
	go env.o.notifyGPURecoveryMoved()
	<-inCallback
	// The callback runs holding callbackMu, which clearing must take: that is what
	// makes it wait for a call in flight.
	require.False(t, env.o.gpuRecovery.callbackMu.TryLock(), "a running callback holds callbackMu")
	cleared := make(chan struct{})
	go func() {
		env.o.SetGPURecoveryCallback(nil)
		close(cleared)
	}()
	close(release)
	select {
	case <-cleared:
	case <-time.After(gpuRecoveryWait):
		require.FailNow(t, "clearing did not return after the callback finished")
	}
}

// gpuRecoveryReloadSettings returns test settings with the given selection.
func gpuRecoveryReloadSettings(backend, device string) *conf.Settings {
	s := conftest.GetTestSettings()
	s.BirdNET.Backend, s.BirdNET.OpenVINODevice = backend, device
	return s
}

func TestReloadEntry_AutoRebuildsOffGPUAfterWarmupFault(t *testing.T) {
	faulted := setGPUFaultSeam(t)
	old := &reloadFakeModel{id: testSecondaryID}
	o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
	o.models[testSecondaryID] = &modelEntry{instance: old}
	o.modelRSS[testSecondaryID] = gpuFaultTestRSS

	dead := newGPUFaultFake(testSecondaryID, faulted)
	ort := &reloadFakeModel{id: testSecondaryID}
	var builds atomic.Int32
	build := func(*Orchestrator, *conf.Settings, int) (ModelInstance, error) {
		if builds.Add(1) == 1 {
			return dead, nil
		}
		return ort, nil
	}

	swapped, err := o.reloadEntry(testSecondaryID, build, reloadOpts{settings: gpuRecoveryReloadSettings("auto", "auto")})

	require.NoError(t, err)
	assert.True(t, swapped)
	assert.Equal(t, int32(2), builds.Load())
	assert.Same(t, ModelInstance(ort), o.models[testSecondaryID].instance, "the off-GPU rebuild is published")
	assert.Equal(t, int32(1), dead.closes.Load(), "the faulted build is closed")
	assert.Equal(t, int32(1), old.closes.Load(), "the replaced instance is closed")
}

func TestReloadEntry_AutoRebuildFaultingAgainKeepsOldInstance(t *testing.T) {
	faulted := setGPUFaultSeam(t)
	old := &reloadFakeModel{id: testSecondaryID}
	o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
	o.models[testSecondaryID] = &modelEntry{instance: old}
	o.modelRSS[testSecondaryID] = gpuFaultTestRSS

	var built []*gpuFaultFakeModel
	build := func(*Orchestrator, *conf.Settings, int) (ModelInstance, error) {
		m := newGPUFaultFake(testSecondaryID, faulted)
		built = append(built, m)
		return m, nil
	}

	swapped, err := o.reloadEntry(testSecondaryID, build, reloadOpts{settings: gpuRecoveryReloadSettings("auto", "auto")})

	require.ErrorIs(t, err, inference.ErrOpenVINOGPUFault)
	assert.False(t, swapped)
	require.Len(t, built, 2, "one rebuild only")
	assert.Equal(t, int32(1), built[0].closes.Load())
	assert.Equal(t, int32(1), built[1].closes.Load())
	assert.Same(t, ModelInstance(old), o.models[testSecondaryID].instance)
	assert.Equal(t, int32(0), old.closes.Load())
	assert.Equal(t, gpuFaultTestRSS, o.modelRSS[testSecondaryID], "the previous RSS figure is restored")
}

func TestReloadEntry_OnlyIfOnFailedGPUSkipsHealthyEntry(t *testing.T) {
	faulted := setGPUFaultSeam(t)
	faulted.Store(true)
	o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
	o.models[testSecondaryID] = &modelEntry{instance: &reloadFakeModel{id: testSecondaryID}}
	var builds atomic.Int32
	build := func(*Orchestrator, *conf.Settings, int) (ModelInstance, error) {
		builds.Add(1)
		return &reloadFakeModel{id: testSecondaryID}, nil
	}

	swapped, err := o.reloadEntry(testSecondaryID, build, reloadOpts{settings: gpuRecoveryReloadSettings("auto", "auto"), onlyIfOnFailedGPU: true})

	require.NoError(t, err)
	assert.False(t, swapped)
	assert.Equal(t, int32(0), builds.Load(), "an entry already off the GPU is not rebuilt")
}

func TestGPURecovery_ReloadWithNothingOnGPUKeepsNotice(t *testing.T) {
	a := &recoveryBuilder{id: testSecondaryID}
	env := newGPURecoveryEnv(t, "auto", "auto", map[string]*recoveryBuilder{testSecondaryID: a}, []string{testSecondaryID})
	env.faulted.Store(true)
	env.o.onOpenVINOGPUFault()
	env.settle()
	before := gpuNoticesNow(t, env.svc)
	require.Len(t, before, 1)

	// Unpublished, a requested recovery stays due instead of running, so whether the
	// reload requested one is observable without racing the pass goroutine.
	env.o.published.Store(false)
	env.o.ReconcileGPUFaultAfterSettingsReload()
	env.settle()

	assert.False(t, env.o.recoveryPending(), "nothing is on the GPU, so the reload requests no recovery")
	after := gpuNoticesNow(t, env.svc)
	require.Len(t, after, 1)
	assert.Equal(t, before[0].ID, after[0].ID, "an unrelated settings reload must not delete and re-raise the notice")
}
