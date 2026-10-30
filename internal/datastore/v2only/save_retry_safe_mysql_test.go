//go:build integration

package v2only

import (
	"context"
	"database/sql"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

// Proxy behaviors for the COMMIT statement of the next transaction.
const (
	commitPass          int32 = iota // forward everything
	commitDropUnsent                 // close both sides when COMMIT arrives, without forwarding it
	commitApplyLoseRply              // forward COMMIT, wait for the server's reply, then close without relaying it
)

const (
	// mysqlComQuery is the MySQL protocol command byte of a text statement.
	mysqlComQuery = 0x03
	// mysqlComStmtClose is the command byte that closes a prepared statement.
	mysqlComStmtClose = 0x19
	// mysqlStmtIDLen is the length of the statement id that follows COM_STMT_CLOSE.
	mysqlStmtIDLen = 4
	// mysqlPacketHeaderLen is the length prefix plus sequence id before the command byte.
	mysqlPacketHeaderLen = 4
	// proxyReadBufferSize holds one client packet; the statements here are small.
	proxyReadBufferSize = 64 * 1024
	// retrySafeTestDatabase is the throwaway database of the test.
	retrySafeTestDatabase = "retry_safe_test"
)

// faultProxy is a TCP proxy in front of the database that can stop accepting
// connections and can swallow the COMMIT of one transaction in two ways.
type faultProxy struct {
	target string
	addr   string
	commit atomic.Int32

	mu    sync.Mutex
	ln    net.Listener
	conns map[net.Conn]struct{}
}

// newFaultProxy starts a proxy to target on a free loopback port.
func newFaultProxy(t *testing.T, target string) *faultProxy {
	t.Helper()
	p := &faultProxy{target: target, conns: map[net.Conn]struct{}{}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p.addr = ln.Addr().String()
	p.serve(ln)
	t.Cleanup(p.stop)
	return p
}

func (p *faultProxy) serve(ln net.Listener) {
	p.mu.Lock()
	p.ln = ln
	p.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.handle(c)
		}
	}()
}

// stop closes the listener and every open connection, so new dials are refused.
func (p *faultProxy) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ln != nil {
		_ = p.ln.Close()
		p.ln = nil
	}
	for c := range p.conns {
		_ = c.Close()
	}
}

// restart listens again on the address the proxy had before stop.
func (p *faultProxy) restart(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", p.addr)
	require.NoError(t, err)
	p.serve(ln)
}

func (p *faultProxy) track(c net.Conn) {
	p.mu.Lock()
	p.conns[c] = struct{}{}
	p.mu.Unlock()
}

// isCommitPacket reports whether buf holds a COM_QUERY packet carrying COMMIT.
// The driver can write the close of a prepared statement right before the
// COMMIT, so one TCP read can hold both packets and the COMMIT may follow
// another packet; every packet in buf is checked.
func isCommitPacket(buf []byte) bool {
	for len(buf) > mysqlPacketHeaderLen {
		payloadLen := int(buf[0]) | int(buf[1])<<8 | int(buf[2])<<16
		end := mysqlPacketHeaderLen + payloadLen
		if end > len(buf) {
			return false // partial packet at the end of the read
		}
		pkt := buf[:end]
		if payloadLen > 0 && pkt[mysqlPacketHeaderLen] == mysqlComQuery &&
			strings.EqualFold(strings.TrimSpace(string(pkt[mysqlPacketHeaderLen+1:])), "COMMIT") {
			return true
		}
		buf = buf[end:]
	}
	return false
}

// mysqlTestPacket frames payload as a MySQL packet with sequence id 0.
func mysqlTestPacket(payload ...byte) []byte {
	n := len(payload)
	return append([]byte{byte(n), byte(n >> 8), byte(n >> 16), 0}, payload...)
}

