//go:build openvino

// This file holds the real OpenVINO backend, compiled only under the "openvino"
// build tag. It ports the validated Phase 0 cgo spike, swapping the spike's
// link-time linking for a runtime dlopen + dlsym of libopenvino_c. The package
// links against -ldl only: linking -lopenvino_c at build time would make ld.so
// abort the process before main() when the library is absent, which would
// defeat the graceful fallback to the ONNX Runtime backend that this feature
// depends on. Every OpenVINO function is therefore resolved through a process
// global symbol table (OVB) and called via fixed-arity static C wrappers so
// that cgo never calls a C function pointer (or a C variadic function) directly
// with Go arguments.

package openvino

/*
#cgo LDFLAGS: -ldl
#include <stdlib.h>
#include <stdio.h>
#include <string.h>
#include <dlfcn.h>
#include <openvino/c/openvino.h>

// Function-pointer typedefs for every OpenVINO C entry point we call. The C
// header declares these symbols, but we never reference them by name at link
// time; we resolve each one with dlsym so the binary keeps loading when
// libopenvino_c is absent.
typedef ov_status_e (*fn_core_create)(ov_core_t**);
typedef void        (*fn_core_free)(ov_core_t*);
typedef ov_status_e (*fn_core_read_model)(const ov_core_t*, const char*, const char*, ov_model_t**);
typedef ov_status_e (*fn_core_compile_model)(const ov_core_t*, const ov_model_t*, const char*, const size_t, ov_compiled_model_t**, ...);
typedef void        (*fn_model_free)(ov_model_t*);
typedef ov_status_e (*fn_model_const_input)(const ov_model_t*, ov_output_const_port_t**);
typedef ov_status_e (*fn_port_get_partial_shape)(const ov_output_const_port_t*, ov_partial_shape_t*);
typedef void        (*fn_output_const_port_free)(ov_output_const_port_t*);
typedef ov_status_e (*fn_partial_shape_create)(const int64_t, const ov_dimension_t*, ov_partial_shape_t*);
typedef ov_status_e (*fn_model_reshape_single_input)(const ov_model_t*, const ov_partial_shape_t);
typedef void        (*fn_partial_shape_free)(ov_partial_shape_t*);
typedef ov_status_e (*fn_compiled_model_create_infer_request)(const ov_compiled_model_t*, ov_infer_request_t**);
typedef void        (*fn_compiled_model_free)(ov_compiled_model_t*);
typedef ov_status_e (*fn_shape_create)(const int64_t, const int64_t*, ov_shape_t*);
typedef ov_status_e (*fn_shape_free)(ov_shape_t*);
typedef ov_status_e (*fn_tensor_create_from_host_ptr)(const ov_element_type_e, const ov_shape_t, void*, ov_tensor_t**);
typedef ov_status_e (*fn_tensor_get_size)(const ov_tensor_t*, size_t*);
typedef ov_status_e (*fn_tensor_data)(const ov_tensor_t*, void**);
typedef void        (*fn_tensor_free)(ov_tensor_t*);
typedef ov_status_e (*fn_infer_request_set_input_tensor_by_index)(ov_infer_request_t*, const size_t, const ov_tensor_t*);
typedef ov_status_e (*fn_infer_request_infer)(ov_infer_request_t*);
typedef ov_status_e (*fn_infer_request_get_output_tensor_by_index)(const ov_infer_request_t*, const size_t, ov_tensor_t**);
typedef void        (*fn_infer_request_free)(ov_infer_request_t*);
typedef const char* (*fn_get_last_err_msg)(void);

// ovbind_t is the process-global table of resolved OpenVINO symbols, filled by
// ovbind_load(path).
typedef struct {
    void* handle;
    fn_core_create                              core_create;
    fn_core_free                                core_free;
    fn_core_read_model                          core_read_model;
    fn_core_compile_model                       core_compile_model;
    fn_model_free                               model_free;
    fn_model_const_input                        model_const_input;
    fn_port_get_partial_shape                   port_get_partial_shape;
    fn_output_const_port_free                   output_const_port_free;
    fn_partial_shape_create                     partial_shape_create;
    fn_model_reshape_single_input               model_reshape_single_input;
    fn_partial_shape_free                       partial_shape_free;
    fn_compiled_model_create_infer_request      compiled_model_create_infer_request;
    fn_compiled_model_free                      compiled_model_free;
    fn_shape_create                             shape_create;
    fn_shape_free                               shape_free;
    fn_tensor_create_from_host_ptr              tensor_create_from_host_ptr;
    fn_tensor_get_size                          tensor_get_size;
    fn_tensor_data                              tensor_data;
    fn_tensor_free                              tensor_free;
    fn_infer_request_set_input_tensor_by_index  infer_request_set_input_tensor_by_index;
    fn_infer_request_infer                      infer_request_infer;
    fn_infer_request_get_output_tensor_by_index infer_request_get_output_tensor_by_index;
    fn_infer_request_free                       infer_request_free;
    fn_get_last_err_msg                         get_last_err_msg;
} ovbind_t;

static ovbind_t OVB; // process-global resolved symbol table

// _ovbind_errbuf holds a COPY of the dlsym failure message. dlerror() returns a
// pointer into libdl's own static buffer, which dlclose() overwrites, so the
// macro copies the string bytes here before dlclose and returns this buffer.
// memset of OVB does not touch it. It is process-static and written only on the
// single-threaded InitOV failure path under ovMu, so it needs no locking.
static char _ovbind_errbuf[256];

// OVBIND_RESOLVE resolves one symbol into OVB. On failure it COPIES the
// dlerror() string into _ovbind_errbuf FIRST (the pointer dlerror returns is
// into libdl's static buffer, which dlclose overwrites), then dlcloses the
// handle and zeroes the ENTIRE OVB table so that OVB.handle is non-NULL if and
// only if every symbol is resolved. Without this, a partial resolution (e.g. an
// older libopenvino_c missing a newer symbol) would leave OVB.handle set, and a
// later InitOV retry would see the handle, skip re-resolution, and later call a
// NULL function pointer. Returns the copied error string from ovbind_load.
#define OVBIND_RESOLVE(field, type, name)                                     \
    do {                                                                      \
        OVB.field = (type)dlsym(OVB.handle, name);                            \
        if (!OVB.field) {                                                     \
            const char* _e = dlerror();                                       \
            snprintf(_ovbind_errbuf, sizeof(_ovbind_errbuf), "%s",            \
                     _e ? _e : "dlsym failed");                               \
            dlclose(OVB.handle);                                              \
            memset(&OVB, 0, sizeof(OVB));                                     \
            return _ovbind_errbuf;                                            \
        }                                                                     \
    } while (0)

// ovbind_load dlopens libopenvino_c from path (or by soname when empty) with
// RTLD_NOW | RTLD_GLOBAL. RTLD_GLOBAL lets the OpenVINO core find its plugin
// shared objects (CPU plugin, tbb) through the standard loader search, exactly
// as the Python wheel and the spike's LD_LIBRARY_PATH arrangement do. It then
// resolves every symbol the backend calls. Returns NULL on success, or the
// first dlerror() string on failure.
static const char* ovbind_load(const char* path) {
    if (OVB.handle) {
        return NULL; // already loaded
    }
    OVB.handle = dlopen(path && path[0] ? path : "libopenvino_c.so", RTLD_NOW | RTLD_GLOBAL);
    if (!OVB.handle) {
        return dlerror();
    }
    OVBIND_RESOLVE(core_create,                              fn_core_create,                              "ov_core_create");
    OVBIND_RESOLVE(core_free,                                fn_core_free,                                "ov_core_free");
    OVBIND_RESOLVE(core_read_model,                          fn_core_read_model,                          "ov_core_read_model");
    OVBIND_RESOLVE(core_compile_model,                       fn_core_compile_model,                       "ov_core_compile_model");
    OVBIND_RESOLVE(model_free,                               fn_model_free,                               "ov_model_free");
    OVBIND_RESOLVE(model_const_input,                        fn_model_const_input,                        "ov_model_const_input");
    OVBIND_RESOLVE(port_get_partial_shape,                   fn_port_get_partial_shape,                   "ov_port_get_partial_shape");
    OVBIND_RESOLVE(output_const_port_free,                   fn_output_const_port_free,                   "ov_output_const_port_free");
    OVBIND_RESOLVE(partial_shape_create,                     fn_partial_shape_create,                     "ov_partial_shape_create");
    OVBIND_RESOLVE(model_reshape_single_input,               fn_model_reshape_single_input,               "ov_model_reshape_single_input");
    OVBIND_RESOLVE(partial_shape_free,                       fn_partial_shape_free,                       "ov_partial_shape_free");
    OVBIND_RESOLVE(compiled_model_create_infer_request,      fn_compiled_model_create_infer_request,      "ov_compiled_model_create_infer_request");
    OVBIND_RESOLVE(compiled_model_free,                      fn_compiled_model_free,                      "ov_compiled_model_free");
    OVBIND_RESOLVE(shape_create,                             fn_shape_create,                             "ov_shape_create");
    OVBIND_RESOLVE(shape_free,                               fn_shape_free,                               "ov_shape_free");
    OVBIND_RESOLVE(tensor_create_from_host_ptr,              fn_tensor_create_from_host_ptr,              "ov_tensor_create_from_host_ptr");
    OVBIND_RESOLVE(tensor_get_size,                          fn_tensor_get_size,                          "ov_tensor_get_size");
    OVBIND_RESOLVE(tensor_data,                              fn_tensor_data,                              "ov_tensor_data");
    OVBIND_RESOLVE(tensor_free,                              fn_tensor_free,                              "ov_tensor_free");
    OVBIND_RESOLVE(infer_request_set_input_tensor_by_index,  fn_infer_request_set_input_tensor_by_index,  "ov_infer_request_set_input_tensor_by_index");
    OVBIND_RESOLVE(infer_request_infer,                      fn_infer_request_infer,                      "ov_infer_request_infer");
    OVBIND_RESOLVE(infer_request_get_output_tensor_by_index, fn_infer_request_get_output_tensor_by_index, "ov_infer_request_get_output_tensor_by_index");
    OVBIND_RESOLVE(infer_request_free,                       fn_infer_request_free,                       "ov_infer_request_free");
    OVBIND_RESOLVE(get_last_err_msg,                         fn_get_last_err_msg,                         "ov_get_last_err_msg");
    return NULL;
}

// Fixed-arity static wrappers. Go calls these, never an OVB.<fn> pointer
// directly, so cgo always sees an ordinary C function.

static ov_status_e ovbind_core_create(ov_core_t** core) {
    return OVB.core_create(core);
}

static void ovbind_core_free(ov_core_t* core) {
    OVB.core_free(core);
}

static ov_status_e ovbind_core_read_model(const ov_core_t* core, const char* model_path, ov_model_t** model) {
    return OVB.core_read_model(core, model_path, NULL, model);
}

// ovbind_compile wraps the variadic ov_core_compile_model (cgo cannot call C
// variadic functions). Ported verbatim from the spike's ovspike_compile: each
// property is a (key, value) pair, so property_args_size is 2 per pair.
static ov_status_e ovbind_compile(const ov_core_t* core, const ov_model_t* model,
                                  const char* dev, const char* prec, const char* nthreads,
                                  ov_compiled_model_t** out) {
    if (nthreads != NULL && nthreads[0] != '\0') {
        return OVB.core_compile_model(core, model, dev, 4, out,
                                      "INFERENCE_PRECISION_HINT", prec,
                                      "INFERENCE_NUM_THREADS", nthreads);
    }
    return OVB.core_compile_model(core, model, dev, 2, out,
                                  "INFERENCE_PRECISION_HINT", prec);
}

static void ovbind_model_free(ov_model_t* model) {
    OVB.model_free(model);
}

// OVBIND_RESHAPE_RANK is the expected input rank [batch, samples].
#define OVBIND_RESHAPE_RANK 2
// OVBIND_BATCH_DIM_INDEX / OVBIND_SAMPLE_DIM_INDEX index the input dimensions.
#define OVBIND_BATCH_DIM_INDEX 0
#define OVBIND_SAMPLE_DIM_INDEX 1
// OVBIND_ERR_BAD_INPUT_SHAPE is returned when the input is not a rank-2 shape
// with a static sample dimension. It is deliberately far outside the ov_status_e
// range (OK..-17, all small negatives) so it cannot collide with a genuine
// OpenVINO status, letting the Go caller distinguish "bad model shape" from a
// real reshape failure and report a clear error instead of a stale
// ov_get_last_err_msg. The model is rejected and the caller falls back to ORT.
#define OVBIND_ERR_BAD_INPUT_SHAPE (-1000)

// ovbind_reshape_static_batch1 reshapes the model's single input to a static
// [1, samples] shape and writes the derived sample count to *out_samples. The
// BirdNET v2.4 model has a dynamic batch dimension, so the partial input shape
// (not the static shape) must be read; only the sample dimension is static.
// All ov_partial_shape_t / ov_output_const_port_t handling stays in C so cgo
// never touches the dynamically allocated dims arrays. Returns OK on success.
static ov_status_e ovbind_reshape_static_batch1(const ov_model_t* model, int64_t* out_samples) {
    ov_output_const_port_t* port = NULL;
    ov_status_e st = OVB.model_const_input(model, &port);
    if (st != OK) {
        return st;
    }

    ov_partial_shape_t ps;
    st = OVB.port_get_partial_shape(port, &ps);
    OVB.output_const_port_free(port);
    if (st != OK) {
        return st;
    }

    // Require a static rank of 2 and a static (min == max), positive sample
    // dimension. ov_rank_t is an ov_dimension_t, so a static rank has min == max.
    if (ps.rank.min != ps.rank.max || ps.rank.max != OVBIND_RESHAPE_RANK ||
        ps.dims[OVBIND_SAMPLE_DIM_INDEX].min != ps.dims[OVBIND_SAMPLE_DIM_INDEX].max ||
        ps.dims[OVBIND_SAMPLE_DIM_INDEX].min <= 0) {
        OVB.partial_shape_free(&ps);
        return OVBIND_ERR_BAD_INPUT_SHAPE;
    }
    int64_t samples = ps.dims[OVBIND_SAMPLE_DIM_INDEX].max;
    OVB.partial_shape_free(&ps);

    ov_dimension_t nd[OVBIND_RESHAPE_RANK];
    nd[OVBIND_BATCH_DIM_INDEX].min = 1;
    nd[OVBIND_BATCH_DIM_INDEX].max = 1;
    nd[OVBIND_SAMPLE_DIM_INDEX].min = samples;
    nd[OVBIND_SAMPLE_DIM_INDEX].max = samples;

    ov_partial_shape_t nps;
    st = OVB.partial_shape_create(OVBIND_RESHAPE_RANK, nd, &nps);
    if (st != OK) {
        return st;
    }
    st = OVB.model_reshape_single_input(model, nps);
    OVB.partial_shape_free(&nps);
    if (st == OK) {
        *out_samples = samples;
    }
    return st;
}

static ov_status_e ovbind_compiled_model_create_infer_request(const ov_compiled_model_t* compiled, ov_infer_request_t** req) {
    return OVB.compiled_model_create_infer_request(compiled, req);
}

static void ovbind_compiled_model_free(ov_compiled_model_t* compiled) {
    OVB.compiled_model_free(compiled);
}

static ov_status_e ovbind_shape_create(const int64_t rank, const int64_t* dims, ov_shape_t* shape) {
    return OVB.shape_create(rank, dims, shape);
}

static ov_status_e ovbind_shape_free(ov_shape_t* shape) {
    return OVB.shape_free(shape);
}

static ov_status_e ovbind_tensor_create_from_host_ptr(const ov_element_type_e type, const ov_shape_t shape, void* host_ptr, ov_tensor_t** tensor) {
    return OVB.tensor_create_from_host_ptr(type, shape, host_ptr, tensor);
}

static ov_status_e ovbind_tensor_get_size(const ov_tensor_t* tensor, size_t* size) {
    return OVB.tensor_get_size(tensor, size);
}

static ov_status_e ovbind_tensor_data(const ov_tensor_t* tensor, void** data) {
    return OVB.tensor_data(tensor, data);
}

static void ovbind_tensor_free(ov_tensor_t* tensor) {
    OVB.tensor_free(tensor);
}

static ov_status_e ovbind_set_input_tensor(ov_infer_request_t* req, const size_t idx, const ov_tensor_t* tensor) {
    return OVB.infer_request_set_input_tensor_by_index(req, idx, tensor);
}

static ov_status_e ovbind_infer(ov_infer_request_t* req) {
    return OVB.infer_request_infer(req);
}

static ov_status_e ovbind_get_output_tensor(const ov_infer_request_t* req, const size_t idx, ov_tensor_t** tensor) {
    return OVB.infer_request_get_output_tensor_by_index(req, idx, tensor);
}

static void ovbind_infer_request_free(ov_infer_request_t* req) {
    OVB.infer_request_free(req);
}

static const char* ovbind_get_last_err_msg(void) {
    return OVB.get_last_err_msg();
}
*/
import "C" //nolint:gocritic // dupImport: cgo import "C" must be separate from regular imports

