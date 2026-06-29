//go:build openvino

package classifier

// openvinoBackendAvailable reports whether this build links the OpenVINO
// backend. True only under the "openvino" build tag (rpi5 image).
const openvinoBackendAvailable = true
