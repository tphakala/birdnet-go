package apicore

import "github.com/tphakala/birdnet-go/internal/conf"

// SettingsUpdater is the facade's settings write hand-off injected into the
// domain handlers that read-modify-write settings (TLS, MQTT TLS, species
// exclusion). It runs mutate on a clone of the current settings under the
// settings writer lock, publishes and persists the result, and returns the
// snapshots it compared. A mutate error, including conf.ErrSettingsUnchanged,
// publishes nothing and is returned as is.
type SettingsUpdater func(mutate func(current, updated *conf.Settings) error) (current, updated *conf.Settings, err error)
