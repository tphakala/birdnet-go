package region

import (
	"embed"
	"encoding/json"
	"io/fs"
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// snapshotFS holds the build-time region snapshots, one regions.json per model
// family. Refresh them from the acoustic-models checkout with the Taskfile
// `sync-region-snapshots` task; see data/README.md. Adding a family is a
// drop-in: the loader keys tables by each file's own repo field, so no code
// change is needed.
//
//go:embed data/*.regions.json
var snapshotFS embed.FS

// snapshotDir is the directory inside snapshotFS holding the embedded snapshots.
const snapshotDir = "data"

// loadTables parses every embedded snapshot once. It is wrapped in
// sync.OnceValues so the first caller pays the parse cost and later callers get
// the cached result. A parse or validation failure of an embedded file is a
// build defect (the package tests exercise this), so it surfaces as an error
// rather than a panic; callers then fall back to the global model.
var loadTables = sync.OnceValues(func() (map[string]*Table, error) {
	entries, err := fs.ReadDir(snapshotFS, snapshotDir)
	if err != nil {
		return nil, errors.New(err).
			Component("classifier.region").
			Category(errors.CategoryValidation).
			Context("operation", "read_snapshot_dir").
			Build()
	}
	tables := make(map[string]*Table, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := snapshotDir + "/" + e.Name()
		data, rerr := snapshotFS.ReadFile(name)
		if rerr != nil {
			return nil, errors.New(rerr).
				Component("classifier.region").
				Category(errors.CategoryValidation).
				Context("operation", "read_snapshot").
				Context("file", name).
				Build()
		}
		var t Table
		if uerr := json.Unmarshal(data, &t); uerr != nil {
			return nil, errors.New(uerr).
				Component("classifier.region").
				Category(errors.CategoryValidation).
				Context("operation", "parse_snapshot").
				Context("file", name).
				Build()
		}
		if verr := t.validate(); verr != nil {
			return nil, verr
		}
		if _, dup := tables[t.Repo]; dup {
			return nil, errors.Newf("duplicate region snapshot for repo %q", t.Repo).
				Component("classifier.region").
				Category(errors.CategoryValidation).
				Context("operation", "load_snapshots").
				Build()
		}
		tables[t.Repo] = &t
	}
	if len(tables) == 0 {
		return nil, errors.Newf("no region snapshots embedded").
			Component("classifier.region").
			Category(errors.CategoryValidation).
			Context("operation", "load_snapshots").
			Build()
	}
	return tables, nil
})

// Tables returns every embedded region table keyed by its HuggingFace repo id.
// The returned map is shared and must not be mutated.
func Tables() (map[string]*Table, error) {
	return loadTables()
}

// TableForRepo returns the region table for a HuggingFace repo id. ok is false
// when no snapshot describes that repo (a family with no region table), in which
// case the caller falls back to the global model. An embedded-snapshot load
// failure also reports ok=false, so a corrupt snapshot degrades to global rather
// than propagating an error into the hot path.
func TableForRepo(repo string) (t *Table, ok bool) {
	tables, err := loadTables()
	if err != nil {
		return nil, false
	}
	t, ok = tables[repo]
	return t, ok
}
