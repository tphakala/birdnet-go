//go:build !onnx

package classifier

import "github.com/tphakala/birdnet-go/internal/errors"

// loadBat is the fallback when the onnx build tag is not set.
func (o *Orchestrator) loadBat(_ int) error {
	return errors.Newf("Bat model requires ONNX build (compile with -tags onnx)").
		Component("classifier.orchestrator").
		Category(errors.CategoryModelInit).
		Build()
}
