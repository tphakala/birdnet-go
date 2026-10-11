package flac

import (
	"bytes"
	"crypto/md5" //nolint:gosec // STREAMINFO MD5 is an integrity digest, not security
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goflacmeta "github.com/tphakala/go-flac"
	goflac "github.com/tphakala/go-flac/pcm"
)

// FLAC stream layout shared by the test helpers that parse encoder output.
const (
	flacMarker         = "fLaC"
	markerLen          = len(flacMarker)
	metaBlockHeaderLen = 4    // 1 type/last-flag byte + 3 length bytes
	metaBlockLastFlag  = 0x80 // set on the final metadata block header
	metaBlockTypeMask  = 0x7f

	vorbisCommentBlockType = 4
	// streamInfoFieldsBeforeMD5 is the byte count of the STREAMINFO fields that
	// precede its 16-byte MD5.
	streamInfoFieldsBeforeMD5 = 18
	// streamInfoMD5Offset is the byte offset of the STREAMINFO MD5 in a file.
	streamInfoMD5Offset = markerLen + metaBlockHeaderLen + streamInfoFieldsBeforeMD5
	md5Len              = 16
)

// hasMetadataBlock walks the FLAC metadata blocks and reports whether a block of
// blockType is present.
func hasMetadataBlock(data []byte, blockType byte) bool {
	if len(data) < markerLen || string(data[:markerLen]) != flacMarker {
		return false
	}
	for off := markerLen; off+metaBlockHeaderLen <= len(data); {
		header := data[off]
		last := header&metaBlockLastFlag != 0
		btype := header & metaBlockTypeMask
		length := int(data[off+1])<<16 | int(data[off+2])<<8 | int(data[off+3])
		if btype == blockType {
			return true
		}
		off += metaBlockHeaderLen + length
		if last {
			break
		}
	}
	return false
}

// encodeTagged encodes pcm with tags and vendor to a temp file and returns the
// file contents.
func encodeTagged(t *testing.T, pcm []byte, tags []Tag, vendor string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clip.flac")
	opts := baseOpts(path, pcm)
	opts.Tags = tags
	opts.Vendor = vendor
	require.NoError(t, EncodePCM(t.Context(), opts))
	data, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	return data
}

// decodeTags decodes a FLAC byte stream and returns its tags, vendor and PCM.
func decodeTags(t *testing.T, data []byte) (tags []goflacmeta.Tag, vendor string, pcm []byte) {
	t.Helper()
	dec, err := goflac.NewDecoder(bytes.NewReader(data))
	require.NoError(t, err)
	pcm, err = io.ReadAll(dec)
	require.NoError(t, err)
	return dec.Tags(), dec.Vendor(), pcm
}

func TestEncodePCM_EmbedsTagsAndVendor(t *testing.T) {
	t.Parallel()
	pcm := makeTestPCM(60000)
	tags := []Tag{
		{Name: "TITLE", Value: "Peukaloinen (Troglodytes troglodytes)"},
		{Name: "BIRDNET_CONFIDENCE", Value: "0.95"},
		{Name: "DATE", Value: "2026-05-31T08:38:28+03:00"},
	}
	data := encodeTagged(t, pcm, tags, "BirdNET-Go 1.2.3")

	gotTags, gotVendor, gotPCM := decodeTags(t, data)
	assert.Equal(t, []goflacmeta.Tag{
		{Name: "TITLE", Value: "Peukaloinen (Troglodytes troglodytes)"},
		{Name: "BIRDNET_CONFIDENCE", Value: "0.95"},
		{Name: "DATE", Value: "2026-05-31T08:38:28+03:00"},
	}, gotTags)
	assert.Equal(t, "BirdNET-Go 1.2.3", gotVendor)
	assert.Equal(t, pcm, gotPCM, "tagged clip must stay lossless")
	assert.True(t, hasSeekTable(data), "SEEKTABLE must survive the extra block")
	sum := md5.Sum(pcm) //nolint:gosec // integrity digest
	assert.Equal(t, sum[:], data[streamInfoMD5Offset:streamInfoMD5Offset+md5Len],
		"STREAMINFO must stay first and carry the real MD5")
}

