package onnx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/testutil"
)

const (
	// concurrentClosers is the number of goroutines that call close at once.
	concurrentClosers = 8

	// concurrentRunners is the number of Runs that must be inside the session
	// at the same time for the read lock to count as shared.
	concurrentRunners = 2

	// testSampleCount, testInputDim and testBatchSize size the inputs of the
	// closed-session tests so that they pin the closed check, not the input
	// validation order.
	testSampleCount = 4
	testInputDim    = 3
	testBatchSize   = 2

	// testRangeFilterInputWidth is the number of floats per range filter input
	// (latitude, longitude, week).
	testRangeFilterInputWidth = 3
)

func TestGuardedSession_CloseWaitsForInFlightRun(t *testing.T) {
	t.Parallel()

	fake := &fakeSession{entered: make(chan struct{}), release: make(chan struct{})}
	g := &guardedSession{s: fake}

	runDone := goErr(func() error { return g.run(nil, nil) })
	testutil.WaitForChannel(t, fake.entered, testutil.DefaultTestTimeout, "Run never started")

	closeDone := goErr(g.close)
	close(fake.release)

	require.NoError(t, awaitErr(t, runDone))
	require.NoError(t, awaitErr(t, closeDone))

	assert.False(t, fake.sawDestroyed, "Destroy must not run while a Run is in flight")
	assert.Equal(t, int32(1), fake.destroys.Load())
	require.ErrorIs(t, g.run(nil, nil), ErrSessionClosed)
	assert.True(t, g.isClosed())
}

func TestGuardedSession_ConcurrentRunsShareTheLock(t *testing.T) {
	t.Parallel()

	fake := &fakeSession{allIn: make(chan struct{}), abort: make(chan struct{}), wantInside: concurrentRunners}
	t.Cleanup(func() { close(fake.abort) })
	g := &guardedSession{s: fake}

	results := make([]<-chan error, concurrentRunners)
	for i := range results {
		results[i] = goErr(func() error { return g.run(nil, nil) })
	}

	// Every Run blocks until all of them are inside, so they can only finish
	// when the guard lets them run at the same time.
	for _, ch := range results {
		require.NoError(t, awaitErr(t, ch))
	}
}

func TestGuardedSession_ConcurrentCloseDestroysOnce(t *testing.T) {
	t.Parallel()

	fake := &fakeSession{}
	g := &guardedSession{s: fake}

	start := make(chan struct{})
	results := make([]<-chan error, concurrentClosers)
	for i := range results {
		results[i] = goErr(func() error {
			<-start
			return g.close()
		})
	}
	close(start)

	for _, ch := range results {
		require.NoError(t, awaitErr(t, ch))
	}
	assert.Equal(t, int32(1), fake.destroys.Load())
}

func TestGuardedSession_DestroyErrorReportedOnceThenClosed(t *testing.T) {
	t.Parallel()

	fake := &fakeSession{destroyErr: errFakeDestroy}
	g := &guardedSession{s: fake}

	require.ErrorIs(t, g.close(), errFakeDestroy)
	require.NoError(t, g.close())

	assert.Equal(t, int32(1), fake.destroys.Load())
	assert.True(t, g.isClosed())
	require.ErrorIs(t, g.run(nil, nil), ErrSessionClosed)
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

		_, err := r.PredictBatchRaw(make([]float32, testBatchSize*testRangeFilterInputWidth), testBatchSize)
		require.ErrorIs(t, err, ErrSessionClosed)
	})
}
