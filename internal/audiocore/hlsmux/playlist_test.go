package hlsmux

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestRenderPlaylistGolden pins the exact bytes of a rendered playlist. The
// tag set, their order and their formatting are what a player parses, so a
// change here should have to be deliberate.
func TestRenderPlaylistGolden(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	r := newSegmentWindow(3)
	r.push(&Segment{Seq: 7, Samples: 96256, Duration: 2005333333 * time.Nanosecond, PDT: base})
	r.push(&Segment{Seq: 8, Samples: 96256, Duration: 2005333334 * time.Nanosecond, PDT: base.Add(2005333333 * time.Nanosecond)})

	want := `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-INDEPENDENT-SEGMENTS
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:7
#EXT-X-MAP:URI="init.mp4"
#EXT-X-PROGRAM-DATE-TIME:2026-07-21T12:00:00.000Z
#EXTINF:2.005333,
segment7.m4s
#EXT-X-PROGRAM-DATE-TIME:2026-07-21T12:00:02.005Z
#EXTINF:2.005333,
segment8.m4s
`
	assert.Equal(t, want, renderPlaylist(r, 2, false))
}

// TestRenderPlaylistWithDiscontinuityGolden covers the tags that only appear
// once a source has stalled and the earlier segments have scrolled away.
func TestRenderPlaylistWithDiscontinuityGolden(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	r := newSegmentWindow(2)
	r.push(&Segment{Seq: 40, Duration: 2 * time.Second, PDT: base, DiscontinuitySeq: 2})
	// A segment preceded by EXT-X-DISCONTINUITY carries its predecessor's
	// discontinuity sequence number plus one, so that the number a client
	// computes for it does not change when segment 40 scrolls out of the
	// window and 41 becomes the first entry.
	r.push(&Segment{
		Seq:              41,
		Duration:         2 * time.Second,
		PDT:              base.Add(9 * time.Second),
		Discontinuity:    true,
		DiscontinuitySeq: 3,
	})

	want := `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-INDEPENDENT-SEGMENTS
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:40
#EXT-X-DISCONTINUITY-SEQUENCE:2
#EXT-X-MAP:URI="init.mp4"
#EXT-X-PROGRAM-DATE-TIME:2026-07-21T12:00:00.000Z
#EXTINF:2.000000,
segment40.m4s
#EXT-X-DISCONTINUITY
#EXT-X-PROGRAM-DATE-TIME:2026-07-21T12:00:09.000Z
#EXTINF:2.000000,
segment41.m4s
`
	assert.Equal(t, want, renderPlaylist(r, 2, false))
}

// TestRenderPlaylistEndedGolden covers the closed stream, where the trailing
// EXT-X-ENDLIST is what stops clients polling forever.
func TestRenderPlaylistEndedGolden(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	r := newSegmentWindow(2)
	r.push(&Segment{Seq: 0, Duration: 1500 * time.Millisecond, PDT: base})

	want := `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-INDEPENDENT-SEGMENTS
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-MAP:URI="init.mp4"
#EXT-X-PROGRAM-DATE-TIME:2026-07-21T12:00:00.000Z
#EXTINF:1.500000,
segment0.m4s
#EXT-X-ENDLIST
`
	assert.Equal(t, want, renderPlaylist(r, 2, true))
}

// TestRenderPlaylistEmptyGolden covers the window a player sees while the very
// first segment is still accumulating.
func TestRenderPlaylistEmptyGolden(t *testing.T) {
	t.Parallel()

	want := `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-INDEPENDENT-SEGMENTS
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-MAP:URI="init.mp4"
`
	assert.Equal(t, want, renderPlaylist(newSegmentWindow(6), 2, false))
}

func TestFormatEXTINF(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"whole seconds", 2 * time.Second, "2.000000"},
		{"aac segment at 48 kHz", 2005333333 * time.Nanosecond, "2.005333"},
		{"sub-second", 500 * time.Millisecond, "0.500000"},
		{"zero", 0, "0.000000"},
		{"single 1024-sample frame", 21333333 * time.Nanosecond, "0.021333"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, formatEXTINF(tt.d))
		})
	}
}
