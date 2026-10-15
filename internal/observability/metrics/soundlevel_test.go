package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSourceID  = "rtsp_test"
	testBand      = "1.0_kHz"
	testAllocRuns = 100
	labelNameKey  = "name"
)

// recordSoundLevelSeries creates one series in every sound level vector that
// carries source and name labels.
func recordSoundLevelSeries(m *SoundLevelMetrics, source string) {
	m.UpdateSoundLevel(source, "overall", -40)
	m.RecordSoundLevelDuration(source, 10)
	m.UpdateOctaveBandLevel(source, testBand, -50, -30, -40)
	m.RecordSoundLevelProcessingDuration(source, "update_metrics", 0.001)
	m.RecordSoundLevelProcessingError(source, "test")
	m.RecordSoundLevelPublishing(source, "mqtt", "success")
	m.RecordSoundLevelPublishingError(source, "mqtt", "test")
}

func newTestSoundLevelMetrics(t *testing.T) (*SoundLevelMetrics, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	m, err := NewSoundLevelMetrics(reg)
	require.NoError(t, err)
	return m, reg
}

// TestRecord_EverySeriesIsLabelledWithSourceID pins that every recorder writes
// its series with the source id in both the source and the name label, so the
// label set stays unchanged for existing dashboards.
func TestRecord_EverySeriesIsLabelledWithSourceID(t *testing.T) {
	m, reg := newTestSoundLevelMetrics(t)

	recordSoundLevelSeries(m, testSourceID)

	families, err := reg.Gather()
	require.NoError(t, err)
	require.Len(t, families, 11, "one family per sound level vector")
	for _, family := range families {
		require.NotEmpty(t, family.GetMetric(), family.GetName())
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, l := range metric.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			assert.Equal(t, testSourceID, labels[labelSource], family.GetName())
			assert.Equal(t, testSourceID, labels[labelNameKey], family.GetName())
		}
	}
}

// TestRecord_RepeatedRecordingDoesNotAllocate pins that recording on an existing
// series allocates nothing.
func TestRecord_RepeatedRecordingDoesNotAllocate(t *testing.T) {
	m, _ := newTestSoundLevelMetrics(t)
	recordSoundLevelSeries(m, testSourceID)

	allocs := testing.AllocsPerRun(testAllocRuns, func() {
		recordSoundLevelSeries(m, testSourceID)
	})
	assert.Zero(t, allocs)
}
