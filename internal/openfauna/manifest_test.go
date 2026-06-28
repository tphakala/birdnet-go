package openfauna

import "testing"

func TestEmbeddedSchemaMajorIsTwo(t *testing.T) {
	major, ok := embeddedSchemaMajor()
	if !ok {
		t.Fatalf("embedded manifest schema_version did not parse")
	}
	if major != 2 {
		t.Fatalf("embedded OpenFauna schema major = %d, want 2 (re-vendor with refresh-data.sh)", major)
	}
}

func TestParseSchemaMajor(t *testing.T) {
	cases := map[string]struct {
		in    string
		major int
		ok    bool
	}{
		"normal":     {`{"schema_version":"2.1.0"}`, 2, true},
		"major only": {`{"schema_version":"3"}`, 3, true},
		"missing":    {`{"species_count":1}`, 0, false},
		"garbage":    {`not json`, 0, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			major, ok := parseSchemaMajor([]byte(tc.in))
			if ok != tc.ok || major != tc.major {
				t.Fatalf("parseSchemaMajor(%q) = (%d,%v), want (%d,%v)", tc.in, major, ok, tc.major, tc.ok)
			}
		})
	}
}
