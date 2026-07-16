package observability

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/tphakala/birdnet-go/internal/classifier/inferencestats"
	"github.com/tphakala/birdnet-go/internal/datastore/dbstats"
)

// CPUUsageFunc is a function that returns the current total CPU usage percentage.
// This allows the collector to reuse the existing CPUCache from the API package
// instead of making a conflicting concurrent cpu.Percent call.
type CPUUsageFunc func() float64

// Collector periodically samples system metrics and records them into a MetricsStore.
type Collector struct {
	store    MetricsStore
	interval time.Duration
	cpuFunc  CPUUsageFunc

	// now is the clock used to timestamp each collection tick. A single tick
	// timestamp is shared by every delta-based collector (disk I/O, database,
	// inference, and health counters) so all rates and recorded timestamps in one
	// collect() reference the same instant. Defaults to time.Now; tests inject a
	// deterministic clock to avoid depending on wall-clock resolution (which is
	// coarse on Windows and made TestCollector_InferenceThroughput flaky).
	now func() time.Time

	// Internal state for disk I/O delta computation
	prevDiskIO   map[string]disk.IOCountersStat
	prevDiskTime time.Time

	// Database latency tracking (optional, set via SetDBCounters)
	dbCounters *dbstats.Counters
	prevDBSnap *dbstats.Snapshot

	// Inference latency tracking (optional, set via SetInferenceCounters)
	inferenceCounters  *inferencestats.CounterMap
	prevInferenceSnaps map[string]*inferencestats.Snapshot

	// Per-model clip length provider for RTF computation (optional, set via SetModelClipFunc)
	modelClipFunc func() map[string]float64
	// Per-model RSS byte provider (optional, set via SetModelRSSFunc)
	modelRSSFunc func() (map[string]int64, int64)
	// Prometheus gauge setters (optional, set via SetInferenceGaugeSetters)
	rtfGauge             func(model string, rtf float64)
	rssGauge             func(model string, bytes int64)
	inferenceGaugeDelete func(model string)
	// gaugeModels tracks which model IDs have had a gauge label set so stale
	// series can be pruned after unload. Accessed only from the collect()
	// goroutine, so no mutex is needed.
	gaugeModels map[string]struct{}

	// Health counter tracking (optional, set via SetHealthStore/SetHealthEvents)
	healthStore     *HealthMetricsStore
	healthEvents    *HealthEventBuffer
	audioRouterFn   func() []AudioRouterSnapshot
	streamHealthFn  func() []StreamHealthSnapshot
	prevAudioSnaps  map[string]AudioRouterSnapshot
	prevStreamSnaps map[string]StreamHealthSnapshot
	// Audio Prometheus gauge setters (optional, set via SetAudioGaugeSetters).
	audioQueueDepthGauge    func(source string, depth float64)
	audioDroppedChunksGauge func(source string, total float64)

	// Track which metrics have had logged errors to avoid log spam
	loggedErrors map[string]bool
}

// NewCollector creates a Collector that samples system metrics at the given interval
// and stores them in the provided MetricsStore.
// The cpuFunc should return the current total CPU usage percentage (e.g. from CPUCache).
// Panics if store is nil or interval is non-positive.
func NewCollector(store MetricsStore, interval time.Duration, cpuFunc CPUUsageFunc) *Collector {
	if store == nil {
		panic("observability: NewCollector requires a non-nil MetricsStore")
	}
	if interval <= 0 {
		panic("observability: NewCollector requires a positive interval")
	}
	return &Collector{
		store:        store,
		interval:     interval,
		cpuFunc:      cpuFunc,
		now:          time.Now,
		prevDiskIO:   make(map[string]disk.IOCountersStat),
		loggedErrors: make(map[string]bool),
	}
}

// Start runs the collection loop until the context is cancelled.
// It should be called in a goroutine.
func (c *Collector) Start(ctx context.Context) {
	// Collect immediately on start, then on each tick
	c.collect()

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collect()
		}
	}
}

