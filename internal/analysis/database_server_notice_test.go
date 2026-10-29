package analysis

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/datastore/dbserver"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// serverReportFor builds a probe report for a raw version and a schema charset.
func serverReportFor(raw, schemaCharset string) databaseServerReport {
	return databaseServerReport{
		assessment: dbserver.AssessRaw(raw),
		charset:    dbserver.CharsetReport{SchemaCharset: schemaCharset},
	}
}

// mixedCharsetReport has a utf8mb4 schema default and one latin1 table.
func mixedCharsetReport() databaseServerReport {
	r := serverReportFor("11.4.13-MariaDB", "utf8mb4")
	r.charset.Tables = []dbserver.TableCharsets{{Name: "legacy", Charsets: []string{"latin1"}}}
	return r
}

// utf8mb4TablesReport has a latin1 schema default and only utf8mb4 tables.
func utf8mb4TablesReport() databaseServerReport {
	r := serverReportFor("8.4.13", "latin1")
	r.charset.Tables = []dbserver.TableCharsets{{Name: "detections", Charsets: []string{"utf8mb4"}}}
	return r
}

// tagCapture records the values passed to setTags.
type tagCapture struct {
	mu   sync.Mutex
	last [3]string
	n    int
}

func (c *tagCapture) set(server, support, charset string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = [3]string{server, support, charset}
	c.n++
	return true
}

func newTestServerNotice(svc notification.NoticeService, tags *tagCapture) *databaseServerNotice {
	n := newDatabaseServerNotice(logger.Global().Module("test"))
	n.service = func() notification.NoticeService { return svc }
	n.setTags = tags.set
	return n
}

func TestDatabaseServerNotice_RaisesOnlyForAttention(t *testing.T) {
	probeErr := databaseServerReport{versionErr: errors.NewStd("down"), charsetErr: errors.NewStd("down")}
	tests := []struct {
		name       string
		report     databaseServerReport
		wantNotice bool
		wantTags   [3]string
	}{
		{"innovation release 9.0", serverReportFor("9.0.1", "utf8mb4"), true, [3]string{"mysql-9.0", "unsupported", "utf8mb4"}},
		{"innovation release 9.1", serverReportFor("9.1.1", "utf8mb4"), true, [3]string{"mysql-9.1", "unsupported", "utf8mb4"}},
		{"innovation release 9.2", serverReportFor("9.2.1", "utf8mb4"), true, [3]string{"mysql-9.2", "unsupported", "utf8mb4"}},
		{"innovation release 9.3", serverReportFor("9.3.1", "utf8mb4"), true, [3]string{"mysql-9.3", "unsupported", "utf8mb4"}},
		{"innovation release 9.4", serverReportFor("9.4.1", "utf8mb4"), true, [3]string{"mysql-9.4", "unsupported", "utf8mb4"}},
		{"innovation release 9.5", serverReportFor("9.5.1", "utf8mb4"), true, [3]string{"mysql-9.5", "unsupported", "utf8mb4"}},
		{"innovation release 9.6", serverReportFor("9.6.1", "utf8mb4"), true, [3]string{"mysql-9.6", "unsupported", "utf8mb4"}},
		{"below the floor mysql 8.0", serverReportFor("8.0.46", "utf8mb4"), true, [3]string{"mysql-unsupported", "unsupported", "utf8mb4"}},
		{"below the floor mysql 5.7", serverReportFor("5.7.44-log", "utf8mb4"), true, [3]string{"mysql-unsupported", "unsupported", "utf8mb4"}},
		{"below the floor mariadb 10.6", serverReportFor("10.6.28-MariaDB", "utf8mb4"), true, [3]string{"mariadb-unsupported", "unsupported", "utf8mb4"}},
		{"below the floor mariadb 10.5", serverReportFor("10.5.29-MariaDB", "utf8mb4"), true, [3]string{"mariadb-unsupported", "unsupported", "utf8mb4"}},
		{"latin1 charset", serverReportFor("8.4.13", "latin1"), true, [3]string{"mysql-8.4", "supported", "latin1"}},
		{"latin1 schema with only utf8mb4 tables", utf8mb4TablesReport(), false, [3]string{"mysql-8.4", "supported", "utf8mb4_tables"}},
		{"utf8mb4 schema with a latin1 table", mixedCharsetReport(), true, [3]string{"mariadb-11.4", "supported", "mixed"}},
		{"no schema row", serverReportFor("11.4.13-MariaDB", ""), false, [3]string{"mariadb-11.4", "supported", "unknown"}},
		{"supported", serverReportFor("11.4.13-MariaDB", "utf8mb4"), false, [3]string{"mariadb-11.4", "supported", "utf8mb4"}},
		{"supported mysql 9.7", serverReportFor("9.7.4", "utf8mb4"), false, [3]string{"mysql-9.7", "supported", "utf8mb4"}},
		{"supported mysql 8.4", serverReportFor("8.4.13", "utf8mb4"), false, [3]string{"mysql-8.4", "supported", "utf8mb4"}},
		{"untracked mysql 9.8", serverReportFor("9.8.0", "utf8mb4"), false, [3]string{"mysql-untracked", "untracked", "utf8mb4"}},
		{"untracked mysql 26.10", serverReportFor("26.10.0", "utf8mb4"), false, [3]string{"mysql-untracked", "untracked", "utf8mb4"}},
		{"unknown", serverReportFor("garbage", "utf8mb4"), false, [3]string{"unknown", "unknown", "utf8mb4"}},
		{"probe error", probeErr, false, [3]string{"unknown", "unknown", "unknown"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				creates := 0
				if tt.wantNotice {
					creates = 1
				}
				rec := newCadenceNoticeRecorder(t, creates, 0)
				tags := &tagCapture{}
				n := newTestServerNotice(rec, tags)

				n.report(&tt.report)
				n.stop()

				assert.Equal(t, tt.wantTags, tags.last)
				if !tt.wantNotice {
					return
				}
				got := rec.last()
				require.NotNil(t, got)
				assert.Equal(t, notification.ComponentDatabase, got.Component)
				assert.Equal(t, notification.DeliveryTargetBell, got.DeliveryTarget)
				assert.Equal(t, notification.MsgDatabaseServerAttentionTitle, got.TitleKey)
				assert.Equal(t, notification.MsgDatabaseServerAttentionMessage, got.MessageKey)
			})
		})
	}
}

