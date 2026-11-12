package noisebank

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/audiocore/wav"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/datastore"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

const (
	testSpace  = "test/space"
	otherSpace = "other/space"
	testDim    = 8

	// clipRate is the sample rate of the test clips, the rate the bat model uses.
	clipRate = 256000
	// pcmBitDepth is the bit depth of the test clips.
	pcmBitDepth = 16

	// pcmScale converts the float samples the reader delivers back to the 16-bit
	// level of the clip.
	pcmScale = 32768

	waitFor  = 10 * time.Second
	waitTick = 5 * time.Millisecond
)

// fakeEmbedder stands in for the orchestrator. Window n of a clip, which it reads
// from the signal the test clips carry, gets the score scores[n] (zero past the
// end) and the vector basis(n).
type fakeEmbedder struct {
	mu      sync.Mutex
	calls   int
	loaded  bool
	space   string
	scores  []float32
	timing  classifier.PredictTiming
	results []datastoreResult
	spaceAt func(call int) string
	// entered receives the call number when a call starts. release, when set, is
	// received from before each call returns; closing it frees every call. The
	// wait ignores the context on purpose: a real model finishes its current window
	// after the job was cancelled.
	entered chan int
	release chan struct{}
	lens    []int
	times   []time.Time
	// failAt makes the call with that number fail, and panicAt makes it panic.
	failAt  map[int]error
	panicAt map[int]bool
	// noVector makes every call return no embedding.
	noVector bool
}

type datastoreResult = datastore.Results

func newFakeEmbedder() *fakeEmbedder {
	return &fakeEmbedder{loaded: true, space: testSpace, entered: make(chan int, 1024)}
}

// windowIndex reads which window of its clip a window is from the level the test
// clip holds there: window n starts in a stretch at level n+1.
func windowIndex(window []float32) int {
	return int(math.Round(float64(window[0])*pcmScale)) - 1
}

func basis(n int) []float32 {
	v := make([]float32, testDim)
	v[n%testDim] = 1
	return v
}

func (f *fakeEmbedder) PredictModelTimed(_ context.Context, _ string, sample [][]float32) (classifier.Prediction, error) {
	n := windowIndex(sample[0])
	f.mu.Lock()
	f.calls++
	f.lens = append(f.lens, len(sample[0]))
	f.times = append(f.times, time.Now())
	release := f.release
	space := f.space
	if f.spaceAt != nil {
		space = f.spaceAt(n)
	}
	var score float32
	if n < len(f.scores) {
		score = f.scores[n]
	}
	failure := f.failAt[n]
	doPanic := f.panicAt[n]
	noVector := f.noVector
	f.mu.Unlock()

	f.entered <- n
	if release != nil {
		<-release
	}
	if doPanic {
		panic("model crashed")
	}
	if failure != nil {
		return classifier.Prediction{}, failure
	}
	if noVector {
		return classifier.Prediction{Timing: f.timing}, nil
	}
	return classifier.Prediction{
		Results:   f.results,
		Timing:    f.timing,
		Embedding: classifier.WindowEmbedding{Vector: basis(n), Space: space, TopScore: score},
	}, nil
}

func (f *fakeEmbedder) ModelSpecFor(string) (classifier.ModelSpec, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.loaded {
		return classifier.ModelSpec{}, false
	}
	return classifier.ModelRegistry[classifier.RegistryIDBat].Spec, true
}

func (f *fakeEmbedder) EffectiveBaseOverlap() time.Duration { return 0 }

func (f *fakeEmbedder) EmbeddingSpaceFor(string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.space, f.loaded
}

func (f *fakeEmbedder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeEmbedder) setSpace(space string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.space = space
}

func (f *fakeEmbedder) setScores(scores ...float32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scores = scores
}

// blockCalls makes every call wait until the returned function releases them all.
func (f *fakeEmbedder) blockCalls() (unblock func()) {
	release := make(chan struct{})
	f.mu.Lock()
	f.release = release
	f.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(release) }) }
}

