package audiocore

import (
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// ErrSourceNotFound is returned when a source ID is not found in the registry.
var ErrSourceNotFound = errors.Newf("source not found").
	Component("audiocore").Category(errors.CategoryNotFound).Build()

// ErrBufferNotFound is an alias for buffer.ErrBufferNotFound so that errors.Is
// checks behave identically whether callers import the parent or the subpackage.
var ErrBufferNotFound = buffer.ErrBufferNotFound

// ErrRouteExists is returned when attempting to add a duplicate route.
var ErrRouteExists = errors.Newf("route already exists").
	Component("audiocore").Category(errors.CategoryConflict).Build()

// ErrConsumerClosed is returned when writing to a closed consumer.
var ErrConsumerClosed = errors.Newf("consumer closed").
	Component("audiocore").Category(errors.CategoryState).Build()
