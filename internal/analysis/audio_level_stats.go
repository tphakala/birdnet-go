package analysis

import (
	"context"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

const audioLevelStatsInterval = 5 * time.Minute

// audioLevelAccum holds accumulated audio level measurements for one source.
type audioLevelAccum struct {
	Sum         int64
	Count       int64
	Min         int
	Max         int
	ZeroCount   int64
	ClipCount   int64
	initialized bool
}

// AudioLevelStats aggregates audio level measurements per source and
// logs a periodic info-level summary.
type AudioLevelStats struct {
	mu    sync.Mutex
	stats map[string]*audioLevelAccum // keyed by source display name

	cancel context.CancelFunc
}

// NewAudioLevelStats creates a new audio level stats aggregator.
func NewAudioLevelStats() *AudioLevelStats {
	return &AudioLevelStats{
		stats: make(map[string]*audioLevelAccum),
	}
}

// Record records a single audio level measurement for the given source.
func (als *AudioLevelStats) Record(sourceName string, level int, clipping bool) {
	als.mu.Lock()
	defer als.mu.Unlock()

	a := als.stats[sourceName]
	if a == nil {
		a = &audioLevelAccum{}
		als.stats[sourceName] = a
	}

	if !a.initialized {
		a.Min = level
		a.Max = level
		a.initialized = true
	} else {
		if level < a.Min {
			a.Min = level
		}
		if level > a.Max {
			a.Max = level
		}
	}

	a.Sum += int64(level)
	a.Count++
	if level == 0 {
		a.ZeroCount++
	}
	if clipping {
		a.ClipCount++
	}
}

// Start launches the periodic logging goroutine.
func (als *AudioLevelStats) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	als.cancel = cancel

	go als.run(ctx)
}

// Stop cancels the periodic logging goroutine.
func (als *AudioLevelStats) Stop() {
	if als.cancel != nil {
		als.cancel()
	}
}

func (als *AudioLevelStats) run(ctx context.Context) {
	ticker := time.NewTicker(audioLevelStatsInterval)
	defer ticker.Stop()

	log := GetLogger()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			als.logAndReset(log)
		}
	}
}

func (als *AudioLevelStats) logAndReset(log logger.Logger) {
	als.mu.Lock()
	snapshot := als.stats
	als.stats = make(map[string]*audioLevelAccum, len(snapshot))
	als.mu.Unlock()

	for source, a := range snapshot {
		if a.Count == 0 {
			continue
		}

		avgLevel := int(a.Sum / a.Count)
		zeroPct := int(a.ZeroCount * 100 / a.Count)
		clipPct := int(a.ClipCount * 100 / a.Count)

		log.Info("audio level stats",
			logger.String("source", source),
			logger.Int("avg_level", avgLevel),
			logger.Int("min_level", a.Min),
			logger.Int("max_level", a.Max),
			logger.Int("zero_pct", zeroPct),
			logger.Int("clipping_pct", clipPct),
			logger.Int64("samples", a.Count),
			logger.String("period", audioLevelStatsInterval.String()),
		)
	}
}