import "unsafe" //nolint:gocritic // dupImport: false positive with cgo import "C"

import (
	"strconv"
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// deviceCPU is the OpenVINO device name the backend targets. ARMv8.2 f16
	// acceleration runs on the CPU plugin.
	deviceCPU = "CPU"
	// inputPortIndex is the index of the single audio input tensor.
	inputPortIndex = 0
	// outputPortIndex is the index of the single logits output tensor.
	outputPortIndex = 0
	// expectedInputRank is the rank of the model input shape, [batch, samples].
	expectedInputRank = 2
	// float32Bytes is the size in bytes of one float32 element.
	float32Bytes = 4
)

// ovOK reports whether an OpenVINO status code is success (OK == 0). It is a
// helper so the success comparison appears once rather than at every call site
// (and avoids a gocritic dupSubExpr false positive in the cgo-rewritten file).
func ovOK(status C.ov_status_e) bool {
	return status == C.OK
}

var (
	// ovMu guards the process-global core and its loaded flag. It serializes
	// InitOV and DestroyOV only; per-classifier inference is unsynchronized and
	// callers must serialize Classifier access (see the Classifier doc).
	ovMu sync.Mutex
	// ovLoaded reports whether the library is loaded and the core is created.
	ovLoaded bool
	// ovCore is the process-global ov_core_t shared by all classifiers.
	ovCore *C.ov_core_t
)