// testEnv is a real v2 SQLite database with a service over a fake embedder.
type testEnv struct {
	t        *testing.T
	db       *gorm.DB
	dets     repository.DetectionRepository
	examples repository.NoiseExampleRepository
	emb      *fakeEmbedder
	svc      *Service
	clipDir  string
	grid     windowGrid
	model    entities.AIModel
	label    entities.Label

	mu     sync.Mutex
	opened []*os.File
	opens  int
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	mgr, err := v2.NewSQLiteManager(v2.Config{DirectPath: filepath.Join(t.TempDir(), "noise.db")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	require.NoError(t, mgr.Initialize())

	e := &testEnv{
		t:        t,
		db:       mgr.DB(),
		dets:     repository.NewDetectionRepository(mgr.DB(), nil, false, false),
		examples: repository.NewNoiseExampleRepository(mgr.DB(), nil, false),
		emb:      newFakeEmbedder(),
		clipDir:  t.TempDir(),
	}
	e.model = entities.AIModel{Name: classifier.ModelRegistry[classifier.RegistryIDBat].DetectionName, Version: "1", ModelType: entities.ModelTypeBat}
	require.NoError(t, e.db.Create(&e.model).Error)
	labelType := entities.LabelType{Name: "bat_species"}
	require.NoError(t, e.db.Create(&labelType).Error)
	e.label = entities.Label{ScientificName: "Eptesicus nilssonii", ModelID: e.model.ID, LabelTypeID: labelType.ID}
	require.NoError(t, e.db.Create(&e.label).Error)

	spec, ok := e.emb.ModelSpecFor("")
	require.True(t, ok)
	svc := NewService(Config{Embedder: e.emb, Examples: e.examples, Detections: e.dets})
	e.grid = svc.gridFor(&spec)
	e.svc = e.start(svc)
	return e
}

// start starts svc and arranges for it to be stopped at the end of the test.
func (e *testEnv) start(svc *Service) *Service {
	e.t.Helper()
	require.NoError(e.t, svc.Start(e.t.Context()))
	e.t.Cleanup(func() {
		ctx, cancel := e.stopContext()
		defer cancel()
		_ = svc.Stop(ctx)
	})
	return svc
}

// open is the OpenClipFunc handed to the service. It counts opens and keeps every
// file so the test can check each one was closed.
func (e *testEnv) open(clipName string) (*os.File, error) {
	f, err := os.Open(filepath.Join(e.clipDir, clipName)) //nolint:gosec // G304: test clip in a temp dir
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.opens++
	e.opened = append(e.opened, f)
	return f, nil
}

func (e *testEnv) openCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.opens
}

// assertAllClosed checks that every file the service opened was closed.
func (e *testEnv) assertAllClosed() {
	e.t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, f := range e.opened {
		_, err := f.Stat()
		assert.Error(e.t, err, "file %d returned by the opener was left open", i)
	}
}

// writeClip writes a clip of windows whole analysis windows on the model's grid.
func (e *testEnv) writeClip(name string, windows int) {
	e.t.Helper()
	writeTestClip(e.t, filepath.Join(e.clipDir, name), e.grid, windows)
}

// writeTestClip writes a WAV clip of windows whole windows of the grid at path.
func writeTestClip(t *testing.T, path string, g windowGrid, windows int) {
	t.Helper()
	samples := g.window + (windows-1)*g.hop
	pcm := make([]byte, 2*samples)
	for i := range samples {
		// The level steps up by one at every hop, so a window shows which one it is.
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(1+i/g.hop)) //nolint:gosec // G115: small positive level
	}
	require.NoError(t, wav.EncodePCM(t.Context(), &wav.Options{
		PCMData: pcm, OutputPath: path, SampleRate: clipRate, BitDepth: pcmBitDepth,
	}))
}

// detection stores a detection of the bat model with a clip name and a hit
// contribution of the bat model, and returns its ID.
func (e *testEnv) detection(clipName string) uint {
	e.t.Helper()
	det := &entities.Detection{LabelID: e.label.ID, ModelID: e.model.ID, DetectedAt: 1000, Confidence: 0.9}
	if clipName != "" {
		det.ClipName = &clipName
	}
	require.NoError(e.t, e.dets.Save(e.t.Context(), det))
	require.NoError(e.t, e.dets.SaveModelContributions(e.t.Context(), det.ID, []*entities.DetectionModelContribution{
		{ModelID: e.model.ID, HitCount: 1, MaxConfidence: 0.9},
	}))
	return det.ID
}

// markedDetection stores a detection with a clip of the given window count.
func (e *testEnv) markedDetection(clipName string, windows int) uint {
	e.t.Helper()
	e.writeClip(clipName, windows)
	return e.detection(clipName)
}

func (e *testEnv) mark(id uint) (Status, error) {
	e.t.Helper()
	return e.svc.Mark(e.t.Context(), MarkRequest{DetectionID: id, OpenClip: e.open})
}

func (e *testEnv) mustMark(id uint) Status {
	e.t.Helper()
	st, err := e.mark(id)
	require.NoError(e.t, err)
	return st
}

func (e *testEnv) status(id uint) Status {
	e.t.Helper()
	st, err := e.svc.Status(e.t.Context(), id, e.open)
	require.NoError(e.t, err)
	return st
}

