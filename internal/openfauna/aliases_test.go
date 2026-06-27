package openfauna

import "testing"

func TestCanonicalNameKnownAlias(t *testing.T) {
	// Streptopelia senegalensis (BirdNET v2.4) -> Spilopelia senegalensis (eBird).
	// This is the Laughing Dove case from the discussion that motivated aliasing.
	got := CanonicalName("Streptopelia senegalensis")
	if got != "Spilopelia senegalensis" {
		t.Fatalf("CanonicalName(legacy) = %q, want %q", got, "Spilopelia senegalensis")
	}
}

func TestCanonicalNameCaseAndSpaceInsensitive(t *testing.T) {
	got := CanonicalName("  streptopelia SENEGALENSIS  ")
	if got != "Spilopelia senegalensis" {
		t.Fatalf("CanonicalName(messy case) = %q, want %q", got, "Spilopelia senegalensis")
	}
}

func TestCanonicalNameUnknownReturnedUnchanged(t *testing.T) {
	// A non-aliased name must pass through verbatim so callers can apply it
	// unconditionally.
	const in = "Turdus merula"
	if got := CanonicalName(in); got != in {
		t.Fatalf("CanonicalName(non-alias) = %q, want unchanged %q", got, in)
	}
}

func TestCanonicalNameAlreadyCanonicalUnchanged(t *testing.T) {
	// The canonical target itself is not an alias key, so it resolves to itself.
	const in = "Spilopelia senegalensis"
	if got := CanonicalName(in); got != in {
		t.Fatalf("CanonicalName(canonical) = %q, want unchanged %q", got, in)
	}
}

func TestAliasCountNonZero(t *testing.T) {
	// Guards against the embedded artifact going missing or failing to parse.
	if n := AliasCount(); n < 100 {
		t.Fatalf("AliasCount() = %d, want a populated map (>=100)", n)
	}
}
