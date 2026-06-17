//go:build !openvino

package classifier

// openvinoBackendAvailable reports whether this build links the OpenVINO
// backend. False for normal builds; the dispatch never attempts OpenVINO.
const openvinoBackendAvailable = false