// Metric key constants for collected system metrics.
const (
	// expectedMetricCount is a lower-bound hint for the map pre-allocation per tick.
	// Per-model RTF keys add len(models) more entries each tick, so the map may grow
	// beyond this value. This is intentional: the map grows as needed.
	expectedMetricCount = 13

	metricCPUTotal          = "cpu.total"
	metricMemoryUsedPercent = "memory.used_percent"
	metricCPUTemperature    = "cpu.temperature"
	metricDiskUsedPercent   = "disk.used_percent.%s"
	metricDiskIORead        = "disk.io.read.%s"
	metricDiskIOWrite       = "disk.io.write.%s"
	metricDBReadLatency     = "db.read_latency_ms"
	metricDBWriteLatency    = "db.write_latency_ms"
	metricDBReadLatencyMax  = "db.read_latency_max_ms"
	metricDBWriteLatencyMax = "db.write_latency_max_ms"
	metricDBQueriesPerSec   = "db.queries_per_sec"
)

func inferenceMetricKey(modelID string) string {
	return inferencestats.MetricKey(modelID)
}

// collect gathers all system metrics and records them as a single batch.
func (c *Collector) collect() {
	points := make(map[string]float64, expectedMetricCount)

	// Single authoritative timestamp for this tick. Every delta-based collector
	// computes its delta against this instant and the previous tick's instant
	// (a rate for disk I/O/database/inference, a recorded count for health
	// counters), so they stay mutually consistent and the timing is fully
	// controllable in tests.
	tick := c.now()

	c.collectCPU(points)
	c.collectMemory(points)
	c.collectTemperature(points)
	c.collectDisk(points, tick)
	c.collectDatabase(points, tick)
	c.collectInference(points, tick)
	c.collectAudio(points)

	if len(points) > 0 {
		c.store.RecordBatch(points)
	}

	c.collectModelRSS()
	c.pruneInferenceGauges()
	c.collectHealthCounters(tick)
}

// collectCPU reads CPU usage from the injected function.
func (c *Collector) collectCPU(points map[string]float64) {
	if c.cpuFunc != nil {
		points[metricCPUTotal] = c.cpuFunc()
	}
}

// collectMemory reads memory usage via gopsutil.
func (c *Collector) collectMemory(points map[string]float64) {
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		c.logOnce("memory", "failed to collect memory metrics: %v", err)
		return
	}
	points[metricMemoryUsedPercent] = memInfo.UsedPercent
}

// collectTemperature reads CPU temperature from Linux thermal zones via the
// shared ReadCPUTemperature reader. Gracefully skipped on non-Linux or if no
// suitable sensor is found.
func (c *Collector) collectTemperature(points map[string]float64) {
	celsius, _, err := ReadCPUTemperature(DefaultThermalBasePath)
	if err == nil {
		points[metricCPUTemperature] = celsius
	}
}

// collectDisk reads disk usage and I/O statistics via gopsutil.
func (c *Collector) collectDisk(points map[string]float64, tick time.Time) {
	c.collectDiskUsage(points)
	c.collectDiskIO(points, tick)
}

// collectDiskUsage reads disk usage percentages for each partition.
func (c *Collector) collectDiskUsage(points map[string]float64) {
	partitions, err := disk.Partitions(false)
	if err != nil {
		c.logOnce("disk_partitions", "failed to list disk partitions: %v", err)
		return
	}

	for i := range partitions {
		p := &partitions[i]
		if skipCollectorFS(p.Fstype) {
			continue
		}
		usage, err := disk.Usage(p.Mountpoint)
		if err != nil {
			c.logOnce("disk_usage_"+p.Mountpoint, "failed to get disk usage for %s: %v", p.Mountpoint, err)
			continue
		}
		key := fmt.Sprintf(metricDiskUsedPercent, sanitizeMountpoint(p.Mountpoint))
		points[key] = usage.UsedPercent
	}
}

// collectDiskIO computes disk I/O rates (bytes/sec) as deltas between ticks.
func (c *Collector) collectDiskIO(points map[string]float64, tick time.Time) {
	counters, err := disk.IOCounters()
	if err != nil {
		c.logOnce("disk_io", "failed to read disk I/O counters: %v", err)
		return
	}

	if !c.prevDiskTime.IsZero() {
		elapsed := tick.Sub(c.prevDiskTime).Seconds()
		if elapsed > 0 {
			for device := range counters {
				counter := counters[device]
				prev, ok := c.prevDiskIO[device]
				if !ok {
					continue
				}
				// Sanitize device name to remove any path prefixes (platform-dependent)
				name := filepath.Base(device)
				// Guard against counter resets (device swap, kernel rollover)
				if counter.ReadBytes >= prev.ReadBytes {
					readRate := float64(counter.ReadBytes-prev.ReadBytes) / elapsed
					points[fmt.Sprintf(metricDiskIORead, name)] = readRate
				}
				if counter.WriteBytes >= prev.WriteBytes {
					writeRate := float64(counter.WriteBytes-prev.WriteBytes) / elapsed
					points[fmt.Sprintf(metricDiskIOWrite, name)] = writeRate
				}
			}
		}
	}

	c.prevDiskIO = counters
	c.prevDiskTime = tick
}

