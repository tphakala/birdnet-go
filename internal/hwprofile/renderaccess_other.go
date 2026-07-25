//go:build !unix

package hwprofile

// canAccessReadWrite reports whether this process may open path for reading and
// writing. DRM render nodes are a Linux concept and no non-unix host enumerates
// one, so this is only ever called with a path that does not exist; it reports
// false rather than pretending to know.
func canAccessReadWrite(_ string) bool { return false }