func TestIsCommitPacket(t *testing.T) {
	t.Parallel()
	query := func(sql string) []byte { return mysqlTestPacket(append([]byte{mysqlComQuery}, sql...)...) }
	stmtClose := mysqlTestPacket(append([]byte{mysqlComStmtClose}, make([]byte, mysqlStmtIDLen)...)...)
	// Payloads past 255 and 65535 bytes need the second and third length bytes.
	const (
		twoByteLenPayload   = 300
		threeByteLenPayload = 70000
	)
	longInsert := func(n int) []byte { return query("INSERT " + strings.Repeat("x", n)) }
	cat := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	partialCommit := query("COMMIT")[:8]

	tests := []struct {
		name string
		buf  []byte
		want bool
	}{
		{"commit only", query("COMMIT"), true},
		{"commit lower case", query("commit"), true},
		{"commit after statement close", cat(stmtClose, query("COMMIT")), true},
		{"commit after a payload needing two length bytes", cat(longInsert(twoByteLenPayload), query("COMMIT")), true},
		{"commit after a payload needing three length bytes", cat(longInsert(threeByteLenPayload), query("COMMIT")), true},
		{"commit after empty packet", cat(mysqlTestPacket(), query("COMMIT")), true},
		{"non-commit query", query("SELECT 1"), false},
		{"statement close only", stmtClose, false},
		{"empty buffer", nil, false},
		{"shorter than a header", []byte{1, 0}, false},
		{"header only", mysqlTestPacket(), false},
		{"partial commit packet", partialCommit, false},
		{"close then partial commit", cat(stmtClose, partialCommit), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isCommitPacket(tt.buf))
		})
	}
}

func (p *faultProxy) handle(client net.Conn) {
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
		buf := make([]byte, proxyReadBufferSize)
		for {
			n, err := server.Read(buf)
			if err != nil {
				closeBoth()
				return
			}
			if loseReply.Load() {
				closeBoth()
				return
			}
			if _, err := client.Write(buf[:n]); err != nil {
				closeBoth()
				return
			}
		}
	}()

	buf := make([]byte, proxyReadBufferSize)
	for {
		n, err := client.Read(buf)
		if err != nil {
			closeBoth()
			return
		}
		if isCommitPacket(buf[:n]) {
			switch {
			case p.commit.CompareAndSwap(commitDropUnsent, commitPass):
				closeBoth()
				return
			case p.commit.CompareAndSwap(commitApplyLoseRply, commitPass):
				loseReply.Store(true)
			}
		}
		if _, err := server.Write(buf[:n]); err != nil {
			closeBoth()
			return
		}
	}
}

// newFaultProxyStore starts a MySQL container, puts a faultProxy in front of it
// and opens a Datastore through the proxy. The returned direct connection goes
// straight to the server, so rows can be counted without the proxy's faults.
// MYSQL_TEST_IMAGE selects the server.
func newFaultProxyStore(t *testing.T) (ds *Datastore, proxy *faultProxy, direct *sql.DB) {
	t.Helper()
	ctx := t.Context()

	cfg := &containers.MySQLConfig{
		Database:     retrySafeTestDatabase,
		RootPassword: "test",
		Username:     "testuser",
		Password:     "testpass",
		ImageTag:     "8.4",
	}
	container, err := containers.NewMySQLContainer(ctx, cfg)
	require.NoError(t, err, "failed to start MySQL container")
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil { //nolint:gocritic // cleanup must outlive the test context
			t.Logf("failed to terminate MySQL container: %v", err)
		}
	})
	host, err := container.GetHost(ctx)
	require.NoError(t, err)
	port, err := container.GetPort(ctx)
	require.NoError(t, err)
	proxy = newFaultProxy(t, net.JoinHostPort(host, strconv.Itoa(port)))
	proxyHost, proxyPort, err := net.SplitHostPort(proxy.addr)
	require.NoError(t, err)

	testLogger := logger.NewConsoleLogger("v2only_retry_safe_test", logger.LogLevelError)
	manager, err := v2.NewMySQLManager(&v2.MySQLConfig{
		Host: proxyHost, Port: proxyPort,
		Username: cfg.Username, Password: cfg.Password, Database: cfg.Database,
		Logger: testLogger,
	})
	require.NoError(t, err)
	require.NoError(t, manager.Initialize())
	ds, err = New(buildConfigForManager(t, manager, testLogger, true, nil))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ds.Close() })
	ds.timezone = time.UTC

	sqlDB, err := manager.DB().DB()
	require.NoError(t, err)
	// Dropped idle connections force the next save to dial again.
	sqlDB.SetMaxIdleConns(0)

	// Rows are counted over a direct connection, never through the proxy.
	direct, err = sql.Open("mysql", container.GetDSN())
	require.NoError(t, err)
	t.Cleanup(func() { _ = direct.Close() })
	return ds, proxy, direct
}

