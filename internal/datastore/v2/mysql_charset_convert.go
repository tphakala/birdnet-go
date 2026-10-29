package v2

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/tphakala/birdnet-go/internal/datastore/dbserver"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const (
	// utf8mb4ConvertLockWaitSeconds bounds the wait for the table lock and for the
	// metadata lock of the ALTER, the same bound as the mixed schema drop. A
	// session that holds the table makes the conversion of that table skip until
	// the next start.
	utf8mb4ConvertLockWaitSeconds = mixedSchemaLockWaitSeconds

	// opConvertEmptyTable is the operation field of the conversion log lines.
	opConvertEmptyTable = "convert_empty_table_utf8mb4"

	// charsetListSeparator joins the character sets of one table in a log field.
	charsetListSeparator = ","

	sqlReadLockWaitTimeout = "SELECT @@SESSION.lock_wait_timeout"
	sqlSetLockWaitTimeout  = "SET SESSION lock_wait_timeout = %d"
	sqlLockTableWrite      = "LOCK TABLES `%s` WRITE"
	sqlUnlockTables        = "UNLOCK TABLES"
	// sqlConvertTable has no COLLATE, so the table gets the server's utf8mb4
	// default collation, the same as a table created with utf8mb4TableOptions.
	sqlConvertTable = "ALTER TABLE `%s` CONVERT TO CHARACTER SET " + dbserver.CharsetUTF8MB4
)

// convertibleTables returns the tables of this manager's v2 schema that have a
// text column outside utf8mb4, in name order. Tables are matched by the names
// the entities get from namer, so tables outside this naming mode never match.
// In the unprefixed mode a v2 table that shares its name with a legacy table is
// matched, as AutoMigrate matches it. Tables without text columns are not in the
// report and are never candidates.
func convertibleTables(namer schema.Namer, report dbserver.CharsetReport) ([]string, error) {
	v2Tables := make(map[string]struct{})
	for _, model := range v2Entities() {
		parsed, err := schema.Parse(model, &sync.Map{}, namer)
		if err != nil {
			return nil, fmt.Errorf("parse v2 entity %T: %w", model, err)
		}
		v2Tables[parsed.Table] = struct{}{}
	}
	var tables []string
	for _, name := range report.NonUTF8MB4Tables() {
		if _, ok := v2Tables[name]; ok {
			tables = append(tables, name)
		}
	}
	return tables, nil
}

