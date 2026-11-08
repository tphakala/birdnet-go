//go:build integration

package v2

import (
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/dbfailure"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// MySQL wire protocol constants for the statement-matching fault proxy.
const (
	faultComQuery       = 0x03
	faultComStmtPrepare = 0x16
	// faultPacketHeaderLen is the 3 byte payload length plus the sequence id.
	faultPacketHeaderLen = 4
	faultPayloadLenBytes = 3
	faultErrPacketMarker = 0xFF
	faultSQLStateMarker  = '#'
	// faultErrFixedLen is the marker, error number and SQL state marker of an ERR payload.
	faultErrFixedLen    = 4
	faultSQLState       = "HY000"
	faultServerMessage  = "injected server error"
	faultReadBufferSize = 64 * 1024
	// faultServerErrorNumber is ER_QUERY_TIMEOUT, a server error that is not a
	// connectivity failure and not a connection-time rejection.
	faultServerErrorNumber = 3024
)

// faultAction is what the proxy does to the statement its rule matches.
type faultAction int32

const (
	// faultDropBeforeServer closes both sides without forwarding the statement.
	faultDropBeforeServer faultAction = iota + 1
	// faultApplyLoseReply forwards the statement, then closes both sides when the
	// server's reply arrives, so the server applied it and the client never learns.
	faultApplyLoseReply
	// faultReplyServerError answers the statement with a MySQL ERR packet and keeps the
	// connection open. The server never sees the statement.
	faultReplyServerError
)

// mixedSchemaLostConnectionWarn is the start of the Warn that a connection lost during a
// reconcile step logs.
const mixedSchemaLostConnectionWarn = "lost the database connection while reconciling"

// startupFaultProxy forwards to a MySQL server and applies one one-shot rule to the
// first client statement (COM_QUERY or COM_STMT_PREPARE) whose text contains the
// rule's substring. fired reports how many times a rule hit, so a test fails when the
// pattern no longer matches the SQL.
type startupFaultProxy struct {
	target string
	addr   string

	mu      sync.Mutex
	ln      net.Listener
	conns   map[net.Conn]struct{}
	match   string
	action  faultAction
	errorNo uint16
	armed   bool
	hits    atomic.Int32
}

func newStartupFaultProxy(t *testing.T, target string) *startupFaultProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := &startupFaultProxy{target: target, addr: ln.Addr().String(), ln: ln, conns: map[net.Conn]struct{}{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.handle(c)
		}
	}()
	t.Cleanup(p.stop)
	return p
}

func (p *startupFaultProxy) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.ln.Close()
	for c := range p.conns {
		_ = c.Close()
	}
}

// arm sets the one-shot rule. errorNo is used by faultReplyServerError only.
func (p *startupFaultProxy) arm(match string, action faultAction, errorNo uint16) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.match, p.action, p.errorNo, p.armed = match, action, errorNo, true
}

// disarm removes the rule, whether or not it fired.
func (p *startupFaultProxy) disarm() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.armed = false
}

func (p *startupFaultProxy) fired() int { return int(p.hits.Load()) }

// claim returns the rule when statement matches it, and disarms it.
func (p *startupFaultProxy) claim(statement string) (faultAction, uint16, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.armed || !strings.Contains(statement, p.match) {
		return 0, 0, false
	}
	p.armed = false
	p.hits.Add(1)
	return p.action, p.errorNo, true
}

func (p *startupFaultProxy) track(c net.Conn) {
	p.mu.Lock()
	p.conns[c] = struct{}{}
	p.mu.Unlock()
}

// findStatement walks the packets of one client read and returns the offset, sequence id
// and statement text of the first complete COM_QUERY or COM_STMT_PREPARE packet that the
// armed rule claims. A packet cut by the end of the read is ignored.
func (p *startupFaultProxy) findStatement(buf []byte) (offset int, seq byte, action faultAction, errorNo uint16, found bool) {
	for offset < len(buf) {
		rest := buf[offset:]
		if len(rest) <= faultPacketHeaderLen {
			return 0, 0, 0, 0, false
		}
		payloadLen := int(binary.LittleEndian.Uint32(append(rest[:faultPayloadLenBytes:faultPayloadLenBytes], 0)))
		end := faultPacketHeaderLen + payloadLen
		if end > len(rest) {
			return 0, 0, 0, 0, false
		}
		command := rest[faultPacketHeaderLen]
		if payloadLen > 0 && (command == faultComQuery || command == faultComStmtPrepare) {
			if action, errorNo, ok := p.claim(string(rest[faultPacketHeaderLen+1 : end])); ok {
				return offset, rest[faultPayloadLenBytes], action, errorNo, true
			}
		}
		offset += end
	}
	return 0, 0, 0, 0, false
}

