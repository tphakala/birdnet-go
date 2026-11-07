//go:build !openvino

package classifier

// serveOVProbeChildIfRequested is a no-op without the openvino tag: the OpenVINO
// device probe never runs, so this test binary is never re-executed as its child.
func serveOVProbeChildIfRequested() {}

// exitIfOVProbeHookTestChild is a no-op without the openvino tag, for the same
// reason.
func exitIfOVProbeHookTestChild() {}