// lastErr builds an EnhancedError from the OpenVINO status code and the
// library's last error message, prefixed by the failing operation.
func lastErr(op string, status C.ov_status_e) error {
	msg := C.GoString(C.ovbind_get_last_err_msg())
	return errors.Newf("openvino: %s failed: status=%d: %s", op, int(status), msg).Build()
}

// InitOV dlopens libopenvino_c (libraryPath, or by soname when empty) and
// creates the process-global core. It is idempotent and safe to retry after a
// failure so hot-reload recovery can re-attempt with a corrected path. It
// mirrors inference.InitONNXRuntime semantics. The core is freed only by
// DestroyOV, never on a model reload.
func InitOV(libraryPath string) error {
	ovMu.Lock()
	defer ovMu.Unlock()
	if ovLoaded {
		return nil
	}

	cPath := C.CString(libraryPath)
	defer C.free(unsafe.Pointer(cPath))
	if msg := C.ovbind_load(cPath); msg != nil {
		return errors.Newf("openvino: failed to load libopenvino_c: %s", C.GoString(msg)).Build()
	}

	var core *C.ov_core_t
	if st := C.ovbind_core_create(&core); !ovOK(st) {
		return lastErr("ov_core_create", st)
	}
	ovCore = core
	ovLoaded = true
	return nil
}

