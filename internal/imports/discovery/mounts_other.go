//go:build !linux

package discovery

// defaultNetworkMountPrefixes returns nil on non-linux platforms; the v1
// scanner only ships network-mount filtering for Linux.
func defaultNetworkMountPrefixes() []string { return nil }
