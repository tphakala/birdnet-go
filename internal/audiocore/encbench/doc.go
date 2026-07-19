// Package encbench holds an on-demand comparison harness that measures the
// native Go clip encoders against the FFmpeg export path on the same PCM.
//
// It has no production code. The harness lives behind the "enccompare" build
// tag so it never runs in CI, where FFmpeg availability and machine load would
// make the numbers meaningless. Run it deliberately, on the hardware you care
// about:
//
//	go test -tags enccompare -v ./internal/audiocore/encbench/
//
// It reports wall time, CPU time (including the FFmpeg child process), peak
// RSS and output size per format, which is the evidence the native-encoder
// rollout in internal/audiocore/nativeenc is judged on.
package encbench
