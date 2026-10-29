package dbserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssessRaw(t *testing.T) {
	tests := []struct {
		raw    string
		vendor Vendor
		series string
		level  SupportLevel
	}{
		// Strings read from live servers.
		{"8.4.13", VendorMySQL, "8.4", SupportSupported},
		{"10.11.19-MariaDB-ubu2204", VendorMariaDB, "10.11", SupportSupported},
		{"11.4.13-MariaDB-ubu2404", VendorMariaDB, "11.4", SupportSupported},
		{"11.8.9-MariaDB-ubu2404", VendorMariaDB, "11.8", SupportSupported},
		// Handshake prefix, distro build and a series without a live sample.
		{"5.5.5-10.11.19-MariaDB-ubu2204", VendorMariaDB, "10.11", SupportSupported},
		{"10.11.6-MariaDB-1:10.11.6+maria~deb12", VendorMariaDB, "10.11", SupportSupported},
		{"12.3.3-MariaDB", VendorMariaDB, "12.3", SupportSupported},
		// Tabled MySQL releases: the 9.7 LTS is supported, the 9.0 to 9.6
		// Innovation run is listed as not supported (both ends of the run).
		{"9.7.4", VendorMySQL, "9.7", SupportSupported},
		{"9.0.1", VendorMySQL, "9.0", SupportUnsupported},
		{"9.1.0", VendorMySQL, "9.1", SupportUnsupported},
		{"9.2.0", VendorMySQL, "9.2", SupportUnsupported},
		{"9.3.0", VendorMySQL, "9.3", SupportUnsupported},
		{"9.4.0", VendorMySQL, "9.4", SupportUnsupported},
		{"9.5.0", VendorMySQL, "9.5", SupportUnsupported},
		{"9.6.1", VendorMySQL, "9.6", SupportUnsupported},
		// Untabled series below the vendor floor.
		{"8.0.46", VendorMySQL, "8.0", SupportUnsupported},
		{"8.0.46-log", VendorMySQL, "8.0", SupportUnsupported},
		{"5.7.44-log", VendorMySQL, "5.7", SupportUnsupported},
		{"10.6.28-MariaDB", VendorMariaDB, "10.6", SupportUnsupported},
		{"10.5.29-MariaDB", VendorMariaDB, "10.5", SupportUnsupported},
		{"10.7.8-MariaDB", VendorMariaDB, "10.7", SupportUnsupported},
		{"8.0.36-28", VendorMySQL, "8.0", SupportUnsupported},
		// Untabled series at or above the floor, including a two-digit major.
		{"9.8.0", VendorMySQL, "9.8", SupportUntracked},
		{"26.10.0", VendorMySQL, "26.10", SupportUntracked},
		{"13.0.2-MariaDB", VendorMariaDB, "13.0", SupportUntracked},
		{"11.5.2-MariaDB", VendorMariaDB, "11.5", SupportUntracked},
		// Unparseable.
		{"", "", "", SupportUnknown},
		{"MariaDB", "", "", SupportUnknown},
		{"8", "", "", SupportUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			a := AssessRaw(tt.raw)
			assert.Equal(t, tt.level, a.Level)
			assert.Equal(t, tt.level == SupportSupported, a.Supported())
			assert.Equal(t, tt.vendor, a.Version.Vendor)
			if tt.level != SupportUnknown {
				assert.Equal(t, tt.series, a.Version.Series())
			}
			assert.Equal(t, tt.raw, a.Version.Raw)
		})
	}
}

func TestParseVersion_Name(t *testing.T) {
	v, err := ParseVersion("10.11.19-MariaDB-ubu2204")
	require.NoError(t, err)
	assert.Equal(t, "MariaDB 10.11.19", v.Name())

	v, err = ParseVersion("8.4")
	require.NoError(t, err)
	assert.Equal(t, "MySQL 8.4", v.Name())

	_, err = ParseVersion("not a version")
	require.Error(t, err)
}

func TestServerTag_Values(t *testing.T) {
	tests := []struct {
		raw string
		tag string
	}{
		{"8.4.13", "mysql-8.4"},
		{"9.0.1", "mysql-9.0"},
		{"9.1.0", "mysql-9.1"},
		{"9.2.0", "mysql-9.2"},
		{"9.3.0", "mysql-9.3"},
		{"9.4.0", "mysql-9.4"},
		{"9.5.0", "mysql-9.5"},
		{"9.6.1", "mysql-9.6"},
		{"9.7.4", "mysql-9.7"},
		{"5.7.44-log", "mysql-unsupported"},
		{"8.0.46", "mysql-unsupported"},
		{"10.6.28-MariaDB", "mariadb-unsupported"},
		{"9.8.0", "mysql-untracked"},
		{"26.10.0", "mysql-untracked"},
		{"10.11.19-MariaDB", "mariadb-10.11"},
		{"11.4.13-MariaDB", "mariadb-11.4"},
		{"11.8.9-MariaDB", "mariadb-11.8"},
		{"12.3.3-MariaDB", "mariadb-12.3"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			a := AssessRaw(tt.raw)
			assert.Equal(t, tt.tag, ServerTag(&a))
		})
	}
}

