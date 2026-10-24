package onnx

import (
	"sync"
	"sync/atomic"
	"testing"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// concurrentClosers is the number of goroutines that call close at once.
	concurrentClosers = 8

	// testSampleCount, testInputDim and testBatchSize size the inputs of the
	// closed-session tests so that they pin the closed check, not the input
	// validation order.
	testSampleCount = 4
	testInputDim    = 3
	testBatchSize   = 2
)

// fakeSession is a sessionRunner that records how Run and Destroy interact.
//
// destroyed and sawDestroyed are plain fields on purpose: Run reads destroyed
// after it is released, so a Destroy that is not ordered against Run is a data
// race the race detector reports. Do not turn them into atomics.
type fakeSession struct {
	entered   chan struct{} // closed on the first Run
	release   chan struct{} // when non-nil, Run blocks until it is closed
	enterOnce sync.Once

	destroyed    bool
	sawDestroyed bool
	destroys     atomic.Int32
}

func (f *fakeSession) Run(_, _ []ort.Value) error {
	if f.entered != nil {
		f.enterOnce.Do(func() { close(f.entered) })
	}
	if f.release != nil {
		<-f.release
	}
	f.sawDestroyed = f.destroyed
	return nil
}

func (f *fakeSession) Destroy() error {
	f.destroyed = true
	f.destroys.Add(1)
	return nil
}

func TestGuardedSession_CloseWaitsForInFlightRun(t *testing.T) {
	t.Parallel()

	fake := &fakeSession{entered: make(chan struct{}), release: make(chan struct{})}
	g := &guardedSession{s: fake}

	runDone := make(chan error, 1)
	go func() { runDone <- g.run(nil, nil) }()
	<-fake.entered

	closeDone := make(chan error, 1)
	go func() { closeDone <- g.close() }()
	close(fake.release)

	require.NoError(t, <-runDone)
	require.NoError(t, <-closeDone)

	assert.False(t, fake.sawDestroyed, "Destroy must not run while a Run is in flight")
	assert.Equal(t, int32(1), fake.destroys.Load())
	require.ErrorIs(t, g.run(nil, nil), ErrSessionClosed)
	assert.True(t, g.isClosed())
}

func TestGuardedSession_ConcurrentCloseDestroysOnce(t *testing.T) {
	t.Parallel()

	fake := &fakeSession{}
	g := &guardedSession{s: fake}

	start := make(chan struct{})
	results := make(chan error, concurrentClosers)
	for range concurrentClosers {
		go func() {
			<-start
			results <- g.close()
		}()
	}
	close(start)

	for range concurrentClosers {
		require.NoError(t, <-results)
	}
	assert.Equal(t, int32(1), fake.destroys.Load())
}

func TestCloseThenPredict_ReturnsErrSessionClosed(t *testing.T) {
	t.Parallel()

	t.Run("Classifier", func(t *testing.T) {
		t.Parallel()
		fake := &fakeSession{}
		c := &Classifier{
			session: guardedSession{s: fake},
			config:  ModelConfig{SampleCount: testSampleCount, InputShape: []int64{1, testSampleCount}},
		}
		require.NoError(t, c.Close())
		require.NoError(t, c.Close())
		assert.Equal(t, int32(1), fake.destroys.Load())

		audio := make([]float32, testSampleCount)
		_, _, err := c.PredictRawWithEmbeddings(audio)
		require.ErrorIs(t, err, ErrSessionClosed)
		_, err = c.Predict(audio)
		require.ErrorIs(t, err, ErrSessionClosed)
		_, err = c.PredictBatch([][]float32{audio})
		require.ErrorIs(t, err, ErrSessionClosed)
	})

	t.Run("CustomClassifier", func(t *testing.T) {
		t.Parallel()
		fake := &fakeSession{}
		c := &CustomClassifier{session: guardedSession{s: fake}, inputDim: testInputDim}
		require.NoError(t, c.Close())
		require.NoError(t, c.Close())
		assert.Equal(t, int32(1), fake.destroys.Load())

		emb := make([]float32, testInputDim)
		_, err := c.PredictRaw(emb)
		require.ErrorIs(t, err, ErrSessionClosed)
		_, err = c.PredictBatch([][]float32{emb})
		require.ErrorIs(t, err, ErrSessionClosed)
	})

	t.Run("RangeFilter", func(t *testing.T) {
		t.Parallel()
		fake := &fakeSession{}
		r := &RangeFilter{session: guardedSession{s: fake}, labels: make([]string, testInputDim)}
		require.NoError(t, r.Close())
		require.NoError(t, r.Close())
		assert.Equal(t, int32(1), fake.destroys.Load())

		_, err := r.PredictBatchRaw(make([]float32, testBatchSize*3), testBatchSize)
		require.ErrorIs(t, err, ErrSessionClosed)
	})
}
