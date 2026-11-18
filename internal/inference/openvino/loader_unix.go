//go:build openvino && !windows

package openvino

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

// ovloader_errbuf holds a COPY of the last dlerror() text. dlerror() returns a
// pointer into the loader's own buffer, which dlclose() overwrites, so the text
// is copied in the same C call that failed. It is process-static and written
// only on the single-threaded InitOV path under ovMu, so it needs no locking.
static char ovloader_errbuf[256];

// ovloader_record_error copies the pending dlerror() text into
// ovloader_errbuf. POSIX allows dlerror() to return NULL, so a non-NULL
// fallback keeps the Go caller from mistaking a failure for success.
static void ovloader_record_error(const char* fallback) {
    const char* e = dlerror();
    snprintf(ovloader_errbuf, sizeof(ovloader_errbuf), "%s", e != NULL ? e : fallback);
}

// ovloader_error returns the text recorded by the last failed call.
static const char* ovloader_error(void) {
    return ovloader_errbuf;
}

// ovloader_open dlopens path with RTLD_NOW | RTLD_GLOBAL. RTLD_GLOBAL lets the
// OpenVINO core find its plugin shared objects (CPU plugin, tbb) through the
// standard loader search, exactly as the Python wheel and the spike's
// LD_LIBRARY_PATH arrangement do. Returns 0 on failure.
static uintptr_t ovloader_open(const char* path) {
    void* h = dlopen(path, RTLD_NOW | RTLD_GLOBAL);
    if (h == NULL) {
        ovloader_record_error("dlopen failed (no dlerror message)");
    }
    return (uintptr_t)h;
}

// ovloader_sym resolves name in the library handle. Returns 0 on failure.
static uintptr_t ovloader_sym(uintptr_t handle, const char* name) {
    void* p = dlsym((void*)handle, name);
    if (p == NULL) {
        ovloader_record_error("dlsym failed");
    }
    return (uintptr_t)p;
}

// ovloader_close releases the library handle.
static void ovloader_close(uintptr_t handle) {
    dlclose((void*)handle);
}
*/
import "C" //nolint:gocritic // dupImport: cgo import "C" must be separate from regular imports

import "unsafe" //nolint:gocritic // dupImport: false positive with cgo import "C"

import (
	"github.com/tphakala/birdnet-go/internal/errors"
)

// openLibrary loads the shared library at path, or DefaultLibraryName by soname
// when path is empty, and returns its handle. The error text is the raw
// dlerror() string so callers can match on it (isLibraryAbsent in
// internal/classifier).
func openLibrary(path string) (uintptr, error) {
	if path == "" {
		path = DefaultLibraryName
	}
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	h := uintptr(C.ovloader_open(cPath))
	if h == 0 {
		return 0, errors.NewStd(C.GoString(C.ovloader_error()))
	}
	return h, nil
}

// lookupSymbol returns the address of the exported symbol name in the library
// handle. The error text is the raw dlerror() string.
func lookupSymbol(handle uintptr, name string) (uintptr, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	addr := uintptr(C.ovloader_sym(C.uintptr_t(handle), cName))
	if addr == 0 {
		return 0, errors.NewStd(C.GoString(C.ovloader_error()))
	}
	return addr, nil
}

// closeLibrary releases a handle returned by openLibrary. bindLibrary calls it
// only when symbol resolution fails; a successfully bound library is never
// closed.
func closeLibrary(handle uintptr) {
	C.ovloader_close(C.uintptr_t(handle))
}
