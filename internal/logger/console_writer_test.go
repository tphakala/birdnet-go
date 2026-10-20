package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// testLineLen is the byte length of every line produced by testLine.
	testLineLen = 10
	// testQueuedLines is how many fixed-length lines fit in the test queue.
	testQueuedLines = 5
	// testQueueLimit is the byte limit of the writers used in the stall tests.
	testQueueLimit = testLineLen * testQueuedLines
	// testBurstLines is the number of lines written while the sink is stalled.
	testBurstLines = 30
	// testKeptLines is the lines delivered per stall episode: one in flight
	// in the drainer plus the lines that fit in the queue.
	testKeptLines = 1 + testQueuedLines
	// testDroppedPerEpisode is the exact drop count per stall episode.
	testDroppedPerEpisode = testBurstLines - testQueuedLines
	// testConsoleLimit is the console queue limit for the integration tests.
	testConsoleLimit = 2048
	// testConsumedItems is how many records the stand-in results consumer logs.
	testConsumedItems = 500
	// testOversizeLine is longer than any test queue limit, so it is always dropped.
	testOversizeLine = 4096
	// testOversizeBurst is the number of oversize lines written after Close.
	testOversizeBurst = 5
)

// testLine returns a line of exactly testLineLen bytes including the newline.
func testLine(i int) string {
	return fmt.Sprintf("line-%04d\n", i)
}

// blockingSink is an io.Writer that blocks every Write until released.
// It must be created inside a synctest bubble.
type blockingSink struct {
	mu      sync.Mutex
	release chan struct{}
	buf     strings.Builder
}

func newBlockingSink() *blockingSink {
	return &blockingSink{release: make(chan struct{})}
}

// Write blocks until the current release channel is closed, then records p.
func (s *blockingSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	ch := s.release
	s.mu.Unlock()
	<-ch
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

// block installs a fresh channel so later writes stall again.
func (s *blockingSink) block() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.release = make(chan struct{})
}

// unblock releases every write waiting on the current channel.
func (s *blockingSink) unblock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.release:
	default:
		close(s.release)
	}
}

// lines returns the recorded output split into lines.
func (s *blockingSink) lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return splitLines(s.buf.String())
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// lockedBuffer is a healthy, mutex-guarded sink.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// recordingReporter records drop reports and answers with a scripted result.
type recordingReporter struct {
	mu     sync.Mutex
	calls  []int64
	accept func(call int) bool
}

func (r *recordingReporter) report(n int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, n)
	return r.accept(len(r.calls))
}

func (r *recordingReporter) recorded() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// stallEpisode primes the drainer so it is parked in the blocked sink, then
// writes a burst from a single goroutine. It returns after every call
// returned, so a blocking Write would fail the test by deadlocking the bubble.
func stallEpisode(t *testing.T, w *nonBlockingWriter, first int) {
	t.Helper()
	n, err := w.WriteString(testLine(first))
	require.NoError(t, err)
	require.Equal(t, testLineLen, n)
	synctest.Wait()
	for i := 1; i <= testBurstLines; i++ {
		n, err := w.WriteString(testLine(first + i))
		require.NoError(t, err, "a drop must look like a successful write")
		require.Equal(t, testLineLen, n)
	}
	synctest.Wait()
}

// expectedLines returns the lines delivered for one episode starting at first:
// the primed line plus the first testQueuedLines of the burst.
func expectedLines(first int) []string {
	out := make([]string, 0, testKeptLines)
	for i := range testKeptLines {
		out = append(out, strings.TrimSuffix(testLine(first+i), "\n"))
	}
	return out
}