func TestEncodePCM_NoTagsWritesNoVorbisComment(t *testing.T) {
	t.Parallel()
	pcm := makeTestPCM(20000)
	plain := encodeTagged(t, pcm, nil, "")
	sanitizedAway := encodeTagged(t, pcm, []Tag{
		{Name: "", Value: "x"},
		{Name: "OK", Value: "   "},
	}, "")
	assert.False(t, hasMetadataBlock(plain, vorbisCommentBlockType))
	assert.False(t, hasMetadataBlock(sanitizedAway, vorbisCommentBlockType))
	assert.Equal(t, plain, sanitizedAway, "tags that sanitize away must leave the output byte-identical")
}

func TestEncodePCM_InvalidTagInputStillExports(t *testing.T) {
	t.Parallel()
	many := make([]Tag, 200)
	for i := range many {
		many[i] = Tag{Name: "T", Value: "v"}
	}
	noTags := []goflacmeta.Tag{}
	tests := []struct {
		name   string
		tags   []Tag
		vendor string
		// wantTags, when non-nil, is the exact decoded tag list (empty: no tags).
		wantTags []goflacmeta.Tag
	}{
		{
			"invalid utf8 value", []Tag{{Name: "TITLE", Value: "\xff\xfeBird"}}, "",
			[]goflacmeta.Tag{{Name: "TITLE", Value: tagInvalidUTF8Replacement + "Bird"}},
		},
		{"name with equals", []Tag{{Name: "A=B", Value: "x"}}, "", noTags},
		{"non-ascii name", []Tag{{Name: "ÄÖ", Value: "x"}}, "", noTags},
		{"control char name", []Tag{{Name: "\x01", Value: "x"}}, "", noTags},
		{
			"invalid names dropped, valid kept",
			[]Tag{{Name: "A=B", Value: "x"}, {Name: "ÄÖ", Value: "x"}, {Name: "GOOD", Value: "y"}}, "",
			[]goflacmeta.Tag{{Name: "GOOD", Value: "y"}},
		},
		{"huge value", []Tag{{Name: "TITLE", Value: strings.Repeat("ä", 1<<20)}}, "", nil},
		{"too many tags", many, "", nil},
		{"invalid utf8 vendor", nil, "\xc3", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pcm := makeTestPCM(8000)
			data := encodeTagged(t, pcm, tt.tags, tt.vendor)
			gotTags, gotVendor, gotPCM := decodeTags(t, data)
			assert.Equal(t, pcm, gotPCM)
			assert.LessOrEqual(t, len(gotTags), maxTagCount)
			assert.True(t, utf8.ValidString(gotVendor))
			for _, tag := range gotTags {
				assert.True(t, validTagName(tag.Name), "name %q", tag.Name)
				assert.True(t, utf8.ValidString(tag.Value))
				assert.LessOrEqual(t, len(tag.Value), maxTagValueBytes)
			}
			switch {
			case tt.wantTags == nil:
			case len(tt.wantTags) == 0:
				assert.Empty(t, gotTags)
			default:
				assert.Equal(t, tt.wantTags, gotTags)
			}
		})
	}
}

func FuzzSanitizeTags(f *testing.F) {
	f.Add("TITLE", "Wren", "BirdNET-Go 1")
	f.Add("A=B", "\xff\xfe", "\xc3")
	f.Add("ÄÖ", strings.Repeat("ä", 2000), "")
	f.Add("\x01", "   ", "v")
	f.Fuzz(func(t *testing.T, name, value, vendor string) {
		tags := sanitizeTags([]Tag{{Name: name, Value: value}})
		cfg := goflac.Config{
			SampleRate: testSampleRate, BitDepth: testBitDepth, Channels: testChannels,
			Tags: tags, Vendor: sanitizeTagText(vendor),
		}
		require.NoError(t, new(goflac.Encoder).Reset(io.Discard, cfg))
		for _, tag := range tags {
			assert.True(t, validTagName(tag.Name))
			assert.True(t, utf8.ValidString(tag.Value))
			assert.LessOrEqual(t, len(tag.Value), maxTagValueBytes)
		}
	})
}

