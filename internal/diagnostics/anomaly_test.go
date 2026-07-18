package diagnostics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bootWith builds a BootRecord with the fields the differ inspects.
func bootWith(mod func(*BootRecord)) *BootRecord {
	rec := &BootRecord{
		RecordHeader: NewRecordHeader(RecordTypeBoot),
		App:          AppInfo{Version: "20260716"},
		Datastore: DatastoreSnapshot{
			Dialect:          dialectSQLite,
			ConfiguredPath:   "/data/birdnet.db",
			ResolvedAbsPath:  "/data/birdnet.db",
			ConfiguredExists: true,
			ConfiguredSize:   250 * 1024 * 1024,
			StartupDecision:  "v2_restart",
		},
		Mounts: []Mount{
			{Source: "/host/data", Destination: "/data", FSType: "ext4"},
			{Source: "/host/config", Destination: "/config", FSType: "ext4"},
		},
	}
	if mod != nil {
		mod(rec)
	}
	return rec
}

func kinds(anomalies []AnomalyRecord) []string {
	out := make([]string, 0, len(anomalies))
	for _, a := range anomalies {
		out = append(out, a.Kind)
	}
	return out
}

func TestDetectAnomaliesSteadyState(t *testing.T) {
	t.Parallel()
	prev := bootWith(nil)
	cur := bootWith(nil)
	assert.Empty(t, detectAnomalies(prev, cur), "identical boots produce no anomalies")
}

func TestDetectAnomaliesDBLost(t *testing.T) {
	t.Parallel()
	prev := bootWith(nil) // 250 MB DB present
	cur := bootWith(func(r *BootRecord) {
		r.Datastore.ConfiguredExists = false
		r.Datastore.ConfiguredSize = 0
		r.Datastore.StartupDecision = decisionFreshInstall
	})
	anomalies := detectAnomalies(prev, cur)
	require.Contains(t, kinds(anomalies), AnomalyDBLost)
	for _, a := range anomalies {
		if a.Kind == AnomalyDBLost {
			assert.Contains(t, a.Previous, "/data/birdnet.db")
			assert.Contains(t, a.Current, "fresh_install")
		}
	}
}

func TestDetectAnomaliesDBLostNotTriggeredForTrivialSize(t *testing.T) {
	t.Parallel()
	prev := bootWith(func(r *BootRecord) {
		r.Datastore.ConfiguredSize = dbLostMinSizeBytes - 1
	})
	cur := bootWith(func(r *BootRecord) {
		r.Datastore.ConfiguredExists = false
		r.Datastore.ConfiguredSize = 0
		r.Datastore.StartupDecision = decisionFreshInstall
	})
	assert.NotContains(t, kinds(detectAnomalies(prev, cur)), AnomalyDBLost,
		"a schema-only or near-empty previous DB must not raise db_lost")
}

func TestDetectAnomaliesDBLostNotTriggeredForMySQL(t *testing.T) {
	t.Parallel()
	prev := bootWith(func(r *BootRecord) { r.Datastore.Dialect = "mysql" })
	cur := bootWith(func(r *BootRecord) {
		r.Datastore.Dialect = "mysql"
		r.Datastore.ConfiguredExists = false
		r.Datastore.StartupDecision = decisionFreshInstall
	})
	assert.NotContains(t, kinds(detectAnomalies(prev, cur)), AnomalyDBLost,
		"db_lost is a file-level signal, sqlite only")
}

func TestDetectAnomaliesDBPathChanged(t *testing.T) {
	t.Parallel()
	prev := bootWith(nil)
	cur := bootWith(func(r *BootRecord) {
		r.Datastore.ResolvedAbsPath = "/other/birdnet.db"
	})
	assert.Contains(t, kinds(detectAnomalies(prev, cur)), AnomalyDBPathChanged)
}

func TestDetectAnomaliesMountChanged(t *testing.T) {
	t.Parallel()
	prev := bootWith(nil)
	cur := bootWith(func(r *BootRecord) {
		r.Mounts[0].Source = "/different/host/path"
	})
	assert.Contains(t, kinds(detectAnomalies(prev, cur)), AnomalyMountChanged)
}

func TestDetectAnomaliesMountAbsentIsNotAChange(t *testing.T) {
	t.Parallel()
	prev := bootWith(nil)
	cur := bootWith(func(r *BootRecord) { r.Mounts = nil })
	assert.NotContains(t, kinds(detectAnomalies(prev, cur)), AnomalyMountChanged,
		"missing mount data (non-container boot) is not a mount change")
}

func TestDetectAnomaliesVersionRollback(t *testing.T) {
	t.Parallel()
	prev := bootWith(func(r *BootRecord) { r.App.Version = "20260716" })
	cur := bootWith(func(r *BootRecord) { r.App.Version = "20260601" })
	assert.Contains(t, kinds(detectAnomalies(prev, cur)), AnomalyVersionRollback)
}

func TestDetectAnomaliesVersionUpgradeAndEqualNoAnomaly(t *testing.T) {
	t.Parallel()
	prev := bootWith(func(r *BootRecord) { r.App.Version = "20260601" })
	cur := bootWith(func(r *BootRecord) { r.App.Version = "20260716" })
	assert.NotContains(t, kinds(detectAnomalies(prev, cur)), AnomalyVersionRollback)

	cur.App.Version = "20260601"
	assert.NotContains(t, kinds(detectAnomalies(prev, cur)), AnomalyVersionRollback)
}

func TestCompareVersionDates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b string
		want int
	}{
		{"plain dates newer", "20260716", "20260601", 1},
		{"plain dates older", "20260601", "20260716", -1},
		{"equal", "20260716", "20260716", 0},
		{"nightly prefix", "nightly-20260615", "20260601", 1},
		{"nightly vs release same date", "nightly-20260716", "20260716", 0},
		{"dev build incomparable", "dev", "20260716", 0},
		{"empty incomparable", "", "20260716", 0},
		{"both non-date", "dev", "unknown", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, compareVersionDates(tt.a, tt.b))
		})
	}
}

func TestDetectAnomaliesDBLostAtExactThreshold(t *testing.T) {
	t.Parallel()
	// Pins the >= boundary: mutating the gate to > would let this survive.
	prev := bootWith(func(r *BootRecord) {
		r.Datastore.ConfiguredSize = dbLostMinSizeBytes
	})
	cur := bootWith(func(r *BootRecord) {
		r.Datastore.ConfiguredExists = false
		r.Datastore.ConfiguredSize = 0
		r.Datastore.StartupDecision = decisionFreshInstall
	})
	assert.Contains(t, kinds(detectAnomalies(prev, cur)), AnomalyDBLost,
		"db_lost fires at exactly dbLostMinSizeBytes (>= boundary)")
}

func TestDetectAnomaliesDBLostSuppressedWhenPathChanged(t *testing.T) {
	t.Parallel()
	// A deliberate path change is reported by db_path_changed, not db_lost.
	prev := bootWith(nil)
	cur := bootWith(func(r *BootRecord) {
		r.Datastore.ResolvedAbsPath = "/newlocation/birdnet.db"
		r.Datastore.ConfiguredExists = false
		r.Datastore.StartupDecision = decisionFreshInstall
	})
	got := kinds(detectAnomalies(prev, cur))
	assert.NotContains(t, got, AnomalyDBLost, "path change must not raise db_lost")
	assert.Contains(t, got, AnomalyDBPathChanged)
}