func TestAssess_InnovationRowsAreTabledUnsupported(t *testing.T) {
	for minor := 0; minor <= 6; minor++ {
		a := Assess(Version{Vendor: VendorMySQL, Major: 9, Minor: minor})
		require.NotNil(t, a.Release, "9.%d must have a table row", minor)
		assert.False(t, a.Release.Supported)
		assert.Equal(t, SupportUnsupported, a.Level)
	}
	for _, raw := range []string{"5.7.44-log", "8.0.46", "10.6.28-MariaDB"} {
		a := AssessRaw(raw)
		assert.Nil(t, a.Release, "%s is unsupported through the floor rule, not a table row", raw)
		assert.Equal(t, SupportUnsupported, a.Level)
	}
}

func TestServerTag_ClosedSet(t *testing.T) {
	allowed := map[string]bool{
		"mysql-8.4": true, "mysql-9.0": true, "mysql-9.1": true, "mysql-9.2": true, "mysql-9.3": true,
		"mysql-9.4": true, "mysql-9.5": true, "mysql-9.6": true, "mysql-9.7": true,
		"mariadb-10.11": true, "mariadb-11.4": true, "mariadb-11.8": true, "mariadb-12.3": true,
		"mysql-unsupported": true, "mariadb-unsupported": true,
		"mysql-untracked": true, "mariadb-untracked": true,
		"unknown": true,
	}
	raws := []string{
		"8.4.13", "10.11.19-MariaDB-ubu2204", "11.4.13-MariaDB-ubu2404", "11.8.9-MariaDB-ubu2404",
		"5.5.5-10.11.19-MariaDB-ubu2204", "10.11.6-MariaDB-1:10.11.6+maria~deb12", "12.3.3-MariaDB",
		"8.0.46-log", "5.7.44-log", "10.6.28-MariaDB", "10.5.29-MariaDB", "10.7.8-MariaDB", "8.0.36-28",
		"9.0.1", "9.6.1", "9.7.4", "9.8.0", "26.10.0", "13.0.2-MariaDB", "11.5.2-MariaDB", "", "MariaDB", "8", "5.1.0-secret-host.example",
	}
	for _, raw := range raws {
		a := AssessRaw(raw)
		tag := ServerTag(&a)
		assert.True(t, allowed[tag], "tag %q for %q is outside the closed set", tag, raw)
	}
}

func TestSupportedReleases(t *testing.T) {
	assert.Equal(t, "MySQL 8.4, 9.7; MariaDB 10.11, 11.4, 11.8, 12.3", SupportedReleases())
}

func TestCharsetReport(t *testing.T) {
	tests := []struct {
		name       string
		report     CharsetReport
		nonUTF8MB4 []string
		all        bool
		every      bool
		tag        string
	}{
		{
			name:   "empty report",
			report: CharsetReport{},
			tag:    "unknown",
		},
		{
			name:   "all utf8mb4",
			report: CharsetReport{SchemaCharset: "utf8mb4", Tables: []TableCharsets{{"a", []string{"utf8mb4"}}}},
			all:    true,
			every:  true,
			tag:    "utf8mb4",
		},
		{
			name: "latin1 schema with only utf8mb4 tables",
			report: CharsetReport{SchemaCharset: "latin1", Tables: []TableCharsets{
				{"a", []string{"utf8mb4"}},
				{"b", []string{"utf8mb4"}},
			}},
			every: true,
			tag:   "utf8mb4_tables",
		},
		{
			name: "latin1 schema with mixed tables",
			report: CharsetReport{SchemaCharset: "latin1", Tables: []TableCharsets{
				{"inherit", []string{"latin1"}},
				{"fine", []string{"utf8mb4"}},
				{"mixed", []string{"latin1", "utf8mb4"}},
			}},
			nonUTF8MB4: []string{"inherit", "mixed"},
			tag:        "latin1",
		},
		{
			name:       "utf8mb4 schema with an old table",
			report:     CharsetReport{SchemaCharset: "utf8mb4", Tables: []TableCharsets{{"old", []string{"utf8mb3"}}}},
			nonUTF8MB4: []string{"old"},
			tag:        "mixed",
		},
		{
			name:   "other schema charset",
			report: CharsetReport{SchemaCharset: "cp1251"},
			tag:    "other",
		},
		{
			name:   "utf8mb3 schema",
			report: CharsetReport{SchemaCharset: "utf8mb3"},
			tag:    "utf8mb3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.nonUTF8MB4, tt.report.NonUTF8MB4Tables())
			assert.Equal(t, tt.all, tt.report.AllUTF8MB4())
			assert.Equal(t, tt.every, tt.report.EveryTableUTF8MB4())
			assert.Equal(t, tt.tag, CharsetTag(tt.report))
		})
	}
}

func TestCharsetCanHold(t *testing.T) {
	tests := []struct {
		target, source string
		want           bool
	}{
		{"utf8mb4", "utf8mb4", true},
		{"utf8mb4", "cp1251", true},
		{"latin1", "latin1", true},
		{"utf8mb3", "latin1", true},
		{"latin1", "ascii", true},
		{"latin1", "utf8mb4", false},
		{"utf8mb3", "utf8mb4", false},
		{"latin1", "utf8mb3", false},
		{"cp1251", "latin1", false},
		{"cp1251", "cp1251", true},
	}
	for _, tt := range tests {
		t.Run(tt.target+"_holds_"+tt.source, func(t *testing.T) {
			assert.Equal(t, tt.want, CharsetCanHold(tt.target, tt.source))
		})
	}
}