func TestSanitizeTagText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"valid passthrough", "Närhi", "Närhi"},
		{"trim", "  x \n", "x"},
		{"invalid bytes replaced", "a\xffb", "a" + tagInvalidUTF8Replacement + "b"},
		{
			"space at the truncation point is trimmed",
			strings.Repeat("a", maxTagValueBytes-1) + " b",
			strings.Repeat("a", maxTagValueBytes-1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, sanitizeTagText(tt.in))
		})
	}

	t.Run("truncation at multibyte boundary", func(t *testing.T) {
		t.Parallel()
		// 2-byte runes: an odd cap would split the last one.
		got := sanitizeTagText(strings.Repeat("ä", maxTagValueBytes))
		assert.True(t, utf8.ValidString(got))
		assert.LessOrEqual(t, len(got), maxTagValueBytes)
		assert.NotEmpty(t, got)
		// 3-byte runes: 1024 is not a multiple of 3.
		got = sanitizeTagText(strings.Repeat("€", maxTagValueBytes))
		assert.True(t, utf8.ValidString(got))
		assert.LessOrEqual(t, len(got), maxTagValueBytes)
		assert.Greater(t, len(got), maxTagValueBytes-utf8.UTFMax)
	})
}

// TestDepinConfig_ClearsTagsVendorAndSeekTable pins what a pooled encoder is
// re-Reset with: no tag or vendor strings from the finished clip, and no seek
// table (the discard sink is not seekable). The input config is left untouched.
func TestDepinConfig_ClearsTagsVendorAndSeekTable(t *testing.T) {
	t.Parallel()
	tags := []goflacmeta.Tag{{Name: "TITLE", Value: "x"}}
	cfg := goflac.Config{
		SampleRate: testSampleRate, BitDepth: testBitDepth, Channels: testChannels,
		SeekTableInterval: testSampleRate, Tags: tags, Vendor: "v",
	}
	got := depinConfig(&cfg)
	assert.Nil(t, got.Tags)
	assert.Empty(t, got.Vendor)
	assert.Zero(t, got.SeekTableInterval)
	assert.Equal(t, testSampleRate, got.SampleRate)
	assert.Equal(t, tags, cfg.Tags, "the caller's config must not be modified")
	assert.Equal(t, "v", cfg.Vendor)
}

// Not parallel. sync.Pool may drop an item (it does at random under -race), so
// reuse is likely but not guaranteed; the test is correct either way, and the
// depin clearing itself is pinned by TestDepinConfig_ClearsTagsVendorAndSeekTable.
func TestEncoderPool_TagsDoNotLeakAcrossExports(t *testing.T) {
	pcm := makeTestPCM(8000)
	tagsA := []Tag{{Name: "TITLE", Value: "A"}}
	tagsB := []Tag{{Name: "TITLE", Value: "B"}}

	first := encodeTagged(t, pcm, tagsA, "va")
	assert.True(t, hasMetadataBlock(first, vorbisCommentBlockType))

	second := encodeTagged(t, pcm, nil, "")
	assert.False(t, hasMetadataBlock(second, vorbisCommentBlockType), "untagged export must not inherit tags")

	third := encodeTagged(t, pcm, tagsB, "vb")
	gotTags, gotVendor, _ := decodeTags(t, third)
	assert.Equal(t, []goflacmeta.Tag{{Name: "TITLE", Value: "B"}}, gotTags)
	assert.Equal(t, "vb", gotVendor)

	buf, err := EncodePCMToBuffer(t.Context(), &BufferOptions{
		PCMData: pcm, SampleRate: testSampleRate, Channels: testChannels, BitDepth: testBitDepth,
	})
	require.NoError(t, err)
	assert.False(t, hasMetadataBlock(buf.Bytes(), vorbisCommentBlockType), "buffer path never writes tags")
}
