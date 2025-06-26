package analysis

import "github.com/tphakala/birdnet-go/internal/errors"

// ErrAnalysisCanceled is returned when the analysis is canceled by the user
var ErrAnalysisCanceled = errors.NewStd("analysis canceled")
