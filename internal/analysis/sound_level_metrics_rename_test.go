package analysis

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// metricNameLabelCounts gathers the registry and counts series per value of the name label.
func metricNameLabelCounts(t *testing.T, reg *prometheus.Registry) map[string]int {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	counts := map[string]int{}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			for _, l := range metric.GetLabel() {
				if l.GetName() == "name" {
					counts[l.GetValue()]++
				}
			}
		}
	}
	return counts
}

// TestUpdateSoundLevelMetrics_RenameDropsOldNameSeries pins that publishing the
// same source under a new name removes the series of the old name.
func TestUpdateSoundLevelMetrics_RenameDropsOldNameSeries(t *testing.T) {
	reg := prometheus.NewRegistry()
	sl, err := metrics.NewSoundLevelMetrics(reg)
	require.NoError(t, err)
	m := &observability.Metrics{SoundLevel: sl}

	publish := func(name string) {
		updateSoundLevelMetrics(soundlevel.SoundLevelData{
			Timestamp: time.Now(),
			Source:    "src-1",
			Name:      name,
			Duration:  10,
			OctaveBands: map[string]soundlevel.OctaveBandData{
				"1.0_kHz": {CenterFreq: 1000, Min: -50, Max: -30, Mean: -40, SampleCount: 10},
			},
		}, m)
	}

	publish("Old name")
	require.Positive(t, metricNameLabelCounts(t, reg)["Old name"])

	publish("New name")
	counts := metricNameLabelCounts(t, reg)
	assert.Zero(t, counts["Old name"])
	assert.Positive(t, counts["New name"])
}