// TestSaveDetection_RetrySafeMarkerMatchesCommitOutcome_MySQL proves the marker
// the processor relies on: a failed save carries datastore.ErrDetectionSaveRetrySafe
// only when it stored nothing, and a COMMIT whose outcome is unknown does not,
// whether the server applied it or not. MYSQL_TEST_IMAGE selects the server.
func TestSaveDetection_RetrySafeMarkerMatchesCommitOutcome_MySQL(t *testing.T) {
	containers.SkipIfContainerRuntimeUnavailable(t)

	ds, proxy, direct := newFaultProxyStore(t)
	ctx := t.Context()
	countRows := func(clip string) int {
		var n int
		require.NoError(t, direct.QueryRowContext(ctx, "SELECT COUNT(*) FROM detections WHERE clip_name = ?", clip).Scan(&n))
		return n
	}
	newNote := func(clip string) *datastore.Note {
		return &datastore.Note{
			Date: "2024-01-15", Time: "12:30:00",
			ScientificName: "Passer domesticus", CommonName: "House Sparrow",
			Confidence: 0.85, ClipName: clip,
		}
	}

	// A species the store has not seen: the failure comes from the reference
	// data lookups that run before the transaction.
	t.Run("refused_during_reference_lookup", func(t *testing.T) {
		proxy.stop()
		note := newNote("lookup.wav")
		note.ScientificName, note.CommonName = "Parus major", "Great Tit"
		err := ds.Save(note, nil)
		require.Error(t, err)
		require.ErrorIs(t, err, datastore.ErrDetectionSaveRetrySafe, "a failed lookup stored nothing")
		assert.Contains(t, err.Error(), "failed to get/create", "the failure came from the lookups")
		assert.Equal(t, 0, countRows("lookup.wav"))
		proxy.restart(t)
	})

	// Warm the model and label caches so the faults below hit the transaction.
	require.NoError(t, ds.Save(newNote("warmup.wav"), nil))

	t.Run("refused_before_write", func(t *testing.T) {
		const clip = "refused.wav"
		proxy.stop()
		err := ds.Save(newNote(clip), nil)
		require.Error(t, err)
		require.ErrorIs(t, err, datastore.ErrDetectionSaveRetrySafe, "a refused dial stored nothing")
		assert.True(t, errors.IsUnsentError(err))
		assert.Equal(t, 0, countRows(clip))

		proxy.restart(t)
		require.NoError(t, ds.Save(newNote(clip), nil), "a repeat once the server is back")
		assert.Equal(t, 1, countRows(clip), "exactly one row after the repeat")
	})

	commitCases := []struct {
		name     string
		clip     string
		fault    int32
		wantRows int
	}{
		{"commit_dropped_before_server", "dropped.wav", commitDropUnsent, 0},
		{"commit_applied_reply_lost", "applied.wav", commitApplyLoseRply, 1},
	}
	for _, tc := range commitCases {
		t.Run(tc.name, func(t *testing.T) {
			proxy.commit.Store(tc.fault)
			err := ds.Save(newNote(tc.clip), nil)
			require.Error(t, err)
			require.NotErrorIs(t, err, datastore.ErrDetectionSaveRetrySafe, "the commit outcome is unknown")
			assert.False(t, errors.IsUnsentError(err))
			assert.True(t, errors.IsConnectivityError(err), "a connectivity error alone cannot decide a retry")
			require.Eventually(t, func() bool { return countRows(tc.clip) == tc.wantRows },
				5*time.Second, 50*time.Millisecond, "rows stored for %s", tc.clip)
		})
	}
}