// DestroyOV frees the process-global core and resets state so InitOV can run
// again. Call it only on application shutdown or a catastrophic re-init, never
// on a routine model reload (that path uses Classifier.Close). It returns nil
// when nothing is loaded.
func DestroyOV() error {
	ovMu.Lock()
	defer ovMu.Unlock()
	if !ovLoaded {
		return nil
	}
	if ovCore != nil {
		C.ovbind_core_free(ovCore)
		ovCore = nil
	}
	ovLoaded = false
	// The dlopen handle (OVB.handle) is intentionally NOT dlclosed here: it is
	// kept open for the process lifetime so the resolved symbol table is reused
	// if InitOV runs again. Do not add a dlclose here; that would reintroduce a
	// partial-state inconsistency and break the load invariant.
	return nil
}

// classifier holds the per-model OpenVINO state: a compiled model, one infer
// request, and a C-malloc'd input buffer that OpenVINO may retain across
// infer() calls (a Go-owned buffer would violate cgo pointer rules). It is NOT
// goroutine-safe; callers serialize access. The process-global core is shared
// and is not owned here.
type classifier struct {
	model    *C.ov_model_t
	compiled *C.ov_compiled_model_t
	req      *C.ov_infer_request_t
	inBuf    unsafe.Pointer
	samples  int
}

