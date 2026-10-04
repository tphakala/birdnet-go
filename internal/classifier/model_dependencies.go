package classifier

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// dependentNameSeparator joins dependent model names in a DependentsError message.
const dependentNameSeparator = ", "

// CatalogRef identifies a catalog entry in an error or an API payload.
type CatalogRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DependentsError is returned by ModelManager.Uninstall when other models still need
// the entry: they are installed, or actively downloading, with files that include the
// entry's. Nothing is changed when it is returned.
type DependentsError struct {
	// CatalogID is the entry the caller tried to uninstall.
	CatalogID string
	// Name is that entry's display name.
	Name string
	// Dependents lists the models that need it, sorted by catalog ID.
	Dependents []CatalogRef
}

// DependentNames returns the dependents' display names joined for a message.
func (e *DependentsError) DependentNames() string {
	names := make([]string, 0, len(e.Dependents))
	for _, d := range e.Dependents {
		names = append(names, d.Name)
	}
	return strings.Join(names, dependentNameSeparator)
}

// Error implements the error interface.
func (e *DependentsError) Error() string {
	return "cannot uninstall " + e.CatalogID + ": required by " + e.DependentNames()
}

// dependentsError builds the refusal for entry, or nil when nothing depends on it.
// The caller holds mm.mu.
func (mm *ModelManager) dependentsError(entry *CatalogEntry) error {
	dependents := mm.dependentsLocked(entry.ID)
	if len(dependents) == 0 {
		return nil
	}
	return &DependentsError{CatalogID: entry.ID, Name: entry.Name, Dependents: dependents}
}

// dependentsLocked lists the models that need catalogID: installed entries (other
// including a record on the embedded baseline variant) and entries with an actively
// downloading state whose DependsOn names it. A failed, retained
// download state does not count. The result is sorted by catalog ID. The caller
// holds mm.mu.
func (mm *ModelManager) dependentsLocked(catalogID string) []CatalogRef {
	found := make(map[string]CatalogRef)
	add := func(id string, entry *CatalogEntry) {
		if id != catalogID && slices.Contains(entry.DependsOn, catalogID) {
			found[id] = CatalogRef{ID: id, Name: entry.Name}
		}
	}
	for id := range mm.installed {
		if entry, ok := GetCatalogEntry(id); ok {
			add(id, &entry)
		}
	}
	for id, state := range mm.downloading {
		if !state.IsActive() {
			continue
		}
		if entry, ok := GetCatalogEntry(id); ok {
			add(id, &entry)
		}
	}
	refs := slices.Collect(maps.Values(found))
	slices.SortFunc(refs, func(a, b CatalogRef) int { return strings.Compare(a.ID, b.ID) })
	return refs
}

// sharedOnlyRecord builds the install record for a shared-only entry, or reports
// false unless every one of its files exists under sharedDir. ModelPath is the
// geomodel model path when the entry has one, else its first file; LabelsPath is the
// geomodel labels path when present. ScanInstalled, a dependency recorded by an
// install, and a direct install of the entry all derive records through this rule,
// so the record after an install equals what a rescan finds.
func sharedOnlyRecord(entry *CatalogEntry, sharedDir string) (InstalledModel, bool) {
	if !IsSharedOnly(entry) {
		return InstalledModel{}, false
	}
	var modelPath, labelsPath string
	for i, f := range entry.Files {
		p := filepath.Join(sharedDir, f.LocalName)
		if _, err := os.Stat(p); err != nil {
			return InstalledModel{}, false
		}
		if i == 0 {
			modelPath = p
		}
		switch f.Role {
		case RoleGeomodelModel:
			modelPath = p
		case RoleGeomodelLabels:
			labelsPath = p
		}
	}
	return InstalledModel{
		CatalogID:   entry.ID,
		ModelPath:   modelPath,
		LabelsPath:  labelsPath,
		InstalledAt: fileModTime(modelPath),
		Version:     entry.Version,
	}, true
}

// recordDependenciesLocked records each dependency of entry that an install of
// install just made present on disk. A dependency already installed, or with its
// own download in progress (it records itself), is left alone, and one whose files
// are not all present is not recorded. The caller holds mm.mu.
func (mm *ModelManager) recordDependenciesLocked(entry *CatalogEntry) {
	sharedDir := filepath.Join(mm.modelsDir, sharedDirName)
	for _, dep := range dependencyEntries(entry) {
		if _, ok := mm.installed[dep.ID]; ok {
			continue
		}
		if mm.downloading[dep.ID].IsActive() {
			continue
		}
		if rec, ok := sharedOnlyRecord(dep, sharedDir); ok {
			mm.installed[dep.ID] = rec
		}
	}
}

// allVariantIDs returns "" (the default variant) plus every variant ID of entry: the
// union of what an in-flight download of entry might install.
func allVariantIDs(entry *CatalogEntry) []string {
	ids := make([]string, 0, len(entry.Variants)+1)
	ids = append(ids, "")
	for i := range entry.Variants {
		ids = append(ids, entry.Variants[i].ID)
	}
	return ids
}