func TestAttentionText_Unsupported(t *testing.T) {
	for _, raw := range []string{"9.6.1", "8.0.46", "10.6.28-MariaDB"} {
		r := serverReportFor(raw, "utf8mb4")
		got := attentionText(&r, databaseServerReasonUnsupported)
		assert.Contains(t, got, "is not a supported release; upgrade to a supported release (MySQL 8.4, 9.7; MariaDB 10.11, 11.4, 11.8, 12.3)", raw)
		assert.NotContains(t, got, "end of life")
		assert.NotRegexp(t, `\d{4}-\d{2}-\d{2}`, got)
	}
}

func TestDatabaseServerNotice_RetriesWithoutServiceThenGivesUp(t *testing.T) {
	t.Run("gives up after the bounded retries", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			lookups := 0
			n := newTestServerNotice(nil, &tagCapture{})
			n.service = func() notification.NoticeService { lookups++; return nil }

			r := serverReportFor("8.0.46", "utf8mb4")
			n.report(&r)
			time.Sleep(time.Hour)
			synctest.Wait()
			n.stop()

			assert.Equal(t, 1+databaseServerNoticeMaxRetries, lookups)
		})
	})

	t.Run("a service that appears on the second retry gets one notice", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rec := newCadenceNoticeRecorder(t, 1, 0)
			lookups := 0
			n := newTestServerNotice(nil, &tagCapture{})
			n.service = func() notification.NoticeService {
				lookups++
				if lookups < 3 {
					return nil
				}
				return rec
			}

			r := serverReportFor("8.0.46", "utf8mb4")
			n.report(&r)
			time.Sleep(time.Hour)
			synctest.Wait()
			n.stop()

			created, _ := rec.counts()
			assert.Equal(t, 1, created)
			assert.Equal(t, 3, lookups, "no lookups after the notice is raised")
		})
	})
}

func TestDatabaseServerNotice_StopCancelsRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lookups := 0
		n := newTestServerNotice(nil, &tagCapture{})
		n.service = func() notification.NoticeService { lookups++; return nil }

		r := serverReportFor("8.0.46", "utf8mb4")
		n.report(&r)
		n.stop()
		time.Sleep(time.Hour)
		synctest.Wait()

		assert.Equal(t, 1, lookups)
	})
}

// sqliteManagerStub and mysqlManagerStub report only their dialect and a nil
// handle; the injected probe never touches it.
type sqliteManagerStub struct{ datastoreV2.Manager }

func (sqliteManagerStub) IsMySQL() bool { return false }

type mysqlManagerStub struct{ datastoreV2.Manager }

func (mysqlManagerStub) IsMySQL() bool { return true }
func (mysqlManagerStub) DB() *gorm.DB  { return nil }

func TestDatabaseService_ProbeDatabaseServer_OnlyOnMySQL(t *testing.T) {
	tests := []struct {
		name       string
		manager    datastoreV2.Manager
		wantProbes int
	}{
		{"no manager", nil, 0},
		{"sqlite manager", sqliteManagerStub{}, 0},
		{"mysql manager", mysqlManagerStub{}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &DatabaseService{v2Manager: tt.manager}
			probes := 0
			svc.probeServer = func(ctx context.Context, _ *gorm.DB) databaseServerReport {
				probes++
				_, bounded := ctx.Deadline()
				assert.True(t, bounded, "the probe must run under a deadline")
				return serverReportFor("11.4.13-MariaDB", "utf8mb4")
			}
			svc.probeDatabaseServer(logger.Global().Module("test"))
			t.Cleanup(func() {
				if svc.serverNotice != nil {
					svc.serverNotice.stop()
				}
			})

			assert.Equal(t, tt.wantProbes, probes)
			assert.Equal(t, tt.wantProbes == 1, svc.serverNotice != nil)
		})
	}
}
