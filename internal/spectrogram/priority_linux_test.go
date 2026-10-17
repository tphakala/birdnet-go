//go:build linux

package spectrogram

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// rawNiceOffset is the constant the kernel adds to a nice value before
	// getpriority returns it (the syscall reports 20 - nice).
	rawNiceOffset = 20

	// procStatNiceField is the 0-based index of the nice value among the fields
	// that follow the command name in /proc/<pid>/task/<tid>/stat.
	procStatNiceField = 16

	// procSelfTaskDir lists the threads of this process.
	procSelfTaskDir = "/proc/self/task"

	// threadExitTimeout bounds the wait for the kernel to remove an exited thread.
	threadExitTimeout = 5 * time.Second
	// threadExitPoll is the poll interval of that wait.
	threadExitPoll = 10 * time.Millisecond

	// extraGoroutines is how many goroutines run after the render to give the
	// scheduler every chance to reuse a lowered thread.
	extraGoroutines = 64
)

// rawPriority returns the kernel's value for the calling thread: 20 minus nice.
func rawPriority() (int, error) {
	return unix.Getpriority(unix.PRIO_PROCESS, unix.Gettid())
}

// threadNice returns the nice value of thread tid from /proc. A thread that no
// longer exists reports alive false and no error.
func threadNice(tid int) (nice int, alive bool, err error) {
	data, err := os.ReadFile(procSelfTaskDir + "/" + strconv.Itoa(tid) + "/stat") //nolint:gosec // G304: path built from a thread id
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	// The command name is parenthesised and may contain spaces; the numbered
	// fields start after the closing parenthesis.
	text := string(data)
	fields := strings.Fields(text[strings.LastIndex(text, ")")+1:])
	if len(fields) <= procStatNiceField {
		return 0, false, errors.NewStd("short /proc stat line")
	}
	nice, err = strconv.Atoi(fields[procStatNiceField])
	return nice, true, err
}

func TestRunAtLowPriority_LowersOnlyTheRenderThread(t *testing.T) {
	// The caller's thread is pinned so its priority can be read before and after.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	callerTid := unix.Gettid()
	callerBefore, err := rawPriority()
	require.NoError(t, err)

	var renderTid, renderRaw, renderProcNice int
	err = runAtLowPriority(func() error {
		renderTid = unix.Gettid()
		raw, err := rawPriority()
		if err != nil {
			return err
		}
		renderRaw = raw
		nice, _, err := threadNice(renderTid)
		renderProcNice = nice
		return err
	})
	require.NoError(t, err)

	assert.NotEqual(t, callerTid, renderTid, "the render runs on its own thread")
	assert.Equal(t, rawNiceOffset-nativeRenderNice, renderRaw, "getpriority reports the render thread at nice %d", nativeRenderNice)
	assert.Equal(t, nativeRenderNice, renderProcNice, "/proc reports the render thread at nice %d", nativeRenderNice)

	callerAfter, err := rawPriority()
	require.NoError(t, err)
	assert.Equal(t, callerBefore, callerAfter, "the calling thread keeps its priority")
}

// baselinePriority returns the raw priority the process runs at when no render
// is in flight: the highest raw value (the lowest nice) among its live threads.
// A thread a render left behind at the render priority cannot lower it, and a
// process that runs at the render priority throughout reports it.
func baselinePriority(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(procSelfTaskDir)
	require.NoError(t, err)
	base := 0
	for _, e := range entries {
		tid, err := strconv.Atoi(e.Name())
		require.NoError(t, err)
		nice, alive, err := threadNice(tid)
		require.NoError(t, err)
		if alive {
			base = max(base, rawNiceOffset-nice)
		}
	}
	require.NotZero(t, base, "the process has live threads")
	return base
}

func TestRunAtLowPriority_ThreadIsDiscardedNotPooled(t *testing.T) {
	// A process that already runs at the render priority (nice -n 19 go test)
	// cannot show a leak.
	base := baselinePriority(t)
	if base == rawNiceOffset-nativeRenderNice {
		t.Skip("the test process already runs at the render priority")
	}

	var renderTid int
	require.NoError(t, runAtLowPriority(func() error {
		renderTid = unix.Gettid()
		return nil
	}))

	// A goroutine that exits while locked to its thread takes the thread with it
	// (the runtime wedges the process's main thread instead of ending it, and
	// never runs Go code on it again), so the lowered priority cannot reach a later
	// goroutine through the thread pool.
	if renderTid != os.Getpid() {
		assert.Eventually(t, func() bool {
			_, alive, err := threadNice(renderTid)
			return err == nil && !alive
		}, threadExitTimeout, threadExitPoll, "thread %d must exit with its goroutine", renderTid)
	}

	// Goroutines started afterwards, some locked so the runtime must create new
	// threads for them, must all run at the normal priority.
	type observed struct {
		raw int
		err error
	}
	results := make(chan observed, 2*extraGoroutines)
	for i := range 2 * extraGoroutines {
		go func() {
			if i%2 == 0 {
				runtime.LockOSThread() // never unlocked: the thread ends with the goroutine
			}
			runtime.Gosched()
			raw, err := rawPriority()
			results <- observed{raw: raw, err: err}
		}()
	}
	for range 2 * extraGoroutines {
		got := <-results
		require.NoError(t, got.err)
		assert.Equal(t, base, got.raw, "a later goroutine must run at the baseline priority")
	}
}

func TestLowerThreadPriority_AppliesToTheCallingThread(t *testing.T) {
	// The thread is locked and never unlocked, so it exits with the goroutine and
	// the test cannot leave a niced thread in the pool.
	done := make(chan struct{})
	var lowerErr, readErr error
	var raw int
	go func() {
		defer close(done)
		runtime.LockOSThread()
		lowerErr = lowerThreadPriority()
		raw, readErr = rawPriority()
	}()
	<-done
	require.NoError(t, lowerErr)
	require.NoError(t, readErr)
	assert.Equal(t, rawNiceOffset-nativeRenderNice, raw)
}