// TestBatchSaveDynamicThresholds_RepeatAfterLostCommit_MySQL proves what the
// processor's threshold save retry relies on: a failed batch save is classified
// as a connectivity error, and repeating it after a COMMIT whose outcome is
// unknown leaves exactly one row per species with the snapshot's values.
// MYSQL_TEST_IMAGE selects the server.
func TestBatchSaveDynamicThresholds_RepeatAfterLostCommit_MySQL(t *testing.T) {
	containers.SkipIfContainerRuntimeUnavailable(t)
	ds, proxy, direct := newFaultProxyStore(t)
	ctx := t.Context()

	const (
		thresholdLevel = 2
		thresholdValue = 0.65
		thresholdBase  = 0.8
		thresholdHours = 48
	)
	snapshot := func(species ...string) []datastore.DynamicThreshold {
		now := time.Now().UTC()
		out := make([]datastore.DynamicThreshold, 0, len(species))
		for _, name := range species {
			out = append(out, datastore.DynamicThreshold{
				SpeciesName:   name,
				Level:         thresholdLevel,
				CurrentValue:  thresholdValue,
				BaseThreshold: thresholdBase,
				ValidHours:    thresholdHours,
				ExpiresAt:     now.Add(thresholdHours * time.Hour),
				LastTriggered: now,
				FirstCreated:  now,
			})
		}
		return out
	}
	countRows := func(species ...string) int {
		var n int
		for _, name := range species {
			var c int
			require.NoError(t, direct.QueryRowContext(ctx, "SELECT COUNT(*) FROM dynamic_thresholds WHERE species_name = ?", name).Scan(&c))
			n += c
		}
		return n
	}

	// Warm the connection setup so the faults below hit the transaction.
	require.NoError(t, ds.BatchSaveDynamicThresholds(snapshot("warmup species")))

	commitCases := []struct {
		name     string
		species  []string
		fault    int32
		wantRows int
	}{
		{"commit_dropped_before_server", []string{"dropped one", "dropped two"}, commitDropUnsent, 0},
		{"commit_applied_reply_lost", []string{"applied one", "applied two"}, commitApplyLoseRply, 2},
	}
	for _, tc := range commitCases {
		t.Run(tc.name, func(t *testing.T) {
			proxy.commit.Store(tc.fault)
			err := ds.BatchSaveDynamicThresholds(snapshot(tc.species...))
			require.Error(t, err)
			assert.True(t, errors.IsConnectivityError(err), "the processor retries on this classification")
			require.Eventually(t, func() bool { return countRows(tc.species...) == tc.wantRows },
				5*time.Second, 50*time.Millisecond, "rows stored for %v", tc.species)

			require.NoError(t, ds.BatchSaveDynamicThresholds(snapshot(tc.species...)), "a repeat once the fault is gone")
			assert.Equal(t, len(tc.species), countRows(tc.species...), "one row per species after the repeat")

			var level int
			var current float64
			require.NoError(t, direct.QueryRowContext(ctx,
				"SELECT level, current_value FROM dynamic_thresholds WHERE species_name = ?", tc.species[0]).Scan(&level, &current))
			assert.Equal(t, thresholdLevel, level)
			assert.InDelta(t, thresholdValue, current, 0.001)
		})
	}

	t.Run("refused_before_write", func(t *testing.T) {
		species := []string{"refused one", "refused two"}
		proxy.stop()
		err := ds.BatchSaveDynamicThresholds(snapshot(species...))
		require.Error(t, err)
		assert.True(t, errors.IsConnectivityError(err), "a refused dial is a connectivity error")
		proxy.restart(t)

		require.NoError(t, ds.BatchSaveDynamicThresholds(snapshot(species...)), "a repeat once the server is back")
		assert.Equal(t, len(species), countRows(species...))
	})
}
