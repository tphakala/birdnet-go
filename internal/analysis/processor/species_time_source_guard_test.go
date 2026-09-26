package processor

import (
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2only"
)

// Compile-time proof that every concrete datastore backend that can be stored in
// Processor.Ds satisfies the narrow speciesDetectionTimeSource capability that
// MqttAction depends on. The wiring in getDefaultActions derives the capability
// via a runtime type assertion (any(p.Ds).(speciesDetectionTimeSource)); if a
// backend's method signature drifts out of the interface, that assertion would
// silently yield ok=false and the MQTT payload would degrade to permanent null
// fields with the build still green. These assertions turn such drift into a
// build failure instead. The legacy factory (datastore.New) returns
// *SQLiteStore/*MySQLStore, both of which embed DataStore, so they are asserted
// directly rather than via *DataStore to also catch a broken embedding.
var (
	_ speciesDetectionTimeSource = (*datastore.SQLiteStore)(nil)
	_ speciesDetectionTimeSource = (*datastore.MySQLStore)(nil)
	_ speciesDetectionTimeSource = (*v2only.Datastore)(nil)
)
