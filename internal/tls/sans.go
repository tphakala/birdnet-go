package tls

import (
	"net"
	"net/url"
	"strings"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// maxHostnameLen is the maximum allowed length for a hostname per RFC 1035.
const maxHostnameLen = 253

// CollectSANs gathers Subject Alternative Names from the provided host, baseURL,
// local network interfaces, and always includes localhost and 127.0.0.1.
// Duplicate entries are removed.
func CollectSANs(host, baseURL string) []string {
	seen := make(map[string]struct{})
	sans := make([]string, 0, 8)

	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if !isValidHostname(s) {
			log := logger.Global().Module("tls")
			if log != nil {
				log.Warn("skipping invalid SAN entry", logger.String("san", s))
			}
			return
		}
		if _, exists := seen[s]; !exists {
			seen[s] = struct{}{}
			sans = append(sans, s)
		}
	}

	// Add configured host
	if host != "" {
		add(host)
	}

	// Extract hostname from baseURL
	if baseURL != "" {
		if parsed, err := url.Parse(baseURL); err == nil && parsed.Hostname() != "" {
			add(parsed.Hostname())
		}
	}

	// Add non-loopback IPv4 addresses from local interfaces
	addInterfaceAddresses(add)

	// Always include localhost and loopback
	add("localhost")
	add("127.0.0.1")

	return sans
}

// addInterfaceAddresses adds non-loopback IPv4 addresses from network interfaces.
func addInterfaceAddresses(add func(string)) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		log := logger.Global().Module("tls")
		if log != nil {
			log.Warn("failed to get network interface addresses", logger.String("error", err.Error()))
		}
		return
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP
		if ip.IsLoopback() || ip.To4() == nil {
			continue
		}
		add(ip.String())
	}
}

// isValidHostname checks whether s is a valid hostname or IP address for use as a SAN.
func isValidHostname(s string) bool {
	if len(s) > maxHostnameLen {
		return false
	}
	if strings.ContainsAny(s, " \t\n\r") {
		return false
	}
	return true
}
