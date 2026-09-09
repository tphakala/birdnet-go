package processor

import (
	"math"
	"strings"
	"time"
)

// HumanDetection records when human speech was last detected on an audio source
// and which mechanism flagged it (metrics.TriggerLabel or metrics.TriggerVAD), so
// the privacy-filter discard can attribute itself. Keeping the timestamp and the
// trigger in one value (rather than two parallel maps) makes them impossible to
// desynchronise.
type HumanDetection struct {
	Time    time.Time
	Trigger string
}

// VADSpeechHit is one entry in the recent-speech-hit history feed.
type VADSpeechHit struct {
	AtUnix      int64
	Probability float64
	Source      string // display name of the audio source, may be empty
}

// VADStatus is a point-in-time view of the Silero VAD speech gate for the
// inference dashboard. The scalar counters are lifetime totals that survive
// detector reloads; Loaded is false when no detector is currently held.
type VADStatus struct {
	Loaded         bool
	Strategy       string
	Source         string // "embedded", "path", or "" when unloaded
	SampleRate     int
	Invocations    int64
	AvgMs          float64
	MaxMs          float64
	SpeechHits     int64
	LastSpeechUnix int64
	// LastSpeechProbability is the probability of the most recent speech hit (set
	// only on a hit, so it pairs with LastSpeechUnix), not of the last inference.
	LastSpeechProbability float64
	// RecentHits is the newest-first history of the last vadHitCap speech hits.
	RecentHits []VADSpeechHit
}

// vadSourceLabel maps an internal cache key ("path:<file>" or "embedded") to the
// coarse source label reported to operators and the dashboard, without leaking
// the on-disk path.
func vadSourceLabel(cacheKey string) string {
	if strings.HasPrefix(cacheKey, "path:") {
		return "path"
	}
	return cacheKey // "embedded" or ""
}

// recordInference accumulates the lifetime latency counters for one completed
// VAD inference. Safe for lock-free concurrent readers via atomics.
func (g *vadGate) recordInference(dur time.Duration) {
	us := dur.Microseconds()
	g.invocations.Add(1)
	g.totalUs.Add(us)
	// Monotonic max via compare-and-swap; the loop retries only on a racing bump.
	for {
		old := g.maxUs.Load()
		if us <= old || g.maxUs.CompareAndSwap(old, us) {
			break
		}
	}
}

// recordSpeechHit accounts one above-threshold VAD detection for the dashboard.
// The probability is recorded here (not per inference) so LastSpeechProbability
// pairs with LastSpeechUnix and matches the Prometheus vad_last_speech_probability
// gauge, which is likewise set only on a hit. The hit is also pushed to the
// newest-first history ring for the dashboard feed.
func (g *vadGate) recordSpeechHit(at time.Time, prob float32, source string) {
	g.speechHits.Add(1)
	g.lastSpeechUnix.Store(at.Unix())
	g.lastProbBits.Store(math.Float32bits(prob))

	hit := VADSpeechHit{AtUnix: at.Unix(), Probability: float64(prob), Source: source}
	g.histMu.Lock()
	// Prepend newest-first and cap at vadHitCap (drop the oldest). Hits are
	// throttle-bounded and the cap is tiny, so the per-hit reallocation is cheap.
	g.recentHits = append([]VADSpeechHit{hit}, g.recentHits...)
	if len(g.recentHits) > vadHitCap {
		g.recentHits = g.recentHits[:vadHitCap]
	}
	g.histMu.Unlock()
}

// status returns the current dashboard view. It reads only atomics and the
// snapshot pointer, so it never blocks on an in-flight inference holding g.mu.
func (g *vadGate) status() VADStatus {
	st := VADStatus{
		Invocations:           g.invocations.Load(),
		MaxMs:                 float64(g.maxUs.Load()) / 1000.0,
		SpeechHits:            g.speechHits.Load(),
		LastSpeechUnix:        g.lastSpeechUnix.Load(),
		LastSpeechProbability: float64(math.Float32frombits(g.lastProbBits.Load())),
	}
	if st.Invocations > 0 {
		st.AvgMs = float64(g.totalUs.Load()) / float64(st.Invocations) / 1000.0
	}
	if snap := g.snapshot.Load(); snap != nil {
		st.Loaded = true
		st.Strategy = snap.strategy
		st.Source = snap.source
		st.SampleRate = snap.sampleRate
	}
	g.histMu.Lock()
	if n := len(g.recentHits); n > 0 {
		st.RecentHits = make([]VADSpeechHit, n)
		copy(st.RecentHits, g.recentHits)
	}
	g.histMu.Unlock()
	return st
}

// VADStatus returns a read-only snapshot of the privacy-filter Silero VAD speech
// gate for the inference dashboard. It is safe to call concurrently and never
// blocks on an in-flight inference.
func (p *Processor) VADStatus() VADStatus {
	return p.vadGate.status()
}