// discardPinnedConn makes database/sql close the pinned connection instead of
// returning it to the pool. The server releases the table locks of a session
// when it ends, so a connection that failed to unlock never serves another
// query. Closing the connection afterwards returns sql.ErrConnDone, which GORM
// ignores.
func discardPinnedConn(tx *gorm.DB) {
	if conn, ok := tx.Statement.ConnPool.(*sql.Conn); ok {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
}

// convertTableIfEmpty converts one table to utf8mb4 when it holds no row. On one
// pinned connection it takes a WRITE lock on the table, checks for a row under
// the lock and runs ALTER TABLE ... CONVERT TO CHARACTER SET utf8mb4, so no other
// session can insert between the check and the ALTER. It reports whether it
// converted. An error before the ALTER changes nothing. When the ALTER succeeded
// and resetting the session afterwards fails, it returns converted true together
// with that error. beforeAlter and afterAlter are test seams. The table name
// comes from the entity list through the naming strategy, never from input.
func convertTableIfEmpty(ctx context.Context, db *gorm.DB, table string, beforeAlter func(string), afterAlter func(*gorm.DB)) (converted bool, err error) {
	err = db.WithContext(ctx).Connection(func(tx *gorm.DB) (retErr error) {
		var previousWait int
		if err := tx.Raw(sqlReadLockWaitTimeout).Scan(&previousWait).Error; err != nil {
			return fmt.Errorf("read lock wait timeout: %w", err)
		}
		if err := tx.Exec(fmt.Sprintf(sqlSetLockWaitTimeout, utf8mb4ConvertLockWaitSeconds)).Error; err != nil {
			return fmt.Errorf("set lock wait timeout: %w", err)
		}
		// Defers run last in, first out: the unlock runs before the timeout restore.
		defer func() {
			if err := tx.Exec(fmt.Sprintf(sqlSetLockWaitTimeout, previousWait)).Error; err != nil {
				discardPinnedConn(tx)
				if retErr == nil {
					retErr = fmt.Errorf("restore lock wait timeout: %w", err)
				}
			}
		}()

		if err := tx.Exec(fmt.Sprintf(sqlLockTableWrite, table)).Error; err != nil {
			return fmt.Errorf("lock table %s: %w", table, err)
		}
		defer func() {
			if err := tx.Exec(sqlUnlockTables).Error; err != nil {
				discardPinnedConn(tx)
				if retErr == nil {
					retErr = fmt.Errorf("unlock tables: %w", err)
				}
			}
		}()

		hasRow, err := tableHasAnyRow(tx, table)
		if err != nil {
			return fmt.Errorf("check table %s: %w", table, err)
		}
		if hasRow {
			return nil
		}
		if beforeAlter != nil {
			beforeAlter(table)
		}
		if err := tx.Exec(fmt.Sprintf(sqlConvertTable, table)).Error; err != nil {
			return fmt.Errorf("convert table %s: %w", table, err)
		}
		converted = true
		if afterAlter != nil {
			afterAlter(tx)
		}
		return nil
	})
	return converted, err
}

// convertEmptyTablesToUTF8MB4 converts the v2 tables that hold no row and have a
// text column outside utf8mb4. It never fails Initialize: a table that cannot be
// converted keeps its character set, is logged, and is tried again on the next
// start. Tables with rows are never altered. Each table's outcome is logged by
// logConvertOutcome.
func (m *MySQLManager) convertEmptyTablesToUTF8MB4(ctx context.Context) {
	report, err := dbserver.QueryCharset(ctx, m.db)
	if err != nil {
		m.warnConvert("could not read table character sets; empty tables are not converted this start", err)
		return
	}
	tables, err := convertibleTables(m.db.NamingStrategy, report)
	if err != nil {
		m.warnConvert("could not list the v2 tables; empty tables are not converted this start", err)
		return
	}
	for _, table := range tables {
		converted, err := convertTableIfEmpty(ctx, m.db, table, m.beforeConvertAlter, m.afterConvertAlter)
		m.logConvertOutcome(report, table, converted, err)
	}
}

// logConvertOutcome logs what happened to one table. A table is logged as
// converted whenever its ALTER succeeded; an error that arrives after that
// comes from resetting the session, so it is a separate warning.
func (m *MySQLManager) logConvertOutcome(report dbserver.CharsetReport, table string, converted bool, err error) {
	if m.log == nil {
		return
	}
	switch {
	case converted:
		m.log.Info("converted empty table to utf8mb4",
			logger.String("table", table),
			logger.String("from_charsets", strings.Join(tableCharsets(report, table), charsetListSeparator)),
			logger.String("to_charset", dbserver.CharsetUTF8MB4),
			logger.Int64("row_count", 0),
			logger.String("operation", opConvertEmptyTable))
		if err != nil {
			m.log.Warn("table was converted, but its connection could not be reset and was closed",
				logger.String("table", table),
				logger.Error(err),
				logger.String("operation", opConvertEmptyTable))
		}
	case err != nil:
		m.log.Warn("could not convert empty table to utf8mb4; it keeps its character set and the next start tries again",
			logger.String("table", table),
			logger.Error(err),
			logger.String("operation", opConvertEmptyTable))
	default:
		m.log.Debug("table holds rows; left as is",
			logger.String("table", table),
			logger.String("operation", opConvertEmptyTable))
	}
}

// warnConvert logs a setup failure of the conversion step.
func (m *MySQLManager) warnConvert(msg string, err error) {
	if m.log != nil {
		m.log.Warn(msg, logger.Error(err), logger.String("operation", opConvertEmptyTable))
	}
}

// tableCharsets returns the character sets the report lists for table.
func tableCharsets(report dbserver.CharsetReport, table string) []string {
	i := slices.IndexFunc(report.Tables, func(t dbserver.TableCharsets) bool { return t.Name == table })
	if i < 0 {
		return nil
	}
	return report.Tables[i].Charsets
}