func TestNonBlockingWriter_StalledSinkDropsNewestAndReportsOncePerEpisode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := newBlockingSink()
		rep := &recordingReporter{accept: func(int) bool { return true }}
		w := newNonBlockingWriter(sink, testQueueLimit, rep.report)
		t.Cleanup(func() { sink.unblock(); w.flush(consoleFlushTimeout) })

		stallEpisode(t, w, 0)
		assert.Empty(t, rep.recorded(), "no report while the sink is still stalled")

		sink.unblock()
		require.True(t, w.flush(consoleFlushTimeout))
		assert.Equal(t, expectedLines(0), sink.lines(), "newest lines dropped, order kept")
		assert.Equal(t, []int64{testDroppedPerEpisode}, rep.recorded())

		sink.block()
		stallEpisode(t, w, 1000)
		sink.unblock()
		require.True(t, w.flush(consoleFlushTimeout))
		assert.Equal(t, append(expectedLines(0), expectedLines(1000)...), sink.lines())
		assert.Equal(t, []int64{testDroppedPerEpisode, testDroppedPerEpisode}, rep.recorded(),
			"second report carries only the second episode")
	})
}

func TestNonBlockingWriter_KeepsDropCountUntilReporterAccepts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := newBlockingSink()
		rep := &recordingReporter{accept: func(call int) bool { return call > 1 }}
		w := newNonBlockingWriter(sink, testQueueLimit, rep.report)
		t.Cleanup(func() { sink.unblock(); w.flush(consoleFlushTimeout) })

		stallEpisode(t, w, 0)
		sink.unblock()
		require.True(t, w.flush(consoleFlushTimeout))
		assert.Equal(t, []int64{testDroppedPerEpisode}, rep.recorded())
		assert.Equal(t, int64(testDroppedPerEpisode), w.dropped.Load(), "rejected report keeps the count")

		sink.block()
		stallEpisode(t, w, 1000)
		sink.unblock()
		require.True(t, w.flush(consoleFlushTimeout))
		assert.Equal(t, []int64{testDroppedPerEpisode, 2 * testDroppedPerEpisode}, rep.recorded())
		assert.Zero(t, w.dropped.Load())
	})
}

func TestNonBlockingWriter_WriteCopiesCallerBuffer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := newBlockingSink()
		w := newNonBlockingWriter(sink, testConsoleLimit, nil)
		t.Cleanup(func() { sink.unblock(); w.flush(consoleFlushTimeout) })

		_, err := w.WriteString("prime-00\n")
		require.NoError(t, err)
		synctest.Wait()

		buf := []byte("hello-world\n")
		n, err := w.Write(buf)
		require.NoError(t, err)
		require.Equal(t, len(buf), n)
		for i := range buf {
			buf[i] = 'X'
		}

		sink.unblock()
		require.True(t, w.flush(consoleFlushTimeout))
		assert.Equal(t, []string{"prime-00", "hello-world"}, sink.lines())
	})
}

func TestNonBlockingWriter_ConcurrentWritersKeepLinesWholeAndOrdered(t *testing.T) {
	const (
		producers   = 8
		perProducer = 200
		bigLimit    = 1 << 20
	)
	sink := &lockedBuffer{}
	w := newNonBlockingWriter(sink, bigLimit, nil)

	var wg sync.WaitGroup
	for g := range producers {
		wg.Go(func() {
			for i := range perProducer {
				_, err := fmt.Fprintf(w, "g=%d i=%d\n", g, i)
				assert.NoError(t, err)
			}
		})
	}
	wg.Wait()
	require.True(t, w.flush(consoleFlushTimeout))
	require.Zero(t, w.dropped.Load(), "nothing may drop, or it would pass as reordering")

	lines := splitLines(sink.String())
	require.Len(t, lines, producers*perProducer)
	last := make(map[int]int, producers)
	for g := range producers {
		last[g] = -1
	}
	for _, line := range lines {
		var g, i int
		_, err := fmt.Sscanf(line, "g=%d i=%d", &g, &i)
		require.NoError(t, err, "line %q is not whole", line)
		require.Equal(t, fmt.Sprintf("g=%d i=%d", g, i), line)
		assert.Greater(t, i, last[g], "producer %d reordered", g)
		last[g] = i
	}
}