// SetDBCounters sets the database atomic counters for latency tracking.
// Must be called before Start. If not called, database metrics are skipped.
func (c *Collector) SetDBCounters(counters *dbstats.Counters) {
	c.dbCounters = counters
}

// usToMs converts microseconds to milliseconds.
const usToMs = 1000.0

// usPerSecond is the number of microseconds per second, used for RTF computation.
const usPerSecond = 1_000_000.0

// collectDatabase computes database latency and throughput metrics from
// atomic counter snapshots. Requires two consecutive snapshots for deltas.
func (c *Collector) collectDatabase(points map[string]float64, tick time.Time) {
	if c.dbCounters == nil {
		return
	}

	snap := c.dbCounters.Snapshot()
	// Use the shared tick timestamp for delta math and as the stored reference
	// for the next tick, rather than the snapshot's own time.Now().
	snap.CollectedAt = tick

	// Max values are reset-on-read from Snapshot(), always record them
	// (even on the first tick when prevDBSnap is nil)
	points[metricDBReadLatencyMax] = float64(snap.ReadMaxUs) / usToMs
	points[metricDBWriteLatencyMax] = float64(snap.WriteMaxUs) / usToMs

	if c.prevDBSnap != nil {
		elapsed := snap.CollectedAt.Sub(c.prevDBSnap.CollectedAt).Seconds()
		if elapsed > 0 {
			deltaReads := snap.ReadCount - c.prevDBSnap.ReadCount
			deltaWrites := snap.WriteCount - c.prevDBSnap.WriteCount

			if deltaReads > 0 {
				deltaUs := snap.ReadTotalUs - c.prevDBSnap.ReadTotalUs
				points[metricDBReadLatency] = float64(deltaUs) / float64(deltaReads) / usToMs
			}
			if deltaWrites > 0 {
				deltaUs := snap.WriteTotalUs - c.prevDBSnap.WriteTotalUs
				points[metricDBWriteLatency] = float64(deltaUs) / float64(deltaWrites) / usToMs
			}

			points[metricDBQueriesPerSec] = float64(deltaReads+deltaWrites) / elapsed
		}
	}

	c.prevDBSnap = &snap
}

// SetInferenceCounters sets the per-model inference counters for latency tracking.
// Must be called before Start. If not called, inference metrics are skipped.
func (c *Collector) SetInferenceCounters(counters *inferencestats.CounterMap) {
	c.inferenceCounters = counters
}

// SetModelClipFunc sets a function that returns each model's clip length in seconds.
// Used to compute the real-time factor (RTF = avg_inference_s / clip_s).
// Must be called before Start.
func (c *Collector) SetModelClipFunc(f func() map[string]float64) { c.modelClipFunc = f }

// SetModelRSSFunc sets a function that returns per-model host RSS deltas in bytes
// and the runtime baseline. Used to update the RSS Prometheus gauge each tick.
// Must be called before Start.
func (c *Collector) SetModelRSSFunc(f func() (map[string]int64, int64)) { c.modelRSSFunc = f }

// SetInferenceGaugeSetters injects the Prometheus gauge setter functions for RTF,
// RSS, and deletion. All are nil-safe; only non-nil functions are called.
// Must be called before Start.
func (c *Collector) SetInferenceGaugeSetters(rtf func(string, float64), rss func(string, int64), del func(string)) {
	c.rtfGauge = rtf
	c.rssGauge = rss
	c.inferenceGaugeDelete = del
}

// SetAudioGaugeSetters injects the Prometheus gauge setter functions for audio
// queue depth and dropped-chunks total. Both are nil-safe; only non-nil
// functions are called. Must be called before Start.
func (c *Collector) SetAudioGaugeSetters(queueDepth, droppedChunks func(string, float64)) {
	c.audioQueueDepthGauge = queueDepth
	c.audioDroppedChunksGauge = droppedChunks
}

// AudioRouterSnapshot holds cumulative counter values for a single audio source.
type AudioRouterSnapshot struct {
	SourceID string
	Drops    int64
	Errors   int64
	// QueueDepth is the instantaneous maximum inbox occupancy across all routes
	// for this source. It is a gauge (not a counter) and is updated on every
	// collection tick.
	QueueDepth int64
}

