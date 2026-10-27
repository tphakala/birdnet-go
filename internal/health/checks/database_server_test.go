package checks

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore/dbserver"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/health"
)

var databaseServerTestNow = time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

func TestDatabaseServerVersionCheck_Status(t *testing.T) {
	tests := []struct {
		name       string
		probe      DatabaseServerProbe
		status     health.Status
		contains   string
		wantDetail string
	}{
		{"supported", versionProbe("10.11.19-MariaDB-ubu2204"), health.StatusHealthy, "supported until 2028-02-16", "supported"},
		{"tabled end of life", versionProbe("10.6.28-MariaDB"), health.StatusWarning, "past its upstream end of life", "end_of_life"},
		{"untabled end of life", versionProbe("10.5.29-MariaDB"), health.StatusWarning, "older than the oldest supported", "end_of_life"},
		{"untracked", versionProbe("9.7.4"), health.StatusWarning, "not on the list", "untracked"},
		{"unparseable", versionProbe("garbage"), health.StatusWarning, "not recognized", "unknown"},
		{"probe error", func(context.Context) (string, bool, error) {
			return "", true, errors.NewStd("connection refused")
		}, health.StatusUnknown, "connection refused", ""},
		{"not applicable", func(context.Context) (string, bool, error) { return "", false, nil }, health.StatusSkipped, "Not applicable", ""},
		{"nil probe", nil, health.StatusSkipped, "Not applicable", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := NewDatabaseServerVersionCheck(tt.probe, func() time.Time { return databaseServerTestNow })
			assert.Equal(t, "database_server_version", check.Name())
			assert.Equal(t, health.CategoryDatabase, check.Category())
			res := check.Run(t.Context())
			assert.Equal(t, tt.status, res.Status)
			assert.Contains(t, res.Message, tt.contains)
			if tt.wantDetail != "" {
				assert.Equal(t, tt.wantDetail, res.Details["support"])
			}
		})
	}
}

func TestDatabaseServerVersionCheck_EndingSoon(t *testing.T) {
	// MariaDB 10.11 ends 2028-02-16; 30 days before is inside the 90 day window.
	now := time.Date(2028, time.January, 17, 0, 0, 0, 0, time.UTC)
	check := NewDatabaseServerVersionCheck(versionProbe("10.11.19-MariaDB"), func() time.Time { return now })
	res := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, res.Status)
	assert.Contains(t, res.Message, "reaches its upstream end of life on 2028-02-16")
	assert.Equal(t, "2028-02-16", res.Details["eol_date"])
	assert.Equal(t, true, res.Details["supported"])
}

func TestDatabaseCharsetCheck_Status(t *testing.T) {
	manyTables := make([]dbserver.TableCharsets, 0, 12)
	for i := range 12 {
		manyTables = append(manyTables, dbserver.TableCharsets{Name: fmt.Sprintf("t%02d", i), Charsets: []string{"latin1"}})
	}

	tests := []struct {
		name     string
		probe    DatabaseCharsetProbe
		status   health.Status
		contains []string
		absent   string
	}{
		{"all utf8mb4", charsetProbe(dbserver.CharsetReport{
			SchemaCharset: "utf8mb4", Tables: []dbserver.TableCharsets{{Name: "a", Charsets: []string{"utf8mb4"}}},
		}), health.StatusHealthy, []string{"utf8mb4"}, ""},
		{"latin1 schema with 12 tables", charsetProbe(dbserver.CharsetReport{
			SchemaCharset: "latin1", Tables: manyTables,
		}), health.StatusWarning, []string{"latin1", "12 table(s)", "t09", "and 2 more"}, "t10"},
		{"no schema row", charsetProbe(dbserver.CharsetReport{}), health.StatusUnknown, []string{"could not be determined"}, ""},
		{"probe error", func(context.Context) (dbserver.CharsetReport, bool, error) {
			return dbserver.CharsetReport{}, true, errors.NewStd("denied")
		}, health.StatusUnknown, []string{"denied"}, ""},
		{"not applicable", func(context.Context) (dbserver.CharsetReport, bool, error) {
			return dbserver.CharsetReport{}, false, nil
		}, health.StatusSkipped, []string{"Not applicable"}, ""},
		{"nil probe", nil, health.StatusSkipped, []string{"Not applicable"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := NewDatabaseCharsetCheck(tt.probe)
			assert.Equal(t, "database_charset", check.Name())
			assert.Equal(t, health.CategoryDatabase, check.Category())
			res := check.Run(t.Context())
			assert.Equal(t, tt.status, res.Status)
			for _, c := range tt.contains {
				assert.Contains(t, res.Message, c)
			}
			if tt.absent != "" {
				assert.NotContains(t, res.Message, tt.absent)
			}
		})
	}

	res := NewDatabaseCharsetCheck(charsetProbe(dbserver.CharsetReport{SchemaCharset: "latin1", Tables: manyTables})).Run(t.Context())
	named, ok := res.Details["non_utf8mb4_tables"].([]string)
	require.True(t, ok)
	assert.Len(t, named, maxNamedCharsetTables)
	assert.Equal(t, 12, res.Details["non_utf8mb4_count"])
}

func versionProbe(raw string) DatabaseServerProbe {
	return func(context.Context) (string, bool, error) { return raw, true, nil }
}

func charsetProbe(r dbserver.CharsetReport) DatabaseCharsetProbe {
	return func(context.Context) (dbserver.CharsetReport, bool, error) { return r, true, nil }
}
