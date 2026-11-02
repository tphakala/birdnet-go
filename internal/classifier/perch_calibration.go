// perch_calibration.go finds the score calibration of a loaded Perch v2 model.
package classifier

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// perchCalibrationStatus says how a loaded Perch model relates to the catalog.
type perchCalibrationStatus int

const (
	// perchCalibrationUnknownLabels means the labels match no catalog Perch model,
	// so a slice cannot be told from the full model. It is the zero value so an
	// unset lookup never claims a calibration.
	perchCalibrationUnknownLabels perchCalibrationStatus = iota
	// perchCalibrationApplied means the labels are a catalog regional slice with a
	// usable calibration.
	perchCalibrationApplied
	// perchCalibrationFullModel means the labels are the full Perch v2 label set,
	// whose softmax needs no correction.
	perchCalibrationFullModel
)

// perchCalibrationLookup is the result of perchScoreCalibration.
type perchCalibrationLookup struct {
	status perchCalibrationStatus
	// calibration is a copy of the catalog value; set only when status is
	// perchCalibrationApplied.
	calibration ScoreCalibration
	// variantID is the matched catalog variant id; set only when applied.
	variantID string
	// labelsSHA256 is the SHA-256 of the raw labels bytes, for diagnostics.
	labelsSHA256 string
}

// perchScoreCalibration looks up the score calibration for the Perch v2 model
// that was built from labelData (the exact bytes read from the labels file),
// its parsed labels and the model file at modelPath.
//
// The key is the SHA-256 of the labels bytes: regional labels are per tile, and
// the model and labels files of a Perch install always come from one variant
// (validateLabelCount rejects a mismatch). Both catalogs are searched with the
// raw hash and the hash of the canonical form of the parsed labels (one label
// per LF-terminated line, the layout of the published regional labels files),
// which covers copies that were resaved with CRLF line endings or a trailing
// blank line.
//
// The built-in catalog is searched before the active one, so a user-edited
// model-catalog.json can neither override a built-in tile's constant nor attach
// one to the full model; the active catalog only adds entries the build does
// not know. Variants whose calibration is missing, of an unknown method or out
// of range are skipped.
func perchScoreCalibration(labelData []byte, labels []string, modelPath string) perchCalibrationLookup {
	rawHash := labelsSHA256Hex(labelData)
	out := perchCalibrationLookup{labelsSHA256: rawHash}
	hashes := []string{rawHash}

	// The canonical hash is always added, so the built-in catalog is searched with
	// the same hashes as the active one and cannot lose to it on a resaved file.
	if len(labels) > 0 {
		if canonical := labelsSHA256Hex([]byte(strings.Join(labels, "\n") + "\n")); canonical != rawHash {
			hashes = append(hashes, canonical)
		}
	}

	catalogs := [][]CatalogEntry{EmbeddedCatalog, ActiveCatalog()}

	for _, entries := range catalogs {
		matched := perchLabelMatches(entries, hashes)
		if len(matched) == 0 {
			continue
		}
		if slices.ContainsFunc(matched, func(v *CatalogVariant) bool { return v.Region == "" }) {
			out.status = perchCalibrationFullModel
			return out
		}
		if v := pickCalibratedVariant(matched, modelPath); v != nil {
			out.status = perchCalibrationApplied
			out.calibration = *v.ScoreCalibration
			out.variantID = v.ID
			return out
		}
	}

	if perchIsFullLabelCount(catalogs, len(labels)) {
		out.status = perchCalibrationFullModel
	}
	return out
}

// perchLabelMatches returns the Perch v2 variants of entries whose labels-role
// file has one of the SHA-256 values in hashes.
func perchLabelMatches(entries []CatalogEntry, hashes []string) []*CatalogVariant {
	var matched []*CatalogVariant
	for i := range entries {
		if entries[i].RegistryID != RegistryIDPerchV2 {
			continue
		}
		for j := range entries[i].Variants {
			v := &entries[i].Variants[j]
			if variantHasLabelsHash(v, hashes) {
				matched = append(matched, v)
			}
		}
	}
	return matched
}

// variantHasLabelsHash reports whether v's labels-role file matches a hash.
func variantHasLabelsHash(v *CatalogVariant, hashes []string) bool {
	for _, f := range v.Files {
		if f.Role != RoleLabels {
			continue
		}
		for _, h := range hashes {
			if strings.EqualFold(f.SHA256, h) {
				return true
			}
		}
	}
	return false
}

// pickCalibratedVariant chooses among regional variants sharing a labels hash:
// only usable calibrations qualify, a variant whose model file is the one at
// modelPath wins, otherwise the lowest variant id. Both precisions of a tile carry
// the same value by construction, so the choice only affects the logged id.
func pickCalibratedVariant(matched []*CatalogVariant, modelPath string) *CatalogVariant {
	usable := make([]*CatalogVariant, 0, len(matched))
	for _, v := range matched {
		if v.Region != "" && validScoreCalibration(v.ScoreCalibration) {
			usable = append(usable, v)
		}
	}
	if len(usable) == 0 {
		return nil
	}
	slices.SortStableFunc(usable, func(a, b *CatalogVariant) int { return strings.Compare(a.ID, b.ID) })
	base := filepath.Base(modelPath)
	for _, v := range usable {
		for _, f := range v.Files {
			if f.Role == RoleModel && (f.LocalName == base || path.Base(f.RemotePath) == base) {
				return v
			}
		}
	}
	return usable[0]
}

// perchIsFullLabelCount reports whether n equals the species count of a global
// (non-regional) Perch v2 variant, i.e. the label set is the full model's even
// though its bytes are not in the catalog.
func perchIsFullLabelCount(catalogs [][]CatalogEntry, n int) bool {
	for _, entries := range catalogs {
		for i := range entries {
			if entries[i].RegistryID != RegistryIDPerchV2 {
				continue
			}
			for j := range entries[i].Variants {
				if v := &entries[i].Variants[j]; v.Region == "" && v.SpeciesCount == n {
					return true
				}
			}
		}
	}
	return false
}

func labelsSHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
