package render

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func benchRender(b *testing.B, src Source, opt Options) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		_, _, err := Render(b.Context(), src, opt)
		require.NoError(b, err)
	}
}

func BenchmarkRenderBirdLg(b *testing.B) {
	benchRender(b, toneSource(24000, 15, 3000, 0.5), Options{Width: 1026, Rows: 513, RangeDB: 100})
}

func BenchmarkRenderBird48kLg(b *testing.B) {
	benchRender(b, toneSource(sourceRate48k, 15, 3000, 0.5), Options{Width: 1026, Rows: 513, RangeDB: 100, DisplayRate: displayRate24k})
}

func BenchmarkRenderBirdXl(b *testing.B) {
	benchRender(b, toneSource(24000, 15, 3000, 0.5), Options{Width: 2050, Rows: 1025, RangeDB: 100})
}

func BenchmarkRenderBird48kXl(b *testing.B) {
	benchRender(b, toneSource(sourceRate48k, 15, 3000, 0.5), Options{Width: 2050, Rows: 1025, RangeDB: 100, DisplayRate: displayRate24k})
}

func BenchmarkRenderBird48kXlDolph(b *testing.B) {
	benchRender(b, toneSource(sourceRate48k, 15, 3000, 0.5), Options{Width: 2050, Rows: 1025, RangeDB: 100, DisplayRate: displayRate24k, Style: StyleFor("scientific_dark")})
}

func BenchmarkRenderBatLg(b *testing.B) {
	benchRender(b, toneSource(256000, 3, 50000, 0.5), Options{Width: 1026, Rows: 513, RangeDB: 100})
}
