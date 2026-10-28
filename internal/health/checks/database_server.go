package checks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/dbserver"
	"github.com/tphakala/birdnet-go/internal/health"
)

const (
	// databaseServerVersionCheckName is the check ID of the server version check.
	databaseServerVersionCheckName = "database_server_version"
	// databaseCharsetCheckName is the check ID of the character set check.
	databaseCharsetCheckName = "database_charset"
	// databaseServerNotApplicable is the message of both checks on a database
	// that is not MySQL or MariaDB.
	databaseServerNotApplicable = "Not applicable: the database is not MySQL or MariaDB"
	// maxNamedCharsetTables bounds how many tables the charset message names.
	maxNamedCharsetTables = 10
	// databaseCharsetTablesOKFormat is the message when every character column is
	// utf8mb4 but the database default is not; the argument is the default.
	databaseCharsetTablesOKFormat = "All existing character columns use utf8mb4. The database default character set is %s; new tables created without an explicit character set may inherit it."
	// eolDateLayout formats the end-of-life date in messages.
	eolDateLayout = time.DateOnly
)

// DatabaseServerProbe returns the raw SELECT VERSION() string. applicable is
// false when the database is not MySQL or MariaDB (or not reachable through a
// MySQL handle), in which case the check is skipped.
type DatabaseServerProbe func(ctx context.Context) (raw string, applicable bool, err error)

// DatabaseCharsetProbe returns the character set report of the schema, with
// the same applicable contract as DatabaseServerProbe.
type DatabaseCharsetProbe func(ctx context.Context) (report dbserver.CharsetReport, applicable bool, err error)

// DatabaseServerVersionCheck reports whether the MySQL or MariaDB server is a
// release under upstream support. It never reports critical: an old server
// that works is a warning, not an outage.
type DatabaseServerVersionCheck struct {
	probe DatabaseServerProbe
	now   func() time.Time
}

// NewDatabaseServerVersionCheck creates the check. A nil probe skips it.
func NewDatabaseServerVersionCheck(probe DatabaseServerProbe, now func() time.Time) *DatabaseServerVersionCheck {
	if now == nil {
		now = time.Now
	}
	return &DatabaseServerVersionCheck{probe: probe, now: now}
}

// Name returns the check identifier.
func (c *DatabaseServerVersionCheck) Name() string { return databaseServerVersionCheckName }

// Category returns the database category.
func (c *DatabaseServerVersionCheck) Category() health.Category { return health.CategoryDatabase }

// Run reads the server version and classifies it.
func (c *DatabaseServerVersionCheck) Run(ctx context.Context) health.Result {
	start := time.Now()
	if c.probe == nil {
		return databaseServerResult(c.Name(), c.Category(), start, health.StatusSkipped, databaseServerNotApplicable, nil)
	}
	raw, applicable, err := c.probe(ctx)
	if !applicable && err == nil {
		return databaseServerResult(c.Name(), c.Category(), start, health.StatusSkipped, databaseServerNotApplicable, nil)
	}
	if err != nil {
		return databaseServerResult(c.Name(), c.Category(), start, health.StatusUnknown,
			fmt.Sprintf("Unable to read the database server version: %v", err), nil)
	}

	a := dbserver.AssessRaw(raw, c.now())
	status, msg := describeServerAssessment(&a)
	details := map[string]any{
		"version":   a.Version.Raw,
		"support":   string(a.Level),
		"supported": a.Supported(),
	}
	if a.Level != dbserver.SupportUnknown {
		details["vendor"] = string(a.Version.Vendor)
		details["series"] = a.Version.Series()
	}
	if a.Release != nil {
		details["eol_date"] = a.Release.EOL.Format(eolDateLayout)
	}
	return databaseServerResult(c.Name(), c.Category(), start, status, msg, details)
}

