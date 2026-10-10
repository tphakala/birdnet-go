package analysis

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Keep-up evaluation constants. The evaluator judges whether analysis consumes
// audio as fast as it arrives, using the bytes the analysis ring discarded
// unread. That measure is independent of how the load is split across models.
const (
	// keepUpWindow is the length of one evaluation window.
	keepUpWindow = time.Minute

	// keepUpLostFractionThreshold is the share of written audio a window may lose
	// before it counts as lossy. It sits above the loss a healthy pipeline has at
	// overlap 0: the ring holds exactly one clip and the monitor polls every 100 ms,
	// so writes between a full ring and the next poll overwrite the oldest bytes
	// (about 1.7 percent on average, under about 4 percent in the worst window).
	// It is also well below what a one-off 3 s stall costs inside a single window.
	keepUpLostFractionThreshold = 0.05

	// keepUpEvalWindows is how many of the latest windows with audio are judged.
	keepUpEvalWindows = 5

	// keepUpLossyToWarn is how many lossy windows among the last keepUpEvalWindows
	// start an episode. Model warm-up and the latency probe stall inference for a
	// few seconds, which touches at most two one-minute windows, so a single stall
	// never reaches this count.
	keepUpLossyToWarn = 3

	// keepUpCleanToRecover is how many consecutive clean windows with audio end an
	// episode.
	keepUpCleanToRecover = 2

	// percentScale converts a fraction to percent for log fields.
	percentScale = 100
)

// keepUpTransition is the episode change a closed window caused.
type keepUpTransition int

const (
	keepUpNone      keepUpTransition = iota // no change
	keepUpStarted                           // the pipeline began losing audio persistently
	keepUpRecovered                         // the pipeline caught up again
)

// keepUpWindowResult describes one closed evaluation window.
type keepUpWindowResult struct {
	start, end   time.Time
	written      int64   // bytes written to the ring in the window
	lost         int64   // bytes overwritten unread in the window
	windowsRead  int64   // analysis windows read in the window
	lostFraction float64 // lost / written; 0 when nothing was written
	hasAudio     bool    // false when no audio arrived, so the window is no evidence
	lossy        bool    // lostFraction reached keepUpLostFractionThreshold
	lossyWindows int     // lossy windows among the last keepUpEvalWindows
}

// keepUpState evaluates one (source, model) analysis buffer. It is owned by the
// monitor goroutine and needs no locking.
type keepUpState struct {
	buf         *buffer.AnalysisBuffer
	windowStart time.Time
	base        buffer.AnalysisBufferStats

	// history holds the lossy flags of the latest windows with audio, oldest first.
	history    [keepUpEvalWindows]bool
	historyLen int
	cleanRun   int
	lagging    bool
}

// observe feeds the buffer's cumulative stats at time now. It closes the current
// window when keepUpWindow has elapsed and reports the result and any episode
// transition. A first call, a different buffer instance (reallocation) or
// counters below the base start a fresh window without judging it: a
// reallocation is no evidence either way, and the episode and history carry over.
func (s *keepUpState) observe(ab *buffer.AnalysisBuffer, st buffer.AnalysisBufferStats, now time.Time) (res keepUpWindowResult, closed bool, tr keepUpTransition) {
	if s.buf == nil || ab != s.buf ||
		st.WrittenBytes < s.base.WrittenBytes || st.LostBytes < s.base.LostBytes || st.WindowsRead < s.base.WindowsRead {
		s.buf = ab
		s.base = st
		s.windowStart = now
		return keepUpWindowResult{}, false, keepUpNone
	}
	if now.Sub(s.windowStart) < keepUpWindow {
		return keepUpWindowResult{}, false, keepUpNone
	}

	res = keepUpWindowResult{
		start:       s.windowStart,
		end:         now,
		written:     st.WrittenBytes - s.base.WrittenBytes,
		lost:        st.LostBytes - s.base.LostBytes,
		windowsRead: st.WindowsRead - s.base.WindowsRead,
		hasAudio:    st.WrittenBytes > s.base.WrittenBytes,
	}
	s.windowStart = now
	s.base = st

	if !res.hasAudio {
		res.lossyWindows = s.lossyCount()
		return res, true, keepUpNone
	}
	res.lostFraction = float64(res.lost) / float64(res.written)
	res.lossy = res.lostFraction >= keepUpLostFractionThreshold
	s.push(res.lossy)
	res.lossyWindows = s.lossyCount()

	if res.lossy {
		s.cleanRun = 0
		if !s.lagging && res.lossyWindows >= keepUpLossyToWarn {
			s.lagging = true
			return res, true, keepUpStarted
		}
		return res, true, keepUpNone
	}
	s.cleanRun++
	if s.lagging && s.cleanRun >= keepUpCleanToRecover {
		s.lagging = false
		s.historyLen = 0
		return res, true, keepUpRecovered
	}
	return res, true, keepUpNone
}

// push appends a lossy flag, dropping the oldest once keepUpEvalWindows are held.
func (s *keepUpState) push(lossy bool) {
	if s.historyLen == keepUpEvalWindows {
		copy(s.history[:], s.history[1:])
		s.historyLen--
	}
	s.history[s.historyLen] = lossy
	s.historyLen++
}

// lossyCount returns the number of lossy windows currently in the history.
func (s *keepUpState) lossyCount() int {
	n := 0
	for _, lossy := range s.history[:s.historyLen] {
		if lossy {
			n++
		}
	}
	return n
}

// observeKeepUp evaluates the buffer's counters at now and logs episode
// transitions. It runs on every monitor tick, before the window is read.
func (m *BufferManager) observeKeepUp(cfg *monitorConfig, ab *buffer.AnalysisBuffer, state *keepUpState, now time.Time) {
	res, closed, tr := state.observe(ab, ab.Stats(), now)
	if !closed {
		return
	}
	switch tr {
	case keepUpStarted:
		m.logger.Warn("analysis is not keeping up with incoming audio",
			logger.String("source_id", cfg.sourceID),
			logger.String("model_id", cfg.modelID),
			logger.Float64("lost_audio_pct", res.lostFraction*percentScale),
			logger.Duration("window", res.end.Sub(res.start)),
			logger.Int("lossy_windows", res.lossyWindows),
			logger.Int("evaluated_windows", keepUpEvalWindows),
			logger.Duration("buffer_interval", bufferIntervalFor(m.bn, cfg.modelID)))
	case keepUpRecovered:
		m.logger.Info("analysis caught up with incoming audio",
			logger.String("source_id", cfg.sourceID),
			logger.String("model_id", cfg.modelID))
	case keepUpNone:
	}
}
