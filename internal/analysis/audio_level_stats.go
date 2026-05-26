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
	sum         int64
	count       int64
	min         int
	max         int
	zeroCount   int64
	clipCount   int64
	initialized bool
}

// AudioLevelStats aggregates audio level measurements per source and
// logs a periodic info-level summary.
type AudioLevelStats struct {
	mu    sync.Mutex
	stats map[string]*audioLevelAccum // keyed by source display name

	startOnce sync.Once
	cancel    context.CancelFunc
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
		a.min = level
		a.max = level
		a.initialized = true
	} else {
		if level < a.min {
			a.min = level
		}
		if level > a.max {
			a.max = level
		}
	}

	a.sum += int64(level)
	a.count++
	if level == 0 {
		a.zeroCount++
	}
	if clipping {
		a.clipCount++
	}
}

// Start launches the periodic logging goroutine. Safe to call multiple times.
func (als *AudioLevelStats) Start() {
	als.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		als.cancel = cancel
		go als.run(ctx)
	})
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
		if a.count == 0 {
			continue
		}

		avgLevel := int(a.sum / a.count)
		zeroPct := int(a.zeroCount * 100 / a.count)
		clipPct := int(a.clipCount * 100 / a.count)

		log.Info("audio level stats",
			logger.String("source", source),
			logger.Int("avg_level", avgLevel),
			logger.Int("min_level", a.min),
			logger.Int("max_level", a.max),
			logger.Int("zero_pct", zeroPct),
			logger.Int("clipping_pct", clipPct),
			logger.Int64("samples", a.count),
			logger.Duration("period", audioLevelStatsInterval),
			logger.String("operation", "audio_level_stats_report"),
		)
	}
}