// errPacket builds a MySQL ERR packet with the given sequence id.
func errPacket(seq byte, errorNo uint16) []byte {
	payload := make([]byte, 0, faultErrFixedLen+len(faultSQLState)+len(faultServerMessage))
	payload = append(payload, faultErrPacketMarker, 0, 0, faultSQLStateMarker)
	binary.LittleEndian.PutUint16(payload[1:], errorNo)
	payload = append(payload, faultSQLState...)
	payload = append(payload, faultServerMessage...)
	n := len(payload)
	return append([]byte{byte(n), byte(n >> 8), byte(n >> 16), seq}, payload...)
}

func (p *startupFaultProxy) handle(client net.Conn) {
	server, err := net.Dial("tcp", p.target)
	if err != nil {
		_ = client.Close()
		return
	}
	p.track(client)
	p.track(server)
	closeBoth := func() {
		_ = client.Close()
		_ = server.Close()
	}

	var loseReply atomic.Bool
	go func() {
		buf := make([]byte, faultReadBufferSize)
		for {
			n, err := server.Read(buf)
			if err != nil || loseReply.Load() {
				closeBoth()
				return
			}
			if _, err := client.Write(buf[:n]); err != nil {
				closeBoth()
				return
			}
		}
	}()

	buf := make([]byte, faultReadBufferSize)
	for {
		n, err := client.Read(buf)
		if err != nil {
			closeBoth()
			return
		}
		data := buf[:n]
		if offset, seq, action, errorNo, ok := p.findStatement(data); ok {
			switch action {
			case faultDropBeforeServer:
				closeBoth()
				return
			case faultApplyLoseReply:
				loseReply.Store(true)
			case faultReplyServerError:
				// Forward what came before the statement, answer the statement itself.
				if _, err := server.Write(data[:offset]); err != nil {
					closeBoth()
					return
				}
				if _, err := client.Write(errPacket(seq+1, errorNo)); err != nil {
					closeBoth()
					return
				}
				continue
			}
		}
		if _, err := server.Write(data); err != nil {
			closeBoth()
			return
		}
	}
}

// settingsViaProxy points the test settings at the proxy instead of the container.
func settingsViaProxy(t *testing.T, p *startupFaultProxy) *conf.Settings {
	t.Helper()
	host, port, err := net.SplitHostPort(p.addr)
	require.NoError(t, err)
	return buildTestSettings(host, port, coexTestDBName, coexTestUser, coexTestPassword)
}

