package speciesindex

import "testing"

// BenchmarkBuild measures a full snapshot build (name maps plus the additive
// canonical memo) over the v2.4 corpus, so the memo's per-label CanonicalName
// cost is visible. Rebuilds run only on model load/unload/reload and locale
// change, so this cost is off the request path.
func BenchmarkBuild(b *testing.B) {
	labels := loadCorpus(b)
	b.ReportAllocs()
	for b.Loop() {
		_ = Build(labels, nil, "en")
	}
}