// describeServerAssessment maps an assessment to a health status and message.
func describeServerAssessment(a *dbserver.Assessment) (status health.Status, message string) {
	name := a.Version.Name()
	supported := dbserver.SupportedReleases()
	switch a.Level {
	case dbserver.SupportSupported:
		return health.StatusHealthy, fmt.Sprintf("%s is supported until %s", name, a.Release.EOL.Format(eolDateLayout))
	case dbserver.SupportEndingSoon:
		return health.StatusWarning, fmt.Sprintf("%s reaches its upstream end of life on %s. Plan an upgrade to a supported release (%s).",
			name, a.Release.EOL.Format(eolDateLayout), supported)
	case dbserver.SupportEndOfLife:
		if a.Release != nil {
			return health.StatusWarning, fmt.Sprintf("%s is past its upstream end of life (%s) and is not supported. Upgrade to a supported release (%s).",
				name, a.Release.EOL.Format(eolDateLayout), supported)
		}
		return health.StatusWarning, fmt.Sprintf("%s is older than the oldest supported release and is not supported. Upgrade to a supported release (%s).",
			name, supported)
	case dbserver.SupportUntracked:
		return health.StatusWarning, fmt.Sprintf("%s is not on the list of releases BirdNET-Go is tested with (%s).", name, supported)
	case dbserver.SupportUnknown:
		return health.StatusWarning, fmt.Sprintf("The database server version %q was not recognized. Supported releases: %s.", a.Version.Raw, supported)
	}
	return health.StatusUnknown, "Unknown support level"
}

// DatabaseCharsetCheck reports whether the schema and its tables use utf8mb4.
type DatabaseCharsetCheck struct {
	probe DatabaseCharsetProbe
}

// NewDatabaseCharsetCheck creates the check. A nil probe skips it.
func NewDatabaseCharsetCheck(probe DatabaseCharsetProbe) *DatabaseCharsetCheck {
	return &DatabaseCharsetCheck{probe: probe}
}

// Name returns the check identifier.
func (c *DatabaseCharsetCheck) Name() string { return databaseCharsetCheckName }

// Category returns the database category.
func (c *DatabaseCharsetCheck) Category() health.Category { return health.CategoryDatabase }

// Run reads the character sets of the schema and its tables.
func (c *DatabaseCharsetCheck) Run(ctx context.Context) health.Result {
	start := time.Now()
	if c.probe == nil {
		return databaseServerResult(c.Name(), c.Category(), start, health.StatusSkipped, databaseServerNotApplicable, nil)
	}
	report, applicable, err := c.probe(ctx)
	if !applicable && err == nil {
		return databaseServerResult(c.Name(), c.Category(), start, health.StatusSkipped, databaseServerNotApplicable, nil)
	}
	if err != nil {
		return databaseServerResult(c.Name(), c.Category(), start, health.StatusUnknown,
			fmt.Sprintf("Unable to read the database character sets: %v", err), nil)
	}

	if report.SchemaCharset == "" {
		return databaseServerResult(c.Name(), c.Category(), start, health.StatusUnknown,
			"The database default character set could not be determined", nil)
	}

	bad := report.NonUTF8MB4Tables()
	named := bad
	if len(named) > maxNamedCharsetTables {
		named = named[:maxNamedCharsetTables]
	}
	details := map[string]any{
		"schema_charset":     report.SchemaCharset,
		"schema_collation":   report.SchemaCollation,
		"non_utf8mb4_tables": named,
		"non_utf8mb4_count":  len(bad),
	}
	if report.AllUTF8MB4() {
		return databaseServerResult(c.Name(), c.Category(), start, health.StatusHealthy,
			"The database and all its tables use utf8mb4", details)
	}
	if report.EveryTableUTF8MB4() {
		return databaseServerResult(c.Name(), c.Category(), start, health.StatusHealthy,
			fmt.Sprintf(databaseCharsetTablesOKFormat, report.SchemaCharset), details)
	}

	var msg strings.Builder
	fmt.Fprintf(&msg, "The database default character set is %s", report.SchemaCharset)
	if len(bad) > 0 {
		fmt.Fprintf(&msg, " and %d table(s) are not utf8mb4: %s", len(bad), strings.Join(named, ", "))
		if len(bad) > len(named) {
			fmt.Fprintf(&msg, " and %d more", len(bad)-len(named))
		}
	}
	msg.WriteString(". Text outside that character set, such as emoji or some scripts, is rejected or stored incorrectly.")
	return databaseServerResult(c.Name(), c.Category(), start, health.StatusWarning, msg.String(), details)
}

// databaseServerResult builds a Result for the two database server checks.
func databaseServerResult(name string, category health.Category, start time.Time, status health.Status, msg string, details map[string]any) health.Result {
	return health.Result{
		Name:       name,
		Category:   category,
		Status:     status,
		Message:    msg,
		Details:    details,
		DurationMS: float64(time.Since(start).Microseconds()) / 1000,
		Timestamp:  time.Now(),
	}
}
