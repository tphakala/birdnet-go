package analysis

import "errors"

// ErrAnalysisCanceled is returned when the analysis is canceled by the user
var ErrAnalysisCanceled = errors.New("analysis canceled")
