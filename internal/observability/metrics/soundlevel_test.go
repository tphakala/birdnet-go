package metrics

import (
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSourceID    = "rtsp_test"
	testOtherSource = "rtsp_other"
	testNameA       = "Feeder"
	testNameB       = "Feeder renamed"
	testBand        = "1.0_kHz"
	testAllocRuns   = 100
)

// recordSoundLevelSeries creates one series in every sound level vector that
// carries source and name labels.
func recordSoundLevelSeries(m *SoundLevelMetrics, source, name string) {
	m.UpdateSoundLevel(source, name, "overall", -40)
	m.RecordSoundLevelDuration(source, name, 10)
	m.UpdateOctaveBandLevel(source, name, testBand, -50, -30, -40)
	m.RecordSoundLevelProcessingDuration(source, name, "update_metrics", 0.001)
	m.RecordSoundLevelProcessingError(source, name, "test")
	m.RecordSoundLevelPublishing(source, name, "mqtt", "success")
	m.RecordSoundLevelPublishingError(source, name, "mqtt", "test")
}

// seriesWithName returns how many gathered series carry the given source and name labels.
func seriesWithName(t *testing.T, reg *prometheus.Registry, source, name string) int {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	count := 0
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, l := range metric.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels[labelSource] == source && labels["name"] == name {
				count++
			}
		}
	}
	return count
}

func newTestSoundLevelMetrics(t *testing.T) (*SoundLevelMetrics, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	m, err := NewSoundLevelMetrics(reg)
	require.NoError(t, err)
	return m, reg
}

// TestTrackSourceName_RenameDropsOldNameSeries pins that when a source's name
// changes, its series under the old name are removed from every vector, series
// of other sources are kept, and the new name's series exist.
func TestTrackSourceName_RenameDropsOldNameSeries(t *testing.T) {
	m, reg := newTestSoundLevelMetrics(t)

	m.TrackSourceName(testSourceID, testNameA)
	recordSoundLevelSeries(m, testSourceID, testNameA)
	m.TrackSourceName(testOtherSource, testNameA)
	recordSoundLevelSeries(m, testOtherSource, testNameA)
	require.Positive(t, seriesWithName(t, reg, testSourceID, testNameA))

	m.TrackSourceName(testSourceID, testNameB)
	recordSoundLevelSeries(m, testSourceID, testNameB)

	assert.Zero(t, seriesWithName(t, reg, testSourceID, testNameA), "old name series must be gone")
	assert.Positive(t, seriesWithName(t, reg, testSourceID, testNameB), "new name series must exist")
	assert.Positive(t, seriesWithName(t, reg, testOtherSource, testNameA), "another source with the same name is untouched")
}

// TestTrackSourceName_SameNameKeepsSeriesAndDoesNotAllocate pins the steady
// state: an unchanged name deletes nothing and allocates nothing.
func TestTrackSourceName_SameNameKeepsSeriesAndDoesNotAllocate(t *testing.T) {
	m, reg := newTestSoundLevelMetrics(t)

	m.TrackSourceName(testSourceID, testNameA)
	recordSoundLevelSeries(m, testSourceID, testNameA)
	before := seriesWithName(t, reg, testSourceID, testNameA)
	require.Positive(t, before)

	allocs := testing.AllocsPerRun(testAllocRuns, func() { m.TrackSourceName(testSourceID, testNameA) })

	assert.Zero(t, allocs)
	assert.Equal(t, before, seriesWithName(t, reg, testSourceID, testNameA))
}

// TestRecord_StaleNameIsDropped pins that a recorder finishing an interval under
// a name the source no longer has cannot recreate the deleted series, for every
// recorder (the publishers, the measurement and the processing recorders).
func TestRecord_StaleNameIsDropped(t *testing.T) {
	m, reg := newTestSoundLevelMetrics(t)

	m.TrackSourceName(testSourceID, testNameA)
	recordSoundLevelSeries(m, testSourceID, testNameA)
	require.Positive(t, seriesWithName(t, reg, testSourceID, testNameA))

	m.TrackSourceName(testSourceID, testNameB)
	recordSoundLevelSeries(m, testSourceID, testNameA)

	assert.Zero(t, seriesWithName(t, reg, testSourceID, testNameA), "a stale write must not recreate the old series")

	recordSoundLevelSeries(m, testSourceID, testNameB)
	assert.Positive(t, seriesWithName(t, reg, testSourceID, testNameB), "the current name still records")
}

// TestRecord_UnseenSourceRecords pins that a source never passed to
// TrackSourceName records normally through every recorder.
func TestRecord_UnseenSourceRecords(t *testing.T) {
	m, reg := newTestSoundLevelMetrics(t)

	recordSoundLevelSeries(m, testSourceID, testNameA)

	assert.Positive(t, seriesWithName(t, reg, testSourceID, testNameA))
	m2, reg2 := newTestSoundLevelMetrics(t)
	m2.RecordSoundLevelPublishing(testSourceID, testNameA, "sse", "success")
	m2.RecordSoundLevelPublishingError(testSourceID, testNameA, "sse", "broadcast_error")
	assert.Equal(t, 2, seriesWithName(t, reg2, testSourceID, testNameA))
}

// TestRecord_CurrentNameDoesNotAllocate pins the steady state of the stale-name
// check on every recorder.
func TestRecord_CurrentNameDoesNotAllocate(t *testing.T) {
	m, _ := newTestSoundLevelMetrics(t)
	m.TrackSourceName(testSourceID, testNameA)
	recordSoundLevelSeries(m, testSourceID, testNameA)

	allocs := testing.AllocsPerRun(testAllocRuns, func() {
		recordSoundLevelSeries(m, testSourceID, testNameA)
	})
	assert.Zero(t, allocs)
}

const (
	stressRecorders  = 8
	stressRenames    = 200
	stressRecordsMin = 50
)

// TestRecord_ConcurrentRenameLeavesOnlyCurrentName is a stress test:
// recorders keep writing under the previous name while the source is renamed,
// and once they stop only the final name may have series. It cannot fail
// deterministically before the fix (the window between the name check and the
// write is a few instructions wide), so it relies on -race scheduling and many
// iterations.
func TestRecord_ConcurrentRenameLeavesOnlyCurrentName(t *testing.T) {
	for i := range stressRenames {
		m, reg := newTestSoundLevelMetrics(t)
		oldName, newName := testNameA, testNameB

		m.TrackSourceName(testSourceID, oldName)
		stop := make(chan struct{})
		var wg sync.WaitGroup
		started := make(chan struct{}, stressRecorders)
		for range stressRecorders {
			wg.Go(func() {
				started <- struct{}{}
				for {
					select {
					case <-stop:
						return
					default:
						recordSoundLevelSeries(m, testSourceID, oldName)
					}
				}
			})
		}
		for range stressRecorders {
			<-started
		}
		m.TrackSourceName(testSourceID, newName)
		close(stop)
		wg.Wait()

		require.Zero(t, seriesWithName(t, reg, testSourceID, oldName), "iteration %d: the old name's series were recreated", i)
	}
}
