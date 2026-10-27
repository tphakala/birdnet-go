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

// databaseServerTestNow is the fixed clock of the notice tests.
var databaseServerTestNow = time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)

// serverReportFor builds a probe report for a raw version and a schema charset.
func serverReportFor(raw, schemaCharset string) databaseServerReport {
	return databaseServerReport{
		assessment: dbserver.AssessRaw(raw, databaseServerTestNow),
		charset:    dbserver.CharsetReport{SchemaCharset: schemaCharset},
	}
}

// endingSoonReport is MariaDB 10.11 assessed 30 days before its end of life.
func endingSoonReport() databaseServerReport {
	r := serverReportFor("10.11.19-MariaDB", "utf8mb4")
	r.assessment = dbserver.AssessRaw("10.11.19-MariaDB", time.Date(2028, time.January, 17, 0, 0, 0, 0, time.UTC))
	return r
}

// mixedCharsetReport has a utf8mb4 schema default and one latin1 table.
func mixedCharsetReport() databaseServerReport {
	r := serverReportFor("11.4.13-MariaDB", "utf8mb4")
	r.charset.Tables = []dbserver.TableCharsets{{Name: "legacy", Charsets: []string{"latin1"}}}
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
		{"end of life", serverReportFor("8.0.46", "utf8mb4"), true, [3]string{"mysql-8.0", "end_of_life", "utf8mb4"}},
		{"untabled end of life", serverReportFor("10.5.29-MariaDB", "utf8mb4"), true, [3]string{"mariadb-end_of_life", "end_of_life", "utf8mb4"}},
		{"latin1 charset", serverReportFor("8.4.13", "latin1"), true, [3]string{"mysql-8.4", "supported", "latin1"}},
		{"ending soon", endingSoonReport(), true, [3]string{"mariadb-10.11", "ending_soon", "utf8mb4"}},
		{"utf8mb4 schema with a latin1 table", mixedCharsetReport(), true, [3]string{"mariadb-11.4", "supported", "mixed"}},
		{"no schema row", serverReportFor("11.4.13-MariaDB", ""), false, [3]string{"mariadb-11.4", "supported", "unknown"}},
		{"supported", serverReportFor("11.4.13-MariaDB", "utf8mb4"), false, [3]string{"mariadb-11.4", "supported", "utf8mb4"}},
		{"untracked", serverReportFor("9.7.4", "utf8mb4"), false, [3]string{"mysql-untracked", "untracked", "utf8mb4"}},
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
			svc.probeServer = func(ctx context.Context, _ *gorm.DB, _ time.Time) databaseServerReport {
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
