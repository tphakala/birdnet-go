package audiocore

import "github.com/tphakala/birdnet-go/internal/errors"

// ErrSourceNotFound is returned when a source ID is not found in the registry.
var ErrSourceNotFound = errors.Newf("source not found").
	Component("audiocore").Category(errors.CategoryNotFound).Build()

// ErrBufferNotFound is returned when a buffer is not found for a source.
var ErrBufferNotFound = errors.Newf("buffer not found").
	Component("audiocore").Category(errors.CategoryBuffer).Build()

// ErrRouteExists is returned when attempting to add a duplicate route.
var ErrRouteExists = errors.Newf("route already exists").
	Component("audiocore").Category(errors.CategoryConflict).Build()

// ErrConsumerClosed is returned when writing to a closed consumer.
var ErrConsumerClosed = errors.Newf("consumer closed").
	Component("audiocore").Category(errors.CategoryState).Build()
