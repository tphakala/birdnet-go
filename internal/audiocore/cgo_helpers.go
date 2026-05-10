package audiocore

// #include <stdlib.h>
import "C" //nolint:gocritic // dupImport: cgo import "C" must be separate from regular imports

import "unsafe" //nolint:gocritic // dupImport: false positive with cgo import "C"

// freeDeviceIDPtr releases C heap memory allocated by DeviceID.Pointer().
// DeviceID.Pointer() calls C.CBytes which allocates on the C heap;
// callers must free this memory when it is no longer needed.
func freeDeviceIDPtr(ptr unsafe.Pointer) {
	if ptr != nil {
		C.free(ptr)
	}
}