// hasSharedFile reports whether files contain a shared-role file named localName.
func hasSharedFile(files []CatalogFile, localName string) bool {
	return slices.ContainsFunc(files, func(f CatalogFile) bool {
		return isSharedRole(f.Role) && f.LocalName == localName
	})
}

// usesSharedFileLocked reports whether entry, installed (or downloading) as any of
// variantIDs, reaches the shared file localName: through its own files, or through a
// dependency's files. A variant that does not resolve counts with every file set of
// the entry, erring toward keeping the file. The caller holds mm.mu.
func (mm *ModelManager) usesSharedFileLocked(entry *CatalogEntry, variantIDs []string, localName string) bool {
	for _, vid := range variantIDs {
		sets := entryFileSets(entry)
		if files, ok := variantFilesByID(entry, vid); ok {
			sets = [][]CatalogFile{files}
		}
		if slices.ContainsFunc(sets, func(files []CatalogFile) bool { return hasSharedFile(files, localName) }) {
			return true
		}
	}
	for _, dep := range dependencyEntries(entry) {
		var rec *InstalledModel
		if im, ok := mm.installed[dep.ID]; ok {
			rec = &im
		}
		if hasSharedFile(dependencyFiles(dep, rec), localName) {
			return true
		}
	}
	return false
}

// sharedFileInUseLocked reports whether any entry other than excludeID still reaches
// the shared file localName: an installed record through the files of its installed
// variant, or an actively downloading entry through the union of all its variants. It
// is the single retention predicate for shared files. The caller holds mm.mu.
func (mm *ModelManager) sharedFileInUseLocked(excludeID, localName string) bool {
	for id := range mm.installed {
		if id == excludeID {
			continue
		}
		if entry, ok := GetCatalogEntry(id); ok &&
			mm.usesSharedFileLocked(&entry, []string{mm.installed[id].VariantID}, localName) {
			return true
		}
	}
	for id, state := range mm.downloading {
		if id == excludeID || !state.IsActive() {
			continue
		}
		if entry, ok := GetCatalogEntry(id); ok &&
			mm.usesSharedFileLocked(&entry, allVariantIDs(&entry), localName) {
			return true
		}
	}
	return false
}

// cleanupSharedFilesLocked deletes the shared files an uninstall of removed (as
// removedVariant) leaves unused: every shared-role file of the variant's OWN files
// that no other installed or actively downloading entry reaches. Dependency files are
// never deleted here: a dependency is its own catalog entry, removed by its own
// uninstall or, for a component, by autoremoveComponentsLocked, so a dependent's
// uninstall cannot delete a geomodel the range filter still uses. A recorded variant
// that no longer resolves falls back to the default variant's files. Geomodel files
// are kept when skipGeomodel is set (the range-filter reload failed, so a session may
// still hold them). The caller holds mm.mu.
func (mm *ModelManager) cleanupSharedFilesLocked(log logger.Logger, removed *CatalogEntry, removedVariant string, skipGeomodel bool) {
	files, ok := variantFilesByID(removed, removedVariant)
	if !ok {
		files, _ = variantFilesByID(removed, "")
	}
	for _, f := range files {
		if !isSharedRole(f.Role) || (skipGeomodel && isGeomodelRole(f.Role)) {
			continue
		}
		if mm.sharedFileInUseLocked(removed.ID, f.LocalName) {
			log.Debug("Retaining shared file; another model still needs it",
				logger.String("catalog_id", removed.ID),
				logger.String("file", f.LocalName))
			continue
		}
		path := filepath.Join(mm.modelsDir, sharedDirName, f.LocalName)
		if err := os.Remove(path); err != nil {
			if !os.IsNotExist(err) {
				log.Warn("Failed to remove shared file",
					logger.String("path", path),
					logger.Error(err))
			}
			continue
		}
		log.Info("Removed shared file",
			logger.String("path", path))
	}
}

// autoremoveComponentsLocked removes each Component dependency of removed that is
// installed but no longer needed: nothing installed or actively downloading depends
// on it, and it has no download of its own in flight. Its record is dropped and its
// files go through the same per-file retention as any shared file. It does not call
// Uninstall (which takes mm.mu). The caller holds mm.mu.
func (mm *ModelManager) autoremoveComponentsLocked(log logger.Logger, removed *CatalogEntry, skipGeomodel bool) {
	for _, dep := range dependencyEntries(removed) {
		if !dep.Component {
			continue
		}
		if _, ok := mm.installed[dep.ID]; !ok {
			continue
		}
		if mm.downloading[dep.ID].IsActive() || len(mm.dependentsLocked(dep.ID)) > 0 {
			continue
		}
		delete(mm.installed, dep.ID)
		mm.cleanupSharedFilesLocked(log, dep, "", skipGeomodel)
		log.Info("Removed unused component",
			logger.String("catalog_id", dep.ID),
			logger.String("required_by", removed.ID))
	}
}
