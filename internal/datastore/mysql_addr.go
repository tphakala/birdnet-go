package datastore

import (
	"net"
	"strings"
)

// Delimiters of an IPv6 literal written in URL form, such as "[::1]".
const (
	ipv6LiteralOpen  = "["
	ipv6LiteralClose = "]"
)

// MySQLAddr joins a MySQL host and port into the address used inside a DSN
// "tcp(...)" section or shown as a location. It accepts hostnames, IPv4
// addresses and IPv6 literals written either bare ("::1") or in URL form
// ("[::1]"); an IPv6 host always comes out bracketed exactly once ("[::1]:3306").
// One surrounding pair of brackets is removed before joining so a host that is
// already bracketed is not bracketed twice. An empty host or port is passed
// through unchanged ("" and "3306" give ":3306").
func MySQLAddr(host, port string) string {
	if inner, ok := strings.CutPrefix(host, ipv6LiteralOpen); ok {
		if inner, ok = strings.CutSuffix(inner, ipv6LiteralClose); ok {
			host = inner
		}
	}
	return net.JoinHostPort(host, port)
}
