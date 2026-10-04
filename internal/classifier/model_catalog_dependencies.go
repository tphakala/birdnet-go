package classifier

import "slices"

// This file resolves catalog dependencies (CatalogEntry.DependsOn). A dependency is
// a flat, shared-only entry (a geomodel, a taxonomy file) whose files are installed
// together with every variant of the entry that names it. Everything
// here is a pure function of the active catalog, so a dependency's identity depends
// only on catalog IDs and never on version strings or file names.
//
// The active catalog shares Files backing arrays with every reader and must never
// be mutated (see catalogMu), so nothing here appends onto a catalog-owned slice or
// writes into a catalog-owned element: results are always freshly built slices.

// dependencyEntries returns the catalog entries named by entry.DependsOn, in
// declared order. IDs that are unknown to the active catalog, name the entry itself,
// or repeat an earlier ID are skipped. The pointers address copies; the Files slices
// they hold are shared with the catalog and read-only.
func dependencyEntries(entry *CatalogEntry) []*CatalogEntry {
	if entry == nil || len(entry.DependsOn) == 0 {
		return nil
	}
	deps := make([]*CatalogEntry, 0, len(entry.DependsOn))
	seen := make(map[string]struct{}, len(entry.DependsOn))
	for _, id := range entry.DependsOn {
		if id == entry.ID {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if dep, ok := GetCatalogEntry(id); ok {
			deps = append(deps, &dep)
		}
	}
	return deps
}

// dependencyFiles returns the files an entry installs on behalf of the dependency
// dep: a new slice of copies of dep.Files, each with an empty HuggingFaceRepo filled
// from dep.HuggingFaceRepo so the download uses the dependency's repository, not the
// dependent's. It is the single place that resolves dependency files. installed is
// the dependency's install record (nil when absent); it is unused today and is
// passed so that a later dependency carrying per-release variants can resolve the
// installed release's files here, without touching the callers.
func dependencyFiles(dep *CatalogEntry, _ *InstalledModel) []CatalogFile {
	files := make([]CatalogFile, len(dep.Files))
	copy(files, dep.Files)
	for i := range files {
		if files[i].HuggingFaceRepo == "" {
			files[i].HuggingFaceRepo = dep.HuggingFaceRepo
		}
	}
	return files
}

// EffectiveFiles returns every file installing the given variant of entry puts on
// disk: the variant's own files followed by the files of each dependency, in
// DependsOn order. An empty variantID selects the default variant. A dependency file
// whose LocalName the variant already carries is skipped (the inline copy wins). Every
// variant is treated alike, including the embedded baseline: its own files (none) plus
// its dependencies' files. ok is false exactly when the variant does not resolve. The result is a new slice the caller
// may keep, but the files it holds are values, never pointers into the catalog.
func EffectiveFiles(entry *CatalogEntry, variantID string) (files []CatalogFile, ok bool) {
	own, ok := variantFilesByID(entry, variantID)
	if !ok {
		return nil, false
	}
	files = slices.Clone(own)
	for _, dep := range dependencyEntries(entry) {
		for _, f := range dependencyFiles(dep, nil) {
			if !slices.ContainsFunc(files, func(have CatalogFile) bool { return have.LocalName == f.LocalName }) {
				files = append(files, f)
			}
		}
	}
	return files, true
}

// ProvidesGeomodel reports whether installing the given variant of entry puts a
// geomodel file on disk, from the variant itself or from a dependency.
func ProvidesGeomodel(entry *CatalogEntry, variantID string) bool {
	if entry == nil {
		return false
	}
	files, ok := EffectiveFiles(entry, variantID)
	return ok && slices.ContainsFunc(files, func(f CatalogFile) bool { return isGeomodelRole(f.Role) })
}

// geomodelDependency returns the dependency that supplies the range-filter geomodel
// for entry (DependsOn is entry-level, so every variant shares it): the first
// dependency carrying a complete geomodel tuple and a geomodel version. It reports
// false when no dependency qualifies.
func geomodelDependency(entry *CatalogEntry) (*CatalogEntry, bool) {
	if entry == nil {
		return nil, false
	}
	for _, dep := range dependencyEntries(entry) {
		if hasGeomodelTuple(dep) && dep.GeomodelVersion != "" {
			return dep, true
		}
	}
	return nil, false
}