// reshapeToStaticBatch1 reshapes the model's single input to a static
// [1, samples] shape and returns the per-segment sample count. The sample count
// is read from the model's partial input shape (its sample dimension is static
// even though the batch dimension is dynamic), so it is derived rather than
// hardcoded. The reshape mirrors the spike's "key" step: OpenVINO cannot
// compile or infer a dynamic-batch model without a static input shape. The
// read-validate-reshape sequence runs atomically in C so cgo never handles the
// dynamically allocated partial-shape dims.
func reshapeToStaticBatch1(model *C.ov_model_t) (int, error) {
	var samples C.int64_t
	st := C.ovbind_reshape_static_batch1(model, &samples)
	if ovOK(st) {
		return int(samples), nil
	}
	// A bad input shape is signalled by a distinct out-of-range sentinel rather
	// than a genuine ov_status_e, so report it directly: the reshape was never
	// reached and ov_get_last_err_msg would return a stale or empty message.
	if int(st) == int(C.OVBIND_ERR_BAD_INPUT_SHAPE) {
		return 0, errors.Newf("openvino: model input is not a static rank-2 shape; cannot reshape for batch-1 inference").
			Category(errors.CategoryModelInit).Build()
	}
	return 0, lastErr("ov_model_reshape_single_input", st)
}

