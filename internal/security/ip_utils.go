package security

import (
	"net"
	"strings"
)

// parseIPWithZone parses an IP address string, stripping any IPv6 zone ID
// (the "%interface" suffix, e.g., "%eth0", "%wlan0") before parsing.
// Go's net.ParseIP does not handle zone IDs (RFC 6874), so clients
// connecting via IPv6 link-local addresses would otherwise fail to parse.
func parseIPWithZone(ipStr string) net.IP {
	if before, _, found := strings.Cut(ipStr, "%"); found {
		ipStr = before
	}
	return net.ParseIP(ipStr)
}
