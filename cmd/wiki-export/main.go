// Command wiki-export prepares the doc/wiki markdown for publishing to the
// GitHub project wiki. For each page it remaps the page name to its wiki slug,
// rewrites intra-doc links so they resolve on the wiki, and injects a
// "do not edit" banner. Image assets are copied through verbatim.
//
// Usage:
//
//	go run ./cmd/wiki-export [srcDir] [outDir]
//
// srcDir defaults to doc/wiki and outDir to .wiki-staging.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// docWikiRepoDir is the canonical repository-relative location of the wiki
// sources. Link rewriting always resolves repo-file links against this path,
// independent of where the files are physically read from, so generated blob
// URLs are stable.
const docWikiRepoDir = "doc/wiki"

func main() {
	srcDir := filepath.FromSlash(docWikiRepoDir)
	outDir := ".wiki-staging"
	if len(os.Args) > 1 {
		srcDir = os.Args[1]
	}
	if len(os.Args) > 2 {
		outDir = os.Args[2]
	}
	if err := export(srcDir, outDir); err != nil {
		fmt.Fprintf(os.Stderr, "wiki-export: %v\n", err)
		os.Exit(1)
	}
}

// export transforms every markdown page in srcDir and writes the wiki-ready
// result into outDir, copying any images/ subdirectory through unchanged.
func export(srcDir, outDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("reading source dir %s: %w", srcDir, err)
	}

	basenames := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		basenames = append(basenames, strings.TrimSuffix(e.Name(), ".md"))
	}
	slices.Sort(basenames) // deterministic output order
	idx := buildPageIndex(basenames)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir %s: %w", outDir, err)
	}

	for _, base := range basenames {
		srcPath := filepath.Join(srcDir, base+".md")
		raw, err := os.ReadFile(srcPath) // #nosec G304 -- name comes from a listed dir entry
		if err != nil {
			return fmt.Errorf("reading %s: %w", srcPath, err)
		}
		content := rewriteLinks(string(raw), docWikiRepoDir, idx)
		content = injectBanner(content, base+".md")

		outPath := filepath.Join(outDir, wikiPageName(base)+".md")
		if err := os.WriteFile(outPath, []byte(content), 0o644); err != nil { //nolint:gosec // public wiki content
			return fmt.Errorf("writing %s: %w", outPath, err)
		}
		fmt.Printf("wrote %s\n", outPath)
	}

	return copyImages(srcDir, outDir)
}

// copyImages mirrors the images/ subdirectory of srcDir into outDir. It is a
// no-op when there is no images directory.
func copyImages(srcDir, outDir string) error {
	imgSrc := filepath.Join(srcDir, "images")
	info, err := os.Stat(imgSrc)
	switch {
	case os.IsNotExist(err):
		return nil
	case err != nil:
		return fmt.Errorf("stat %s: %w", imgSrc, err)
	case !info.IsDir():
		return nil
	}

	return filepath.WalkDir(imgSrc, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return fmt.Errorf("relativizing %s: %w", p, err)
		}
		dst := filepath.Join(outDir, rel)
		if d.IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return fmt.Errorf("creating %s: %w", dst, err)
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks/devices: never publish a link target's contents
		}
		data, err := os.ReadFile(p) // #nosec G304 -- walking a known images dir
		if err != nil {
			return fmt.Errorf("reading %s: %w", p, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil { //nolint:gosec // public image asset
			return fmt.Errorf("writing %s: %w", dst, err)
		}
		return nil
	})
}
