package security

import (
	"net/http"

	"github.com/markbates/goth/gothic"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// errNoContextProvider is returned when a request reaches gothic without the
// provider its handler chose.
var errNoContextProvider = errors.NewStd("no OAuth provider set in the request context")

// contextProviderName returns the provider the handler stored with
// gothic.GetContextWithProvider. The default resolver also reads the "provider"
// query parameter, route parameters and any provider key in the session, all of
// which a request can influence; reading only the context value keeps gothic on
// the provider the handler authorised.
func contextProviderName(req *http.Request) (string, error) {
	if name, ok := req.Context().Value(gothic.ProviderParamKey).(string); ok && name != "" {
		return name, nil
	}
	return "", errNoContextProvider
}

// init installs the resolver once, before any request, so handlers that never
// call InitializeGoth cannot fall back to the default.
func init() {
	gothic.GetProviderName = contextProviderName
}
