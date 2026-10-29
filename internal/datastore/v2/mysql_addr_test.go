package v2

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/testutil"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// ipv6DialWait bounds how long a test waits for the fake server to see a
// connection that the client under test should have made.
const ipv6DialWait = 5 * time.Second

// TestMySQLConnections_DialIPv6LiteralHost pins that both v2 MySQL DSN
// builders dial an IPv6 literal host in the bare and the bracketed form. The
// fake server drops the connection, so every open fails; the test checks that
// the connection arrived.
func TestMySQLConnections_DialIPv6LiteralHost(t *testing.T) {
	t.Parallel()

	builders := []struct {
		name string
		open func(cfg *MySQLConfig) error
	}{
		{"startup DSN", func(cfg *MySQLConfig) error {
			dsn := buildMySQLStartupDSN(mysqlTestSettings(cfg))
			_, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{
				Logger: gormlogger.Default.LogMode(gormlogger.Silent),
			})
			return err
		}},
		{"MySQL manager", func(cfg *MySQLConfig) error {
			_, err := NewMySQLManager(cfg)
			return err
		}},
	}

	for _, builder := range builders {
		for _, host := range []string{"::1", "[::1]"} {
			t.Run(builder.name+"/"+host, func(t *testing.T) {
				t.Parallel()
				port, accepted := testutil.ListenIPv6Loopback(t)

				err := builder.open(&MySQLConfig{
					Host: host, Port: port, Username: "u", Password: "p", Database: "d",
				})
				require.Error(t, err)

				testutil.WaitForChannel(t, accepted, ipv6DialWait,
					fmt.Sprintf("no connection reached the IPv6 loopback listener for host %q", host))
			})
		}
	}
}
