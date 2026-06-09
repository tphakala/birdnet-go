package batch

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// audioExtensions are the corpus file types the batch path accepts. FFmpeg
// handles the actual decode, so this is a walk filter, not a format gate.
var audioExtensions = map[string]bool{
	".wav": true, ".flac": true, ".mp3": true,
	".m4a": true, ".aac": true, ".opus": true, ".ogg": true,
}

// DirectoryItems walks root and returns one Item per audio file, keyed by
// the path relative to root. Hidden directories are skipped. Order is
// deterministic (sorted by key) so limits and reruns behave predictably.
func DirectoryItems(root string) ([]Item, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var items []Item
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name != "." && strings.HasPrefix(name, ".") && path != absRoot {
				return filepath.SkipDir
			}
			return nil
		}
		if !audioExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return err
		}
		items = append(items, Item{Path: path, Key: rel})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	return items, nil
}
