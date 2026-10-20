package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// consoleQueueMaxBytes bounds the memory held by the console queue. About
	// 1300 to 2600 typical INFO lines fit, which rides out a sink pause of
	// minutes at the usual console rate, stays small on a Raspberry Pi, and
	// limits how stale a backlog can be when it bursts out after recovery.
	consoleQueueMaxBytes = 256 * 1024

	// consoleFlushTimeout is how long shutdown waits for queued console output
	// to be written. A write stuck in the kernel cannot be cancelled, so the
	// wait is bounded and the drainer is abandoned on timeout.
	consoleFlushTimeout = time.Second

	// consoleFlushPollInterval is how often a flush checks whether the drainer
	// has finished. Polling is deliberate: the writer uses no channels or
	// timers, so a flush from one testing/synctest bubble cannot touch an
	// object that belongs to another.
	consoleFlushPollInterval = 10 * time.Millisecond

	// consoleDropModule is the module name on the drop summary record.
	consoleDropModule = "logger"

	// consoleDropCountKey is the attribute key carrying the dropped line count
	// on the drop summary record. It is documented in the logger README.
	consoleDropCountKey = "dropped_lines"

	// consoleDropMessage is the message of the drop summary record.
	consoleDropMessage = "Console output was dropped because stdout was not draining"
)

// nonBlockingWriter is an io.Writer and io.StringWriter that never blocks its
// callers on the underlying sink. Lines are appended to a byte-bounded FIFO and
// written by a drainer goroutine that runs only while something is queued.
// When the queue is full the newest line is dropped and counted; Write still
// reports success for a dropped line, by design, because a console sink must
// never fail or stall a log call. Each Write or WriteString call is one whole
// line and is written to the sink with one Write call, in arrival order.
type nonBlockingWriter struct {
	out      io.Writer
	maxBytes int
	// report writes the drop summary and returns false when it could not
	// (no file sink yet), in which case the count is kept for a later attempt.
	report func(dropped int64) bool

	mu      sync.Mutex
	queue   []string // FIFO of complete lines; queue[head:] is pending
	head    int      // index of the next line to write
	queued  int      // bytes pending (excludes the line in flight)
	running bool     // drainer goroutine active

	dropped  atomic.Int64 // lines dropped and not yet reported
	reportMu sync.Mutex   // serializes report attempts
}

var (
	_ io.Writer       = (*nonBlockingWriter)(nil)
	_ io.StringWriter = (*nonBlockingWriter)(nil)
)

// newNonBlockingWriter creates a writer in front of out that holds at most
// maxBytes of pending output. report may be nil.
func newNonBlockingWriter(out io.Writer, maxBytes int, report func(int64) bool) *nonBlockingWriter {
	return &nonBlockingWriter{out: out, maxBytes: maxBytes, report: report}
}

// stdoutConsole is the one process-wide non-blocking writer in front of
// os.Stdout. A package-level instance is right here despite the preference
// against singletons: os.Stdout is itself one process-wide sink and this
// writer has to serialize every writer of it. Code that needs another sink
// injects its own instance. It starts no goroutine until a line is queued.
var stdoutConsole = newNonBlockingWriter(os.Stdout, consoleQueueMaxBytes, reportConsoleDropsToGlobal)

// WriteString queues s as one line. It never blocks on the sink. A line that
// does not fit in the queue is dropped and counted, and the call still
// reports success.
func (w *nonBlockingWriter) WriteString(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	w.mu.Lock()
	if w.queued+len(s) > w.maxBytes {
		w.mu.Unlock()
		w.dropped.Add(1)
		return len(s), nil
	}
	if w.head > 0 && w.head >= len(w.queue)/2 {
		// Reclaim the consumed prefix so a backlog that never fully drains
		// cannot grow the slice without bound.
		n := copy(w.queue, w.queue[w.head:])
		clear(w.queue[n:])
		w.queue = w.queue[:n]
		w.head = 0
	}
	w.queue = append(w.queue, s)
	w.queued += len(s)
	start := !w.running
	w.running = true
	w.mu.Unlock()
	if start {
		go w.drain()
	}
	return len(s), nil
}