// consoleTestConfig builds a config with main file, console and one console_also module.
func consoleTestConfig(dir string, withModule bool) *LoggingConfig {
	cfg := &LoggingConfig{
		DefaultLevel: "info",
		Timezone:     "UTC",
		Console:      &ConsoleOutput{Enabled: true, Level: "info"},
		FileOutput:   &FileOutput{Enabled: true, Path: filepath.Join(dir, "application.log"), Level: "info"},
	}
	if withModule {
		cfg.ModuleOutputs = map[string]ModuleOutput{
			"analysis.processor": {
				Enabled:     true,
				FilePath:    filepath.Join(dir, "processor.log"),
				Level:       "info",
				ConsoleAlso: true,
			},
		}
	}
	return cfg
}

// readLogLines reads a log file written through cl and returns its non-empty lines.
func readLogLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test-controlled path in t.TempDir
	require.NoError(t, err)
	return splitLines(string(data))
}

// dropSummaries returns the decoded drop-summary records found in lines.
func dropSummaries(t *testing.T, lines []string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range lines {
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec))
		if rec["module"] == consoleDropModule {
			out = append(out, rec)
		}
	}
	return out
}

func TestCentralLogger_StalledConsoleDoesNotBlockModuleLogging(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		sink := newBlockingSink()
		console := newNonBlockingWriter(sink, testConsoleLimit, reportConsoleDropsToGlobal)
		cl, err := newCentralLogger(consoleTestConfig(dir, true), console)
		require.NoError(t, err)

		globalLoggerMu.Lock()
		prev := globalLogger
		globalLoggerMu.Unlock()
		SetGlobal(cl)
		t.Cleanup(func() { SetGlobal(prev) })
		t.Cleanup(func() { _ = cl.Close() })
		t.Cleanup(func() { sink.unblock(); console.flush(consoleFlushTimeout) })

		// Base-handler path: console first, then the main file.
		cl.Module("main").Info("priming the console")
		synctest.Wait()

		var consumed atomic.Int64
		items := make(chan int, testConsumedItems)
		for i := range testConsumedItems {
			items <- i
		}
		close(items)
		go func() {
			log := cl.Module("analysis.processor")
			for i := range items {
				log.Info("Created new pending detection", Int("item", i))
				consumed.Add(1)
			}
		}()
		synctest.Wait()
		require.Equal(t, int64(testConsumedItems), consumed.Load(), "consumer must not stall on the console")

		require.NoError(t, cl.Flush())
		assert.Len(t, readLogLines(t, filepath.Join(dir, "processor.log")), testConsumedItems)
		assert.Len(t, readLogLines(t, filepath.Join(dir, "application.log")), 1, "console stall must not withhold the main file line")

		sink.unblock()
		require.True(t, console.flush(consoleFlushTimeout))
		require.NoError(t, cl.Flush())

		delivered := len(sink.lines())
		total := 1 + testConsumedItems
		require.Less(t, delivered, total, "some console lines must have been dropped")
		summaries := dropSummaries(t, readLogLines(t, filepath.Join(dir, "application.log"))[1:])
		require.Len(t, summaries, 1)
		assert.InDelta(t, float64(total-delivered), summaries[0]["dropped_lines"], 0)
		assert.Equal(t, "WARN", summaries[0]["level"])
		for _, line := range sink.lines() {
			assert.NotContains(t, line, "dropped_lines", "the summary must never go to the console")
		}
	})
}