// StreamHealthSnapshot holds cumulative counter values for a single RTSP stream,
// keyed by the stable internal source ID (not the raw URL) to avoid leaking
// credentials into metric keys and to remain stable across URL changes.
type StreamHealthSnapshot struct {
	SourceID     string
	RestartCount int
}

// SetAudioRouter injects a function that provides cumulative audio counter snapshots.
// Must be called before Start.
func (c *Collector) SetAudioRouter(fn func() []AudioRouterSnapshot) {
	c.audioRouterFn = fn
}

// SetStreamHealth injects a function that provides cumulative stream counter snapshots.
// Must be called before Start.
func (c *Collector) SetStreamHealth(fn func() []StreamHealthSnapshot) {
	c.streamHealthFn = fn
}

// SetHealthStore sets the dedicated health metrics store for hourly bucket aggregation.
// Must be called before Start.
func (c *Collector) SetHealthStore(store *HealthMetricsStore) {
	c.healthStore = store
}

// SetHealthEvents sets the event ring buffer for recording individual health events.
// Must be called before Start.
func (c *Collector) SetHealthEvents(buf *HealthEventBuffer) {
	c.healthEvents = buf
}

func (c *Collector) collectInference(points map[string]float64, tick time.Time) {
	if c.inferenceCounters == nil {
		return
	}

	var clips map[string]float64
	if c.modelClipFunc != nil {
		clips = c.modelClipFunc()
	}

	snaps := c.inferenceCounters.SnapshotAll()
	// Stamp every snapshot with the shared tick timestamp so throughput deltas
	// (and the previous-snapshot references stored below) use one consistent,
	// test-controllable clock instead of each Snapshot's own time.Now().
	for modelID := range snaps {
		s := snaps[modelID]
		s.CollectedAt = tick
		snaps[modelID] = s
	}

	if c.prevInferenceSnaps == nil {
		c.prevInferenceSnaps = make(map[string]*inferencestats.Snapshot, len(snaps))
		for modelID := range snaps {
			snap := snaps[modelID]
			c.prevInferenceSnaps[modelID] = &snap
		}
		return
	}

	for modelID, snap := range snaps {
		key := inferenceMetricKey(modelID)
		prev, hasPrev := c.prevInferenceSnaps[modelID]

		if !hasPrev {
			s := snap
			c.prevInferenceSnaps[modelID] = &s
			continue
		}

		// avg_ms and rtf use the raw signed delta, exactly as before Phase 3.
		// A counter reset (negative delta) falls through to the else branch and
		// zeroes avg_ms, preserving the original Phase 1 behavior.
		deltaInvokes := snap.InvokeCount - prev.InvokeCount
		if deltaInvokes > 0 {
			deltaUs := snap.InvokeTotalUs - prev.InvokeTotalUs
			points[key] = float64(deltaUs) / float64(deltaInvokes) / usToMs

			if clips != nil {
				if clip, ok := clips[modelID]; ok && clip > 0 {
					intervalAvgSec := (float64(deltaUs) / float64(deltaInvokes)) / usPerSecond
					rtf := intervalAvgSec / clip
					points[inferencestats.RTFMetricKey(modelID)] = rtf
					if c.rtfGauge != nil {
						c.rtfGauge(modelID, rtf)
						if c.gaugeModels == nil {
							c.gaugeModels = make(map[string]struct{})
						}
						c.gaugeModels[modelID] = struct{}{}
					}
				}
			}
		} else {
			points[key] = 0
		}

		// Throughput and error_rate use reset-adjusted deltas so that a counter
		// reset (e.g. process restart) is treated as the absolute current value
		// rather than a negative spike. These locals are scoped to the new series
		// and do not affect avg_ms or rtf above.
		tpInvokes := deltaInvokes
		if tpInvokes < 0 {
			tpInvokes = snap.InvokeCount
		}
		tpErrors := snap.InvokeErrors - prev.InvokeErrors
		if tpErrors < 0 {
			tpErrors = snap.InvokeErrors
		}

		// Elapsed seconds between the two snapshots, used for throughput computation.
		elapsedSeconds := snap.CollectedAt.Sub(prev.CollectedAt).Seconds()

		// Throughput: invocations per second over the tick interval.
		if elapsedSeconds > 0 {
			points[inferencestats.ThroughputMetricKey(modelID)] = float64(tpInvokes) / elapsedSeconds
		} else {
			points[inferencestats.ThroughputMetricKey(modelID)] = 0
		}

		// Error rate: errors / (errors + invocations) over the tick interval, range [0, 1].
		total := tpErrors + tpInvokes
		if total > 0 {
			points[inferencestats.ErrorRateMetricKey(modelID)] = float64(tpErrors) / float64(total)
		} else {
			points[inferencestats.ErrorRateMetricKey(modelID)] = 0
		}

		s := snap
		c.prevInferenceSnaps[modelID] = &s
	}

	for modelID := range c.prevInferenceSnaps {
		if _, ok := snaps[modelID]; !ok {
			delete(c.prevInferenceSnaps, modelID)
		}
	}
}

