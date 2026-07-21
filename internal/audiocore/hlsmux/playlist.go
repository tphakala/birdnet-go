package hlsmux

import (
	"strconv"
	"strings"
	"time"
)

const (
	// playlistVersion is the EXT-X-VERSION this package emits. Fragmented MP4
	// segments carried by EXT-X-MAP require version 7 (RFC 8216 section 8).
	playlistVersion = 7

	// extinfPrecision is the number of fractional digits in an EXTINF
	// duration. Six digits resolve a single sample at 48 kHz (about 20.8
	// microseconds) with room to spare, so the playlist never rounds away a
	// difference the timeline actually has.
	extinfPrecision = 6

	// pdtLayout formats EXT-X-PROGRAM-DATE-TIME. RFC 8216 requires an ISO 8601
	// date-time, and milliseconds are what every packager emits in practice.
	pdtLayout = "2006-01-02T15:04:05.000Z07:00"

	// playlistFixedLines is the number of header lines rendered before the
	// segment list, used only to pre-size the builder.
	playlistFixedLines = 5

	// playlistBytesPerSegment is a generous per-segment estimate for
	// pre-sizing the builder: an EXTINF line, a PDT line and a filename.
	playlistBytesPerSegment = 96
)

// renderPlaylist builds the HLS media playlist describing the ring's current
// window.
//
// It is a media playlist, not a master playlist, which is why no CODECS
// attribute appears: RFC 8216 places CODECS on EXT-X-STREAM-INF, a master
// playlist tag. Players determine the codec by probing the init segment named
// by EXT-X-MAP.
func renderPlaylist(r *ring, ended bool) string {
	var b strings.Builder
	b.Grow(playlistFixedLines*32 + len(r.window())*playlistBytesPerSegment)

	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:")
	b.WriteString(strconv.Itoa(playlistVersion))
	b.WriteString("\n#EXT-X-TARGETDURATION:")
	b.WriteString(strconv.Itoa(r.targetDuration()))
	b.WriteString("\n#EXT-X-MEDIA-SEQUENCE:")
	b.WriteString(strconv.FormatUint(r.mediaSequence(), 10))
	b.WriteString("\n")
	if ds := r.discontinuitySequence(); ds != 0 {
		b.WriteString("#EXT-X-DISCONTINUITY-SEQUENCE:")
		b.WriteString(strconv.FormatUint(ds, 10))
		b.WriteString("\n")
	}
	// EXT-X-MAP is emitted even for an empty window so that a player polling
	// during the first segment's worth of audio already knows the init segment
	// and can fetch it before any media arrives.
	b.WriteString(`#EXT-X-MAP:URI="`)
	b.WriteString(InitSegmentName)
	b.WriteString("\"\n")

	for i := range r.window() {
		seg := &r.segments[i]
		if seg.Discontinuity {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		b.WriteString("#EXT-X-PROGRAM-DATE-TIME:")
		b.WriteString(seg.PDT.Format(pdtLayout))
		b.WriteString("\n#EXTINF:")
		b.WriteString(formatEXTINF(seg.Duration))
		b.WriteString(",\n")
		b.WriteString(SegmentName(seg.Seq))
		b.WriteString("\n")
	}

	// EXT-X-ENDLIST tells a client to stop polling. Without it a player keeps
	// refreshing a playlist that will never change again.
	if ended {
		b.WriteString("#EXT-X-ENDLIST\n")
	}

	return b.String()
}

// formatEXTINF renders a segment duration as the fixed-point seconds value an
// EXTINF tag carries.
func formatEXTINF(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', extinfPrecision, 64)
}
