package classifier

// registerTestV24 registers inst as the loaded BirdNET v2.4 entry (the range-filter
// anchor and default model) in a hand-built test Orchestrator, standing in for the
// removed o.primary field so the anchor-based accessors resolve it. It creates the
// models map if needed and preserves any entries already present.
func registerTestV24(o *Orchestrator, inst ModelInstance) {
	if o.models == nil {
		o.models = map[string]*modelEntry{}
	}
	o.models[RegistryIDBirdNETV24] = &modelEntry{instance: inst}
}
