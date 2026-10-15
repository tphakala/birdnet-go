package analysis

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestSoundLevelBridge_RenameDropsOldNameSeries pins that the pipeline records
// each interval's name before any publisher sees it, so a rename removes the
// series of the old name and a late write under it is dropped.
func TestSoundLevelBridge_RenameDropsOldNameSeries(t *testing.T) {
	p, engine, src := newNameTestPipeline(t)
	reg := prometheus.NewRegistry()
	sl, err := metrics.NewSoundLevelMetrics(reg)
	require.NoError(t, err)
	p.apiService = &APIServerService{metrics: &observability.Metrics{SoundLevel: sl}}
	p.registerSoundLevelConsumers([]string{src.ID}, "unit_test_metrics_rename")

	first := publishOneSecond(t, p, engine, src.ID)
	sl.RecordSoundLevelPublishing(first.Source, first.Name, "mqtt", "success")
	require.Positive(t, metricNameLabelCounts(t, reg)[first.Name])

	require.True(t, engine.Registry().UpdateDisplayName(src.ID, nameTestRenamed))
	second := publishOneSecond(t, p, engine, src.ID)
	require.Equal(t, nameTestRenamed, second.Name)

	// A publisher that was still working on the first interval finishes now.
	sl.RecordSoundLevelPublishing(first.Source, first.Name, "mqtt", "success")
	sl.RecordSoundLevelPublishing(second.Source, second.Name, "mqtt", "success")

	counts := metricNameLabelCounts(t, reg)
	assert.Zero(t, counts[first.Name])
	assert.Positive(t, counts[nameTestRenamed])
}
