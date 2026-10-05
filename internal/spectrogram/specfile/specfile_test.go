package specfile

import (
	"io/fs"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

func TestIsRenderFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		base string
		want bool
	}{
		{"prerender lower", "b.png", "b", true},
		{"prerender upper", "b.PNG", "b", true},
		{"sized raw", "b_1026px.png", "b", true},
		{"sized legend", "b_514px-legend.png", "b", true},
		{"sized style dr profile legend", "b_1026px-scientific_dark-dr80-bat-v2-legend.png", "b", true},
		{"short width", "b_123px.png", "b", true},
		{"extended capture sibling", "b_15s_1026px.png", "b", false},
		{"longer base", "bx_1026px.png", "b", false},
		{"missing digits", "b_px.png", "b", false},
		{"wrong extension", "b_1026px.jpg", "b", false},
		{"audio file", "b.wav", "b", false},
		{"temp file", "b.png.123.4.temp", "b", false},
		{"trailing junk after px", "b_1026pxx.png", "b", false},
		{"empty token", "b_1026px-.png", "b", false},
		{"empty base", "b.png", "", false},
		{"unrelated", "other.png", "b", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsRenderFor(tt.file, tt.base))
		})
	}
}

func TestParseRender(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		want Render
		ok   bool
	}{
		{"raw", "b_1026px.png", Render{Width: 1026, Raw: true}, true},
		{"legend", "b_514px-legend.png", Render{Width: 514, Raw: false}, true},
		{"styled raw", "b_2050px-scientific_dark.png", Render{Width: 2050, Raw: true}, true},
		{"styled legend", "b_2050px-scientific_dark-legend.png", Render{Width: 2050, Raw: false}, true},
		{"prerender lower", "b.png", Render{Raw: true, Unsized: true}, true},
		{"prerender upper", "b.PNG", Render{Raw: true, Unsized: true}, true},
		{"not a render", "b_15s_1026px.png", Render{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := ParseRender(tt.file, "b")
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

// fakeEntry is a directory entry whose Info can be made to fail.
type fakeEntry struct {
	name    string
	dir     bool
	link    bool
	size    int64
	infoErr error
}

func (f fakeEntry) Name() string { return f.name }
func (f fakeEntry) IsDir() bool  { return f.dir }
func (f fakeEntry) Type() fs.FileMode {
	switch {
	case f.dir:
		return fs.ModeDir
	case f.link:
		return fs.ModeSymlink
	}
	return 0
}
func (f fakeEntry) Info() (fs.FileInfo, error) {
	if f.infoErr != nil {
		return nil, f.infoErr
	}
	return fakeInfo{f}, nil
}

type fakeInfo struct{ e fakeEntry }

func (i fakeInfo) Name() string       { return i.e.name }
func (i fakeInfo) Size() int64        { return i.e.size }
func (i fakeInfo) Mode() fs.FileMode  { return i.e.Type() }
func (i fakeInfo) ModTime() time.Time { return time.Time{} }
func (i fakeInfo) IsDir() bool        { return i.e.dir }
func (i fakeInfo) Sys() any           { return nil }

func TestMatching_SkipsDirectoriesAndOtherClips(t *testing.T) {
	t.Parallel()

	entries := []fs.DirEntry{
		fakeEntry{name: "b_514px.png", size: 5},
		fakeEntry{name: "b.png", size: 0},
		fakeEntry{name: "b_1026px.png", dir: true},
		fakeEntry{name: "b_15s_514px.png", size: 5},
		fakeEntry{name: "b.wav", size: 5},
	}

	got := Matching(entries, "b")

	names := make([]string, 0, len(got))
	for _, e := range got {
		names = append(names, e.Name())
	}
	assert.Equal(t, []string{"b_514px.png", "b.png"}, names, "empty files still match; deleting needs them")
}

func TestRenders_ReturnsNonEmptyRendersAndReportsInfoErrors(t *testing.T) {
	t.Parallel()

	t.Run("non-empty renders only", func(t *testing.T) {
		t.Parallel()
		entries := []fs.DirEntry{
			fakeEntry{name: "b_514px.png", size: 5},
			fakeEntry{name: "b.png", size: 0},
			fakeEntry{name: "other_514px.png", size: 5},
		}
		got, indeterminate := Renders(entries, "b")
		require.Len(t, got, 1)
		assert.Equal(t, "b_514px.png", got[0].Name)
		assert.Equal(t, Render{Width: 514, Raw: true}, got[0].Render)
		assert.False(t, indeterminate)
	})

	t.Run("symlinks and directories are not renders", func(t *testing.T) {
		t.Parallel()
		entries := []fs.DirEntry{
			fakeEntry{name: "b_514px.png", link: true, size: 12},
			fakeEntry{name: "b_1026px.png", dir: true, size: 4096},
			fakeEntry{name: "b_2050px.png", size: 5},
		}
		got, indeterminate := Renders(entries, "b")
		require.Len(t, got, 1)
		assert.Equal(t, "b_2050px.png", got[0].Name)
		assert.False(t, indeterminate)
	})

	t.Run("an entry that cannot be inspected is reported", func(t *testing.T) {
		t.Parallel()
		entries := []fs.DirEntry{
			fakeEntry{name: "b_514px.png", infoErr: errors.NewStd("boom")},
		}
		got, indeterminate := Renders(entries, "b")
		assert.Empty(t, got)
		assert.True(t, indeterminate)
	})

	t.Run("empty clip base matches nothing", func(t *testing.T) {
		t.Parallel()
		got, indeterminate := Renders([]fs.DirEntry{fakeEntry{name: "b.png", size: 5}}, "")
		assert.Empty(t, got)
		assert.False(t, indeterminate)
	})
}