// collectModelRSS sets the per-model RSS Prometheus gauge each tick. RSS is set
// shortly after load (the warm-up + measurement is deferred and run off o.mu,
// so it lands a moment after the model becomes visible) and stable until reload;
// setting it every tick is idempotent and handles model add/remove, and the tick
// cadence naturally picks up the value once the deferred warm-up records it. RSS
// is not written to the ring buffer in Phase 1.
func (c *Collector) collectModelRSS() {
	if c.modelRSSFunc == nil || c.rssGauge == nil {
		return
	}
	perModel, _ := c.modelRSSFunc()
	for id, bytes := range perModel {
		c.rssGauge(id, bytes)
		if c.gaugeModels == nil {
			c.gaugeModels = make(map[string]struct{})
		}
		c.gaugeModels[id] = struct{}{}
	}
}

// pruneInferenceGauges deletes gauge label values for models that are no longer
// loaded, preventing stale Prometheus series after unload/reload. The canonical
// loaded set is modelClipFunc() (all loaded models, independent of RSS
// availability). No-op until the clip func and delete callback are wired.
func (c *Collector) pruneInferenceGauges() {
	if c.modelClipFunc == nil || c.inferenceGaugeDelete == nil || len(c.gaugeModels) == 0 {
		return
	}
	loaded := c.modelClipFunc()
	for id := range c.gaugeModels {
		if _, ok := loaded[id]; !ok {
			c.inferenceGaugeDelete(id)
			delete(c.gaugeModels, id)
		}
	}
}

// logOnce logs a message for a metric category only on the first occurrence.
func (c *Collector) logOnce(category, format string, args ...any) {
	if c.loggedErrors[category] {
		return
	}
	c.loggedErrors[category] = true
	GetLogger().Warn(fmt.Sprintf(format, args...))
}

