//go:build linux && cgo

package mempolicy

/*
// mallopt and M_ARENA_MAX are glibc malloc tuning hooks, declared here directly
// rather than via <malloc.h> so the file compiles on any libc that links mallopt
// without exposing the glibc extension. musl provides mallopt as a no-op but
// omits M_ARENA_MAX (the value -8 matches glibc); older musl omitted the
// declaration entirely, which this extern restores.
extern int mallopt(int, int);
#ifndef M_ARENA_MAX
#define M_ARENA_MAX -8
#endif
*/
import "C"

// setArenaMax caps glibc malloc arenas via mallopt(M_ARENA_MAX, n), returning
// true on success (glibc returns 1). It must run before the malloc-heavy
// inference threads start to have effect. Profiling shows this is a cheap
// backstop, not a primary memory lever: the dominant cost is loaded model
// weights (see the package doc). On musl the call links and returns 0, so this
// reports the cap was not applied.
func setArenaMax(n int) bool {
	return C.mallopt(C.M_ARENA_MAX, C.int(n)) == 1
}
