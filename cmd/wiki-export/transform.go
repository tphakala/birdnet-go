package main

import (
	"net/url"
	"path"
	"regexp"
	"strings"
)

const (
	// repoBlobBase is the prefix for linking to a file in the repository's
	// default branch. Relative links that point at repo files (not wiki pages)
	// are rewritten to absolute blob URLs so they resolve on the wiki.
	repoBlobBase = "https://github.com/tphakala/birdnet-go/blob/main/"

	// wikiURLPrefix is the public base URL of the project wiki. Absolute links
	// that point back at our own wiki are normalized to relative page slugs.
	wikiURLPrefix = "https://github.com/tphakala/birdnet-go/wiki/"

	// bannerMarker is an HTML comment that flags a page as managed by the sync.
	// It is also used to make banner injection idempotent.
	bannerMarker = "<!-- wiki-sync:managed -->"

	// unicodeHyphen is U+2010 (HYPHEN), which legacy wiki page names used in
	// place of an ASCII hyphen. It is normalized away during rewriting.
	unicodeHyphen = "‐"
)

// pageSlugMap maps a source basename (lower-case, no extension) to its wiki
// page name when the two differ. Pages not listed here are published under
// their basename unchanged.
var pageSlugMap = map[string]string{
	"guide":                        "BirdNET-Go-Guide",
	"faq":                          "FAQ",
	"index":                        "Home",
	"onnx-runtime-installation":    "ONNX-Runtime-Installation",
	"database-doctor":              "Database-Doctor",
	"file-analysis":                "File-Analysis",
	"realtime-analysis":            "Realtime-Analysis",
	"training-a-custom-classifier": "Training-a-Custom-Classifier",
}

// wikiPageName returns the published wiki page name (no extension) for a source
// basename (no extension).
func wikiPageName(base string) string {
	if name, ok := pageSlugMap[strings.ToLower(base)]; ok {
		return name
	}
	return base
}

// buildPageIndex maps normalized link keys to wiki page names. Each source page
// is registered under both its basename and its wiki slug (lower-cased) so a
// link written either way resolves to the same page.
func buildPageIndex(basenames []string) map[string]string {
	idx := make(map[string]string, len(basenames)*2)
	for _, b := range basenames {
		name := wikiPageName(b)
		idx[strings.ToLower(b)] = name
		idx[strings.ToLower(name)] = name
	}
	return idx
}

// linkPattern matches a markdown inline link, capturing an optional leading
// "!" (image), the link text, and the target (URL plus optional title). Note
// it does not handle nested brackets in the text (e.g. a linked image
// `[![alt](img)](page)`) or links inside inline `code` spans; those are rare in
// the wiki docs and are left as authored.
var linkPattern = regexp.MustCompile(`(!?)\[([^\]]*)\]\(([^)]*)\)`)

var imageExts = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg"}

// rewriteLinks rewrites markdown links in content so they resolve on the
// published wiki. sourceDir is the repo-relative directory of the source file
// (always "doc/wiki"); idx is the page index from buildPageIndex. Links inside
// fenced code blocks are left untouched.
func rewriteLinks(content, sourceDir string, idx map[string]string) string {
	lines := strings.Split(content, "\n")
	fenced := fenceMask(lines)
	for i, line := range lines {
		if fenced[i] {
			continue
		}
		lines[i] = linkPattern.ReplaceAllStringFunc(line, func(m string) string {
			return rewriteOneLink(m, sourceDir, idx)
		})
	}
	return strings.Join(lines, "\n")
}

// fenceMask reports, per line, whether the line lies inside a fenced code block.
// Fence delimiter lines themselves are reported as outside (false). Pairing
// follows CommonMark: a block opened with N backticks or tildes (indented at
// most 3 spaces) closes only on a line of at least N of the SAME character with
// no trailing content, so a tilde line cannot close a backtick block.
func fenceMask(lines []string) []bool {
	mask := make([]bool, len(lines))
	var fenceChar byte
	var fenceLen int
	for i, line := range lines {
		if fenceChar == 0 {
			if c, n, ok := fenceOpener(line); ok {
				fenceChar, fenceLen = c, n
			}
			continue
		}
		if fenceCloses(line, fenceChar, fenceLen) {
			fenceChar, fenceLen = 0, 0
			continue
		}
		mask[i] = true
	}
	return mask
}

func fenceOpener(line string) (ch byte, runLen int, ok bool) {
	body := strings.TrimLeft(line, " ")
	if len(line)-len(body) > 3 || len(body) < 3 {
		return 0, 0, false
	}
	c := body[0]
	if c != '`' && c != '~' {
		return 0, 0, false
	}
	n := leadingRun(body, c)
	if n < 3 {
		return 0, 0, false
	}
	return c, n, true
}

func fenceCloses(line string, ch byte, openLen int) bool {
	body := strings.TrimLeft(line, " ")
	if len(line)-len(body) > 3 {
		return false
	}
	n := leadingRun(body, ch)
	return n >= openLen && strings.TrimSpace(body[n:]) == ""
}

func leadingRun(s string, ch byte) int {
	n := 0
	for n < len(s) && s[n] == ch {
		n++
	}
	return n
}

