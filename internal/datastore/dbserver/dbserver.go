// Package dbserver identifies the MySQL or MariaDB server behind a connection,
// classifies its release against the supported list, and reads the
// character sets and the size of the schema the application uses.
//
// The release table in this package and the supported list in
// internal/datastore/AGENTS.md ("HARD RULE: support only the MySQL and MariaDB
// LTS releases we test") change together: when a release joins or leaves the
// list, update both. The package never blocks anything by itself; callers
// decide what to do with an Assessment.
//
// It is a leaf package (standard library, gorm and internal/errors only) so
// the health checks, the API handlers, the startup code and the support dump
// can share one parser and one table.
package dbserver

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Vendor identifies the database server product.
type Vendor string

const (
	// VendorMySQL is MySQL and every build treated as MySQL (Percona, distro builds).
	VendorMySQL Vendor = "mysql"
	// VendorMariaDB is MariaDB.
	VendorMariaDB Vendor = "mariadb"
)

// Display returns the vendor's product name for messages.
func (v Vendor) Display() string {
	if v == VendorMariaDB {
		return "MariaDB"
	}
	return "MySQL"
}

const (
	// componentName is the error component of this package.
	componentName = "datastore.dbserver"

	// mariaDBMarker is the case-insensitive substring that marks a MariaDB
	// version string.
	mariaDBMarker = "mariadb"
	// mariaDBHandshakePrefix is the replication-compatibility prefix MariaDB
	// sends in the protocol handshake. SELECT VERSION() does not return it; it
	// is stripped as insurance for a string that came from the driver.
	mariaDBHandshakePrefix = "5.5.5-"

	// versionMatchMinParts is the least FindStringSubmatch returns for a match
	// of versionPattern (the full match, major and minor); versionMatchPatchIdx
	// is the index of the optional patch group.
	versionMatchMinParts = 3
	versionMatchPatchIdx = 3
)