// TestMySQL_StartupDetection_MixedSchema_ConnectionLossRetriesWithoutLosingRows loses the
// connection at each step of the mixed schema reconcile. The first attempt must return
// the unknown state (which the boot retries) with every table that held rows intact; the
// second attempt, with the proxy healthy, must reach the decision the database holds.
func TestMySQL_StartupDetection_MixedSchema_ConnectionLossRetriesWithoutLosingRows(t *testing.T) {
	seedKeepLegacy := func(t *testing.T) {
		t.Helper()
		seedLegacyTables(t, coexTestDB)
		seedOrphanedBareV2Tables(t, coexTestDB)
	}
	seedKeepV2 := func(t *testing.T) {
		t.Helper()
		seedBareV2WithDetections(t, 2, true)
		require.NoError(t, coexTestDB.AutoMigrate(&datastore.Note{}))
	}
	seedFresh := func(t *testing.T) {
		t.Helper()
		seedBareV2WithDetections(t, 0, false)
		require.NoError(t, coexTestDB.AutoMigrate(&datastore.Note{}))
	}

	tests := []struct {
		name       string
		seed       func(t *testing.T)
		match      string
		action     faultAction
		afterFirst func(t *testing.T, before, after map[string]int64, log string)
		// noWarn is set when the connection was lost before any reconcile step ran, so
		// no reconcile Warn is logged.
		noWarn bool
		// wantWarn are the rendered step and dropped_empty_tables fields of the Warn.
		wantWarn     []string
		wantDecision string
	}{
		{
			name: "P1 probe", seed: seedKeepLegacy,
			match: "SELECT EXISTS(SELECT 1 FROM `notes`)", action: faultDropBeforeServer,
			afterFirst: func(t *testing.T, before, after map[string]int64, _ string) {
				t.Helper()
				assert.Equal(t, before, after, "a failed probe changes nothing")
			},
			noWarn:       true,
			wantDecision: DecisionMixedSchemaKeptLegacy,
		},
		{
			name: "D1 lock", seed: seedKeepV2,
			match: "LOCK TABLES", action: faultDropBeforeServer,
			afterFirst: func(t *testing.T, before, after map[string]int64, _ string) {
				t.Helper()
				assert.Equal(t, before, after, "a failed lock changes nothing")
			},
			wantWarn:     []string{`step="removing the empty legacy tables"`, "dropped_empty_tables=none"},
			wantDecision: DecisionMixedSchemaKeptV2,
		},
		{
			name: "D2 partial drop", seed: seedKeepLegacy,
			match: "DROP TABLE `detection_reviews`", action: faultApplyLoseReply,
			afterFirst: func(t *testing.T, before, after map[string]int64, log string) {
				t.Helper()
				assertNoRowsLost(t, before, after, "partial drop")
				assert.False(t, tableExists(t, coexTestDB, coexTestDBName, "detection_locks"), "a table dropped before the loss stays dropped")
				assert.True(t, tableExists(t, coexTestDB, coexTestDBName, "detections"), "the keyed table is dropped last")
			},
			wantWarn:     []string{`step="removing the empty enhanced detection tables"`, `dropped_empty_tables="detection_locks, detection_comments"`},
			wantDecision: DecisionMixedSchemaKeptLegacy,
		},
		{
			name: "D3 keyed table dropped", seed: seedKeepLegacy,
			match: "DROP TABLE `detections`", action: faultApplyLoseReply,
			afterFirst: func(t *testing.T, before, after map[string]int64, _ string) {
				t.Helper()
				assertNoRowsLost(t, before, after, "keyed table dropped")
			},
			wantWarn:     []string{`step="removing the empty enhanced detection tables"`, `dropped_empty_tables="detection_locks, detection_comments, detection_reviews, detection_predictions"`},
			wantDecision: DecisionLegacyMode,
		},
		{
			name: "M1 marker", seed: seedFresh,
			match: "UPDATE `migration_states` SET state", action: faultDropBeforeServer,
			afterFirst: func(t *testing.T, before, after map[string]int64, _ string) {
				t.Helper()
				assert.Equal(t, before, after, "a failed marker write changes nothing")
				var state string
				require.NoError(t, coexTestDB.Raw("SELECT state FROM migration_states WHERE id = 1").Scan(&state).Error)
				assert.Equal(t, string(entities.MigrationStatusIdle), state)
			},
			wantWarn:     []string{`step="marking the empty enhanced database as completed"`, "dropped_empty_tables=none"},
			wantDecision: DecisionMixedSchemaFreshV2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dropAllTables(t, coexTestDB, coexTestDBName)
			tt.seed(t)
			proxy := newStartupFaultProxy(t, net.JoinHostPort(coexTestHost, coexTestPort))
			settings := settingsViaProxy(t, proxy)
			before := tableRowCounts(t)

			transport := bindCaptureTransport(t)
			proxy.arm(tt.match, tt.action, 0)
			var state StartupState
			log := logtest.Capture(t, func() { state = checkMySQLMigrationState(settings) })
			proxy.disarm()
			sentry.Flush(time.Second)

			require.Equal(t, 1, proxy.fired(), "the rule must hit exactly once")
			assert.Empty(t, transport.captured(), "a lost connection is retried, not reported as a refusal")
			if tt.noWarn {
				assert.NotContains(t, log, mixedSchemaLostConnectionWarn)
			} else {
				assert.Contains(t, log, mixedSchemaLostConnectionWarn)
				for _, field := range tt.wantWarn {
					assert.Contains(t, log, field, "the Warn names the step and the empty tables already dropped")
				}
			}
			assert.Equal(t, DecisionStateUnknown, state.Decision)
			require.ErrorIs(t, state.Error, ErrStartupStateUnknown)
			require.NotErrorIs(t, state.Error, ErrMixedSchemaDatabase)
			assert.True(t, errors.IsConnectivityError(state.Error))
			assert.Equal(t, dbfailure.KindMySQLUnreachable, dbfailure.Classify(state.Error).Kind)
			tt.afterFirst(t, before, tableRowCounts(t), log)

			state = checkMySQLMigrationState(settings)
			require.NoError(t, state.Error)
			assert.Equal(t, tt.wantDecision, state.Decision, "the next attempt reaches the decision the database holds")
			assertNoRowsLost(t, before, tableRowCounts(t), "after the second attempt")
		})
	}
}

// TestMySQL_StartupDetection_V2MarkerCountServerError_KeepsCause fails the count of the
// v2 migration marker tables with a server error. The cause must stay in the state's
// error, and the boot decision must stay the legacy fallback.
func TestMySQL_StartupDetection_V2MarkerCountServerError_KeepsCause(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)
	require.NoError(t, coexTestDB.AutoMigrate(&datastore.Note{}))
	proxy := newStartupFaultProxy(t, net.JoinHostPort(coexTestHost, coexTestPort))
	settings := settingsViaProxy(t, proxy)

	proxy.arm("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name IN", faultReplyServerError, faultServerErrorNumber)
	state := checkMySQLMigrationState(settings)

	require.Equal(t, 1, proxy.fired(), "the rule must hit exactly once")
	assert.Equal(t, DecisionV2Corrupted, state.Decision)
	assert.True(t, state.LegacyRequired)
	require.Error(t, state.Error)
	myErr, ok := errors.AsType[*mysql.MySQLError](state.Error)
	require.True(t, ok, "the server error stays in the chain: %v", state.Error)
	assert.Equal(t, uint16(faultServerErrorNumber), myErr.Number)
	require.NotErrorIs(t, state.Error, ErrStartupStateUnknown)
	assert.Equal(t, dbfailure.KindUnknown, dbfailure.Classify(state.Error).Kind, "Start keeps the legacy fallback")
}
