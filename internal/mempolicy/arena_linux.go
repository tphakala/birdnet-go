//go:build linux && cgo

package mempolicy

/*
#include <malloc.h>

// M_ARENA_MAX is a glibc extension. All of this project's Linux build targets
// are glibc (Debian); the guard keeps the file compilable on a libc that omits
// the constant (e.g. musl), where mallopt is a no-op and the call reports failure.
#ifndef M_ARENA_MAX
#define M_ARENA_MAX -8
#endif
*/
import "C"

// setArenaMax caps glibc malloc arenas via mallopt(M_ARENA_MAX, n), returning
// true on success (glibc returns 1). It must run before the malloc-heavy
// inference threads start to have effect. Profiling shows this is a cheap
// backstop, not a primary memory lever: the dominant cost is loaded model
// weights (see the package doc).
func setArenaMax(n int) bool {
	return C.mallopt(C.M_ARENA_MAX, C.int(n)) == 1
}
