package conf

// HasLifeList reports whether lifer notifications are enabled and a
// non-empty life list has been imported.
func (s *Settings) HasLifeList() bool {
	return s.Realtime.LifeList.Enabled && len(s.Realtime.LifeList.Species) > 0
}

// IsOnLifeList reports whether the given label (BirdNET format
// "ScientificName_CommonName", or scientific-name only) is on the user's
// imported life list. Unlike IsSpeciesIncluded (range_filter.go), this is
// always an O(n) canonicalized scan rather than a precomputed map lookup:
// LifeList.Species is edited directly through the general settings save
// endpoint (see LifeListSettings' doc comment for why a cached map would go
// stale), and this is checked once per saved detection, not a hot path.
func (s *Settings) IsOnLifeList(result string) bool {
	target := canonicalSci(result)
	for _, label := range s.Realtime.LifeList.Species {
		if canonicalSci(label) == target {
			return true
		}
	}
	return false
}
