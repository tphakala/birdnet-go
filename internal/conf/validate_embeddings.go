package conf

import "github.com/tphakala/birdnet-go/internal/errors"

// validateEmbeddingsSettings checks the embedding capture configuration.
// Only the fp16 vector format is implemented today; int8 is reserved behind the
// storage discriminator and rejected here so a misconfiguration surfaces at
// startup instead of silently dropping every captured embedding at encode time.
//
// The error wording deliberately avoids the substrings ParseSettings treats as
// non-fatal warnings ("not supported", "fallback", ...): an unsupported format
// must fail startup, not be demoted to a warning. See TestValidateEmbeddingsSettings_UnsupportedFormatIsFatal.
func validateEmbeddingsSettings(cfg *EmbeddingsConfig) error {
	switch cfg.Storage.Format {
	case "", "fp16":
		// "" defaults to fp16 at encode time.
	default:
		return errors.Newf("embeddings.storage.format %q is invalid: only \"fp16\" is implemented", cfg.Storage.Format).
			Component("conf").
			Category(errors.CategoryValidation).
			Build()
	}
	if cfg.Storage.MaxRows < 0 {
		return errors.Newf("embeddings.storage.maxrows must be >= 0, got %d", cfg.Storage.MaxRows).
			Component("conf").
			Category(errors.CategoryValidation).
			Build()
	}
	return nil
}