// Write queues a copy of p as one line. The copy matters because slog reuses
// its buffer after Write returns.
func (w *nonBlockingWriter) Write(p []byte) (int, error) {
	return w.WriteString(string(p))
}

// drain writes queued lines to the sink in order until the queue is empty,
// reports the drop count each time it finds the queue empty, and exits.
func (w *nonBlockingWriter) drain() {
	for {
		w.mu.Lock()
		if w.head == len(w.queue) {
			// Reuse the backing array rather than reslicing from the front.
			clear(w.queue)
			w.queue = w.queue[:0]
			w.head = 0
			w.mu.Unlock()

			w.reportDrops(w.report)

			w.mu.Lock()
			if w.head == len(w.queue) {
				w.running = false
				w.mu.Unlock()
				return
			}
			w.mu.Unlock()
			continue
		}
		line := w.queue[w.head]
		w.queue[w.head] = ""
		w.head++
		w.queued -= len(line)
		w.mu.Unlock()

		// A closed or broken stdout must not stop the loop.
		_, _ = io.WriteString(w.out, line)
	}
}

// reportDrops sends the pending drop count to report and clears it, or keeps
// it when report returns false. Attempts are serialized so a report in flight
// is never missed by a concurrent attempt.
func (w *nonBlockingWriter) reportDrops(report func(int64) bool) {
	if report == nil || w.dropped.Load() == 0 {
		return
	}
	w.reportMu.Lock()
	defer w.reportMu.Unlock()
	n := w.dropped.Swap(0)
	if n > 0 && !report(n) {
		w.dropped.Add(n)
	}
}

// flush waits up to timeout for the drainer to finish and reports whether the
// queue was fully written. It returns false if the sink is still blocked.
func (w *nonBlockingWriter) flush(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		w.mu.Lock()
		running := w.running
		w.mu.Unlock()
		if !running {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		time.Sleep(min(consoleFlushPollInterval, remaining))
	}
}

// FlushConsole waits up to one second for queued console output to be written
// to stdout and returns false if stdout is still blocked. Call it before the
// process exits so the last console lines are not lost.
func FlushConsole() bool {
	return stdoutConsole.flush(consoleFlushTimeout)
}

// reportConsoleDropsToGlobal reports n dropped console lines to the global
// logger's main log file. It does not create the fallback global logger and
// returns false when none is set, so the count is kept.
func reportConsoleDropsToGlobal(n int64) bool {
	globalLoggerMu.Lock()
	cl := globalLogger
	globalLoggerMu.Unlock()
	if cl == nil {
		return false
	}
	return cl.writeConsoleDropSummary(n)
}

// writeConsoleDropSummary writes one WARN record with the dropped line count
// to the main log file only, never to the console. It returns false when there
// is no open main file, so the caller keeps the count. The read lock is held
// across the write so Close cannot close the file underneath it.
func (cl *CentralLogger) writeConsoleDropSummary(n int64) bool {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	if cl.mainWriter == nil || cl.config.FileOutput == nil {
		return false
	}
	handler := slog.NewJSONHandler(cl.mainWriter, &slog.HandlerOptions{
		Level:       parseLogLevel(cl.config.FileOutput.Level),
		ReplaceAttr: timeZoneReplaceAttr(cl.timezone),
	})
	ctx := context.Background()
	if !handler.Enabled(ctx, slog.LevelWarn) {
		return true
	}
	rec := slog.NewRecord(time.Now(), slog.LevelWarn, consoleDropMessage, 0)
	rec.AddAttrs(slog.String(moduleKey, consoleDropModule), slog.Int64(consoleDropCountKey, n))
	return handler.Handle(ctx, rec) == nil
}