// NewClassifier reads modelPath, compiles it for the CPU device at
// opts.PrecisionHint, creates one infer request, and binds a C-owned input
// tensor sized to the model's input shape. InitOV must have succeeded first;
// otherwise it returns ErrOpenVINOUnavailable. The returned Classifier is not
// goroutine-safe.
func NewClassifier(modelPath string, opts Options) (Classifier, error) {
	ovMu.Lock()
	loaded := ovLoaded
	core := ovCore
	ovMu.Unlock()
	if !loaded || core == nil {
		return nil, ErrOpenVINOUnavailable
	}

	precision := opts.PrecisionHint
	if precision == "" {
		precision = DefaultPrecisionHint
	}
	threads := ""
	if opts.Threads > 0 {
		threads = strconv.Itoa(opts.Threads)
	}

	cPath := C.CString(modelPath)
	defer C.free(unsafe.Pointer(cPath))

	var model *C.ov_model_t
	if st := C.ovbind_core_read_model(core, cPath, &model); !ovOK(st) {
		return nil, lastErr("ov_core_read_model", st)
	}

	// Reshape the input to static [1, samples] before compiling. BirdNET v2.4
	// has a dynamic batch dimension, which OpenVINO cannot compile or infer.
	samples, err := reshapeToStaticBatch1(model)
	if err != nil {
		C.ovbind_model_free(model)
		return nil, err
	}

	cDev := C.CString(deviceCPU)
	defer C.free(unsafe.Pointer(cDev))
	cPrec := C.CString(precision)
	defer C.free(unsafe.Pointer(cPrec))
	cThreads := C.CString(threads)
	defer C.free(unsafe.Pointer(cThreads))

	var compiled *C.ov_compiled_model_t
	if st := C.ovbind_compile(core, model, cDev, cPrec, cThreads, &compiled); !ovOK(st) {
		C.ovbind_model_free(model)
		return nil, lastErr("ov_core_compile_model", st)
	}

	var req *C.ov_infer_request_t
	if st := C.ovbind_compiled_model_create_infer_request(compiled, &req); !ovOK(st) {
		C.ovbind_compiled_model_free(compiled)
		C.ovbind_model_free(model)
		return nil, lastErr("ov_compiled_model_create_infer_request", st)
	}

	// Allocate the input buffer in C memory so OpenVINO may retain the pointer
	// across infer() without violating cgo pointer-passing rules.
	inBuf := C.malloc(C.size_t(samples) * C.size_t(float32Bytes))
	if inBuf == nil {
		C.ovbind_infer_request_free(req)
		C.ovbind_compiled_model_free(compiled)
		C.ovbind_model_free(model)
		return nil, errors.Newf("openvino: failed to allocate %d-sample input buffer", samples).Build()
	}

	dims := []C.int64_t{1, C.int64_t(samples)}
	var shape C.ov_shape_t
	if st := C.ovbind_shape_create(expectedInputRank, &dims[0], &shape); !ovOK(st) {
		C.free(inBuf)
		C.ovbind_infer_request_free(req)
		C.ovbind_compiled_model_free(compiled)
		C.ovbind_model_free(model)
		return nil, lastErr("ov_shape_create", st)
	}

	var inTensor *C.ov_tensor_t
	if st := C.ovbind_tensor_create_from_host_ptr(C.F32, shape, inBuf, &inTensor); !ovOK(st) {
		C.ovbind_shape_free(&shape)
		C.free(inBuf)
		C.ovbind_infer_request_free(req)
		C.ovbind_compiled_model_free(compiled)
		C.ovbind_model_free(model)
		return nil, lastErr("ov_tensor_create_from_host_ptr", st)
	}
	C.ovbind_shape_free(&shape)

	if st := C.ovbind_set_input_tensor(req, inputPortIndex, inTensor); !ovOK(st) {
		C.ovbind_tensor_free(inTensor)
		C.free(inBuf)
		C.ovbind_infer_request_free(req)
		C.ovbind_compiled_model_free(compiled)
		C.ovbind_model_free(model)
		return nil, lastErr("ov_infer_request_set_input_tensor_by_index", st)
	}
	// The tensor keeps a reference to inBuf; the wrapper handle itself is no
	// longer needed once the request holds it.
	C.ovbind_tensor_free(inTensor)

	return &classifier{
		model:    model,
		compiled: compiled,
		req:      req,
		inBuf:    inBuf,
		samples:  samples,
	}, nil
}

