package dbserver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testNow is a fixed clock for the assessment tests.
var testNow = time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

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
		// Tabled end of life and untabled series below the vendor floor.
		{"8.0.46", VendorMySQL, "8.0", SupportEndOfLife},
		{"8.0.46-log", VendorMySQL, "8.0", SupportEndOfLife},
		{"5.7.44-log", VendorMySQL, "5.7", SupportEndOfLife},
		{"10.6.28-MariaDB", VendorMariaDB, "10.6", SupportEndOfLife},
		{"10.5.29-MariaDB", VendorMariaDB, "10.5", SupportEndOfLife},
		{"10.7.8-MariaDB", VendorMariaDB, "10.7", SupportEndOfLife},
		{"8.0.36-28", VendorMySQL, "8.0", SupportEndOfLife},
		// Untabled series at or above the floor, including a two-digit major.
		{"9.7.4", VendorMySQL, "9.7", SupportUntracked},
		{"9.6.1", VendorMySQL, "9.6", SupportUntracked},
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
			a := AssessRaw(tt.raw, testNow)
			assert.Equal(t, tt.level, a.Level)
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

func TestAssess_EndOfLifeBoundaries(t *testing.T) {
	v, err := ParseVersion("10.11.19-MariaDB")
	require.NoError(t, err)
	eol := Assess(v, testNow).Release.EOL

	day := 24 * time.Hour
	tests := []struct {
		name  string
		now   time.Time
		level SupportLevel
	}{
		{"91 days before", eol.Add(-91 * day), SupportSupported},
		{"90 days before", eol.Add(-90 * day), SupportEndingSoon},
		{"1 day before", eol.Add(-day), SupportEndingSoon},
		{"on the date", eol, SupportEndOfLife},
		{"after", eol.Add(day), SupportEndOfLife},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := Assess(v, tt.now)
			assert.Equal(t, tt.level, a.Level)
			assert.Equal(t, tt.level == SupportSupported || tt.level == SupportEndingSoon, a.Supported())
		})
	}
}

func TestServerTag_ClosedSet(t *testing.T) {
	allowed := map[string]bool{
		"mysql-8.4": true, "mysql-8.0": true, "mysql-5.7": true,
		"mariadb-10.11": true, "mariadb-11.4": true, "mariadb-11.8": true, "mariadb-12.3": true, "mariadb-10.6": true,
		"mysql-end_of_life": true, "mariadb-end_of_life": true,
		"mysql-untracked": true, "mariadb-untracked": true,
		"unknown": true,
	}
	raws := []string{
		"8.4.13", "10.11.19-MariaDB-ubu2204", "11.4.13-MariaDB-ubu2404", "11.8.9-MariaDB-ubu2404",
		"5.5.5-10.11.19-MariaDB-ubu2204", "10.11.6-MariaDB-1:10.11.6+maria~deb12", "12.3.3-MariaDB",
		"8.0.46-log", "5.7.44-log", "10.6.28-MariaDB", "10.5.29-MariaDB", "10.7.8-MariaDB", "8.0.36-28",
		"9.7.4", "26.10.0", "13.0.2-MariaDB", "11.5.2-MariaDB", "", "MariaDB", "8", "5.1.0-secret-host.example",
	}
	for _, raw := range raws {
		a := AssessRaw(raw, testNow)
		tag := ServerTag(&a)
		assert.True(t, allowed[tag], "tag %q for %q is outside the closed set", tag, raw)
	}
}

func TestSupportedReleases(t *testing.T) {
	assert.Equal(t, "MySQL 8.4; MariaDB 10.11, 11.4, 11.8, 12.3", SupportedReleases())
}

func TestCharsetReport(t *testing.T) {
	tests := []struct {
		name       string
		report     CharsetReport
		nonUTF8MB4 []string
		all        bool
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
			tag:    "utf8mb4",
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
