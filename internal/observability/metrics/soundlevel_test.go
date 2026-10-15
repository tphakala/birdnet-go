package metrics

import (
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
