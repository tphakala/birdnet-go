package support

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	// trustedProxiesKey and plainHTTPSessionClientsKey are the lowercase yaml
	// leaf keys of Security.TrustedProxies and Security.PlainHTTPSessionClients.
	trustedProxiesKey          = "trustedproxies"
	plainHTTPSessionClientsKey = "plainhttpsessionclients"

	addressScopeLoopback    = "loopback"
	addressScopePrivate     = "private"
	addressScopeLinkLocal   = "link-local"
	addressScopeUnspecified = "unspecified"
	addressScopePublic      = "public"
	addressKindAddress      = "address"
	addressKindRange        = "range"

	// redactedAddressFormat renders an address class, for example
	// "[redacted private range]".
	redactedAddressFormat = "[redacted %s %s]"
)

// isAddressListKey reports whether a lowercase config key holds a list of IP
// addresses or ranges that should be reported by class instead of blanked.
func isAddressListKey(lowerKey string) bool {
	return lowerKey == trustedProxiesKey || lowerKey == plainHTTPSessionClientsKey
}

// formatRedactedAddress renders the placeholder for an address scope and kind.
func formatRedactedAddress(scope, kind string) string {
	return fmt.Sprintf(redactedAddressFormat, scope, kind)
}

// redactAddressList keeps the shape of an address list (length and order) but
// replaces each entry with its address class. Support needs the topology (is
// the proxy private or public, is it a single host or a range), not the
// addresses themselves. A scalar string is handled too, because the dump reads
// raw YAML rather than the typed struct. Any other shape (a map, a number, a
// map or nested list as an item) is blanked whole, since recursing into it
// would keep values under keys that are not sensitive.
func redactAddressList(value any) any {
	if isDefaultValue(value) {
		return value
	}

	switch v := value.(type) {
	case string:
		return classifyAddressEntry(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			if s, ok := item.(string); ok {
				out[i] = classifyAddressEntry(s)
			} else {
				out[i] = redactedPlaceholder
			}
		}
		return out
	default:
		return redactedPlaceholder
	}
}

// classifyAddressEntry maps one list entry to a fixed placeholder. Empty
// entries pass through, and the reserved cloudflare preset is kept because it
// is a fixed token, not user data. Anything that is not an address or a prefix
// becomes the plain redacted placeholder.
func classifyAddressEntry(entry string) string {
	trimmed := strings.TrimSpace(entry)
	if trimmed == "" {
		return entry
	}
	if strings.EqualFold(trimmed, conf.TrustedProxyCloudflarePreset) {
		return conf.TrustedProxyCloudflarePreset
	}

	if prefix, err := netip.ParsePrefix(trimmed); err == nil {
		kind := addressKindRange
		if prefix.IsSingleIP() {
			kind = addressKindAddress
		}
		return formatRedactedAddress(rangeScope(prefix), kind)
	}
	if addr, err := netip.ParseAddr(trimmed); err == nil {
		return formatRedactedAddress(addressScope(addr), addressKindAddress)
	}
	return redactedPlaceholder
}

// addressScope classifies an address by reachability class. IPv4-mapped IPv6
// addresses are classified by their IPv4 form.
func addressScope(addr netip.Addr) string {
	addr = addr.Unmap()
	switch {
	case addr.IsLoopback():
		return addressScopeLoopback
	case addr.IsPrivate():
		return addressScopePrivate
	case addr.IsLinkLocalUnicast():
		return addressScopeLinkLocal
	case addr.IsUnspecified():
		return addressScopeUnspecified
	default:
		return addressScopePublic
	}
}

// rangeScope classifies a prefix by its first and last address. A range whose
// ends fall in different scopes reaches beyond the lower scope into public
// space, so it is reported as public rather than by its base address.
func rangeScope(prefix netip.Prefix) string {
	first := prefix.Masked().Addr()
	scope := addressScope(first)
	if prefix.IsSingleIP() {
		return scope
	}
	if lastScope := addressScope(lastAddr(prefix)); lastScope != scope {
		return addressScopePublic
	}
	return scope
}

// lastAddr returns the highest address of a masked prefix.
func lastAddr(prefix netip.Prefix) netip.Addr {
	addr := prefix.Masked().Addr()
	raw := addr.AsSlice()
	bits := prefix.Bits()
	for i := range raw {
		hostBits := min(max(8*(i+1)-bits, 0), 8)
		raw[i] |= byte(1<<hostBits - 1)
	}
	last, _ := netip.AddrFromSlice(raw)
	return last
}
