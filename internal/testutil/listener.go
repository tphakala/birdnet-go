package testutil

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// ipv6LoopbackListenAddr asks the OS for a free port on the IPv6 loopback.
const ipv6LoopbackListenAddr = "[::1]:0"

// ListenIPv6Loopback starts a TCP listener on the IPv6 loopback ([::1]) that
// accepts connections and closes them at once, so a client sees a dropped
// connection instead of a protocol handshake. It returns the listener port and
// a channel that receives a value for every connection accepted (buffered, so
// a client that never waits cannot block the listener).
//
// The test is skipped when the host has no IPv6 loopback. The listener is
// closed when the test ends.
func ListenIPv6Loopback(t *testing.T) (port string, accepted <-chan struct{}) {
	t.Helper()

	listener, err := net.Listen("tcp", ipv6LoopbackListenAddr)
	if err != nil {
		t.Skipf("IPv6 loopback is not available: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	_, port, err = net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)

	const acceptedBuffer = 16
	ch := make(chan struct{}, acceptedBuffer)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return // listener closed by the cleanup
			}
			select {
			case ch <- struct{}{}:
			default:
			}
			_ = conn.Close()
		}
	}()
	return port, ch
}