// versionPattern matches the leading major.minor[.patch] of a version string.
var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)(?:\.(\d+))?`)

// Version is a parsed server version.
type Version struct {
	Vendor Vendor
	Major  int
	Minor  int
	Patch  int
	// Raw is the string the server reported, trimmed.
	Raw string

	hasPatch bool
}

// Series returns the release series, for example "10.11".
func (v Version) Series() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// Name returns a display name built from the parsed parts, for example
// "MariaDB 10.11.19". The raw string is not used, so distro suffixes do not
// reach messages.
func (v Version) Name() string {
	if v.hasPatch {
		return fmt.Sprintf("%s %s.%d", v.Vendor.Display(), v.Series(), v.Patch)
	}
	return fmt.Sprintf("%s %s", v.Vendor.Display(), v.Series())
}

// ParseVersion parses the string returned by SELECT VERSION(). A string that
// contains "mariadb" in any case is MariaDB; everything else is MySQL. It
// fails when the string has no leading major.minor.
func ParseVersion(raw string) (Version, error) {
	trimmed := strings.TrimSpace(raw)
	vendor := VendorMySQL
	if strings.Contains(strings.ToLower(trimmed), mariaDBMarker) {
		vendor = VendorMariaDB
		trimmed = strings.TrimPrefix(trimmed, mariaDBHandshakePrefix)
	}

	m := versionPattern.FindStringSubmatch(trimmed)
	if len(m) < versionMatchMinParts {
		return Version{}, errors.Newf("unrecognized database server version string").
			Component(componentName).
			Category(errors.CategoryValidation).
			Context("operation", "parse_version").
			Build()
	}

	major, errMajor := strconv.Atoi(m[1])
	minor, errMinor := strconv.Atoi(m[2])
	if errMajor != nil || errMinor != nil {
		return Version{}, errors.Newf("database server version number out of range").
			Component(componentName).
			Category(errors.CategoryValidation).
			Context("operation", "parse_version").
			Build()
	}
	v := Version{Vendor: vendor, Major: major, Minor: minor, Raw: strings.TrimSpace(raw)}
	if m[versionMatchPatchIdx] != "" {
		if patch, err := strconv.Atoi(m[versionMatchPatchIdx]); err == nil {
			v.Patch = patch
			v.hasPatch = true
		}
	}
	return v, nil
}

// Release is one entry of the release table.
type Release struct {
	Vendor Vendor
	Major  int
	Minor  int
	// Supported reports that BirdNET-Go supports the series. An unsupported
	// entry names a known series above the vendor floor that is not supported
	// (the MySQL 9.0 to 9.6 Innovation releases); series below the floor need
	// no entry.
	Supported bool
}

// Series returns the release series, for example "8.4".
func (r Release) Series() string {
	return fmt.Sprintf("%d.%d", r.Major, r.Minor)
}

// releases is the release table, ascending per vendor, with no dates.
// Supported entries are the tested list in internal/datastore/AGENTS.md. The
// vendor floor (the first supported entry of a vendor) makes every older series
// unsupported, so only unsupported series above the floor are listed.
var releases = []Release{
	{Vendor: VendorMySQL, Major: 8, Minor: 4, Supported: true},
	{Vendor: VendorMySQL, Major: 9, Minor: 0},
	{Vendor: VendorMySQL, Major: 9, Minor: 1},
	{Vendor: VendorMySQL, Major: 9, Minor: 2},
	{Vendor: VendorMySQL, Major: 9, Minor: 3},
	{Vendor: VendorMySQL, Major: 9, Minor: 4},
	{Vendor: VendorMySQL, Major: 9, Minor: 5},
	{Vendor: VendorMySQL, Major: 9, Minor: 6},
	{Vendor: VendorMySQL, Major: 9, Minor: 7, Supported: true},

	{Vendor: VendorMariaDB, Major: 10, Minor: 11, Supported: true},
	{Vendor: VendorMariaDB, Major: 11, Minor: 4, Supported: true},
	{Vendor: VendorMariaDB, Major: 11, Minor: 8, Supported: true},
	{Vendor: VendorMariaDB, Major: 12, Minor: 3, Supported: true},
}

// SupportLevel is the outcome of assessing a server version.
type SupportLevel string

const (
	// SupportSupported is a tabled release that BirdNET-Go supports.
	SupportSupported SupportLevel = "supported"
	// SupportUnsupported is a tabled release marked as not supported, or an
	// untabled release older than the oldest supported one.
	SupportUnsupported SupportLevel = "unsupported"
	// SupportUntracked is an untabled release at or above the oldest supported
	// one: a newer series the table does not list yet (a new LTS or Innovation
	// release, or a MariaDB rolling release). It only produces a note.
	SupportUntracked SupportLevel = "untracked"
	// SupportUnknown is a version string that could not be parsed.
	SupportUnknown SupportLevel = "unknown"
)

// Assessment is the classification of one server version.
type Assessment struct {
	Level   SupportLevel
	Version Version
	// Release is the matching table entry, nil for an untabled series.
	Release *Release
}

// Supported reports that BirdNET-Go supports the release.
func (a *Assessment) Supported() bool {
	return a.Level == SupportSupported
}

// Assess classifies v. A tabled series is supported or not as its entry says;
// an untabled series below the vendor floor (the lowest supported entry) is
// unsupported; an untabled series at or above the floor is untracked.
func Assess(v Version) Assessment {
	for i := range releases {
		r := &releases[i]
		if r.Vendor != v.Vendor || r.Major != v.Major || r.Minor != v.Minor {
			continue
		}
		a := Assessment{Version: v, Release: r, Level: SupportUnsupported}
		if r.Supported {
			a.Level = SupportSupported
		}
		return a
	}

	floor, ok := vendorFloor(v.Vendor)
	if ok && (v.Major < floor.Major || (v.Major == floor.Major && v.Minor < floor.Minor)) {
		return Assessment{Level: SupportUnsupported, Version: v}
	}
	return Assessment{Level: SupportUntracked, Version: v}
}

// AssessRaw parses raw and assesses it. An unparseable string yields
// SupportUnknown with only Version.Raw set.
func AssessRaw(raw string) Assessment {
	v, err := ParseVersion(raw)
	if err != nil {
		return Assessment{Level: SupportUnknown, Version: Version{Raw: strings.TrimSpace(raw)}}
	}
	return Assess(v)
}

// vendorFloor returns the lowest supported release of a vendor.
func vendorFloor(vendor Vendor) (Release, bool) {
	for _, r := range releases {
		if r.Vendor == vendor && r.Supported {
			return r, true
		}
	}
	return Release{}, false
}

// SupportedReleases returns the supported list built from the table, for
// example "MySQL 8.4, 9.7; MariaDB 10.11, 11.4, 11.8, 12.3".
func SupportedReleases() string {
	vendors := []Vendor{VendorMySQL, VendorMariaDB}
	parts := make([]string, 0, len(vendors))
	for _, vendor := range vendors {
		var series []string
		for _, r := range releases {
			if r.Vendor == vendor && r.Supported {
				series = append(series, r.Series())
			}
		}
		if len(series) > 0 {
			parts = append(parts, vendor.Display()+" "+strings.Join(series, ", "))
		}
	}
	return strings.Join(parts, "; ")
}

// Tag values that are not derived from the table.
const (
	tagUnknown     = "unknown"
	tagUnsupported = "unsupported"
	tagUntracked   = "untracked"
	tagOther       = "other"
	tagMixed       = "mixed"
	tagUTF8MB4Tbls = "utf8mb4_tables"
	tagSeparator   = "-"
	charsetASCII   = "ascii"
	charsetLatin1  = "latin1"
	charsetUTF8MB3 = "utf8mb3"
)

// ServerTag returns a bounded telemetry value for a: "<vendor>-<series>" for a
// tabled release, "<vendor>-unsupported" or "<vendor>-untracked" otherwise,
// and "unknown" when the version could not be parsed. The raw version string
// never appears in it.
func ServerTag(a *Assessment) string {
	if a.Level == SupportUnknown {
		return tagUnknown
	}
	vendor := string(a.Version.Vendor)
	if a.Release != nil {
		return vendor + tagSeparator + a.Release.Series()
	}
	if a.Level == SupportUnsupported {
		return vendor + tagSeparator + tagUnsupported
	}
	return vendor + tagSeparator + tagUntracked
}

// CharsetUTF8MB4 is the character set the application needs for all text.
const CharsetUTF8MB4 = "utf8mb4"

// CharsetReport holds the character sets of the current schema.
type CharsetReport struct {
	// SchemaCharset and SchemaCollation are the schema defaults, which tables
	// created without an explicit character set inherit.
	SchemaCharset   string
	SchemaCollation string
	// Tables lists base tables that have text columns, in name order.
	Tables []TableCharsets
}

// TableCharsets lists the distinct character sets of one table's text columns.
type TableCharsets struct {
	Name     string
	Charsets []string
}

// NonUTF8MB4Tables returns the names of tables with a text column outside utf8mb4.
func (r CharsetReport) NonUTF8MB4Tables() []string {
	var names []string
	for _, t := range r.Tables {
		if slices.ContainsFunc(t.Charsets, func(cs string) bool { return cs != CharsetUTF8MB4 }) {
			names = append(names, t.Name)
		}
	}
	return names
}

// AllUTF8MB4 reports that the schema default and every table are utf8mb4.
func (r CharsetReport) AllUTF8MB4() bool {
	return r.SchemaCharset == CharsetUTF8MB4 && len(r.NonUTF8MB4Tables()) == 0
}

// EveryTableUTF8MB4 reports that the schema has at least one table with text
// columns and every such table is utf8mb4, whatever the schema default is. The
// check covers existing character columns only; the schema default can still
// be inherited by tables created later without an explicit character set.
func (r CharsetReport) EveryTableUTF8MB4() bool {
	return len(r.Tables) > 0 && len(r.NonUTF8MB4Tables()) == 0
}

// CharsetTag returns a bounded telemetry value for r: "utf8mb4" when
// everything is utf8mb4, "utf8mb4_tables" when every table is utf8mb4 but the
// schema default is not, "mixed" when the schema default is utf8mb4 but a table
// is not, otherwise the schema default when it is "utf8mb3" or "latin1" (some
// table is not utf8mb4 or no table has text columns), "other" for any other
// schema default, and "unknown" when no schema default was read.
func CharsetTag(r CharsetReport) string {
	switch {
	case r.SchemaCharset == "":
		return tagUnknown
	case r.AllUTF8MB4():
		return CharsetUTF8MB4
	case r.EveryTableUTF8MB4():
		return tagUTF8MB4Tbls
	case r.SchemaCharset == CharsetUTF8MB4:
		// The schema default is fine; some existing table is not.
		return tagMixed
	case r.SchemaCharset == charsetUTF8MB3, r.SchemaCharset == charsetLatin1:
		return r.SchemaCharset
	default:
		return tagOther
	}
}

// charsetRank orders the character sets whose repertoires nest: every ascii
// character is in latin1, and every latin1 character is in utf8mb3.
var charsetRank = map[string]int{
	charsetASCII:   1,
	charsetLatin1:  2,
	charsetUTF8MB3: 3,
}

// CharsetCanHold reports whether a column in character set target can store
// every character of a column in character set source. It is true when the two
// are equal, when target is utf8mb4, or when both are in the nested order
// ascii < latin1 < utf8mb3 and source ranks at or below target. Any other pair
// is false, including a pair with a character set outside that order.
// Collations are not considered.
func CharsetCanHold(target, source string) bool {
	if target == source || target == CharsetUTF8MB4 {
		return true
	}
	t, tok := charsetRank[target]
	s, sok := charsetRank[source]
	return tok && sok && s <= t
}

// TagUnknown is the telemetry value for a probe that failed.
const TagUnknown = tagUnknown

// SQL for the probes. The aliases are lowercase and explicit because MySQL 8
// returns information_schema column names in upper case. The character set
// comes from information_schema.COLUMNS: joining COLLATION_CHARACTER_SET_APPLICABILITY
// on the table collation hides utf8mb4_uca1400 tables on MariaDB 11.4 and later.
const (
	queryVersion = `SELECT VERSION()`

	querySchemaSize = `SELECT SUM(DATA_LENGTH + INDEX_LENGTH) AS schema_size ` +
		`FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()`

	querySchemaCharset = `SELECT DEFAULT_CHARACTER_SET_NAME AS schema_charset, ` +
		`DEFAULT_COLLATION_NAME AS schema_collation ` +
		`FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = DATABASE()`

	queryColumnCharsets = `SELECT c.TABLE_NAME AS table_name, c.CHARACTER_SET_NAME AS charset_name ` +
		`FROM information_schema.COLUMNS c ` +
		`JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = c.TABLE_SCHEMA AND t.TABLE_NAME = c.TABLE_NAME ` +
		`WHERE c.TABLE_SCHEMA = DATABASE() AND t.TABLE_TYPE = 'BASE TABLE' AND c.CHARACTER_SET_NAME IS NOT NULL ` +
		`GROUP BY c.TABLE_NAME, c.CHARACTER_SET_NAME ORDER BY c.TABLE_NAME, c.CHARACTER_SET_NAME`
)

// QueryVersion returns the raw SELECT VERSION() string of the server behind db.
func QueryVersion(ctx context.Context, db *gorm.DB) (string, error) {
	var version string
	if err := db.WithContext(ctx).Raw(queryVersion).Row().Scan(&version); err != nil {
		return "", errors.New(err).
			Component(componentName).
			Category(errors.CategoryDatabase).
			Context("operation", "query_server_version").
			Build()
	}
	return version, nil
}

// QuerySchemaSize returns the size in bytes of the current schema: the data
// plus the indexes of every table in it. A schema with no tables returns 0.
// The figures come from InnoDB table statistics, which both MySQL and MariaDB
// refresh lazily (MySQL also caches them per information_schema_stats_expiry,
// 24 hours by default), so the value can lag recent writes until the next
// ANALYZE TABLE.
func QuerySchemaSize(ctx context.Context, db *gorm.DB) (int64, error) {
	var size sql.NullInt64
	if err := db.WithContext(ctx).Raw(querySchemaSize).Row().Scan(&size); err != nil {
		return 0, errors.New(err).
			Component(componentName).
			Category(errors.CategoryDatabase).
			Context("operation", "query_schema_size").
			Build()
	}
	return size.Int64, nil
}

// QuerySchemaCharset reads the default character set and collation of the
// current schema. Both are empty when the server returns no schema row.
func QuerySchemaCharset(ctx context.Context, db *gorm.DB) (charset, collation string, err error) {
	var schema []struct {
		SchemaCharset   string `gorm:"column:schema_charset"`
		SchemaCollation string `gorm:"column:schema_collation"`
	}
	if err := db.WithContext(ctx).Raw(querySchemaCharset).Scan(&schema).Error; err != nil {
		return "", "", errors.New(err).
			Component(componentName).
			Category(errors.CategoryDatabase).
			Context("operation", "query_schema_charset").
			Build()
	}
	if len(schema) == 0 {
		return "", "", nil
	}
	return schema[0].SchemaCharset, schema[0].SchemaCollation, nil
}

// QueryCharset reads the default character set of the current schema and the
// character sets of the text columns of its base tables. It only reads, and it
// returns the error of either read.
func QueryCharset(ctx context.Context, db *gorm.DB) (CharsetReport, error) {
	conn := db.WithContext(ctx)

	schemaCharset, schemaCollation, err := QuerySchemaCharset(ctx, db)
	if err != nil {
		return CharsetReport{}, err
	}

	var cols []struct {
		TableName   string `gorm:"column:table_name"`
		CharsetName string `gorm:"column:charset_name"`
	}
	if err := conn.Raw(queryColumnCharsets).Scan(&cols).Error; err != nil {
		return CharsetReport{}, errors.New(err).
			Component(componentName).
			Category(errors.CategoryDatabase).
			Context("operation", "query_column_charsets").
			Build()
	}

	report := CharsetReport{SchemaCharset: schemaCharset, SchemaCollation: schemaCollation}
	for _, c := range cols {
		if n := len(report.Tables); n > 0 && report.Tables[n-1].Name == c.TableName {
			report.Tables[n-1].Charsets = append(report.Tables[n-1].Charsets, c.CharsetName)
			continue
		}
		report.Tables = append(report.Tables, TableCharsets{Name: c.TableName, Charsets: []string{c.CharsetName}})
	}
	return report, nil
}
