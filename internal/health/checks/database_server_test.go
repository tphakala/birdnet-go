package checks

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore/dbserver"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/health"
)

func TestDatabaseServerVersionCheck_Status(t *testing.T) {
	tests := []struct {
		name       string
		probe      DatabaseServerProbe
		status     health.Status
		contains   string
		wantDetail string
	}{
		{"supported mysql 8.4", versionProbe("8.4.13"), health.StatusHealthy, "is a supported release", "supported"},
		{"supported mysql 9.7", versionProbe("9.7.4"), health.StatusHealthy, "is a supported release", "supported"},
		{"supported mariadb 10.11", versionProbe("10.11.19-MariaDB-ubu2204"), health.StatusHealthy, "is a supported release", "supported"},
		{"supported mariadb 11.4", versionProbe("11.4.13-MariaDB"), health.StatusHealthy, "is a supported release", "supported"},
		{"supported mariadb 11.8", versionProbe("11.8.9-MariaDB"), health.StatusHealthy, "is a supported release", "supported"},
		{"supported mariadb 12.3", versionProbe("12.3.3-MariaDB"), health.StatusHealthy, "is a supported release", "supported"},
		{"innovation 9.0", versionProbe("9.0.1"), health.StatusWarning, "is not a supported release", "unsupported"},
		{"innovation 9.1", versionProbe("9.1.0"), health.StatusWarning, "is not a supported release", "unsupported"},
		{"innovation 9.2", versionProbe("9.2.0"), health.StatusWarning, "is not a supported release", "unsupported"},
		{"innovation 9.3", versionProbe("9.3.0"), health.StatusWarning, "is not a supported release", "unsupported"},
		{"innovation 9.4", versionProbe("9.4.0"), health.StatusWarning, "is not a supported release", "unsupported"},
		{"innovation 9.5", versionProbe("9.5.0"), health.StatusWarning, "is not a supported release", "unsupported"},
		{"innovation 9.6", versionProbe("9.6.1"), health.StatusWarning, "is not a supported release", "unsupported"},
		{"below the floor mysql 5.7", versionProbe("5.7.44-log"), health.StatusWarning, "is not a supported release", "unsupported"},
		{"below the floor mysql 8.0", versionProbe("8.0.46"), health.StatusWarning, "is not a supported release", "unsupported"},
		{"below the floor mariadb 10.6", versionProbe("10.6.28-MariaDB"), health.StatusWarning, "is not a supported release", "unsupported"},
		{"untracked mysql 9.8", versionProbe("9.8.0"), health.StatusWarning, "not on the list", "untracked"},
		{"untracked mysql 26.10", versionProbe("26.10.0"), health.StatusWarning, "not on the list", "untracked"},
		{"unparseable", versionProbe("garbage"), health.StatusWarning, "not recognized", "unknown"},
		{"probe error", func(context.Context) (string, bool, error) {
			return "", true, errors.NewStd("connection refused")
		}, health.StatusUnknown, "connection refused", ""},
		{"not applicable", func(context.Context) (string, bool, error) { return "", false, nil }, health.StatusSkipped, "Not applicable", ""},
		{"nil probe", nil, health.StatusSkipped, "Not applicable", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := NewDatabaseServerVersionCheck(tt.probe)
			assert.Equal(t, "database_server_version", check.Name())
			assert.Equal(t, health.CategoryDatabase, check.Category())
			res := check.Run(t.Context())
			assert.Equal(t, tt.status, res.Status)
			assert.Contains(t, res.Message, tt.contains)
			assert.NotRegexp(t, `\d{4}-\d{2}-\d{2}`, res.Message)
			assert.NotContains(t, strings.ToLower(res.Message), "end of life")
			assert.NotContains(t, res.Details, "eol_date")
			if tt.wantDetail != "" {
				assert.Equal(t, tt.wantDetail, res.Details["support"])
				assert.Equal(t, tt.wantDetail == "supported", res.Details["supported"])
			}
		})
	}
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
		{"latin1 schema with only utf8mb4 tables", charsetProbe(dbserver.CharsetReport{
			SchemaCharset: "latin1", Tables: []dbserver.TableCharsets{
				{Name: "a", Charsets: []string{"utf8mb4"}}, {Name: "b", Charsets: []string{"utf8mb4"}},
			},
		}), health.StatusHealthy, []string{"All existing character columns use utf8mb4", "latin1"}, "rejected"},
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