// PredictRaw copies samples into the C input buffer, runs one inference, and
// returns a freshly allocated Go slice of the raw output logits. It returns an
// error if the input length does not match the model's input sample count. Not
// goroutine-safe.
func (c *classifier) PredictRaw(samples []float32) ([]float32, error) {
	if c.req == nil || c.inBuf == nil {
		return nil, ErrOpenVINOUnavailable
	}
	if len(samples) != c.samples {
		return nil, errors.Newf("openvino: input length %d does not match model input %d", len(samples), c.samples).Build()
	}

	dst := unsafe.Slice((*float32)(c.inBuf), c.samples)
	copy(dst, samples)

	if st := C.ovbind_infer(c.req); !ovOK(st) {
		return nil, lastErr("ov_infer_request_infer", st)
	}

	var outTensor *C.ov_tensor_t
	if st := C.ovbind_get_output_tensor(c.req, outputPortIndex, &outTensor); !ovOK(st) {
		return nil, lastErr("ov_infer_request_get_output_tensor_by_index", st)
	}
	defer C.ovbind_tensor_free(outTensor)

	var n C.size_t
	if st := C.ovbind_tensor_get_size(outTensor, &n); !ovOK(st) {
		return nil, lastErr("ov_tensor_get_size", st)
	}

	var dataPtr unsafe.Pointer
	if st := C.ovbind_tensor_data(outTensor, &dataPtr); !ovOK(st) {
		return nil, lastErr("ov_tensor_data", st)
	}

	src := unsafe.Slice((*float32)(dataPtr), int(n))
	out := make([]float32, int(n))
	copy(out, src)
	return out, nil
}

// Close frees the infer request, compiled model, read model, and the C input
// buffer, in that order. It does not touch the process-global core. Close is
// idempotent.
func (c *classifier) Close() error {
	if c.req != nil {
		C.ovbind_infer_request_free(c.req)
		c.req = nil
	}
	if c.compiled != nil {
		C.ovbind_compiled_model_free(c.compiled)
		c.compiled = nil
	}
	if c.model != nil {
		C.ovbind_model_free(c.model)
		c.model = nil
	}
	if c.inBuf != nil {
		C.free(c.inBuf)
		c.inBuf = nil
	}
	return nil
}