// waitUntil polls cond until it holds. cond runs on the test goroutine, so it may
// use require.
func (e *testEnv) waitUntil(what string, cond func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(waitFor)
	for !cond() {
		if time.Now().After(deadline) {
			require.FailNow(e.t, "timed out waiting for "+what)
		}
		time.Sleep(waitTick)
	}
}

// jobEnded reports whether the worker is done with the detection's job.
func (e *testEnv) jobEnded(id uint) bool {
	e.svc.mu.Lock()
	defer e.svc.mu.Unlock()
	j := e.svc.jobs[id]
	return j != nil && (j.phase == phaseLearned || j.phase == phaseFailed)
}

// waitState waits until the job of the detection reports state. For the two final
// states it also waits until the worker has finished with the job: a cancelled job
// reports as failed at once, while it may still be on its way to the commit.
func (e *testEnv) waitState(id uint, state State) Status {
	e.t.Helper()
	var st Status
	e.waitUntil("state "+string(state), func() bool {
		// No opener: polling must not open the clip, which tests count.
		var err error
		st, err = e.svc.Status(e.t.Context(), id, nil)
		require.NoError(e.t, err)
		if st.State == StateLearned || st.State == StateFailed {
			return st.State == state && e.jobEnded(id)
		}
		return st.State == state
	})
	return st
}

// markAndLearn marks the detection and waits for its job to store its examples.
func (e *testEnv) markAndLearn(id uint) Status {
	e.t.Helper()
	e.mustMark(id)
	return e.waitState(id, StateLearned)
}

// waitCalls waits until the embedder has been entered n times in total.
func (e *testEnv) waitCalls(n int) {
	e.t.Helper()
	e.waitUntil("embedder calls", func() bool { return e.emb.callCount() >= n })
}

func (e *testEnv) review(id uint) *entities.DetectionReview {
	e.t.Helper()
	review, err := e.dets.GetReview(e.t.Context(), id)
	if err != nil {
		require.ErrorIs(e.t, err, repository.ErrReviewNotFound)
		return nil
	}
	return review
}

func (e *testEnv) attached(id uint) int {
	e.t.Helper()
	by, err := e.examples.CountForDetection(e.t.Context(), id)
	require.NoError(e.t, err)
	total := 0
	for _, n := range by {
		total += int(n)
	}
	return total
}

func (e *testEnv) rowCount() int {
	e.t.Helper()
	rows, err := e.examples.ListAll(e.t.Context())
	require.NoError(e.t, err)
	return len(rows)
}

// offsets returns the stored window offsets of the detection, ascending.
func (e *testEnv) offsets(id uint) []int {
	e.t.Helper()
	rows, err := e.examples.ListAll(e.t.Context())
	require.NoError(e.t, err)
	var out []int
	for i := range rows {
		if rows[i].DetectionID != nil && *rows[i].DetectionID == id {
			out = append(out, rows[i].WindowOffsetMs)
		}
	}
	slices.Sort(out)
	return out
}

// dumpBank lists every example of a bank as a sorted string, owner included.
func dumpBank(b *Bank) []string {
	var out []string
	for name, sp := range b.snap.Load().spaces {
		for i, owner := range sp.owners {
			vec := sp.data[i*sp.dim : (i+1)*sp.dim]
			parts := make([]string, len(vec))
			for k, x := range vec {
				parts[k] = strconv.FormatFloat(float64(x), 'f', 4, 32)
			}
			out = append(out, name+"|"+strconv.Itoa(int(owner))+"|"+strings.Join(parts, ","))
		}
	}
	slices.Sort(out)
	return out
}

// assertBankMatchesDatabase checks the invariant of the bank: it equals what a
// service started now over the same database would load.
func (e *testEnv) assertBankMatchesDatabase(step string) {
	e.t.Helper()
	fresh := NewService(Config{Embedder: e.emb, Examples: e.examples, Detections: e.dets})
	require.NoError(e.t, fresh.Start(e.t.Context()))
	defer func() {
		ctx, cancel := e.stopContext()
		defer cancel()
		_ = fresh.Stop(ctx)
	}()
	assert.Equal(e.t, dumpBank(fresh.Bank()), dumpBank(e.svc.Bank()), "%s: the bank must equal the table", step)
}

// restart replaces the service by a new one over the same database.
func (e *testEnv) restart() {
	e.t.Helper()
	ctx, cancel := e.stopContext()
	defer cancel()
	require.NoError(e.t, e.svc.Stop(ctx))
	e.svc = e.start(NewService(Config{Embedder: e.emb, Examples: e.examples, Detections: e.dets}))
}

// stopContext bounds a Stop. It outlives the test context, which is already done
// when cleanups run.
func (e *testEnv) stopContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(e.t.Context()), waitFor)
}