// sanitizeMountpoint converts a mountpoint path to a metric-safe name.
// Unix:    "/" -> "root", "/home" -> "home", "/mnt/data" -> "mnt-data"
// Windows: "C:\" -> "C", "C:\Users" -> "C-Users"
func sanitizeMountpoint(mount string) string {
	if mount == "/" {
		return "root"
	}
	// Remove leading slash, replace remaining slashes with dashes
	name := strings.TrimPrefix(mount, "/")
	// Handle Windows drive letters and backslashes
	name = strings.ReplaceAll(name, `\`, "-")
	name = strings.ReplaceAll(name, ":", "")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.TrimRight(name, "-")
	return name
}

// skipCollectorFSTypes contains virtual/pseudo filesystem types that should not be tracked.
var skipCollectorFSTypes = map[string]bool{
	"sysfs": true, "proc": true, "procfs": true, "devfs": true,
	"devtmpfs": true, "debugfs": true, "securityfs": true, "tmpfs": true,
	"ramfs": true, "overlay": true, "overlayfs": true, "fusectl": true,
	"devpts": true, "hugetlbfs": true, "mqueue": true, "cgroup": true,
	"cgroupfs": true, "pstore": true, "binfmt_misc": true, "bpf": true,
	"tracefs": true, "configfs": true, "autofs": true, "efivarfs": true,
}

// skipCollectorFS returns true for virtual/pseudo filesystem types that should not be tracked.
func skipCollectorFS(fstype string) bool {
	return skipCollectorFSTypes[fstype]
}

// collectAudio records the aggregate audio queue depth into the MetricsStore
// batch so it is available to the frontend sparkline series and the metrics
// history API. Only records when audioRouterFn is wired; no-ops otherwise.
func (c *Collector) collectAudio(points map[string]float64) {
	if c.audioRouterFn == nil {
		return
	}
	snaps := c.audioRouterFn()
	var sum int64
	for _, s := range snaps {
		sum += s.QueueDepth
	}
	points[MetricKeyAudioQueueDepthAggregate] = float64(sum)
}

// collectHealthCounters samples cumulative audio and stream counters,
// computes deltas from the previous snapshot, and records them into the
// dedicated HealthMetricsStore. Follows the same delta pattern as collectDiskIO.
func (c *Collector) collectHealthCounters(tick time.Time) {
	if c.healthStore == nil {
		return
	}
	c.collectAudioHealthCounters(tick)
	c.collectStreamHealthCounters(tick)
}

// collectAudioHealthCounters computes deltas for audio drops and overruns and
// updates Prometheus gauges. Queue depth is recorded into the MetricsStore
// batch by collectAudio (called from collect), not here.
func (c *Collector) collectAudioHealthCounters(now time.Time) {
	if c.audioRouterFn == nil {
		return
	}

	snaps := c.audioRouterFn()
	current := make(map[string]AudioRouterSnapshot, len(snaps))
	for _, s := range snaps {
		current[s.SourceID] = s
	}

	for id, cur := range current {
		prev, ok := c.prevAudioSnaps[id]
		if !ok {
			// First tick or new source: seed the store keys so health checks
			// show "Healthy" instead of "Skipped" even with zero drops.
			c.healthStore.RecordAt(MetricPrefixAudioDrops+id, 0, now)
			c.healthStore.RecordAt(MetricPrefixAudioOverruns+id, 0, now)
			// Results-queue detection drops are recorded by the analysis pipeline
			// (push), not by this collector, but they are keyed by the same source
			// IDs. Seed a zero here so ResultsQueueDropCheck reads "Healthy" from
			// startup instead of "Skipped", consistent with the audio drop checks.
			c.healthStore.RecordAt(MetricPrefixResultsQueueDrops+id, 0, now)
			// Prometheus audio gauges are intentionally NOT set on the seeding tick:
			// only the health-store keys are seeded. Gauges are set from the second
			// tick onward once a previous snapshot exists for delta computation.
			continue
		}
		c.recordHealthDelta(MetricPrefixAudioDrops+id, cur.Drops, prev.Drops, id, MetricTypeAudioDrops, now)
		c.recordHealthDelta(MetricPrefixAudioOverruns+id, cur.Errors, prev.Errors, id, MetricTypeAudioOverruns, now)

		// Update Prometheus gauges if wired.
		if c.audioQueueDepthGauge != nil {
			c.audioQueueDepthGauge(id, float64(cur.QueueDepth))
		}
		if c.audioDroppedChunksGauge != nil {
			c.audioDroppedChunksGauge(id, float64(cur.Drops))
		}
	}

	c.prevAudioSnaps = current
}

// collectStreamHealthCounters computes deltas for stream restart counts.
func (c *Collector) collectStreamHealthCounters(now time.Time) {
	if c.streamHealthFn == nil {
		return
	}

	snaps := c.streamHealthFn()
	current := make(map[string]StreamHealthSnapshot, len(snaps))
	for _, s := range snaps {
		current[s.SourceID] = s
	}

	for sourceID, cur := range current {
		prev, ok := c.prevStreamSnaps[sourceID]
		if !ok {
			c.healthStore.RecordAt(MetricPrefixStreamRestarts+sourceID, 0, now)
			continue
		}
		c.recordHealthDelta(MetricPrefixStreamRestarts+sourceID, int64(cur.RestartCount), int64(prev.RestartCount), sourceID, MetricTypeStreamRestarts, now)
	}

	c.prevStreamSnaps = current
}

// recordHealthDelta computes the delta between current and previous counter values
// and records it into the health store. Handles counter resets: if current < previous,
// treat current as the delta (fresh start from zero).
func (c *Collector) recordHealthDelta(key string, current, previous int64, source, metric string, now time.Time) {
	var delta int64
	if current >= previous {
		delta = current - previous
	} else {
		delta = current
	}

	if delta <= 0 {
		return
	}

	c.healthStore.RecordAt(key, delta, now)

	if c.healthEvents != nil {
		c.healthEvents.Add(HealthEvent{
			Time:   now,
			Source: source,
			Delta:  delta,
			Metric: metric,
		})
	}
}

// CPU temperature reading is provided by the shared ReadCPUTemperature reader
// in thermal.go, used by collectTemperature above.