func TestCentralLogger_CloseIsBoundedAndRecordsDropsWhenSinkNeverRecovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		sink := newBlockingSink()
		console := newNonBlockingWriter(sink, testConsoleLimit, nil)
		cl, err := newCentralLogger(consoleTestConfig(dir, false), console)
		require.NoError(t, err)

		var wrapperCalls atomic.Int64
		console.report = func(n int64) bool {
			wrapperCalls.Add(1)
			return cl.writeConsoleDropSummary(n)
		}
		t.Cleanup(func() { _ = cl.Close() })
		t.Cleanup(func() { sink.unblock(); console.flush(consoleFlushTimeout) })

		log := cl.Module("main")
		log.Info("priming the console")
		synctest.Wait()
		for i := range testConsumedItems {
			log.Info("burst while stalled", Int("item", i))
		}
		synctest.Wait()

		console.mu.Lock()
		pending := len(console.queue) - console.head
		console.mu.Unlock()
		expectedDropped := int64(1 + testConsumedItems - 1 - pending)
		require.Positive(t, expectedDropped)

		start := time.Now()
		require.NoError(t, cl.Close())
		elapsed := time.Since(start)
		assert.GreaterOrEqual(t, elapsed, consoleFlushTimeout)
		assert.Less(t, elapsed, consoleFlushTimeout+consoleFlushPollInterval)

		lines := readLogLines(t, filepath.Join(dir, "application.log"))
		summaries := dropSummaries(t, lines)
		require.Len(t, summaries, 1)
		assert.InDelta(t, float64(expectedDropped), summaries[0]["dropped_lines"], 0)
		assert.Zero(t, wrapperCalls.Load(), "Close reports into its own file, not through the writer's report function")

		for range testOversizeBurst {
			_, err := console.WriteString(strings.Repeat("x", testOversizeLine) + "\n")
			require.NoError(t, err)
		}
		sink.unblock()
		require.True(t, console.flush(consoleFlushTimeout))
		assert.Equal(t, int64(1), wrapperCalls.Load(), "drainer reported against the closed logger")
		assert.Equal(t, int64(testOversizeBurst), console.dropped.Load(), "count stays pending after Close")
	})
}

func TestNonBlockingWriter_EmptyWriteIsIgnored(t *testing.T) {
	sink := &lockedBuffer{}
	w := newNonBlockingWriter(sink, testQueueLimit, nil)
	n, err := w.WriteString("")
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.True(t, w.flush(consoleFlushTimeout))
	assert.Empty(t, sink.String())
}

func TestNonBlockingWriter_LineQueuedDuringReportIsStillWritten(t *testing.T) {
	sink := &lockedBuffer{}
	var w *nonBlockingWriter
	w = newNonBlockingWriter(sink, testQueueLimit, func(int64) bool {
		_, _ = w.WriteString("late\n")
		return true
	})
	_, err := w.WriteString(strings.Repeat("x", testOversizeLine) + "\n")
	require.NoError(t, err)
	require.Equal(t, int64(1), w.dropped.Load())
	_, err = w.WriteString(testLine(0))
	require.NoError(t, err)
	require.True(t, w.flush(consoleFlushTimeout))
	assert.Equal(t, testLine(0)+"late\n", sink.String())
}

func TestConsoleWiring_DefaultsUseSharedStdoutWriter(t *testing.T) {
	globalLoggerMu.Lock()
	prev := globalLogger
	globalLogger = nil
	globalLoggerMu.Unlock()
	t.Cleanup(func() { SetGlobal(prev) })

	assert.False(t, reportConsoleDropsToGlobal(1), "no global logger: count must be kept")

	fallback := Global()
	assert.Same(t, stdoutConsole, fallback.console)
	fallback.Module("wiring").Info("global fallback console line")

	cl, err := NewCentralLogger(&LoggingConfig{
		DefaultLevel: "info",
		Console:      &ConsoleOutput{Enabled: false},
		FileOutput:   &FileOutput{Enabled: false},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cl.Close() })
	assert.Same(t, stdoutConsole, cl.console)
	cl.Module("wiring").Info("stdout fallback handler line")

	NewConsoleLogger("wiring", LogLevelInfo).Info("boot console line")
	NewSlogLogger(nil, LogLevelInfo, nil).Info("nil writer line")

	assert.True(t, FlushConsole())
}

func TestCentralLogger_DropSummarySkippedWhenFileLevelDisablesWarn(t *testing.T) {
	cfg := consoleTestConfig(t.TempDir(), false)
	cfg.FileOutput.Level = "error"
	cl, err := newCentralLogger(cfg, newNonBlockingWriter(&lockedBuffer{}, testQueueLimit, nil))
	require.NoError(t, err)
	t.Cleanup(func() { _ = cl.Close() })

	assert.True(t, cl.writeConsoleDropSummary(3), "disabled level must not accumulate the count")
	require.NoError(t, cl.Flush())
	assert.Empty(t, readLogLines(t, cfg.FileOutput.Path))
}