func rewriteOneLink(match, sourceDir string, idx map[string]string) string {
	sub := linkPattern.FindStringSubmatch(match)
	if sub == nil {
		return match
	}
	bang, text, target := sub[1], sub[2], sub[3]
	if bang == "!" {
		return match // image link, leave the target untouched
	}
	rewritten := rewriteTarget(target, sourceDir, idx)
	if rewritten == target {
		return match
	}
	return "[" + text + "](" + rewritten + ")"
}

// rewriteTarget rewrites a link target, preserving an optional trailing title.
func rewriteTarget(target, sourceDir string, idx map[string]string) string {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return target
	}
	urlPart := trimmed
	title := ""
	if i := strings.IndexAny(trimmed, " \t"); i >= 0 {
		urlPart = trimmed[:i]
		title = trimmed[i:]
	}
	resolved := resolveURL(urlPart, sourceDir, idx)
	if resolved == urlPart {
		return target
	}
	return resolved + title
}

func resolveURL(u, sourceDir string, idx map[string]string) string {
	switch {
	case strings.HasPrefix(u, "#"):
		return u // same-page anchor
	case strings.HasPrefix(u, wikiURLPrefix):
		return resolveWikiURL(u[len(wikiURLPrefix):], idx)
	case isExternalURL(u):
		return u
	default:
		return resolveRelative(u, sourceDir, idx)
	}
}

// resolveWikiURL normalizes an absolute self-wiki link into a relative page slug.
func resolveWikiURL(rest string, idx map[string]string) string {
	slug, anchor := splitAnchor(rest)
	slug = decodePercent(slug)
	slug = strings.ReplaceAll(slug, unicodeHyphen, "-")
	slug = strings.TrimSuffix(path.Base(slug), ".md")
	if name, ok := idx[strings.ToLower(slug)]; ok {
		return name + anchorSuffix(anchor)
	}
	return slug + anchorSuffix(anchor)
}

// resolveRelative rewrites a repo-relative link so it resolves on the wiki. A
// bare wiki slug (no path, no extension) or a link that resolves to a sibling
// page inside the wiki source directory becomes an extensionless slug. Any
// other repo file becomes an absolute blob URL. Matching is restricted to true
// siblings so a non-wiki file that merely shares a basename with a wiki page
// (e.g. ../../internal/installation.md) is not misrouted to the wiki page.
func resolveRelative(u, sourceDir string, idx map[string]string) string {
	pathPart, anchor := splitAnchor(u)
	norm := strings.ReplaceAll(pathPart, unicodeHyphen, "-")
	if isImagePath(norm) {
		return u
	}

	// Bare wiki-slug reference: no directory and no .md extension.
	if !strings.Contains(norm, "/") && !strings.HasSuffix(strings.ToLower(norm), ".md") {
		if name, ok := idx[strings.ToLower(norm)]; ok {
			return name + anchorSuffix(anchor)
		}
	}

	clean := path.Clean(path.Join(sourceDir, norm))
	base := strings.TrimSuffix(path.Base(clean), ".md")
	// Only a link that actually resolves to a sibling page in the wiki source
	// directory is treated as a wiki page.
	if clean == path.Join(sourceDir, base+".md") {
		if name, ok := idx[strings.ToLower(base)]; ok {
			return name + anchorSuffix(anchor)
		}
	}

	clean = strings.TrimPrefix(clean, "./")
	if strings.HasPrefix(clean, "..") {
		return u // escapes the repo root; leave the link as authored
	}
	return repoBlobBase + clean + anchorSuffix(anchor)
}

func isExternalURL(u string) bool {
	return strings.Contains(u, "://") ||
		strings.HasPrefix(u, "//") || // protocol-relative
		strings.HasPrefix(u, "mailto:") ||
		strings.HasPrefix(u, "tel:")
}

func isImagePath(p string) bool {
	lower := strings.ToLower(p)
	for _, ext := range imageExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return strings.HasPrefix(p, "images/")
}

func splitAnchor(s string) (link, anchor string) {
	if before, after, found := strings.Cut(s, "#"); found {
		return before, after
	}
	return s, ""
}

func anchorSuffix(anchor string) string {
	if anchor == "" {
		return ""
	}
	return "#" + anchor
}

func decodePercent(s string) string {
	if decoded, err := url.PathUnescape(s); err == nil {
		return decoded
	}
	return s
}

// bannerText renders the "do not edit" banner for a source file (relative to
// doc/wiki, e.g. "installation.md").
func bannerText(sourceRel string) string {
	src := "doc/wiki/" + sourceRel
	return bannerMarker + "\n" +
		"> **This page is generated.** It is published automatically from [`" + src +
		"`](" + repoBlobBase + src + ") in the BirdNET-Go repository. " +
		"Do not edit it here; changes are overwritten on the next sync. " +
		"To propose a change, open a pull request against the source file."
}

// injectBanner inserts the managed-page banner just after the first H1 heading,
// or at the top of the document when there is none. It is idempotent.
func injectBanner(content, sourceRel string) string {
	if strings.Contains(content, bannerMarker) {
		return content
	}
	banner := bannerText(sourceRel)
	lines := strings.Split(content, "\n")
	fenced := fenceMask(lines)
	for i, line := range lines {
		if fenced[i] || !strings.HasPrefix(line, "# ") {
			continue
		}
		out := make([]string, 0, len(lines)+2)
		out = append(out, lines[:i+1]...)
		out = append(out, "", banner)
		out = append(out, lines[i+1:]...)
		return strings.Join(out, "\n")
	}
	return banner + "\n\n" + content
}
