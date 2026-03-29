//go:build !onnx

package classifier

import "github.com/tphakala/birdnet-go/internal/errors"

// loadPerch is the fallback when the onnx build tag is not set.
func (o *Orchestrator) loadPerch(_ int) error {
	return errors.Newf("Perch model requires ONNX build (compile with -tags onnx)").
		Component("classifier.orchestrator").
		Category(errors.CategoryModelInit).
		Build()
}
