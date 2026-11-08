package datastore

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

// ipv6DialWait bounds how long a test waits for the fake server to see a
// connection that the client under test should have made.
const ipv6DialWait = 5 * time.Second

func TestMySQLAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
		port string
		want string
	}{
		{"hostname", "db.example", "3306", "db.example:3306"},
		{"unbracketed IPv6 literal", "::1", "3306", "[::1]:3306"},
		{"bracketed IPv6 literal is not double bracketed", "[::1]", "3306", "[::1]:3306"},
		{"empty host keeps the old join", "", "3306", ":3306"},
		{"lone opening bracket is not stripped", "[", "3306", "[:3306"},
		{"opening bracket without closing bracket is not stripped", "[::1", "3306", "[[::1]:3306"},
		{"closing bracket without opening bracket is not stripped", "::1]", "3306", "[::1]]:3306"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, MySQLAddr(tt.host, tt.port))
		})
	}
}

// TestMySQLStoreGetDatabaseStats_LocationBracketsIPv6Host pins the location
// shown for a store that is not connected: an IPv6 literal host is bracketed
// once in both input forms and a hostname is shown as before.
func TestMySQLStoreGetDatabaseStats_LocationBracketsIPv6Host(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host string
		want string
	}{
		{"::1", "[::1]:3306/d"},
		{"[::1]", "[::1]:3306/d"},
		{"db.example", "db.example:3306/d"},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Parallel()
			settings := &conf.Settings{}
			settings.Output.MySQL.Host = tt.host
			settings.Output.MySQL.Port = "3306"
			settings.Output.MySQL.Database = "d"

			store := &MySQLStore{Settings: settings}
			stats, err := store.GetDatabaseStats(t.Context())
			require.ErrorIs(t, err, ErrDBNotConnected)
			assert.Equal(t, tt.want, stats.Location)
		})
	}
}

// TestMySQLStoreOpen_DialsIPv6LiteralHost pins that the legacy store dials an
// IPv6 literal host in both the bare and the bracketed form. The fake server
// drops the connection, so Open fails; the test checks that it got there.
func TestMySQLStoreOpen_DialsIPv6LiteralHost(t *testing.T) {
	t.Parallel()

	for _, host := range []string{"::1", "[::1]"} {
		t.Run(host, func(t *testing.T) {
			t.Parallel()
			port, accepted := testutil.ListenIPv6Loopback(t)

			settings := &conf.Settings{}
			settings.Output.MySQL.Enabled = true
			settings.Output.MySQL.Host = host
			settings.Output.MySQL.Port = port
			settings.Output.MySQL.Username = "u"
			settings.Output.MySQL.Password = "p"
			settings.Output.MySQL.Database = "d"

			store := &MySQLStore{Settings: settings}
			require.Error(t, store.Open())

			testutil.WaitForChannel(t, accepted, ipv6DialWait,
				fmt.Sprintf("no connection reached the IPv6 loopback listener for host %q", host))
		})
	}
}
