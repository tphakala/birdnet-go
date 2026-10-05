// Package specfile holds the filename grammar of spectrogram renders kept next
// to an audio clip. It imports only the standard library so that packages that
// internal/spectrogram itself depends on (such as the disk manager) can share
// the grammar without an import cycle.
package specfile

import (
	"io/fs"
	"strconv"
	"strings"
)

const (
	// renderWidthToken follows the pixel width in a sized render name.
	renderWidthToken = "px"
	// pngExt and pngExtUpper are the accepted image extensions.
	pngExt      = ".png"
	pngExtUpper = ".PNG"
	// renderTokenSep separates the width from the style, dynamic range,
	// frequency profile and legend tokens.
	renderTokenSep = "-"
	// sizedSep separates the clip base name from the pixel width.
	sizedSep = "_"
	// legendSuffix marks a render drawn with axes and legend (not raw).
	legendSuffix = "-legend"
)

// Render describes one spectrogram file belonging to a clip.
type Render struct {
	// Width is the pixel width from the file name; 0 for an unsized render.
	Width int
	// Raw is false when the render carries a legend.
	Raw bool
	// Unsized is true for the prerender file "<base>.png" / "<base>.PNG".
	Unsized bool
}

// IsRenderFor reports whether name is a spectrogram render of the clip whose
// base name (file name without its audio extension) is clipBase. It accepts
// "<base>.png", "<base>.PNG", "<base>_<digits>px.png" and
// "<base>_<digits>px-<tokens>.png", anchored on the full base. The names are
// written by buildSpectrogramPaths in internal/api/v2/media/media.go and
// BuildSpectrogramPath in internal/spectrogram/utils.go. The result depends
// only on the two arguments.
func IsRenderFor(name, clipBase string) bool {
	_, ok := ParseRender(name, clipBase)
	return ok
}

// ParseRender parses name as a render of the clip with base name clipBase and
// reports whether it is one. An empty clipBase never matches. Temporary files
// ("<base>.png.<pid>.<seq>.temp") do not end in .png and never match.
func ParseRender(name, clipBase string) (r Render, ok bool) {
	if clipBase == "" {
		return Render{}, false
	}
	rest, found := strings.CutPrefix(name, clipBase)
	if !found {
		return Render{}, false
	}
	if rest == pngExt || rest == pngExtUpper {
		return Render{Raw: true, Unsized: true}, true
	}
	body, found := strings.CutSuffix(rest, pngExt)
	if !found {
		return Render{}, false
	}
	body, found = strings.CutPrefix(body, sizedSep)
	if !found {
		return Render{}, false
	}
	digits, tokens, hasPx := strings.Cut(body, renderWidthToken)
	if !hasPx || !allDigits(digits) {
		return Render{}, false
	}
	if tokens != "" {
		// Anything after "px" must be a "-" separated, non-empty token list.
		tail, sep := strings.CutPrefix(tokens, renderTokenSep)
		if !sep || tail == "" {
			return Render{}, false
		}
	}
	width, err := strconv.Atoi(digits)
	if err != nil {
		return Render{}, false
	}
	return Render{Width: width, Raw: !strings.HasSuffix(tokens, legendSuffix)}, true
}

// allDigits reports whether s is non-empty and made only of ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Found is a non-empty render of a clip found in a directory listing.
type Found struct {
	// Name is the file name within the listed directory.
	Name string
	// Render is the parsed name.
	Render Render
	// Mode is the entry's file mode, so a caller can refuse symlinks.
	Mode fs.FileMode
}

// Matching returns the non-directory entries of a directory listing that are
// renders of the clip with base name clipBase, empty files included. Callers that
// remove a clip's renders use it directly.
func Matching(entries []fs.DirEntry, clipBase string) []fs.DirEntry {
	var out []fs.DirEntry
	for _, entry := range entries {
		if entry.IsDir() || !IsRenderFor(entry.Name(), clipBase) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// Renders returns the non-empty renders of the clip with base name clipBase in a
// directory listing. An empty file is not a kept render: an interrupted render
// leaves one and the API refuses to serve it. indeterminate is true when a
// matching entry could not be inspected, so a caller that must not act on a
// guess can leave the clip alone.
func Renders(entries []fs.DirEntry, clipBase string) (found []Found, indeterminate bool) {
	for _, entry := range Matching(entries, clipBase) {
		info, err := entry.Info()
		if err != nil {
			indeterminate = true
			continue
		}
		if info.Size() == 0 {
			continue
		}
		render, _ := ParseRender(entry.Name(), clipBase)
		found = append(found, Found{Name: entry.Name(), Render: render, Mode: info.Mode()})
	}
	return found, indeterminate
}
